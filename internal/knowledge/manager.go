package knowledge

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/jackc/pgx/v5"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
)

const TopicChanged = "knowledge.changed"

type Manager struct {
	mu          sync.Mutex
	host        agentruntime.ModuleHost
	imageModels ragconfig.ImageModelResolver
	directory   string
	state       diskState
	engine      *engine
	active      int
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	imports     chan struct{}
	queries     chan struct{}
	runs        map[string]context.CancelFunc
}

type Job struct {
	ID           string    `json:"id"`
	CollectionID string    `json:"collection_id"`
	DocumentID   string    `json:"document_id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	Warnings     []string  `json:"warnings,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type jobRecord struct {
	Job
	Source rag.SourceFile `json:"source"`
	Retry  bool           `json:"retry"`
}
type Document struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Size      int64     `json:"size"`
	Revision  int64     `json:"revision"`
	Status    string    `json:"status"`
	Stage     string    `json:"stage"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Retryable bool      `json:"retryable"`
}
type DocumentPage struct {
	Documents []Document `json:"documents"`
	HasMore   bool       `json:"has_more"`
}

func New(ctx context.Context, host agentruntime.ModuleHost, images ragconfig.ImageModelResolver) (*Manager, error) {
	if host.DataDir == "" || host.Logger == nil || host.Credentials == nil {
		return nil, errors.New("知识库缺少数据目录、日志或凭据存储")
	}
	stopping, cancel := context.WithCancel(context.Background())
	m := &Manager{host: host, imageModels: images, directory: filepath.Join(host.DataDir, "knowledge"), ctx: stopping, cancel: cancel,
		imports: make(chan struct{}, 2), queries: make(chan struct{}, 2), runs: map[string]context.CancelFunc{}}
	if err := m.load(ctx); err != nil {
		cancel()
		return nil, err
	}
	return m, nil
}

func (m *Manager) Module() agentruntime.Module {
	return agentruntime.Module{ID: "knowledge", Capabilities: []agentruntime.CapabilityProvider{m},
		Start: func(context.Context) error { return nil }, Stop: m.Stop, Close: m.Close}
}

// acquire 把配置、外部连接和操作生命周期锁定在一起，禁止运行中换库或释放连接。
func (m *Manager) acquire(ctx context.Context, revision uint64, initialize bool) (*engine, context.Context, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	fail := func() { stop(); cancel() }
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		fail()
		return nil, nil, nil, err
	}
	if m.closed || !m.state.Enabled {
		m.mu.Unlock()
		fail()
		return nil, nil, nil, errors.New("请先在设置中启用并配置 RAG")
	}
	if revision != 0 && revision != m.state.Revision {
		m.mu.Unlock()
		fail()
		return nil, nil, nil, errors.New("知识库配置或 Agent 绑定已变化，请重新发起本轮对话")
	}
	if initialize && m.active != 0 {
		m.mu.Unlock()
		fail()
		return nil, nil, nil, errors.New("请等待知识库任务结束后初始化")
	}
	if initialize && m.engine != nil {
		m.engine.Close()
		m.engine = nil
	}
	if m.engine == nil {
		connect, done := context.WithTimeout(ctx, 45*time.Second)
		var err error
		m.engine, err = m.buildEngine(connect, initialize)
		done()
		if err != nil {
			m.mu.Unlock()
			fail()
			return nil, nil, nil, err
		}
	}
	e := m.engine
	m.active++
	m.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { fail(); m.mu.Lock(); m.active--; m.mu.Unlock() }) }
	return e, ctx, release, nil
}

// Initialize 是显式建库/迁移入口；不会调用 Embedding 或图片模型。
func (m *Manager) Initialize(ctx context.Context) error {
	e, ctx, release, err := m.acquire(ctx, 0, true)
	if err != nil {
		return err
	}
	defer release()
	m.mu.Lock()
	collections := slices.Clone(m.state.Collections)
	m.mu.Unlock()
	for _, c := range collections {
		if _, err := e.RecoverDocuments(ctx, c.ID); err != nil {
			return err
		}
	}
	m.host.Logger.Info(ctx, "rag.initialize", "database", e.config.Database.Name, "dimensions", e.config.Embedding.Dimensions)
	return nil
}

func (m *Manager) collection(id string) (Collection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.IndexFunc(m.state.Collections, func(c Collection) bool { return c.ID == id })
	if i < 0 {
		return Collection{}, errors.New("知识库不存在")
	}
	return clone(m.state.Collections[i]), nil
}

func (m *Manager) Documents(ctx context.Context, collection string, offset int) (DocumentPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := m.collection(collection); err != nil {
		return DocumentPage{}, err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return DocumentPage{}, err
	}
	defer release()
	// 只按截止时间恢复遗留状态，不抢占仍在运行的任务。
	if _, err := e.RecoverDocuments(ctx, collection); err != nil {
		return DocumentPage{}, err
	}
	rows, err := e.store.ListDocuments(ctx, collection, 51, offset)
	if err != nil {
		return DocumentPage{}, err
	}
	page := DocumentPage{Documents: []Document{}, HasMore: len(rows) > 50}
	for _, r := range rows[:min(len(rows), 50)] {
		page.Documents = append(page.Documents, Document{r.ID, r.Title, r.Name, r.SourceFile.Type, r.Size, r.Revision, r.ParseStatus, r.ProcessingStage, logging.SafeErrorText(errors.New(r.ErrorMessage), 1200), r.UpdatedAt, rag.CanRetryDocument(r)})
	}
	return page, nil
}

// Upload 只由原生文件选择器调用。先保存原格式快照，再启动有并发上限的后台任务。
func (m *Manager) Upload(ctx context.Context, collection string, files []string) ([]Job, error) {
	if len(files) == 0 {
		return []Job{}, nil
	}
	if len(files) > 16 {
		return nil, errors.New("一次最多上传 16 个文件")
	}
	if _, err := m.collection(collection); err != nil {
		return nil, err
	}
	e, op, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			release()
		}
	}()
	records := make([]jobRecord, 0, len(files))
	for _, filename := range files {
		_, source, err := e.sources.Save(op, document.Source{URI: filename})
		if err != nil {
			return nil, err
		}
		id := "doc-" + source.Hash + "-" + source.Type // 同内容、同格式只导入一次；不同解析格式不误判为重复。
		existing, err := e.GetDocument(op, collection, id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil && existing.Revision > 0 {
			continue
		}
		if slices.ContainsFunc(records, func(j jobRecord) bool { return j.DocumentID == id }) {
			continue
		}
		records = append(records, jobRecord{Job: Job{ID: strings.ToLower(rand.Text()), CollectionID: collection, DocumentID: id, Name: source.Name, Status: "queued", UpdatedAt: time.Now().UTC()}, Source: source})
	}
	if len(records) == 0 {
		return []Job{}, nil
	}
	if err := m.queue(op, records); err != nil {
		return nil, err
	}
	// 上传请求返回后仍继续执行；应用退出和单文档取消由 Manager 自己控制。
	owned = true
	m.launch(e, records, release)
	jobs := make([]Job, 0, len(records))
	for _, r := range records {
		jobs = append(jobs, r.Job)
	}
	return jobs, nil
}

func pending(status string) bool { return status == "queued" || status == "processing" }
func (m *Manager) queue(ctx context.Context, records []jobRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("知识库已关闭")
	}
	count := 0
	for _, j := range m.state.Jobs {
		if pending(j.Status) {
			count++
		}
	}
	if count+len(records) > 16 {
		return errors.New("待处理文档最多 16 个，请等待或取消部分任务")
	}
	for _, r := range records {
		if slices.ContainsFunc(m.state.Jobs, func(j jobRecord) bool {
			return pending(j.Status) && j.CollectionID == r.CollectionID && j.DocumentID == r.DocumentID
		}) {
			return errors.New("该文档正在导入，请等待完成")
		}
	}
	next := clone(m.state)
	// 只保留最近的任务摘要，正式文档与失败状态仍保存在数据库。
	if len(next.Jobs) > 100 {
		next.Jobs = slices.DeleteFunc(next.Jobs, func(j jobRecord) bool { return !pending(j.Status) })
	}
	next.Jobs = append(next.Jobs, records...)
	if err := m.persist(ctx, next); err != nil {
		return err
	}
	m.state = next
	return nil
}

func (m *Manager) launch(e *engine, records []jobRecord, release func()) {
	// 在锁内登记 WaitGroup，Stop 不能在 Add 之前开始等待。
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		release()
		return
	}
	m.wg.Add(1)
	contexts := make([]context.Context, len(records))
	for i, r := range records {
		ctx, cancel := context.WithTimeout(m.ctx, e.config.Ingest.Effective().Timeout)
		contexts[i] = ctx
		m.runs[r.ID] = cancel
	}
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		defer release()
		var workers sync.WaitGroup
		for i, r := range records {
			workers.Add(1)
			go func(ctx context.Context, r jobRecord) { defer workers.Done(); m.runImport(ctx, e, r) }(contexts[i], r)
		}
		workers.Wait()
	}()
}

func (m *Manager) runImport(ctx context.Context, e *engine, r jobRecord) {
	started := time.Now()
	var err error
	select {
	case m.imports <- struct{}{}:
		defer func() { <-m.imports }()
	case <-ctx.Done():
		err = ctx.Err()
	}
	var response rag.IngestResponse
	if err == nil {
		m.updateJob(r.ID, "processing", "", nil)
		c := e.config.Chunking
		req := rag.IngestRequest{CollectionID: r.CollectionID, DocumentID: r.DocumentID, StoredSource: &r.Source, Splitter: c.Splitter, ParentChild: c.ParentChild, ParentChunkSize: c.ParentChunkSize, ChildChunkSize: c.ChildChunkSize}
		if r.Retry {
			response, err = e.RetryDocument(ctx, req)
		} else {
			response, err = e.Ingest(ctx, req)
		}
	}
	status, message := "completed", ""
	warnings := []string{}
	for _, doc := range response.Documents {
		for _, w := range doc.Warnings {
			warnings = append(warnings, logging.RedactText(w, 1200))
		}
	}
	if err != nil {
		status = "failed"
		message = logging.SafeErrorText(err, 1200)
		if errors.Is(err, context.Canceled) {
			status = "cancelled"
		}
	}
	m.updateJob(r.ID, status, message, warnings)
	m.mu.Lock()
	cancel := m.runs[r.ID]
	delete(m.runs, r.ID)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// 默认保留导入结果与提示数量，避免大文档的逐张图片提示占满日志。
	attrs := []any{"job_id", r.ID, "collection_id", r.CollectionID, "document_id", r.DocumentID,
		"status", status, "duration_ms", time.Since(started).Milliseconds(), "parents", response.ParentCount, "children", response.ChildCount, "warning_count", len(warnings)}
	write := m.host.Logger.Info
	if status == "failed" {
		write = m.host.Logger.Warn
	}
	if message != "" {
		attrs = append(attrs, "error", message)
	}
	write(context.Background(), "rag.ingest", attrs...)
	if len(warnings) > 0 {
		m.host.Logger.Debug(context.Background(), "rag.ingest.details", "job_id", r.ID, "warnings", warnings)
	}
}

func (m *Manager) updateJob(id, status, message string, warnings []string) {
	m.mu.Lock()
	next := clone(m.state)
	i := slices.IndexFunc(next.Jobs, func(j jobRecord) bool { return j.ID == id })
	if i >= 0 {
		next.Jobs[i].Status = status
		next.Jobs[i].Error = message
		next.Jobs[i].Warnings = warnings
		next.Jobs[i].UpdatedAt = time.Now().UTC()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := m.persist(ctx, next); err != nil {
			m.host.Logger.Error(ctx, "rag.job.persist", "job_id", id, "error", logging.SafeErrorText(err, 500))
		}
		cancel()
		m.state = next
	}
	m.mu.Unlock()
	if m.host.Events != nil {
		_ = m.host.Events.Publish(context.Background(), TopicChanged, map[string]string{"job_id": id, "status": status})
	}
}

func (m *Manager) Cancel(ctx context.Context, collection, documentID string) error {
	if _, err := m.collection(collection); err != nil {
		return err
	}
	m.mu.Lock()
	for _, j := range m.state.Jobs {
		if j.CollectionID == collection && j.DocumentID == documentID && pending(j.Status) {
			if cancel := m.runs[j.ID]; cancel != nil {
				cancel()
			}
		}
	}
	m.mu.Unlock()
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return err
	}
	defer release()
	return e.CancelDocument(ctx, collection, documentID)
}

func (m *Manager) Retry(ctx context.Context, collection, documentID string) ([]Job, error) {
	return m.reprocess(ctx, collection, documentID, false)
}

// Reindex 是用户明确发起的重新索引，供切分参数调整后使用；普通重复上传仍直接跳过。
func (m *Manager) Reindex(ctx context.Context, collection, documentID string) ([]Job, error) {
	return m.reprocess(ctx, collection, documentID, true)
}
func (m *Manager) reprocess(ctx context.Context, collection, documentID string, reindex bool) ([]Job, error) {
	if _, err := m.collection(collection); err != nil {
		return nil, err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			release()
		}
	}()
	r := jobRecord{Job: Job{ID: strings.ToLower(rand.Text()), CollectionID: collection, DocumentID: documentID, Status: "queued", UpdatedAt: time.Now().UTC()}, Retry: true}
	// 尚未开始解析的中断任务可能没有数据库记录，仍可使用已保存的上传原件。
	m.mu.Lock()
	for i := len(m.state.Jobs) - 1; i >= 0; i-- {
		old := m.state.Jobs[i]
		if old.CollectionID == collection && old.DocumentID == documentID {
			r.Source = old.Source
			r.Name = old.Name
			break
		}
	}
	m.mu.Unlock()
	record, err := e.GetDocument(ctx, collection, documentID)
	if errors.Is(err, pgx.ErrNoRows) && r.Source.Path != "" {
		r.Retry = false
	} else if err != nil {
		return nil, err
	} else {
		r.Name = record.Name
		if record.ParseStatus == rag.DocumentProcessing {
			return nil, errors.New("文档仍在处理，请先取消后重试")
		}
		if !reindex && !rag.CanRetryDocument(record) {
			return nil, errors.New("只能重试失败或已取消的文档")
		}
		if reindex {
			r.Source = record.SourceFile
			r.Retry = false
		}
	}
	if err := m.queue(ctx, []jobRecord{r}); err != nil {
		return nil, err
	}
	owned = true
	m.launch(e, []jobRecord{r}, release)
	return []Job{r.Job}, nil
}

func (m *Manager) Delete(ctx context.Context, collection, documentID string) error {
	if _, err := m.collection(collection); err != nil {
		return err
	}
	if err := m.Cancel(ctx, collection, documentID); err != nil {
		return err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return err
	}
	defer release()
	if err := e.DeleteDocument(ctx, collection, documentID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := clone(m.state)
	next.Jobs = slices.DeleteFunc(next.Jobs, func(j jobRecord) bool { return j.CollectionID == collection && j.DocumentID == documentID })
	if err := m.persist(ctx, next); err != nil {
		return err
	}
	m.state = next
	return nil
}

// PreviewDocument 展示已发布的分块摘要，正文按页读取到 UI，不返回整篇转换文本。
func (m *Manager) PreviewDocument(ctx context.Context, collection, documentID string, offset int) ([]Source, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if offset < 0 {
		return nil, errors.New("无效的分块页码")
	}
	c, err := m.collection(collection)
	if err != nil {
		return nil, err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return nil, err
	}
	defer release()
	r, err := e.GetDocument(ctx, collection, documentID)
	if err != nil {
		return nil, err
	}
	chunks, err := e.store.PageChunks(ctx, collection, documentID, offset)
	if err != nil {
		return nil, err
	}
	result := []Source{}
	for _, chunk := range chunks {
		hit := retrieval.SearchResult{CollectionID: collection, DocumentID: documentID, DocumentRevision: r.Revision, ChunkID: chunk.ID, Content: chunk.Content, ContextHeader: chunk.ContextHeader, SourceLocators: retrieval.Locators(chunk.Metadata)}
		source, err := e.source(ctx, c, hit, 1800)
		if err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, nil
}

func (m *Manager) DocumentName(ctx context.Context, collection, documentID string) (string, error) {
	if _, err := m.collection(collection); err != nil {
		return "", err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return "", err
	}
	defer release()
	r, err := e.GetDocument(ctx, collection, documentID)
	return filepath.Base(r.Name), err
}

// DownloadOriginal 的目标路径只来自桌面保存对话框；工具不暴露文件写入能力。
func (m *Manager) DownloadOriginal(ctx context.Context, collection, documentID, destination string) error {
	if _, err := m.collection(collection); err != nil {
		return err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return err
	}
	defer release()
	r, err := e.GetDocument(ctx, collection, documentID)
	if err != nil {
		return err
	}
	if _, err := e.sources.Resolve(ctx, r.SourceFile); err != nil {
		return err
	}
	input, err := e.sources.Open(r.Path)
	if err != nil {
		return err
	}
	defer input.Close()
	// 使用临时文件完成复制再替换，失败不留下半份下载。
	output, err := os.CreateTemp(filepath.Dir(destination), ".humbert-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if _, err = io.Copy(output, input); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return err
	}
	if err = output.Close(); err != nil {
		return err
	}
	return atomicfile.Replace(output.Name(), destination)
}

func (m *Manager) Stop(ctx context.Context) error {
	m.cancel()
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) Close(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		m.engine.Close()
		m.engine = nil
	}
	return nil
}

func (m *Manager) ID() string { return "knowledge" }

// logFailure 只记录脱敏错误，不把数据库 DSN、文档正文或模型密钥写入日志。
func logFailure(err error) string {
	if err == nil {
		return ""
	}
	return logging.SafeErrorText(fmt.Errorf("%w", err), 1200)
}
