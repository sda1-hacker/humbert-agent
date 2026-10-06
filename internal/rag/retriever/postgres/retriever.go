package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

const (
	DefaultTopK             = 50
	MaxTopK                 = 200
	DefaultVectorThreshold  = 0.15
	DefaultKeywordThreshold = 0.30
	DefaultDimensions       = 1024
)

var (
	ErrMissingCollectionID = errors.New("postgres retriever: missing collection id")
	ErrMissingEmbedder     = errors.New("postgres retriever: missing embedder")
	ErrEmptyQuery          = errors.New("postgres retriever: empty query")
	ErrInvalidTopK         = errors.New("postgres retriever: invalid top k")
	ErrInvalidThreshold    = errors.New("postgres retriever: invalid score threshold")
	ErrInvalidEmbedding    = errors.New("postgres retriever: invalid query embedding")
	ErrSubIndexUnsupported = errors.New("postgres retriever: sub index is not supported")
	ErrDSLUnsupported      = errors.New("postgres retriever: DSLInfo is not supported yet")
)

// 编译期接口检查。
var _ einoretriever.Retriever = (*VectorRetriever)(nil)
var _ einoretriever.Retriever = (*BM25Retriever)(nil)

// 数据库访问接口。

// rows 是 Retriever 真正需要的最小 RowSet 接口。
//
// 这样单元测试无需真实 PostgreSQL。
type rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// queryer 是最小查询接口。
type queryer interface {
	Query(
		ctx context.Context,
		sql string,
		args ...any,
	) (rows, error)
}

// poolQueryer 把 pgxpool.Pool 适配到 queryer。
type poolQueryer struct {
	pool *pgxpool.Pool
}

// Query 将连接池查询适配到内部行读取接口。
func (p poolQueryer) Query(
	ctx context.Context,
	sql string,
	args ...any,
) (rows, error) {
	return p.pool.Query(ctx, sql, args...)
}

// 向量召回器。

// VectorConfig 配置 Dense Retriever。
type VectorConfig struct {
	// CollectionID 组件默认知识库范围，原生调用可通过 WithIndex 覆盖。
	CollectionID string

	// Embedder 必须与 Indexing 阶段使用完全相同的模型。
	Embedder embedding.Embedder

	// TopK 召回候选数量，调用时可通过 WithTopK 覆盖。
	TopK int

	// cosine similarity 最小值。
	//
	// 默认参考当前 WeKnora：
	//
	//     0.15
	ScoreThreshold float64

	// 当前数据库是 halfvec(1024)。
	Dimensions int
}

// DefaultVectorConfig 返回向量召回的候选数量、维度与默认阈值。
func DefaultVectorConfig() VectorConfig {
	return VectorConfig{
		TopK:           DefaultTopK,
		ScoreThreshold: DefaultVectorThreshold,
		Dimensions:     DefaultDimensions,
	}
}

// VectorRetriever 使用 pgvector 的半精度向量与 HNSW 索引进行余弦相似度召回。
type VectorRetriever struct {
	db     queryer
	config VectorConfig
}

// NewVectorRetriever 校验模型和数据库配置，创建 Eino 向量检索器。
func NewVectorRetriever(
	pool *pgxpool.Pool,
	cfg VectorConfig,
) (*VectorRetriever, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, fmt.Errorf("postgres vector retriever: nil pool")
	}

	return newVectorRetriever(poolQueryer{pool: pool}, cfg), nil
}

func newVectorRetriever(db queryer, cfg VectorConfig) *VectorRetriever {
	if cfg.TopK <= 0 {
		cfg.TopK = DefaultTopK
	}

	if cfg.Dimensions <= 0 {
		cfg.Dimensions = DefaultDimensions
	}

	return &VectorRetriever{
		db:     db,
		config: cfg,
	}
}

// Retrieve 实现 Eino Retriever。
func (r *VectorRetriever) Retrieve(
	ctx context.Context,
	query string,
	opts ...einoretriever.Option,
) ([]*schema.Document, error) {
	results, err := r.searchResults(ctx, query, opts...)
	if err != nil {
		return nil, err
	}

	return retrieval.Documents(results), nil
}

