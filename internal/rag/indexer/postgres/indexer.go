package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	"github.com/sda1-hacker/humbert-agent/internal/rag/embeddinginput"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

var _ indexer.Indexer = (*Indexer)(nil)

const DefaultEmbeddingBatchSize = 64

var (
	ErrMissingCollectionID   = errors.New("postgres indexer: missing collection id")
	ErrMissingEmbedder       = errors.New("postgres indexer: missing embedder")
	ErrMissingChunkID        = errors.New("postgres indexer: missing document id")
	ErrEmptySearchContent    = errors.New("postgres indexer: empty search content")
	ErrSubIndexesUnsupported = errors.New("postgres indexer: sub indexes are not supported")
	ErrInvalidEmbedding      = errors.New("postgres indexer: invalid embedding")
	ErrIncompleteSource      = errors.New("postgres indexer: chunked documents require WithIngestionBatch with the complete source")
	ErrPinnedEmbedding       = errors.New("postgres indexer: embedding options cannot override a pinned collection profile")
)

// Config 只包含 PostgreSQL 实现所需的设置，检索文本在应用层统一构造。
type Config struct {
	// ProfileID 知识库绑定的向量空间 ID 与配置快照。
	ProfileID, ProfileJSON string
	// VersionedDocuments 要求通过预留版本发布，防止旧任务覆盖新文档。
	VersionedDocuments bool
	// CollectionID 原生组件的默认知识库范围，可由 WithIndex 指定。
	CollectionID string
	// Embedder 用于索引的 Eino 向量模型。
	Embedder embedding.Embedder
	// EmbeddingBatchSize 单次请求最多处理的文本条数。
	EmbeddingBatchSize int
	// InputBudget 完整检索文本的单条和单批 token 预算。
	InputBudget embeddinginput.Budget
}

// DefaultConfig 返回默认向量请求批次大小。
func DefaultConfig() Config { return Config{EmbeddingBatchSize: DefaultEmbeddingBatchSize} }

type storeOptions struct{ embeddingOptions []embedding.Option }

// WithEmbeddingOptions 使用 Eino 实现专属 Option 向模型转发调用参数。
func WithEmbeddingOptions(opts ...embedding.Option) indexer.Option {
	copied := append([]embedding.Option(nil), opts...)
	return indexer.WrapImplSpecificOptFn(func(o *storeOptions) { o.embeddingOptions = copied })
}

// 这些内部接口用于测试事务，不是新增的检索库接口。
type transaction interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}
type database interface {
	Begin(context.Context) (transaction, error)
}
type poolDatabase struct{ pool *pgxpool.Pool }

// Begin 将连接池事务适配到内部测试接口。
func (d poolDatabase) Begin(ctx context.Context) (transaction, error) { return d.pool.Begin(ctx) }

// Indexer Eino 原生索引器，在 PostgreSQL 中保存完整文档和检索索引。
type Indexer struct {
	db     database
	config Config
}

// NewIndexer 校验配置并绑定连接池，连接池的关闭由组装层负责。
func NewIndexer(pool *pgxpool.Pool, cfg Config) (*Indexer, error) {
	if err := cfg.InputBudget.Validate(); err != nil {
		return nil, err
	}
	if cfg.EmbeddingBatchSize < 0 {
		return nil, errors.New("postgres indexer: negative embedding batch size")
	}
	if pool == nil {
		return nil, errors.New("postgres indexer: nil pool")
	}
	return newIndexerWithDatabase(poolDatabase{pool}, cfg), nil
}
func newIndexerWithDatabase(db database, cfg Config) *Indexer {
	if cfg.EmbeddingBatchSize == 0 {
		cfg.EmbeddingBatchSize = DefaultEmbeddingBatchSize
	}
	return &Indexer{db: db, config: cfg}
}

