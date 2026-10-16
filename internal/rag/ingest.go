package rag

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

var ErrIngestLimit = errors.New("rag: ingestion limit exceeded")
var ErrIngestRunning = errors.New("rag: document ingestion is already running")
var ErrServiceClosed = errors.New("rag: service is closed")

type ingestRun struct {
	cancel   context.CancelFunc
	finished bool
	stopping int
}

// beginIngest 只允许同一文档同时运行一个任务，取消不会误伤随后开始的新导入。
func (s *Service) beginIngest(ctx context.Context, req IngestRequest) (context.Context, func(), error) {
	ctx, endRequest, err := s.beginRequest(ctx, s.config.Ingest.Timeout)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	id := req.DocumentID
	if id == "" {
		id = req.Source.URI
	}
	key := req.CollectionID + "\x00" + id
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		endRequest()
		return nil, nil, ErrServiceClosed
	}
	if s.ingesting[key] != nil {
		s.mu.Unlock()
		cancel()
		endRequest()
		return nil, nil, ErrIngestRunning
	}
	run := &ingestRun{cancel: cancel}
	s.ingesting[key] = run
	s.mu.Unlock()
	finish := func() {
		cancel()
		s.mu.Lock()
		run.finished = true
		if run.stopping == 0 {
			delete(s.ingesting, key)
		}
		s.mu.Unlock()
		endRequest()
	}
	select {
	case s.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.slots
			finish()
			return nil, nil, err
		}
		return ctx, func() { <-s.slots; finish() }, nil
	case <-ctx.Done():
		finish()
		return nil, nil, ctx.Err()
	}
}

// 停止与数据库作废之间保留文档占位，防止旧任务退出后新导入被迟到的取消操作误伤。
func (s *Service) stopIngest(collectionID, documentID string) func() {
	key := collectionID + "\x00" + documentID
	s.mu.Lock()
	run := s.ingesting[key]
	if run == nil {
		run = &ingestRun{cancel: func() {}, finished: true}
		s.ingesting[key] = run
	}
	run.cancel()
	run.stopping++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		run.stopping--
		if run.finished && run.stopping == 0 {
			delete(s.ingesting, key)
		}
		s.mu.Unlock()
	}
}

// beginRequest 将接收请求与关闭服务放在同一把锁下，防止 Wait 开始后再添加任务。
// 所有访问存储的业务入口均登记，结束时注销监听并释放等待计数。
func (s *Service) beginRequest(ctx context.Context, timeout time.Duration) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, ErrServiceClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancelCause := context.WithCancelCause(ctx)
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	stop := context.AfterFunc(s.stopping, func() { cancelCause(ErrServiceClosed) })
	s.active.Add(1)
	return ctx, func() { stop(); cancel(); cancelCause(context.Canceled); s.active.Done() }, nil
}

// fitInputs 在版本 ID 分配前按完整检索文本进一步切分，保留父块关系和原文字符坐标。
// 标题本身超出预算时明确报错，不静默截断原文或来源。
func (s *Service) fitInputs(ctx context.Context, batch *IngestionBatch) error {
	budget := s.config.InputBudget.Effective()
	limit := min(budget.MaxInputTokens, budget.MaxBatchTokens)
	layout, err := chunker.NewLayout(ctx, batch.Markdown)
	if err != nil {
		return err
	}
	update := func(record ChunkRecord, chunk chunker.Chunk) ChunkRecord {
		record.Content, record.StartRune, record.EndRune = chunk.Content, chunk.Start, chunk.End
		record.Metadata = retrieval.CloneMetadata(record.Metadata)
		record.Metadata[retrieval.MetaChunkStart], record.Metadata[retrieval.MetaChunkEnd] = chunk.Start, chunk.End
		if len(batch.SourceBlocks) > 0 {
			record.Metadata[documenttext.MetaSourceBlocks] = batch.SourceBlocks
		}
		documenttext.AttachLocators(record.Metadata, chunk.Start, chunk.End)
		if locators, ok := record.Metadata[documenttext.MetaSourceLocators].([]documenttext.SourceLocator); ok {
			for i := range locators {
				locators[i].Quote = layout.Quote(locators[i].Start, locators[i].End)
			}
		}
		return record
	}
	// 父块和子块使用同一份原文结构索引；扩展原子边界后，子块仍处于对应父块范围内。
	for i, parent := range batch.Parents {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch.Parents[i] = update(parent, layout.Normalize(chunker.Chunk{Start: parent.StartRune, End: parent.EndRune, ContextHeader: parent.ContextHeader}))
	}
	out := make([]ChunkRecord, 0, len(batch.Children))
	for _, original := range batch.Children {
		fits := func(chunk chunker.Chunk) (bool, error) {
			record := original
			record.Content, record.StartRune, record.EndRune = chunk.Content, chunk.Start, chunk.End
			count := budget.CountTokens(indexDocument(*batch, record, s.config.InputBuilder).Content)
			if count < 0 {
				return false, errors.New("rag: tokenizer returned a negative count")
			}
			return count <= limit, nil
		}
		emit := func(chunk chunker.Chunk) error {
			// 软切分可能多次落入同一个原子区域；修复边界后不重复保存完全相同的块。
			if len(out) > 0 {
				last := out[len(out)-1]
				if last.StartRune == chunk.Start && last.EndRune == chunk.End && last.ContextHeader == original.ContextHeader && last.ParentChunkID == original.ParentChunkID {
					return nil
				}
			}
			if len(out) >= s.config.Ingest.MaxChunks {
				return fmt.Errorf("%w: more than %d chunks", ErrIngestLimit, s.config.Ingest.MaxChunks)
			}
			record := update(original, chunk)
			record.ChunkIndex = len(out)
			record.ID = fmt.Sprintf("%s#chunk-%06d", batch.DocumentID, record.ChunkIndex)
			record.Metadata[retrieval.MetaChunkIndex] = record.ChunkIndex
			out = append(out, record)
			return nil
		}
		chunk := chunker.Chunk{Content: original.Content, ContextHeader: original.ContextHeader, Start: original.StartRune, End: original.EndRune}
		if err := layout.Fit(ctx, chunk, fits, emit); err != nil {
			if errors.Is(err, chunker.ErrStructureBudget) {
				return fmt.Errorf("%w: 结构块或标题超过完整向量输入预算（%d）；请增大预算或拆分源文档: %w", ErrIngestLimit, limit, err)
			}
			return err
		}
	}
	batch.Children = out
	return nil
}
