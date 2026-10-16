package rag

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// storeBatch 复用原生 Store；分离索引先确认可见性，再发布正文，失败时安全补偿。
func (s *Service) storeBatch(ctx context.Context, batch IngestionBatch) (ids []string, warnings []string, err error) {
	var previous []ChunkRecord
	started, published := false, false
	stage := StageIndex
	advance := func(next string) error {
		stage = next
		if s.documents != nil {
			return s.documents.AdvanceDocument(ctx, batch.CollectionID, batch.DocumentID, batch.Attempt, next)
		}
		return ctx.Err()
	}
	defer func() {
		if recover() != nil {
			if published {
				warnings = append(warnings, "文档已发布，旧索引清理发生内部异常")
			} else {
				err = &IngestionError{Stage: stage, Err: errors.New("rag: 索引组件发生内部异常")}
			}
		}
		if err != nil {
			var staged *IngestionError
			if !errors.As(err, &staged) {
				err = &IngestionError{Stage: stage, Err: err}
			}
		}
		if err == nil || s.publisher == nil || !started || published {
			return
		}
		// 网络错误可能发生在提交之后，先锁定版本确认未发布，禁止直接删除。
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), ragconfig.CleanupTimeout)
		defer cancel()
		discarded, cleanupErr := s.discardAttempt(cleanup, batch.CollectionID, batch.DocumentID, batch.Attempt)
		if cleanupErr == nil && discarded {
			cleanupErr = s.removeIndex(cleanup, batch.CollectionID, chunkIDs(batch.Children))
		}
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("clean up unpublished index: %w", cleanupErr))
		}
	}()
	if s.publisher != nil {
		previous, err = s.chunks.ListChunks(ctx, batch.CollectionID, batch.DocumentID)
		if err != nil {
			return nil, nil, err
		}
	}
	started = true
	ids, err = s.indexer.Store(ctx, IndexDocuments(batch, s.config.InputBuilder), indexer.WithIndex(batch.CollectionID), WithIngestionBatch(batch))
	if err != nil || s.publisher == nil {
		return ids, nil, err
	}
	if err = ctx.Err(); err == nil {
		err = validateStoredIDs(batch.Children, ids)
	}
	if err == nil && s.beforePublish != nil {
		if err = advance(StageReady); err == nil {
			err = s.beforePublish(ctx, batch)
		}
	}
	if err == nil {
		if err = advance(StagePublish); err == nil {
			err = s.publisher.PublishDocument(ctx, batch)
			published = err == nil
		}
	}
	if err == nil && len(previous) > 0 {
		// 版本化 ID 不会与新版本重合；替换成功后清理旧向量，防止候选池被长期占用。
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), ragconfig.CleanupTimeout)
		defer cancel()
		if cleanupErr := s.removeIndex(cleanup, batch.CollectionID, chunkIDs(previous)); cleanupErr != nil {
			warnings = append(warnings, "文档已发布，旧索引清理失败: "+logging.SafeErrorText(cleanupErr, 1024))
		}
	}
	return ids, warnings, err
}

func chunkIDs(chunks []ChunkRecord) []string {
	ids := make([]string, len(chunks))
	for i, chunk := range chunks {
		ids[i] = chunk.ID
	}
	return ids
}

// qualifyChunkIDs 让不同版本的分块拥有独立 ID。
// 新版本在外部索引中写入失败时，不会覆盖仍可检索的旧版本。
func qualifyChunkIDs(batch *IngestionBatch) {
	// 某些原生组件在配置中固定物理集合，不能仅依靠 WithIndex 隔离分块主键。
	// 将逻辑知识库加入身份，避免两个知识库内相同文档 ID/版本互相覆盖。
	namespace := sha256.Sum256([]byte(batch.CollectionID))
	prefix := fmt.Sprintf("%s#c%x#v%d", batch.DocumentID, namespace[:16], batch.Attempt)
	parents := make(map[string]string, len(batch.Parents))
	for i := range batch.Parents {
		parent := &batch.Parents[i]
		id := fmt.Sprintf("%s#parent-%06d", prefix, parent.ChunkIndex)
		parents[parent.ID], parent.ID = id, id
	}
	for i := range batch.Children {
		child := &batch.Children[i]
		child.ID = fmt.Sprintf("%s#chunk-%06d", prefix, child.ChunkIndex)
		if child.ParentChunkID != "" {
			child.ParentChunkID = parents[child.ParentChunkID]
			child.Metadata[retrieval.MetaParentChunkID] = child.ParentChunkID
		}
	}
}

// validateStoredIDs 检查索引器返回的 ID 与全部子块一一对应，拒绝改名或重复。
func validateStoredIDs(chunks []ChunkRecord, ids []string) error {
	if len(chunks) != len(ids) {
		return fmt.Errorf("rag: indexer returned %d ids for %d chunks", len(ids), len(chunks))
	}
	expected := make(map[string]bool, len(chunks))
	for _, chunk := range chunks {
		expected[chunk.ID] = true
	}
	for _, id := range ids {
		if !expected[id] {
			return fmt.Errorf("rag: indexer must preserve chunk ids for document publication")
		}
		delete(expected, id)
	}
	return nil
}

// IngestionOptions 补充 Eino Store 未定义的原件引用、处理快照和父块信息。
// 它通过官方实现专属 Option 传递，不会成为外部索引需要序列化的元数据。
type IngestionOptions struct{ Batch *IngestionBatch }

// WithIngestionBatch 通过 Eino 实现专属选项传递完整文档批次。
func WithIngestionBatch(batch IngestionBatch) indexer.Option {
	return indexer.WrapImplSpecificOptFn(func(o *IngestionOptions) { o.Batch = &batch })
}

// IndexDocuments 只把子块交给索引组件，所有后端接收相同的检索文本。
// Content 是标题、面包屑和正文组成的检索表示；权威分块单独保存供引用和评测使用。
func IndexDocuments(batch IngestionBatch, builder ragconfig.SearchText) []*schema.Document {
	docs := make([]*schema.Document, 0, len(batch.Children))
	for _, c := range batch.Children {
		docs = append(docs, indexDocument(batch, c, builder))
	}
	return docs
}

// indexDocument 共用完整输入构造规则，预算检查和真正写入不能使用不同的标题/元数据。
func indexDocument(batch IngestionBatch, c ChunkRecord, builder ragconfig.SearchText) *schema.Document {
	m := retrieval.CloneMetadata(c.Metadata)
	m[retrieval.MetaCollectionID], m[retrieval.MetaDocumentID] = batch.CollectionID, batch.DocumentID
	m[retrieval.MetaRevision] = strconv.FormatInt(batch.Attempt, 10)
	m[retrieval.MetaRawContent], m[retrieval.MetaContextHeader] = c.Content, c.ContextHeader
	m[retrieval.MetaChunkIndex], m[retrieval.MetaChunkStart], m[retrieval.MetaChunkEnd] = c.ChunkIndex, c.StartRune, c.EndRune
	m[retrieval.MetaParentChunkID] = c.ParentChunkID
	if _, ok := m["title"]; !ok {
		if _, ok := m["_title"]; !ok {
			m["_title"] = batch.Title
		}
	}
	d := &schema.Document{ID: c.ID, Content: c.Content, MetaData: m}
	d.Content = retrieval.BuildInput(builder, d)
	return d
}