// Store 是唯一索引入口。普通 Eino 文档以“一份原文、一个分块”保存；应用层切分的
// 文档通过 WithIngestionBatch 附带完整原文和父块，每个文档版本在一个事务内替换。
// 模型调用放在事务前，避免等待网络时占用数据库事务。
func (p *Indexer) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	collection := strings.TrimSpace(p.config.CollectionID)
	common := indexer.GetCommonOptions(&indexer.Options{Index: &collection, Embedding: p.config.Embedder}, opts...)
	if common.Index == nil || strings.TrimSpace(*common.Index) == "" {
		return nil, ErrMissingCollectionID
	}
	collection = strings.TrimSpace(*common.Index)
	if len(common.SubIndexes) > 0 {
		return nil, ErrSubIndexesUnsupported
	}
	specific := indexer.GetImplSpecificOptions(&storeOptions{}, opts...)
	if p.config.ProfileID != "" && (indexer.GetCommonOptions(nil, opts...).Embedding != nil || len(specific.embeddingOptions) > 0) {
		// 配置签名不能继续声明旧模型，却实际使用调用方临时替换的模型。
		return nil, ErrPinnedEmbedding
	}
	source := indexer.GetImplSpecificOptions(&rag.IngestionOptions{}, opts...).Batch
	if len(docs) == 0 && source == nil {
		return nil, nil
	}
	if common.Embedding == nil && len(docs) > 0 {
		return nil, ErrMissingEmbedder
	}
	ids := make([]string, len(docs))
	seen := make(map[string]bool, len(docs))
	for i, d := range docs {
		if d == nil || strings.TrimSpace(d.ID) == "" {
			return nil, ErrMissingChunkID
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("postgres indexer: duplicate id %q", d.ID)
		}
		seen[d.ID] = true
		if strings.TrimSpace(d.Content) == "" {
			return nil, ErrEmptySearchContent
		}
		ids[i] = d.ID
	}
	if source != nil {
		batch := *source
		if batch.CollectionID != collection || len(docs) != len(batch.Children) {
			return nil, ErrIncompleteSource
		}
		texts := make([]string, len(docs))
		for i, d := range docs {
			raw, ok := d.MetaData[retrieval.MetaRawContent].(string)
			if d.ID != batch.Children[i].ID || !ok || raw != batch.Children[i].Content {
				return nil, ErrIncompleteSource
			}
			texts[i] = d.Content
		}
		if err := p.storeDocument(ctx, batch, texts, common.Embedding, specific.embeddingOptions); err != nil {
			return nil, err
		}
		return ids, nil
	}
	// 有分块身份的输入不能被误当作完整原文，否则一次部分更新会删掉其他分块。
	for _, d := range docs {
		if d.MetaData[retrieval.MetaSourceDocumentID] != nil || d.MetaData[retrieval.MetaDocumentID] != nil {
			return nil, ErrIncompleteSource
		}
	}
	for _, d := range docs {
		batch := rag.IngestionBatch{CollectionID: collection, DocumentID: d.ID, Title: searchcontent.DefaultBuilder().Title(d), Markdown: d.Content, Metadata: d.MetaData,
			Children: []rag.ChunkRecord{{ID: d.ID, DocumentID: d.ID, ChunkType: rag.ChunkTypeText, Content: d.Content, EndRune: utf8.RuneCountInString(d.Content), Metadata: d.MetaData}}}
		if p.config.VersionedDocuments {
			revision, err := p.ReserveDocument(ctx, collection, d.ID)
			if err != nil {
				return nil, err
			}
			batch.Attempt = revision
		}
		if err := p.storeDocument(ctx, batch, []string{d.Content}, common.Embedding, specific.embeddingOptions); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// embedTexts 按输入预算分批生成向量，校验数量、维度和半精度有效范围。
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
		// 半精度转换后归零的向量不能用于余弦检索，需要提前拒绝。
		if math.Abs(float64(values[i])) > math.Ldexp(1, -25) {
			nonzero = true
		}
	}
	if !nonzero {
		return pgvector.HalfVector{}, fmt.Errorf("%w: zero vector after half precision conversion", ErrInvalidEmbedding)
	}

	return pgvector.NewHalfVector(values), nil
}

// marshalMetadata 将元数据编码为 JSON，空元数据仍保存为对象。
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

// nullableString 将空字符串映射为数据库 NULL。
func nullableString(value string) any {
	value = strings.TrimSpace(value)

	if value == "" {
		return nil
	}

	return value
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
