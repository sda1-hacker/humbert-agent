package indexer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

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
	halfVectors, err := p.embedChangedTexts(ctx, batch, texts, embedder, opts)
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

// embedChangedTexts 复用当前文档已发布且模型档案一致的向量；比较的是完整模型输入，不是文件名或块序号。
// 不新增缓存表，也不改变版本发布事务；修改标题/正文/输入模板后只计算发生变化的输入。
func (p *Indexer) embedChangedTexts(ctx context.Context, batch rag.IngestionBatch, texts []string, embedder embedding.Embedder, opts []embedding.Option) ([]any, error) {
	// 即使全部命中，也不能绕过后来收紧的输入预算。
	if _, err := p.config.InputBudget.Plan(texts, p.config.EmbeddingBatchSize); err != nil {
		return nil, err
	}
	vectors := make(map[string]any, len(texts))
	if p.config.ProfileID != "" && len(opts) == 0 && len(texts) > 0 {
		rows, err := p.db.Query(ctx, `SELECT r.search_content,r.embedding::text
FROM retrieval_index r JOIN documents d ON d.collection_id=r.collection_id AND d.id=r.document_id
JOIN rag_collection_profiles p ON p.collection_id=d.collection_id AND p.id=d.embedding_profile_id
WHERE r.collection_id=$1 AND d.source_key=$2 AND p.profile_id=$3 AND r.enabled AND r.search_content=ANY($4::text[])`, batch.CollectionID, batch.DocumentID, p.config.ProfileID, texts)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var text, encoded string
			if err := rows.Scan(&text, &encoded); err != nil {
				rows.Close()
				return nil, err
			}
			var half pgvector.HalfVector
			if err := half.Scan(encoded); err != nil {
				rows.Close()
				return nil, err
			}
			values := half.Slice()
			vector := make([]float64, len(values))
			for i, v := range values {
				vector[i] = float64(v)
			}
			checked, err := makeHalfVector(vector, p.config.Dimensions)
			if err != nil {
				rows.Close()
				return nil, err
			}
			vectors[text] = checked
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	var missing []string
	seen := make(map[string]bool, len(texts))
	for _, text := range texts {
		if vectors[text] == nil && !seen[text] {
			seen[text] = true
			missing = append(missing, text)
		}
	}
	generated, err := embedTexts(ctx, embedder, missing, p.config.EmbeddingBatchSize, opts, p.config.InputBudget, p.config.Dimensions)
	if err != nil {
		return nil, err
	}
	for i, text := range missing {
		vectors[text] = generated[i]
	}
	result := make([]any, len(texts))
	for i, text := range texts {
		result[i] = vectors[text]
	}
	return result, ctx.Err()
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
		if err := lockAttempt(ctx, tx, batch.CollectionID, batch.DocumentID, batch.Attempt); err != nil {
			return err
		}
		// 取得版本锁后用新语句检查任务终态和截止时间，避免等锁期间的失败被旧快照漏掉。
		// 已完成版本仍可按相同快照幂等确认，失败/取消版本则必须重新预留，不能迟到复活。
		var blocked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE collection_id=$1 AND source_key=$2 AND processing_attempt=$3
AND (parse_status IN ('failed','cancelled') OR (parse_status='processing' AND processing_deadline<=clock_timestamp())))`, batch.CollectionID, batch.DocumentID, batch.Attempt).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return ErrStaleIngestion
		}
	}

	// 主表保存原件引用与处理快照，正文只进入分块；成功状态与分块和索引同时提交。
	var documentID int64
	err = tx.QueryRow(ctx, upsertIngestionDocumentSQL,
		batch.CollectionID, batch.DocumentID, batch.Title, documentMetadataJSON,
		batch.Attempt, batch.ContentHash, string(batch.ProcessConfig),
		batch.Source.URI, batch.Source.Path, batch.Source.Name, batch.Source.Type, batch.Source.Size, batch.Source.Hash,
		p.config.ProfileID,
	).Scan(&documentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleIngestion
	}
	if err != nil {
		return fmt.Errorf("upsert ingestion document %q: %w", batch.DocumentID, err)
	}

	// 替换旧分块时，外键级联删除其向量和 BM25 检索行。
	if _, err := tx.Exec(ctx, deleteDocumentChunksSQL, batch.CollectionID, documentID); err != nil {
		return fmt.Errorf("delete previous chunks for document %q: %w", batch.DocumentID, err)
	}

	// 先保存父块，满足子块 parent_chunk_id 的外键约束。
	// 数据库自增 ID 仅在事务内关联；外部索引继续使用包含版本信息的业务键。
	parentIDs := make(map[string]int64, len(preparedParents))
	for _, parent := range preparedParents {
		id, err := insertStoredChunk(ctx, tx, batch.CollectionID, documentID, nil, parent)
		if err != nil {
			return err
		}
		parentIDs[parent.record.ID] = id
	}

	// 图片块可引用正文子块，先插正文，再插图片；不要求外部调用方预先排序批次。
	order := make([]int, 0, len(preparedChildren))
	for _, image := range []bool{false, true} {
		for i, child := range preparedChildren {
			if (child.record.ChunkType != rag.ChunkTypeText) == image {
				order = append(order, i)
			}
		}
	}
	for _, i := range order {
		child := preparedChildren[i]
		var parentID *int64
		if child.record.ParentChunkID != "" {
			id, ok := parentIDs[child.record.ParentChunkID]
			if !ok {
				return fmt.Errorf("parent chunk %q is missing from ingestion", child.record.ParentChunkID)
			}
			parentID = &id
		}
		chunkID, err := insertStoredChunk(ctx, tx, batch.CollectionID, documentID, parentID, child)
		if err != nil {
			return err
		}
		// 图片识别块可关联前面已保存的正文子块，而不必额外制造一层图片父块。
		parentIDs[child.record.ID] = chunkID

		// 分离索引模式只发布原文与分块。
		if halfVectors == nil {
			continue
		}
		// 同一行同时提供 pgvector 的 embedding 与 ParadeDB 的 search_content。
		// pg_search 在当前事务中维护 BM25 索引，无需再在 Go 中单独构建倒排索引。
		if _, err := tx.Exec(ctx, insertRetrievalSQL,
			batch.CollectionID, chunkID, documentID,
			child.searchContent, halfVectors[i], child.metadataJSON,
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
func insertStoredChunk(ctx context.Context, tx pgx.Tx, collectionID string,
	documentID int64, parentID *int64, chunk preparedIngestionChunk) (int64, error) {
	record := chunk.record

	var id int64
	if err := tx.QueryRow(ctx, insertChunkSQL,
		collectionID, record.ID, documentID, record.ChunkIndex, record.ChunkType,
		record.Content, record.ContextHeader, record.StartRune, record.EndRune, parentID, chunk.metadataJSON,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert chunk %q: %w", record.ID, err)
	}

	return id, nil
}

const deleteDocumentChunksSQL = `
DELETE FROM chunks
WHERE collection_id = $1
  AND document_id = $2
`

// 插入列与上方参数按身份、正文、位置分组；业务键与数值外键分别传入，不能互换。
const insertChunkSQL = `
INSERT INTO chunks (
    collection_id,chunk_key,document_id,chunk_index,chunk_type,
    content,context_header,start_rune,end_rune,parent_chunk_id,metadata
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb)
RETURNING id
`

const insertRetrievalSQL = `
INSERT INTO retrieval_index (collection_id,chunk_id,document_id,search_content,embedding,metadata)
VALUES ($1,$2,$3,$4,$5,$6::jsonb)
`

const upsertIngestionDocumentSQL = `
INSERT INTO documents (
    collection_id,source_key,title,metadata,revision,content_hash,process_config,
    source_uri,file_path,file_name,file_type,file_size,file_hash,embedding_profile_id,
    document_type,parse_status,error_message,processed_at,processing_attempt,processing_stage,error_code
)
VALUES (
    $1,$2,$3,$4::jsonb,$5,$6,$7::jsonb,$8,$9,$10,$11,$12,$13,
    (SELECT id FROM rag_collection_profiles WHERE collection_id=$1 AND profile_id=$14),
    CASE WHEN $10='' THEN 'text' ELSE 'file' END,'completed','',NOW(),$5,'completed',''
)
ON CONFLICT (collection_id,source_key)
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
RETURNING id
`
