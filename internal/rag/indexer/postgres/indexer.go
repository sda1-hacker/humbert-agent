package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
	"github.com/sda1-hacker/humbert-agent/internal/rag/embeddinginput"
	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

// 编译期检查。
var _ indexer.Indexer = (*Indexer)(nil)

const (
	DefaultEmbeddingBatchSize = 64

	metaSourceDocumentID = "rag_source_document_id"
	metaChunkIndex       = "rag_chunk_index"
	metaChunkStart       = "rag_chunk_start"
	metaChunkEnd         = "rag_chunk_end"
	metaContextHeader    = "rag_context_header"

	// 下面两个 key 当前 Chunk Transformer 还没有写，
	// 但数据库模型已经为 Parent-Child 持久化预留。
	metaChunkType     = "rag_chunk_type"
	metaParentChunkID = "rag_parent_chunk_id"
)

var (
	ErrMissingCollectionID     = errors.New("postgres indexer: missing collection id")
	ErrMissingEmbedder         = errors.New("postgres indexer: missing embedder")
	ErrMissingChunkID          = errors.New("postgres indexer: missing chunk document id")
	ErrMissingSourceDocumentID = errors.New("postgres indexer: missing source document id")
	ErrEmptySearchContent      = errors.New("postgres indexer: empty search content")
	ErrSubIndexesUnsupported   = errors.New("postgres indexer: sub indexes are not supported")
	ErrInvalidEmbedding        = errors.New("postgres indexer: invalid embedding")
	ErrIncompleteSource        = errors.New("postgres indexer: Store requires a complete source envelope and all flat chunks; use ReplaceDocument for parent-child ingestion")
)

// Config 是 PostgreSQL Indexer 的长期默认配置。
//
// CollectionID:
//
//	默认 collection。
//
//	Eino Store 调用时可以通过：
//
//	    indexer.WithIndex("another-collection")
//
//	临时覆盖。
//
// Embedder:
//
//	默认 Embedding 组件。
//
//	也可以通过：
//
//	    indexer.WithEmbedding(...)
//
//	调用级覆盖。
//
// EmbeddingBatchSize:
//
//	单次 EmbedStrings 发送多少 SearchContent。
//
// SearchBuilder:
//
//	决定最终向量化/BM25 使用什么文本。
type Config struct {
	ProfileID          string
	ProfileJSON        string
	VersionedDocuments bool
	CollectionID       string
	Embedder           embedding.Embedder
	EmbeddingBatchSize int
	InputBudget        embeddinginput.Budget
	SearchBuilder      searchcontent.Builder
}

// DefaultConfig 返回 PostgreSQL Indexer 推荐默认值。
func DefaultConfig() Config {
	return Config{
		EmbeddingBatchSize: DefaultEmbeddingBatchSize,
		SearchBuilder:      searchcontent.DefaultBuilder(),
	}
}

// storeOptions 是 PGIndexer 自己的调用级配置。
//
// Eino 公共 Indexer Option 已经负责：
//
//	WithIndex
//	WithEmbedding
//	WithSubIndexes
//
// 我们这里只补一个 Eino 公共接口没有提供的能力：
//
//	embedding.Option
//
// 例如某些 Embedder 支持：
//
//	embedding.WithModel(...)
type storeOptions struct {
	embeddingOptions []embedding.Option
}

// WithEmbeddingOptions 把 Embedding call options
// 传递给真正的 EmbedStrings。
func WithEmbeddingOptions(opts ...embedding.Option) indexer.Option {
	copied := append([]embedding.Option(nil), opts...)

	return indexer.WrapImplSpecificOptFn(func(options *storeOptions) {
		options.embeddingOptions = copied
	})
}

// transaction 是 Indexer 真正需要的最小事务能力。
//
// 不直接让业务逻辑依赖完整 pgx.Tx 接口，
// 方便单元测试使用 lightweight fake。
type transaction interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// database 同理，只要求 Begin。
type database interface {
	Begin(ctx context.Context) (transaction, error)
}

