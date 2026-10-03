package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

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

	MetaDocumentID    = "rag_document_id"
	MetaParentChunkID = "rag_parent_chunk_id"
	MetaChunkIndex    = "rag_chunk_index"
	MetaChunkStart    = "rag_chunk_start"
	MetaChunkEnd      = "rag_chunk_end"
	MetaContextHeader = "rag_context_header"

	MetaMatchType    = "rag_match_type"
	MetaVectorScore  = "rag_vector_score"
	MetaKeywordScore = "rag_keyword_score"
	MetaVectorRank   = "rag_vector_rank"
	MetaKeywordRank  = "rag_keyword_rank"
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
var _ einoretriever.Retriever = (*HybridRetriever)(nil)

// -----------------------------------------------------------------------------
// Database abstraction
// -----------------------------------------------------------------------------

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

func (p poolQueryer) Query(
	ctx context.Context,
	sql string,
	args ...any,
) (rows, error) {
	return p.pool.Query(ctx, sql, args...)
}

// -----------------------------------------------------------------------------
// Vector Retriever
// -----------------------------------------------------------------------------

// VectorConfig 配置 Dense Retriever。
type VectorConfig struct {
	CollectionID string

	// Embedder 必须与 Indexing 阶段使用完全相同的模型。
	Embedder embedding.Embedder

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

func DefaultVectorConfig() VectorConfig {
	return VectorConfig{
		TopK:           DefaultTopK,
		ScoreThreshold: DefaultVectorThreshold,
		Dimensions:     DefaultDimensions,
	}
}

// VectorRetriever 使用：
//
//	pgvector
//	halfvec
//	HNSW
//	cosine
type VectorRetriever struct {
	db     queryer
	config VectorConfig
}

func NewVectorRetriever(
	pool *pgxpool.Pool,
	cfg VectorConfig,
) (*VectorRetriever, error) {
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
	results, err := r.Search(ctx, query, opts...)
	if err != nil {
		return nil, err
	}

	return searchResultsToDocuments(results), nil
}

// Search 返回与 Eino 解耦的 Retrieval Core SearchResult。
func (r *VectorRetriever) Search(
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

	// ScoreThreshold 是 relevance filter。
	//
	// 对 pgvector 来说：
	//
	//     Score = 1 - cosine_distance
	//
	// 越大越好。
	return filterByScore(results, threshold, topK), nil
}

// -----------------------------------------------------------------------------
// BM25 Retriever
// -----------------------------------------------------------------------------

type BM25Config struct {
	CollectionID string
	TopK         int

	// 这是 raw BM25 score threshold。
	//
	// BM25 没有固定上界。
	ScoreThreshold float64
}

func DefaultBM25Config() BM25Config {
	return BM25Config{
		TopK:           DefaultTopK,
		ScoreThreshold: DefaultKeywordThreshold,
	}
}

type BM25Retriever struct {
	db     queryer
	config BM25Config
}

func NewBM25Retriever(
	pool *pgxpool.Pool,
	cfg BM25Config,
) (*BM25Retriever, error) {
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
	results, err := r.Search(ctx, query, opts...)
	if err != nil {
		return nil, err
	}

	return searchResultsToDocuments(results), nil
}

func (r *BM25Retriever) Search(
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

	return filterByScore(results, threshold, topK), nil
}

// -----------------------------------------------------------------------------
// Hybrid Retriever
// -----------------------------------------------------------------------------

// HybridConfig 同时配置两个 Recall Channel。
type HybridConfig struct {
	CollectionID string

	Embedder embedding.Embedder

	// 最终 Hybrid 返回数量。
	TopK int

	// 每一路 Retriever 的候选池深度。
	//
	// 当前默认与 WeKnora 的 EmbeddingTopK 一样取50。
	ChannelTopK int

	VectorThreshold  float64
	KeywordThreshold float64

	Dimensions int

	RRF retrieval.RRFConfig
}

func DefaultHybridConfig() HybridConfig {
	return HybridConfig{
		TopK:             DefaultTopK,
		ChannelTopK:      DefaultTopK,
		VectorThreshold:  DefaultVectorThreshold,
		KeywordThreshold: DefaultKeywordThreshold,
		Dimensions:       DefaultDimensions,
		RRF:              retrieval.DefaultRRFConfig(),
	}
}

// hybridOptions 是 Hybrid 特有的调用级参数。
//
// Eino 标准 WithScoreThreshold 表示：
//
//	最终 fused score threshold
//
// 两个 Retriever 自己的 threshold 则通过下面两个 Option 控制。
type hybridOptions struct {
	ChannelTopK      *int
	VectorThreshold  *float64
	KeywordThreshold *float64
}

// WithChannelTopK 临时修改两路 Recall Pool。
func WithChannelTopK(topK int) einoretriever.Option {
	return einoretriever.WrapImplSpecificOptFn(
		func(options *hybridOptions) {
			options.ChannelTopK = &topK
		},
	)
}

// WithVectorThreshold 临时修改 Dense threshold。
func WithVectorThreshold(
	threshold float64,
) einoretriever.Option {
	return einoretriever.WrapImplSpecificOptFn(
		func(options *hybridOptions) {
			options.VectorThreshold = &threshold
		},
	)
}

// WithKeywordThreshold 临时修改 raw BM25 threshold。
func WithKeywordThreshold(
	threshold float64,
) einoretriever.Option {
	return einoretriever.WrapImplSpecificOptFn(
		func(options *hybridOptions) {
			options.KeywordThreshold = &threshold
		},
	)
}

type vectorSearcher interface {
	search(
		ctx context.Context,
		query string,
		collectionID string,
		topK int,
		threshold float64,
		embedder embedding.Embedder,
	) ([]retrieval.SearchResult, error)
}

type keywordSearcher interface {
	search(
		ctx context.Context,
		query string,
		collectionID string,
		topK int,
		threshold float64,
	) ([]retrieval.SearchResult, error)
}

type HybridRetriever struct {
	vector  vectorSearcher
	keyword keywordSearcher
	config  HybridConfig
}

func NewHybridRetriever(
	pool *pgxpool.Pool,
	cfg HybridConfig,
) (*HybridRetriever, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres hybrid retriever: nil pool")
	}

	vectorCfg := DefaultVectorConfig()
	vectorCfg.CollectionID = cfg.CollectionID
	vectorCfg.Embedder = cfg.Embedder
	vectorCfg.TopK = cfg.ChannelTopK
	vectorCfg.ScoreThreshold = cfg.VectorThreshold
	vectorCfg.Dimensions = cfg.Dimensions

	keywordCfg := DefaultBM25Config()
	keywordCfg.CollectionID = cfg.CollectionID
	keywordCfg.TopK = cfg.ChannelTopK
	keywordCfg.ScoreThreshold = cfg.KeywordThreshold

	queryDB := poolQueryer{pool: pool}

	return newHybridRetriever(
		newVectorRetriever(queryDB, vectorCfg),
		newBM25Retriever(queryDB, keywordCfg),
		cfg,
	), nil
}

