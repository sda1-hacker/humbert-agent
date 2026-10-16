package indexer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"

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
	halfVectors, err := embedTexts(ctx, embedder, texts, p.config.EmbeddingBatchSize, opts, p.config.InputBudget, p.config.Dimensions)
	if err != nil {
		return &rag.IngestionError{Stage: rag.StageEmbedding, Err: err}
	}
	for i := range children {
		children[i].searchContent = texts[i]
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.replaceStoredDocument(ctx, batch, parents, children, halfVectors); err != nil {
		return &rag.IngestionError{Stage: rag.StagePublish, Err: err}
	}
	return nil
}

// PublishDocument 只保存文档元数据和权威分块，可与外部向量库、BM25 库组合。
// 数据发布前仍在事务内校验预留版本；此入口不调用模型、不写 retrieval_index。
func (p *DocumentStore) PublishDocument(ctx context.Context, batch rag.IngestionBatch) error {
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
func (p *DocumentStore) replaceStoredDocument(ctx context.Context, batch rag.IngestionBatch,
	preparedParents, preparedChildren []preparedIngestionChunk, halfVectors []any) error {
	if batch.ContentHash == "" {
		batch.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(batch.Markdown)))
	}
	metadata := batch.Metadata
	if batch.Source.Path != "" {
		// 已提升为独立管理列的文件信息不再在主表 JSON 中重复保存；分块仍保留来源元数据。
		metadata = maps.Clone(metadata)
		for _, key := range []string{"_source", "file_name", "file_ext", "file_size", "rag_source_content_hash"} {
			delete(metadata, key)
		}
	}
	documentMetadataJSON, err := marshalMetadata(metadata)
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
		// 取得版本锁后用新语句检查任务终态和截止时间，避免等锁期间的失败被旧快照漏掉。
		// 已完成版本仍可按相同快照幂等确认，失败/取消版本则必须重新预留，不能迟到复活。
		var blocked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE collection_id=$1 AND id=$2 AND processing_attempt=$3
AND (parse_status IN ('failed','cancelled') OR (parse_status='processing' AND processing_deadline<=clock_timestamp())))`, batch.CollectionID, batch.DocumentID, batch.Attempt).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return ErrStaleIngestion
		}
	}

	// 主表保存原件引用与处理快照，正文只进入分块；成功状态与分块和索引同时提交。
	tag, err := tx.Exec(
		ctx,
		upsertIngestionDocumentSQL,
		batch.CollectionID,
		batch.DocumentID,
		batch.Title,
		documentMetadataJSON,
		batch.Attempt,
		batch.ContentHash,
		string(batch.ProcessConfig),
		batch.Source.URI, batch.Source.Path, batch.Source.Name, batch.Source.Type, batch.Source.Size, batch.Source.Hash,
		p.config.ProfileID,
	)
	if err != nil {
		return fmt.Errorf("upsert ingestion document %q: %w", batch.DocumentID, err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleIngestion
	}

	// 替换旧分块时，外键级联删除其向量和 BM25 检索行。
	if _, err := tx.Exec(ctx, deleteDocumentChunksSQL, batch.CollectionID, batch.DocumentID); err != nil {
		return fmt.Errorf("delete previous chunks for document %q: %w", batch.DocumentID, err)
	}

	// 先保存父块，满足子块 parent_chunk_id 的外键约束。
	for _, parent := range preparedParents {
		if err := insertStoredChunk(ctx, tx, batch.CollectionID, parent); err != nil {
			return err
		}
	}

	for i, child := range preparedChildren {
		if err := insertStoredChunk(ctx, tx, batch.CollectionID, child); err != nil {
			return err
		}

		// 分离索引模式只发布原文与分块。
		if halfVectors == nil {
			continue
		}
		// 同一行同时提供 pgvector 的 embedding 与 ParadeDB 的 search_content。
		// pg_search 在当前事务中维护 BM25 索引，无需再在 Go 中单独构建倒排索引。
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
			return fmt.Errorf("insert retrieval index for child %q: %w", child.record.ID, err)
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
			return nil, fmt.Errorf("marshal metadata for chunk %q: %w", record.ID, err)
		}

		result = append(result, preparedIngestionChunk{record: record, metadataJSON: metadataJSON})
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

const deleteDocumentChunksSQL = `
DELETE FROM chunks
WHERE collection_id = $1
  AND document_id = $2
`

const insertChunkSQL = `
INSERT INTO chunks (
    collection_id,
    id,
    document_id,
    chunk_index,
    chunk_type,
    content,
    context_header,
    start_rune,
    end_rune,
    parent_chunk_id,
    metadata
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb
)
`

const insertRetrievalSQL = `
INSERT INTO retrieval_index (
    collection_id,
    chunk_id,
    document_id,
    search_content,
    embedding,
    metadata
)
VALUES (
    $1, $2, $3, $4, $5, $6::jsonb
)
`

const upsertIngestionDocumentSQL = `
INSERT INTO documents (
    collection_id,id,title,metadata,revision,content_hash,process_config,
    source_uri,file_path,file_name,file_type,file_size,file_hash,embedding_profile_id,
    document_type,parse_status,error_message,processed_at,processing_attempt,processing_stage,error_code
)
VALUES (
    $1,$2,$3,$4::jsonb,$5,$6,$7::jsonb,$8,$9,$10,$11,$12,$13,$14,
    CASE WHEN $10='' THEN 'text' ELSE 'file' END,'completed','',NOW(),$5,'completed',''
)
ON CONFLICT (
    collection_id,
    id
)
DO UPDATE SET
    title = EXCLUDED.title,
    metadata = EXCLUDED.metadata,
    revision = EXCLUDED.revision,
    content_hash = EXCLUDED.content_hash,
    process_config = EXCLUDED.process_config,
    source_uri=EXCLUDED.source_uri,file_path=EXCLUDED.file_path,file_name=EXCLUDED.file_name,
    file_type=EXCLUDED.file_type,file_size=EXCLUDED.file_size,file_hash=EXCLUDED.file_hash,
    embedding_profile_id=EXCLUDED.embedding_profile_id,document_type=EXCLUDED.document_type,
    parse_status='completed',error_message='',error_code='',processed_at=NOW(),
    processing_attempt=EXCLUDED.processing_attempt,processing_stage='completed',processing_deadline=NULL,
    updated_at = NOW()
WHERE (documents.revision=0 AND EXCLUDED.revision=0)
   OR documents.revision<EXCLUDED.revision
   OR (documents.revision=EXCLUDED.revision AND documents.content_hash=EXCLUDED.content_hash AND documents.process_config=EXCLUDED.process_config)
`