// poolDatabase 把 pgxpool.Pool 适配成上面的最小 database。
type poolDatabase struct {
	pool *pgxpool.Pool
}

func (d poolDatabase) Begin(ctx context.Context) (transaction, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}

	return tx, nil
}

// Indexer 实现 Eino indexer.Indexer。
//
// Store 的语义是：
//
//	“替换这些 source document 的完整 Chunk 集合”
//
// 而不是：
//
//	“向已有文档随便 append 一些 Chunk”。
//
// 为什么？
//
// 假设第一次：
//
//	Chunk 0
//	Chunk 1
//	Chunk 2
//	Chunk 3
//
// 后来调整 ChunkSize，重新 ingest：
//
//	Chunk 0
//	Chunk 1
//
// 如果只是 UPSERT 新 Chunk：
//
//	老 Chunk 2
//	老 Chunk 3
//
// 会永久残留在检索索引里。
//
// 所以当前 PGIndexer 会：
//
//	upsert document
//	delete old chunks of this document
//	insert complete new chunk set
//
// 整个过程在事务中完成。
type Indexer struct {
	db     database
	config Config
}

// NewIndexer 创建正式使用 pgxpool 的 PGIndexer。
func NewIndexer(pool *pgxpool.Pool, config Config) (*Indexer, error) {
	if err := config.InputBudget.Validate(); err != nil {
		return nil, err
	}
	if config.EmbeddingBatchSize < 0 {
		return nil, errors.New("postgres indexer: negative embedding batch size")
	}
	if pool == nil {
		return nil, fmt.Errorf("postgres indexer: nil pool")
	}

	return newIndexerWithDatabase(poolDatabase{pool: pool}, config), nil
}

// newIndexerWithDatabase 主要用于单元测试。
func newIndexerWithDatabase(db database, config Config) *Indexer {
	if config.EmbeddingBatchSize <= 0 {
		config.EmbeddingBatchSize = DefaultEmbeddingBatchSize
	}

	if len(config.SearchBuilder.TitleKeys) == 0 &&
		config.SearchBuilder.ContextHeaderKey == "" {

		config.SearchBuilder = searchcontent.DefaultBuilder()
	}

	config.SearchBuilder.TitleKeys = append([]string(nil), config.SearchBuilder.TitleKeys...)
	return &Indexer{
		db:     db,
		config: config,
	}
}