// searchResults 是适配器内部的查询与结果转换，不对外定义另一套检索接口。
func (r *VectorRetriever) searchResults(
	ctx context.Context,
	query string,
	opts ...einoretriever.Option,
) ([]retrieval.SearchResult, error) {
	collectionID := r.config.CollectionID
	topK := r.config.TopK
	threshold := r.config.ScoreThreshold

	common := einoretriever.GetCommonOptions(
		&einoretriever.Options{
			Index:          &collectionID,
			TopK:           &topK,
			ScoreThreshold: &threshold,
			Embedding:      r.config.Embedder,
		},
		opts...,
	)

	if err := validateCommonOptions(common); err != nil {
		return nil, err
	}

	if common.Embedding == nil {
		return nil, ErrMissingEmbedder
	}

	return r.search(
		ctx,
		query,
		strings.TrimSpace(*common.Index),
		*common.TopK,
		*common.ScoreThreshold,
		common.Embedding,
	)
}

func (r *VectorRetriever) search(
	ctx context.Context,
	query string,
	collectionID string,
	topK int,
	threshold float64,
	embedder embedding.Embedder,
) ([]retrieval.SearchResult, error) {
	query = strings.TrimSpace(query)

	if query == "" {
		return nil, ErrEmptyQuery
	}

	if err := validateTopK(topK); err != nil {
		return nil, err
	}

	if threshold < 0 || threshold > 1 {
		return nil, ErrInvalidThreshold
	}

	vectors, err := embedder.EmbedStrings(
		ctx,
		[]string{query},
	)

	if err != nil {
		return nil, fmt.Errorf("embed retrieval query: %w", err)
	}

	if len(vectors) != 1 {
		return nil, fmt.Errorf(
			"%w: expected one vector, got %d",
			ErrInvalidEmbedding,
			len(vectors),
		)
	}

	queryVector, err := toHalfVector(
		vectors[0],
		r.config.Dimensions,
	)

	if err != nil {
		return nil, err
	}

	resultRows, err := r.db.Query(
		ctx,
		vectorSearchSQL,
		queryVector,
		collectionID,
		topK,
	)

	if err != nil {
		return nil, fmt.Errorf("postgres vector search: %w", err)
	}

	defer resultRows.Close()

	results, err := scanSearchResults(
		resultRows,
		retrieval.MatchVector,
	)

	if err != nil {
		return nil, err
	}

	// 向量相关性分数等于一减余弦距离。
	for i := range results {
		results[i].CollectionID = collectionID
	}
	return filterByScore(results, threshold, topK), nil
}

// 关键词召回器。

// BM25Config 控制关键词召回的知识库范围、数量与原始分数阈值。
type BM25Config struct {
	// CollectionID 组件默认知识库范围，原生调用可通过 WithIndex 覆盖。
	CollectionID string
	// TopK 召回候选数量，调用时可通过 WithTopK 覆盖。
	TopK int

	// 这是 raw BM25 score threshold。
	//
	// BM25 没有固定上界。
	ScoreThreshold float64
}

// DefaultBM25Config 返回关键词召回的默认候选数量。
func DefaultBM25Config() BM25Config {
	return BM25Config{
		TopK:           DefaultTopK,
		ScoreThreshold: DefaultKeywordThreshold,
	}
}

// BM25Retriever 通过 ParadeDB 进行关键词召回的 Eino 检索器。
type BM25Retriever struct {
	db     queryer
	config BM25Config
}

// NewBM25Retriever 校验配置并绑定 PostgreSQL 连接池。
func NewBM25Retriever(
	pool *pgxpool.Pool,
	cfg BM25Config,
) (*BM25Retriever, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, fmt.Errorf("postgres bm25 retriever: nil pool")
	}

	return newBM25Retriever(poolQueryer{pool: pool}, cfg), nil
}

func newBM25Retriever(db queryer, cfg BM25Config) *BM25Retriever {
	if cfg.TopK <= 0 {
		cfg.TopK = DefaultTopK
	}

	return &BM25Retriever{
		db:     db,
		config: cfg,
	}
}

func (r *BM25Retriever) Retrieve(
	ctx context.Context,
	query string,
	opts ...einoretriever.Option,
) ([]*schema.Document, error) {
	results, err := r.searchResults(ctx, query, opts...)
	if err != nil {
		return nil, err
	}

	return retrieval.Documents(results), nil
}

