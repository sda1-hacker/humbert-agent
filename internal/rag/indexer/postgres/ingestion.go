package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
)

var _ application.IngestionStore = (*Indexer)(nil)

var (
	ErrInvalidIngestionBatch = errors.New("postgres indexer: invalid ingestion batch")
	ErrUnknownParentChunk    = errors.New("postgres indexer: child references unknown parent chunk")
)

// preparedIngestionChunk 是 ReplaceDocument 真正落库前的内部模型。
type preparedIngestionChunk struct {
	record application.ChunkRecord

	metadataJSON  string
	searchContent string
}

// ReplaceDocument 原子替换一个完整 Document 的：
//
//	documents
//	parents
//	children
//	retrieval_index
//
// -----------------------------------------------------------------------------
// 非常重要的执行顺序:
//
//  1. 在事务外 Build SearchContent
//
//  2. 在事务外 Embed Children
//
//  3. BEGIN
//
//  4. UPSERT Document + Markdown
//
//  5. DELETE old chunks
//
//  6. INSERT Parents
//
//  7. INSERT Children
//
//  8. INSERT Child retrieval_index
//
//  9. COMMIT
//
// Embedding 不放在数据库事务里，
// 避免远程模型调用长期占用 PostgreSQL Transaction。
func (p *Indexer) ReplaceDocument(ctx context.Context, batch application.IngestionBatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := validateIngestionBatch(batch); err != nil {
		return err
	}

	if p.config.Embedder == nil {
		return ErrMissingEmbedder
	}

	preparedParents, err := prepareStoredChunks(batch.Parents)
	if err != nil {
		return err
	}

	preparedChildren, texts, err := p.prepareSearchableChunks(batch)
	if err != nil {
		return err
	}

	// -------------------------------------------------------------------------
	// 只给 Child 做 Embedding。
	//
	// Parent 不进入 retrieval_index。
	// -------------------------------------------------------------------------

	vectors, err := embedTexts(
		ctx,
		p.config.Embedder,
		texts,
		p.config.EmbeddingBatchSize,
		nil,
	)

	if err != nil {
		return fmt.Errorf("embed ingestion children: %w", err)
	}

	if len(vectors) != len(preparedChildren) {
		return fmt.Errorf(
			"%w: expected %d child vectors, got %d",
			ErrInvalidEmbedding,
			len(preparedChildren),
			len(vectors),
		)
	}

	halfVectors := make([]any, len(vectors))

	for i, vector := range vectors {
		half, err := makeHalfVector(vector)
		if err != nil {
			return fmt.Errorf("child %q: %w", preparedChildren[i].record.ID, err)
		}

		halfVectors[i] = half
	}

	documentMetadataJSON, err := marshalMetadata(batch.Metadata)
	if err != nil {
		return fmt.Errorf("marshal document metadata: %w", err)
	}

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ingestion transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// -------------------------------------------------------------------------
	// 完整 Document 原文在这里正式持久化。
	//
	// 这也是为什么前面不能从 Chunks 反向重建 Markdown。
	// -------------------------------------------------------------------------

	if _, err := tx.Exec(
		ctx,
		upsertIngestionDocumentSQL,
		batch.CollectionID,
		batch.DocumentID,
		batch.Title,
		batch.Markdown,
		documentMetadataJSON,
	); err != nil {
		return fmt.Errorf("upsert ingestion document %q: %w", batch.DocumentID, err)
	}

	// 删除旧 Chunk。
	//
	// retrieval_index 通过 FK ON DELETE CASCADE 一起删除。
	if _, err := tx.Exec(
		ctx,
		deleteDocumentChunksSQL,
		batch.CollectionID,
		batch.DocumentID,
	); err != nil {
		return fmt.Errorf("delete previous chunks for document %q: %w", batch.DocumentID, err)
	}

	// Parent 必须先插入。
	//
	// 因为 Child.parent_chunk_id 有 FK 指向 chunks。
	for _, parent := range preparedParents {
		if err := insertStoredChunk(ctx, tx, batch.CollectionID, parent); err != nil {
			return err
		}
	}

	// Child 后插入。
	for i, child := range preparedChildren {
		if err := insertStoredChunk(ctx, tx, batch.CollectionID, child); err != nil {
			return err
		}

		if _, err := tx.Exec(
			ctx,
			insertRetrievalSQL,
			batch.CollectionID,
			child.record.ID,
			child.record.DocumentID,
			child.searchContent,
			halfVectors[i],
			child.metadataJSON,
		); err != nil {
			return fmt.Errorf(
				"insert retrieval index for child %q: %w",
				child.record.ID,
				err,
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ingestion transaction: %w", err)
	}

	return nil
}

func (p *Indexer) prepareSearchableChunks(
	batch application.IngestionBatch,
) ([]preparedIngestionChunk, []string, error) {
	prepared, err := prepareStoredChunks(batch.Children)
	if err != nil {
		return nil, nil, err
	}

	texts := make([]string, 0, len(prepared))

	for i := range prepared {
		record := prepared[i].record

		metadata := cloneIngestionMetadata(record.Metadata)

		// SearchContent Builder 默认会寻找：
		//
		//     title
		//     _title
		//     file_name
		//
		// 如果 Loader metadata 没有标题，
		// Application Batch.Title 作为最终 fallback。
		if _, exists := metadata["title"]; !exists {
			if _, exists := metadata["_title"]; !exists && batch.Title != "" {
				metadata["_title"] = batch.Title
			}
		}

		doc := &schema.Document{
			ID:       record.ID,
			Content:  record.Content,
			MetaData: metadata,
		}

		searchContent := p.config.SearchBuilder.Build(doc)

		if strings.TrimSpace(searchContent) == "" {
			return nil, nil, fmt.Errorf(
				"%w for child %q",
				ErrEmptySearchContent,
				record.ID,
			)
		}

		prepared[i].searchContent = searchContent
		texts = append(texts, searchContent)
	}

	return prepared, texts, nil
}

func prepareStoredChunks(records []application.ChunkRecord) ([]preparedIngestionChunk, error) {
	result := make([]preparedIngestionChunk, 0, len(records))

	for _, record := range records {
		metadataJSON, err := marshalMetadata(record.Metadata)
		if err != nil {
			return nil, fmt.Errorf(
				"marshal metadata for chunk %q: %w",
				record.ID,
				err,
			)
		}

		result = append(result, preparedIngestionChunk{
			record:       record,
			metadataJSON: metadataJSON,
		})
	}

	return result, nil
}

func validateIngestionBatch(batch application.IngestionBatch) error {
	if strings.TrimSpace(batch.CollectionID) == "" {
		return fmt.Errorf("%w: missing collection id", ErrInvalidIngestionBatch)
	}

	if strings.TrimSpace(batch.DocumentID) == "" {
		return fmt.Errorf("%w: missing document id", ErrInvalidIngestionBatch)
	}

	ids := make(map[string]struct{}, len(batch.Parents)+len(batch.Children))
	parentIDs := make(map[string]struct{}, len(batch.Parents))

	validateRecord := func(record application.ChunkRecord) error {
		if strings.TrimSpace(record.ID) == "" {
			return fmt.Errorf("%w: empty chunk id", ErrInvalidIngestionBatch)
		}

		if record.DocumentID != batch.DocumentID {
			return fmt.Errorf(
				"%w: chunk %q belongs to document %q, batch document is %q",
				ErrInvalidIngestionBatch,
				record.ID,
				record.DocumentID,
				batch.DocumentID,
			)
		}

		if record.ChunkIndex < 0 {
			return fmt.Errorf("%w: chunk %q has negative index", ErrInvalidIngestionBatch, record.ID)
		}

		if record.StartRune < 0 || record.EndRune < record.StartRune {
			return fmt.Errorf(
				"%w: chunk %q has invalid rune range [%d,%d)",
				ErrInvalidIngestionBatch,
				record.ID,
				record.StartRune,
				record.EndRune,
			)
		}

		if _, exists := ids[record.ID]; exists {
			return fmt.Errorf("%w: duplicate chunk id %q", ErrInvalidIngestionBatch, record.ID)
		}

		ids[record.ID] = struct{}{}
		return nil
	}

	for _, parent := range batch.Parents {
		if parent.ChunkType != application.ChunkTypeParentText {
			return fmt.Errorf(
				"%w: parent %q has chunk type %q",
				ErrInvalidIngestionBatch,
				parent.ID,
				parent.ChunkType,
			)
		}

		if err := validateRecord(parent); err != nil {
			return err
		}

		parentIDs[parent.ID] = struct{}{}
	}

	for _, child := range batch.Children {
		if child.ChunkType != application.ChunkTypeText {
			return fmt.Errorf(
				"%w: child %q has chunk type %q",
				ErrInvalidIngestionBatch,
				child.ID,
				child.ChunkType,
			)
		}

		if err := validateRecord(child); err != nil {
			return err
		}

		if child.ParentChunkID == "" {
			continue
		}

		if _, exists := parentIDs[child.ParentChunkID]; !exists {
			return fmt.Errorf(
				"%w: child %q -> %q",
				ErrUnknownParentChunk,
				child.ID,
				child.ParentChunkID,
			)
		}
	}

	return nil
}

func insertStoredChunk(
	ctx context.Context,
	tx transaction,
	collectionID string,
	chunk preparedIngestionChunk,
) error {
	record := chunk.record

	if _, err := tx.Exec(
		ctx,
		insertChunkSQL,
		collectionID,
		record.ID,
		record.DocumentID,
		record.ChunkIndex,
		record.ChunkType,
		record.Content,
		record.ContextHeader,
		record.StartRune,
		record.EndRune,
		nullableString(record.ParentChunkID),
		chunk.metadataJSON,
	); err != nil {
		return fmt.Errorf("insert chunk %q: %w", record.ID, err)
	}

	return nil
}

func cloneIngestionMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return make(map[string]any)
	}

	dst := make(map[string]any, len(src)+1)

	for key, value := range src {
		dst[key] = value
	}

	return dst
}

const upsertIngestionDocumentSQL = `
INSERT INTO documents (
    collection_id,
    id,
    title,
    markdown,
    metadata
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5::jsonb
)
ON CONFLICT (
    collection_id,
    id
)
DO UPDATE SET
    title = EXCLUDED.title,
    markdown = EXCLUDED.markdown,
    metadata = EXCLUDED.metadata,
    updated_at = NOW()
`