// Store 实现 Eino Indexer。
//
// 整体流程：
//
//	Chunk Documents
//	     ↓
//	Build SearchContent
//	     ↓
//	Embed in batches
//	     ↓
//	validate 1024 dimensions
//	     ↓
//	BEGIN
//	     ↓
//	upsert documents
//	     ↓
//	delete stale chunks
//	     ↓
//	insert chunks
//	     ↓
//	insert retrieval_index
//	     ↓
//	COMMIT
//
// Embedding 故意发生在事务外。
//
// 原因：
//
// Embedding 是网络 / 模型调用，可能耗时几百毫秒甚至几秒。
//
// 如果先 BEGIN 再等模型：
//
//	PostgreSQL transaction
//
// 会被白白占用很长时间。
func (p *Indexer) Store(
	ctx context.Context,
	docs []*schema.Document,
	opts ...indexer.Option,
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(docs) == 0 {
		return nil, nil
	}

	collectionID := strings.TrimSpace(p.config.CollectionID)

	baseOptions := &indexer.Options{
		Embedding: p.config.Embedder,
	}

	if collectionID != "" {
		baseOptions.Index = &collectionID
	}

	common := indexer.GetCommonOptions(baseOptions, opts...)
	specific := indexer.GetImplSpecificOptions(&storeOptions{}, opts...)

	if common.Index == nil || strings.TrimSpace(*common.Index) == "" {
		return nil, ErrMissingCollectionID
	}

	collectionID = strings.TrimSpace(*common.Index)

	if len(common.SubIndexes) > 0 {
		return nil, ErrSubIndexesUnsupported
	}

	if common.Embedding == nil {
		return nil, ErrMissingEmbedder
	}

	if p.config.VersionedDocuments {
		return nil, errors.New("rag: versioned collections require application.Ingest or ReplaceDocument")
	}
	if err := p.ensureProfile(ctx, collectionID); err != nil {
		return nil, err
	}
	prepared, documents, texts, ids, err := p.prepareDocuments(docs)
	if err != nil {
		return nil, err
	}

	vectors, err := embedTexts(
		ctx,
		common.Embedding,
		texts,
		p.config.EmbeddingBatchSize,
		specific.embeddingOptions,
		p.config.InputBudget,
	)

	if err != nil {
		return nil, err
	}

	if len(vectors) != len(prepared) {
		return nil, fmt.Errorf(
			"%w: expected %d vectors, got %d",
			ErrInvalidEmbedding,
			len(prepared),
			len(vectors),
		)
	}

	for i, vector := range vectors {
		half, err := makeHalfVector(vector)
		if err != nil {
			return nil, fmt.Errorf(
				"chunk %q: %w",
				prepared[i].chunkID,
				err,
			)
		}

		prepared[i].embedding = half
	}

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin postgres index transaction: %w", err)
	}

	defer func() {
		// pgx 明确允许 Commit 后继续调用 Rollback；
		// 它只会返回 transaction closed。
		//
		// 因此 defer Rollback 是安全模式。
		_ = tx.Rollback(ctx)
	}()

	// map 遍历顺序不稳定。
	//
	// 排序以后：
	//
	//     测试
	//     SQL trace
	//     Debug
	//
	// 都更确定。
	documentIDs := make([]string, 0, len(documents))

	for id := range documents {
		documentIDs = append(documentIDs, id)
	}

	sort.Strings(documentIDs)

	for _, documentID := range documentIDs {
		doc := documents[documentID]

		tag, err := tx.Exec(
			ctx,
			upsertDocumentSQL,
			collectionID,
			doc.documentID,
			doc.title,
			doc.markdown,
			doc.metadataJSON,
			int64(0),
			fmt.Sprintf("%x", sha256.Sum256([]byte(doc.markdown))),
			"{}",
		)
		if err != nil {
			return nil, fmt.Errorf(
				"upsert document %q: %w",
				doc.documentID,
				err,
			)
		}

		if tag.RowsAffected() != 1 {
			return nil, ErrStaleIngestion
		}

		// chunks → retrieval_index 使用 ON DELETE CASCADE。
		//
		// 删除旧 chunks 就会自动删除旧 retrieval rows。
		if _, err := tx.Exec(
			ctx,
			deleteDocumentChunksSQL,
			collectionID,
			doc.documentID,
		); err != nil {
			return nil, fmt.Errorf(
				"delete stale chunks for document %q: %w",
				doc.documentID,
				err,
			)
		}
	}

	for _, chunk := range prepared {
		if _, err := tx.Exec(
			ctx,
			insertChunkSQL,
			collectionID,
			chunk.chunkID,
			chunk.documentID,
			chunk.chunkIndex,
			chunk.chunkType,
			chunk.content,
			chunk.contextHeader,
			chunk.startRune,
			chunk.endRune,
			nullableString(chunk.parentChunkID),
			chunk.metadataJSON,
		); err != nil {
			return nil, fmt.Errorf(
				"insert chunk %q: %w",
				chunk.chunkID,
				err,
			)
		}

		if _, err := tx.Exec(
			ctx,
			insertRetrievalSQL,
			collectionID,
			chunk.chunkID,
			chunk.documentID,
			chunk.searchContent,
			chunk.embedding,
			chunk.metadataJSON,
		); err != nil {
			return nil, fmt.Errorf(
				"insert retrieval index for chunk %q: %w",
				chunk.chunkID,
				err,
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit postgres index transaction: %w", err)
	}

	return ids, nil
}

type preparedDocument struct {
	documentID     string
	title          string
	markdown       string
	expectedChunks int
	metadataJSON   string
}

type preparedChunk struct {
	chunkID       string
	documentID    string
	chunkIndex    int
	chunkType     string
	content       string
	contextHeader string
	startRune     int
	endRune       int
	parentChunkID string
	searchContent string
	metadataJSON  string
	embedding     pgvector.HalfVector
}

// prepareDocuments 做数据库写入之前的纯数据校验和转换。
func (p *Indexer) prepareDocuments(
	docs []*schema.Document,
) (
	[]preparedChunk,
	map[string]preparedDocument,
	[]string,
	[]string,
	error,
) {
	prepared := make([]preparedChunk, 0, len(docs))
	documents := make(map[string]preparedDocument)
	texts := make([]string, 0, len(docs))
	ids := make([]string, 0, len(docs))

	chunkIDs := make(map[string]struct{}, len(docs))
	documentChunkIndexes := make(map[string]map[int]struct{})

	for i, doc := range docs {
		if doc == nil {
			return nil, nil, nil, nil, fmt.Errorf(
				"postgres indexer: docs[%d] is nil",
				i,
			)
		}

		chunkID := strings.TrimSpace(doc.ID)

		if chunkID == "" {
			return nil, nil, nil, nil, ErrMissingChunkID
		}

		if _, exists := chunkIDs[chunkID]; exists {
			return nil, nil, nil, nil, fmt.Errorf(
				"postgres indexer: duplicate chunk id %q",
				chunkID,
			)
		}

		chunkIDs[chunkID] = struct{}{}

		documentID := metadataString(
			doc.MetaData,
			metaSourceDocumentID,
		)

		if documentID == "" {
			return nil, nil, nil, nil, fmt.Errorf(
				"%w for chunk %q",
				ErrMissingSourceDocumentID,
				chunkID,
			)
		}
		chunkType := metadataString(doc.MetaData, metaChunkType)
		if chunkType != "" && chunkType != application.ChunkTypeText || metadataString(doc.MetaData, metaParentChunkID) != "" {
			return nil, nil, nil, nil, ErrIncompleteSource
		}
		chunkType = application.ChunkTypeText

		chunkIndex, err := metadataInt(
			doc.MetaData,
			metaChunkIndex,
		)

		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf(
				"chunk %q: %w",
				chunkID,
				err,
			)
		}

		startRune, err := metadataInt(
			doc.MetaData,
			metaChunkStart,
		)

		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf(
				"chunk %q: %w",
				chunkID,
				err,
			)
		}

		endRune, err := metadataInt(
			doc.MetaData,
			metaChunkEnd,
		)

		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf(
				"chunk %q: %w",
				chunkID,
				err,
			)
		}

		if chunkIndex < 0 {
			return nil, nil, nil, nil, fmt.Errorf(
				"chunk %q: negative chunk index %d",
				chunkID,
				chunkIndex,
			)
		}

		if startRune < 0 || endRune < startRune {
			return nil, nil, nil, nil, fmt.Errorf(
				"chunk %q: invalid rune range [%d,%d)",
				chunkID,
				startRune,
				endRune,
			)
		}

		if _, ok := documentChunkIndexes[documentID]; !ok {
			documentChunkIndexes[documentID] = make(map[int]struct{})
		}

		if _, exists := documentChunkIndexes[documentID][chunkIndex]; exists {
			return nil, nil, nil, nil, fmt.Errorf(
				"postgres indexer: duplicate chunk index %d for document %q",
				chunkIndex,
				documentID,
			)
		}

		documentChunkIndexes[documentID][chunkIndex] = struct{}{}

		searchText := p.config.SearchBuilder.Build(doc)

		if strings.TrimSpace(searchText) == "" {
			return nil, nil, nil, nil, fmt.Errorf(
				"%w for chunk %q",
				ErrEmptySearchContent,
				chunkID,
			)
		}

		chunkMetadataJSON, err := marshalMetadata(withoutAdapterEnvelope(doc.MetaData))

		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf(
				"marshal metadata for chunk %q: %w",
				chunkID,
				err,
			)
		}

		contextHeader := metadataString(
			doc.MetaData,
			metaContextHeader,
		)

		parentChunkID := metadataString(
			doc.MetaData,
			metaParentChunkID,
		)

		prepared = append(prepared, preparedChunk{
			chunkID:       chunkID,
			documentID:    documentID,
			chunkIndex:    chunkIndex,
			chunkType:     chunkType,
			content:       doc.Content,
			contextHeader: contextHeader,
			startRune:     startRune,
			endRune:       endRune,
			parentChunkID: parentChunkID,
			searchContent: searchText,
			metadataJSON:  chunkMetadataJSON,
		})

		texts = append(texts, searchText)
		ids = append(ids, chunkID)

		if _, exists := documents[documentID]; !exists {
			markdown, ok := doc.MetaData[application.MetaSourceMarkdown].(string)
			expected, err := metadataInt(doc.MetaData, application.MetaSourceChunkCount)
			if !ok || strings.TrimSpace(markdown) == "" || err != nil || expected <= 0 {
				return nil, nil, nil, nil, ErrIncompleteSource
			}
			documentMetadata := stripChunkMetadata(doc.MetaData)

			documentMetadataJSON, err := marshalMetadata(documentMetadata)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf(
					"marshal metadata for document %q: %w",
					documentID,
					err,
				)
			}

			title := p.config.SearchBuilder.Title(doc)

			if title == "" {
				title = documentID
			}

			documents[documentID] = preparedDocument{
				documentID:     documentID,
				title:          title,
				markdown:       markdown,
				expectedChunks: expected,
				metadataJSON:   documentMetadataJSON,
			}
		}
		original := documents[documentID]
		markdown, _ := doc.MetaData[application.MetaSourceMarkdown].(string)
		expected, err := metadataInt(doc.MetaData, application.MetaSourceChunkCount)
		if err != nil || expected != original.expectedChunks || markdown != original.markdown || endRune > len([]rune(original.markdown)) {
			return nil, nil, nil, nil, ErrIncompleteSource
		}
	}
	for id, doc := range documents {
		if len(documentChunkIndexes[id]) != doc.expectedChunks {
			return nil, nil, nil, nil, ErrIncompleteSource
		}
		for i := 0; i < doc.expectedChunks; i++ {
			if _, ok := documentChunkIndexes[id][i]; !ok {
				return nil, nil, nil, nil, ErrIncompleteSource
			}
		}
	}

	return prepared, documents, texts, ids, nil
}