func (r *BM25Retriever) searchResults(
	ctx context.Context,
	query string,
	opts ...einoretriever.Option,
) ([]retrieval.SearchResult, error) {
	collectionID := r.config.CollectionID
	topK := r.config.TopK
	threshold := r.config.ScoreThreshold

	common := einoretriever.GetCommonOptions(
		&einoretriever.Options{
			Index:          &collectionID,
			TopK:           &topK,
			ScoreThreshold: &threshold,
		},
		opts...,
	)

	if err := validateCommonOptions(common); err != nil {
		return nil, err
	}

	return r.search(
		ctx,
		query,
		strings.TrimSpace(*common.Index),
		*common.TopK,
		*common.ScoreThreshold,
	)
}

func (r *BM25Retriever) search(
	ctx context.Context,
	query string,
	collectionID string,
	topK int,
	threshold float64,
) ([]retrieval.SearchResult, error) {
	query = strings.TrimSpace(query)

	if query == "" {
		return nil, ErrEmptyQuery
	}

	if err := validateTopK(topK); err != nil {
		return nil, err
	}

	if threshold < 0 || math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return nil, ErrInvalidThreshold
	}

	resultRows, err := r.db.Query(
		ctx,
		bm25SearchSQL,
		collectionID,
		query,
		topK,
	)

	if err != nil {
		return nil, fmt.Errorf("postgres bm25 search: %w", err)
	}

	defer resultRows.Close()

	results, err := scanSearchResults(
		resultRows,
		retrieval.MatchKeyword,
	)

	if err != nil {
		return nil, err
	}

	for i := range results {
		results[i].CollectionID = collectionID
	}
	return filterByScore(results, threshold, topK), nil
}

// -----------------------------------------------------------------------------
// SQL
// -----------------------------------------------------------------------------

// 向量相似度为一减余弦距离；先在子查询中按距离取候选，保留 HNSW 可使用的查询形状。
const vectorSearchSQL = `
WITH nearest AS MATERIALIZED (
    SELECT
        chunk_id,
        document_id,
        embedding <=> $1::halfvec(1024) AS distance
    FROM retrieval_index
    WHERE collection_id = $2
      AND enabled = TRUE
    ORDER BY embedding <=> $1::halfvec(1024)
    LIMIT $3
)
SELECT
    c.id,
    c.document_id,
    c.content,
    c.context_header,
    c.chunk_index,
    c.start_rune,
    c.end_rune,
    c.parent_chunk_id,
    c.metadata || jsonb_build_object('rag_document_revision', d.revision::text),
    1.0 - n.distance AS score
FROM nearest n
JOIN chunks c
  ON c.collection_id = $2
 AND c.id = n.chunk_id
JOIN documents d ON d.collection_id=c.collection_id AND d.id=c.document_id
ORDER BY score DESC, c.id ASC
`

// 关键词查询使用 ParadeDB 的匹配与相关性评分，id 是检索表的唯一索引键。
const bm25SearchSQL = `
SELECT
    c.id,
    c.document_id,
    c.content,
    c.context_header,
    c.chunk_index,
    c.start_rune,
    c.end_rune,
    c.parent_chunk_id,
    c.metadata || jsonb_build_object('rag_document_revision', d.revision::text),
    pdb.score(ri.id)::float8 AS score
FROM retrieval_index ri
JOIN chunks c
  ON c.collection_id = ri.collection_id
 AND c.id = ri.chunk_id
JOIN documents d ON d.collection_id=c.collection_id AND d.id=c.document_id
WHERE ri.collection_id = $1
  AND ri.enabled = TRUE
  AND ri.search_content ||| $2::text
ORDER BY pdb.score(ri.id) DESC, ri.id ASC
LIMIT $3
`

// 数据库行映射。

