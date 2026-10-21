package knowledge

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/jackc/pgx/v5"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
)

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