// embedTexts 按 batch 调用 Eino Embedder。
//
// Eino 保证：
//
//	embeddings[i]
//
// 对应：
//
//	texts[i]
//
// 所以只需要保持 batch append 顺序即可。
func embedTexts(
	ctx context.Context,
	embedder embedding.Embedder,
	texts []string,
	batchSize int,
	opts []embedding.Option,
	budgets ...embeddinginput.Budget,
) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	if batchSize <= 0 {
		batchSize = DefaultEmbeddingBatchSize
	}

	result := make([][]float64, 0, len(texts))
	budget := embeddinginput.Budget{}
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	batches, err := budget.Plan(texts, batchSize)
	if err != nil {
		return nil, err
	}
	for _, batch := range batches {
		start, end := batch.Start, batch.End
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		vectors, err := embedder.EmbedStrings(
			ctx,
			texts[start:end],
			opts...,
		)

		if err != nil {
			return nil, fmt.Errorf(
				"embed documents [%d:%d]: %w",
				start,
				end,
				err,
			)
		}

		if len(vectors) != end-start {
			return nil, fmt.Errorf(
				"%w: embedder returned %d vectors for %d texts",
				ErrInvalidEmbedding,
				len(vectors),
				end-start,
			)
		}

		result = append(result, vectors...)
	}

	return result, nil
}