// scanSearchResults 将数据库行映射为统一召回结果，保留原始通道分数。
func scanSearchResults(
	resultRows rows,
	matchType retrieval.MatchType,
) ([]retrieval.SearchResult, error) {
	var results []retrieval.SearchResult

	for resultRows.Next() {
		var (
			result retrieval.SearchResult

			parentChunkID *string
			rawMetadata   []byte
		)

		if err := resultRows.Scan(
			&result.ChunkID,
			&result.DocumentID,
			&result.Content,
			&result.ContextHeader,
			&result.ChunkIndex,
			&result.StartRune,
			&result.EndRune,
			&parentChunkID,
			&rawMetadata,
			&result.Score,
		); err != nil {
			return nil, fmt.Errorf(
				"scan postgres retrieval row: %w",
				err,
			)
		}

		if parentChunkID != nil {
			result.ParentChunkID = *parentChunkID
		}

		if len(rawMetadata) > 0 {
			if err := json.Unmarshal(
				rawMetadata,
				&result.Metadata,
			); err != nil {
				return nil, fmt.Errorf(
					"decode retrieval metadata for chunk %q: %w",
					result.ChunkID,
					err,
				)
			}
		}

		revision, err := retrieval.Revision(result.Metadata)
		if err != nil {
			return nil, err
		}
		result.DocumentRevision = revision
		result.MatchType = matchType

		switch matchType {
		case retrieval.MatchVector:
			result.VectorScore = result.Score

		case retrieval.MatchKeyword:
			result.KeywordScore = result.Score
		}

		results = append(results, result)
	}

	if err := resultRows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate postgres retrieval rows: %w",
			err,
		)
	}

	return results, nil
}

// Eino 文档映射。

// 配置校验与辅助函数。

// validateCommonOptions 校验 Eino 通用选项，拒绝不支持的子索引和查询表达式。
func validateCommonOptions(
	options *einoretriever.Options,
) error {
	if options == nil ||
		options.Index == nil ||
		strings.TrimSpace(*options.Index) == "" {

		return ErrMissingCollectionID
	}

	if options.TopK == nil {
		return ErrInvalidTopK
	}

	if err := validateTopK(*options.TopK); err != nil {
		return err
	}

	if options.SubIndex != nil {
		return ErrSubIndexUnsupported
	}

	if len(options.DSLInfo) > 0 {
		return ErrDSLUnsupported
	}

	if options.ScoreThreshold == nil || !finite(*options.ScoreThreshold) {
		return ErrInvalidThreshold
	}

	return nil
}

// validateTopK 限制数据库请求的候选数量，防止无界召回。
func validateTopK(topK int) error {
	if topK <= 0 || topK > MaxTopK {
		return fmt.Errorf(
			"%w: expected 1..%d, got %d",
			ErrInvalidTopK,
			MaxTopK,
			topK,
		)
	}

	return nil
}

// filterByScore 保留达到原始通道阈值的结果并限制数量，保持数据库排序。
func filterByScore(
	results []retrieval.SearchResult,
	threshold float64,
	topK int,
) []retrieval.SearchResult {
	if len(results) == 0 {
		return nil
	}

	filtered := make(
		[]retrieval.SearchResult,
		0,
		len(results),
	)

	for _, item := range results {
		if item.Score >= threshold {
			filtered = append(filtered, item)
		}

		if len(filtered) == topK {
			break
		}
	}

	return filtered
}

// toHalfVector 校验维度、有限值、半精度范围和非零向量，再转换为数据库向量类型。
func toHalfVector(
	vector []float64,
	dimensions int,
) (pgvector.HalfVector, error) {
	if len(vector) != dimensions {
		return pgvector.HalfVector{}, fmt.Errorf(
			"%w: expected %d dimensions, got %d",
			ErrInvalidEmbedding,
			dimensions,
			len(vector),
		)
	}

	values := make([]float32, len(vector))
	nonzero := false

	for i, value := range vector {
		if math.IsNaN(value) ||
			math.IsInf(value, 0) {

			return pgvector.HalfVector{}, fmt.Errorf(
				"%w: vector[%d] is not finite",
				ErrInvalidEmbedding,
				i,
			)
		}

		if math.Abs(value) > 65504 {
			return pgvector.HalfVector{}, fmt.Errorf(
				"%w: vector[%d] exceeds half precision range",
				ErrInvalidEmbedding,
				i,
			)
		}

		values[i] = float32(value)
		if math.Abs(float64(values[i])) > math.Ldexp(1, -25) {
			nonzero = true
		}
	}

	if !nonzero {
		return pgvector.HalfVector{}, fmt.Errorf("%w: zero vector after half precision conversion", ErrInvalidEmbedding)
	}
	return pgvector.NewHalfVector(values), nil
}

// 确保 poolQueryer 编译时确实能返回 pgx.Rows。
var _ rows = (pgx.Rows)(nil)