func newHybridRetriever(
	vector vectorSearcher,
	keyword keywordSearcher,
	cfg HybridConfig,
) *HybridRetriever {
	defaults := DefaultHybridConfig()

	if cfg.TopK <= 0 {
		cfg.TopK = defaults.TopK
	}

	if cfg.ChannelTopK <= 0 {
		cfg.ChannelTopK = defaults.ChannelTopK
	}

	if cfg.VectorThreshold == 0 {
		cfg.VectorThreshold = defaults.VectorThreshold
	}

	if cfg.KeywordThreshold == 0 {
		cfg.KeywordThreshold = defaults.KeywordThreshold
	}

	if cfg.Dimensions <= 0 {
		cfg.Dimensions = defaults.Dimensions
	}

	if cfg.RRF.K <= 0 {
		cfg.RRF = defaults.RRF
	}

	return &HybridRetriever{
		vector:  vector,
		keyword: keyword,
		config:  cfg,
	}
}

func (r *HybridRetriever) Retrieve(
	ctx context.Context,
	query string,
	opts ...einoretriever.Option,
) ([]*schema.Document, error) {
	results, err := r.Search(ctx, query, opts...)
	if err != nil {
		return nil, err
	}

	return searchResultsToDocuments(results), nil
}

// Search 并发执行 Dense + BM25，然后做 Weighted RRF。
func (r *HybridRetriever) Search(
	ctx context.Context,
	query string,
	opts ...einoretriever.Option,
) ([]retrieval.SearchResult, error) {
	query = strings.TrimSpace(query)

	if query == "" {
		return nil, ErrEmptyQuery
	}

	collectionID := r.config.CollectionID
	topK := r.config.TopK

	// Hybrid 最终默认不过滤 RRF。
	//
	// 调用方如果需要可以：
	//
	//     retriever.WithScoreThreshold(...)
	finalThreshold := 0.0

	common := einoretriever.GetCommonOptions(
		&einoretriever.Options{
			Index:          &collectionID,
			TopK:           &topK,
			ScoreThreshold: &finalThreshold,
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

	if *common.ScoreThreshold < 0 ||
		*common.ScoreThreshold > 1 {

		return nil, ErrInvalidThreshold
	}

	specific := einoretriever.GetImplSpecificOptions(
		&hybridOptions{},
		opts...,
	)

	channelTopK := r.config.ChannelTopK
	vectorThreshold := r.config.VectorThreshold
	keywordThreshold := r.config.KeywordThreshold

	if specific.ChannelTopK != nil {
		channelTopK = *specific.ChannelTopK
	}

	if specific.VectorThreshold != nil {
		vectorThreshold = *specific.VectorThreshold
	}

	if specific.KeywordThreshold != nil {
		keywordThreshold = *specific.KeywordThreshold
	}

	if err := validateTopK(channelTopK); err != nil {
		return nil, err
	}

	if vectorThreshold < 0 || vectorThreshold > 1 {
		return nil, ErrInvalidThreshold
	}

	if keywordThreshold < 0 {
		return nil, ErrInvalidThreshold
	}

	type response struct {
		results []retrieval.SearchResult
		err     error
	}

	vectorCh := make(chan response, 1)
	keywordCh := make(chan response, 1)

	// 两路 Recall 没有数据依赖，
	// 所以并发执行可以直接降低 Hybrid Retrieval latency。
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()

		results, err := r.vector.search(
			ctx,
			query,
			strings.TrimSpace(*common.Index),
			channelTopK,
			vectorThreshold,
			common.Embedding,
		)

		vectorCh <- response{
			results: results,
			err:     err,
		}
	}()

	go func() {
		defer wg.Done()

		results, err := r.keyword.search(
			ctx,
			query,
			strings.TrimSpace(*common.Index),
			channelTopK,
			keywordThreshold,
		)

		keywordCh <- response{
			results: results,
			err:     err,
		}
	}()

	// Wait 不是为了读取结果所必需，
	// 但让两个 goroutine 生命周期明确结束在当前函数内。
	wg.Wait()

	vectorResponse := <-vectorCh
	keywordResponse := <-keywordCh

	if vectorResponse.err != nil {
		return nil, fmt.Errorf(
			"hybrid vector channel: %w",
			vectorResponse.err,
		)
	}

	if keywordResponse.err != nil {
		return nil, fmt.Errorf(
			"hybrid keyword channel: %w",
			keywordResponse.err,
		)
	}

	fused := retrieval.FuseRRF(
		vectorResponse.results,
		keywordResponse.results,
		r.config.RRF,
	)

	fused = filterByScore(
		fused,
		*common.ScoreThreshold,
		*common.TopK,
	)

	return fused, nil
}

// -----------------------------------------------------------------------------
// SQL
// -----------------------------------------------------------------------------

// vectorSearchSQL:
//
//	<=> = cosine distance
//
//	1 - distance = cosine similarity
//
// 注意内部 CTE：
//
// HNSW 要使用索引，核心形状应该保持：
//
//	ORDER BY embedding <=> query
//	LIMIT N
//
// 不把复杂 Join 排序直接压在 ANN 查询上。
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
    c.metadata,
    1.0 - n.distance AS score
FROM nearest n
JOIN chunks c
  ON c.collection_id = $2
 AND c.id = n.chunk_id
ORDER BY score DESC, c.id ASC
`

// bm25SearchSQL:
//
//	|||
//	    ParadeDB Match Any / OR semantics
//
//	pdb.score(id)
//	    BM25 relevance
//
// retrieval_index.id 是 ParadeDB 的 unique key_field。
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
    c.metadata,
    pdb.score(ri.id)::float8 AS score
FROM retrieval_index ri
JOIN chunks c
  ON c.collection_id = ri.collection_id
 AND c.id = ri.chunk_id
WHERE ri.collection_id = $1
  AND ri.enabled = TRUE
  AND ri.search_content ||| $2::text
ORDER BY pdb.score(ri.id) DESC, ri.id ASC
LIMIT $3
`

// -----------------------------------------------------------------------------
// Row mapping
// -----------------------------------------------------------------------------

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

// -----------------------------------------------------------------------------
// Eino mapping
// -----------------------------------------------------------------------------

func searchResultsToDocuments(
	results []retrieval.SearchResult,
) []*schema.Document {
	docs := make([]*schema.Document, 0, len(results))

	for _, result := range results {
		metadata := cloneMetadata(result.Metadata)

		metadata[MetaDocumentID] = result.DocumentID
		metadata[MetaChunkIndex] = result.ChunkIndex
		metadata[MetaChunkStart] = result.StartRune
		metadata[MetaChunkEnd] = result.EndRune
		metadata[MetaContextHeader] = result.ContextHeader
		metadata[MetaMatchType] = string(result.MatchType)

		if result.ParentChunkID != "" {
			metadata[MetaParentChunkID] = result.ParentChunkID
		}

		if result.VectorRank > 0 {
			metadata[MetaVectorRank] = result.VectorRank
			metadata[MetaVectorScore] = result.VectorScore
		}

		if result.KeywordRank > 0 {
			metadata[MetaKeywordRank] = result.KeywordRank
			metadata[MetaKeywordScore] = result.KeywordScore
		}

		// Vector/BM25 direct Retriever 还没有经过 RRF，
		// 但 raw score 仍应该写进相应 metadata。
		if result.MatchType == retrieval.MatchVector {
			metadata[MetaVectorScore] = result.VectorScore
		}

		if result.MatchType == retrieval.MatchKeyword {
			metadata[MetaKeywordScore] = result.KeywordScore
		}

		doc := &schema.Document{
			ID:       result.ChunkID,
			Content:  result.Content,
			MetaData: metadata,
		}

		// Eino 官方 relevance score。
		doc.WithScore(result.Score)

		docs = append(docs, doc)
	}

	return docs
}

// -----------------------------------------------------------------------------
// Validation / utilities
// -----------------------------------------------------------------------------

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

	if options.ScoreThreshold == nil {
		return ErrInvalidThreshold
	}

	return nil
}

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
	}

	return pgvector.NewHalfVector(values), nil
}

func cloneMetadata(
	src map[string]any,
) map[string]any {
	if len(src) == 0 {
		return make(map[string]any)
	}

	dst := make(map[string]any, len(src)+10)

	for key, value := range src {
		dst[key] = value
	}

	return dst
}

// 确保 poolQueryer 编译时确实能返回 pgx.Rows。
var _ rows = (pgx.Rows)(nil)