// makeHalfVector 校验 Eino float64 embedding，
// 然后转换为 pgvector HalfVector。
//
// pgvector.NewHalfVector 接收 []float32。
//
// PostgreSQL halfvec 最终使用 half precision 存储，
// 但我们在 Go 侧仍先转换成 float32 输入。
func makeHalfVector(vector []float64) (pgvector.HalfVector, error) {
	if len(vector) != EmbeddingDimensions {
		return pgvector.HalfVector{}, fmt.Errorf(
			"%w: expected %d dimensions, got %d",
			ErrInvalidEmbedding,
			EmbeddingDimensions,
			len(vector),
		)
	}

	values := make([]float32, len(vector))
	nonzero := false

	for i, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return pgvector.HalfVector{}, fmt.Errorf(
				"%w: vector[%d] is not finite",
				ErrInvalidEmbedding,
				i,
			)
		}

		// IEEE-754 binary16 最大有限值约为 65504。
		//
		// 正常 Embedding 数值通常远远小于这个值，
		// 这主要用于防止异常模型输出在 half precision
		// 转换时变成 Inf。
		if math.Abs(value) > 65504 {
			return pgvector.HalfVector{}, fmt.Errorf(
				"%w: vector[%d]=%f exceeds half precision range",
				ErrInvalidEmbedding,
				i,
				value,
			)
		}

		values[i] = float32(value)
		// Values below half precision's smallest representable subnormal
		// round to zero. Such a vector cannot participate in cosine search.
		if math.Abs(float64(values[i])) > math.Ldexp(1, -25) {
			nonzero = true
		}
	}
	if !nonzero {
		return pgvector.HalfVector{}, fmt.Errorf("%w: zero vector after half precision conversion", ErrInvalidEmbedding)
	}

	return pgvector.NewHalfVector(values), nil
}

