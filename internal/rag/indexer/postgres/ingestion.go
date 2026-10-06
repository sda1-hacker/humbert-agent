package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
)

// preparedIngestionChunk 是数据库写入前的内部模型。
type preparedIngestionChunk struct {
	record                      rag.ChunkRecord
	metadataJSON, searchContent string
}

// storeDocument 共用唯一的文档替换事务，向量化仅由原生 Store 触发。
func (p *Indexer) storeDocument(ctx context.Context, batch rag.IngestionBatch, texts []string, embedder embedding.Embedder, opts []embedding.Option) error {
	if err := rag.ValidateIngestionBatch(batch); err != nil {
		return err
	}
	if len(texts) != len(batch.Children) {
		return ErrIncompleteSource
	}
	if p.config.VersionedDocuments && batch.Attempt == 0 {
		return errors.New("rag: reserve a document attempt before ingestion")
	}
	if err := p.ensureProfile(ctx, batch.CollectionID); err != nil {
		return err
	}
	var err error
	batch.ProcessConfig, err = processSnapshot(batch, texts, "index_input_hash")
	if err != nil {
		return err
	}
	parents, err := prepareStoredChunks(batch.Parents)
	if err != nil {
		return err
	}
	children, err := prepareStoredChunks(batch.Children)
	if err != nil {
		return err
	}
	vectors, err := embedTexts(ctx, embedder, texts, p.config.EmbeddingBatchSize, opts, p.config.InputBudget)
	if err != nil {
		return err
	}
	halfVectors := make([]any, len(vectors))
	for i, v := range vectors {
		half, err := makeHalfVector(v)
		if err != nil {
			return err
		}
		halfVectors[i] = half
		children[i].searchContent = texts[i]
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.replaceStoredDocument(ctx, batch, parents, children, halfVectors)
}

// PublishDocument 只保存权威原文和分块，可与外部向量库、BM25 库组合。
// 数据发布前仍在事务内校验预留版本；此入口不调用模型、不写 retrieval_index。
func (p *Indexer) PublishDocument(ctx context.Context, batch rag.IngestionBatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rag.ValidateIngestionBatch(batch); err != nil {
		return err
	}
	if batch.Attempt <= 0 {
		return errors.New("rag: reserve a document attempt before publication")
	}
	if err := p.ensureProfile(ctx, batch.CollectionID); err != nil {
		return err
	}
	parents, err := prepareStoredChunks(batch.Parents)
	if err != nil {
		return err
	}
	children, err := prepareStoredChunks(batch.Children)
	if err != nil {
		return err
	}
	batch.ProcessConfig, err = processSnapshot(batch, nil, "document_input_hash")
	if err != nil {
		return err
	}
	return p.replaceStoredDocument(ctx, batch, parents, children, nil)
}

// 同库原子入库和分离索引发布共用同一套事务、版本检查和分块保存规则。
func (p *Indexer) replaceStoredDocument(ctx context.Context, batch rag.IngestionBatch,
	preparedParents, preparedChildren []preparedIngestionChunk, halfVectors []any) error {
	if batch.ContentHash == "" {
		batch.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(batch.Markdown)))
	}
	documentMetadataJSON, err := marshalMetadata(batch.Metadata)
	if err != nil {
		return fmt.Errorf("marshal document metadata: %w", err)
	}

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ingestion transaction: %w", err)
	}

	defer rollbackTransaction(ctx, tx)
	if batch.Attempt > 0 {
		tag, err := tx.Exec(ctx, `UPDATE rag_document_heads SET attempt=attempt WHERE collection_id=$1 AND document_id=$2 AND attempt=$3 AND NOT deleted`, batch.CollectionID, batch.DocumentID, batch.Attempt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrStaleIngestion
		}
	}

	// -------------------------------------------------------------------------
	// 完整 Document 原文在这里正式持久化。
	//
	// 这也是为什么前面不能从 Chunks 反向重建 Markdown。
	// -------------------------------------------------------------------------

	tag, err := tx.Exec(
		ctx,
		upsertIngestionDocumentSQL,
		batch.CollectionID,
		batch.DocumentID,
		batch.Title,
		batch.Markdown,
		documentMetadataJSON,
		batch.Attempt,
		batch.ContentHash,
		string(batch.ProcessConfig),
	)
	if err != nil {
		return fmt.Errorf("upsert ingestion document %q: %w", batch.DocumentID, err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleIngestion
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

		if halfVectors == nil {
			continue
		} // 分离索引模式只发布原文与分块。
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

// prepareStoredChunks 预先序列化分块元数据，避免进入事务后才发现编码错误。
func prepareStoredChunks(records []rag.ChunkRecord) ([]preparedIngestionChunk, error) {
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

// insertStoredChunk 在当前事务内保存分块；检索索引仅由子块生成。
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

const upsertIngestionDocumentSQL = `
INSERT INTO documents (
    collection_id,
    id,
    title,
    markdown,
    metadata,
    revision,
    content_hash,
    process_config
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5::jsonb,
    $6,
    $7,
    $8::jsonb
)
ON CONFLICT (
    collection_id,
    id
)
DO UPDATE SET
    title = EXCLUDED.title,
    markdown = EXCLUDED.markdown,
    metadata = EXCLUDED.metadata,
    revision = EXCLUDED.revision,
    content_hash = EXCLUDED.content_hash,
    process_config = EXCLUDED.process_config,
    updated_at = NOW()
WHERE (documents.revision=0 AND EXCLUDED.revision=0)
   OR documents.revision<EXCLUDED.revision
   OR (documents.revision=EXCLUDED.revision AND documents.content_hash=EXCLUDED.content_hash AND documents.process_config=EXCLUDED.process_config)
`