func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}

	value, ok := metadata[key]
	if !ok {
		return ""
	}

	text, ok := value.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(text)
}

// metadataInt 同时兼容：
//
//	int
//	int32
//	int64
//	float64
//	string
//
// 为什么需要 float64？
//
// schema.Document metadata 如果未来经过 JSON：
//
//	marshal
//	unmarshal
//
// JSON number 默认会变成 float64。
func metadataInt(metadata map[string]any, key string) (int, error) {
	if len(metadata) == 0 {
		return 0, fmt.Errorf("missing metadata %q", key)
	}

	value, ok := metadata[key]
	if !ok {
		return 0, fmt.Errorf("missing metadata %q", key)
	}

	switch typed := value.(type) {
	case int:
		return typed, nil

	case int32:
		return int(typed), nil

	case int64:
		return int(typed), nil

	case float64:
		if math.Trunc(typed) != typed {
			return 0, fmt.Errorf(
				"metadata %q is not an integer: %v",
				key,
				typed,
			)
		}

		return int(typed), nil

	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, fmt.Errorf(
				"metadata %q is not an integer: %q",
				key,
				typed,
			)
		}

		return parsed, nil

	default:
		return 0, fmt.Errorf(
			"metadata %q has unsupported type %T",
			key,
			value,
		)
	}
}

func marshalMetadata(metadata map[string]any) (string, error) {
	if metadata == nil {
		return "{}", nil
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// stripChunkMetadata 构造 document-level metadata。
//
// Chunk-specific 信息不应该写进 documents.metadata，
//
// 否则 documents 表会出现这种奇怪数据：
//
//	rag_chunk_index = 0
//	rag_chunk_start = 0
//
// 好像整个 Document 只代表第一个 Chunk。
func stripChunkMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}

	result := make(map[string]any, len(metadata))

	for key, value := range metadata {
		result[key] = value
	}

	delete(result, metaChunkIndex)
	delete(result, metaChunkStart)
	delete(result, metaChunkEnd)
	delete(result, metaContextHeader)
	delete(result, metaChunkType)
	delete(result, metaParentChunkID)
	delete(result, application.MetaSourceMarkdown)
	delete(result, application.MetaSourceChunkCount)

	return result
}

func withoutAdapterEnvelope(metadata map[string]any) map[string]any {
	copy := make(map[string]any, len(metadata))
	for key, value := range metadata {
		copy[key] = value
	}
	delete(copy, application.MetaSourceMarkdown)
	delete(copy, application.MetaSourceChunkCount)
	return copy
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)

	if value == "" {
		return nil
	}

	return value
}

const upsertDocumentSQL = upsertIngestionDocumentSQL

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
