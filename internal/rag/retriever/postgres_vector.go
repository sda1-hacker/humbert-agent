package retriever

import (
	"context"
	"fmt"
	"math"
	"strings"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

var _ eino.Retriever = (*VectorRetriever)(nil)

// VectorRetriever 负责查询向量化与 pgvector SQL，HNSW 索引由数据库维护。
type VectorRetriever struct {
	db     queryer
	config ragconfig.Vector
}

// NewVectorRetriever 绑定已迁移的数据库，连接池由装配方关闭。
func NewVectorRetriever(pool *pgxpool.Pool, cfg ragconfig.Vector) (*VectorRetriever, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, fmt.Errorf("postgres vector retriever: nil pool")
	}
	if cfg.TopK == 0 {
		cfg.TopK = ragconfig.DefaultTopK
	}
	if cfg.Dimensions == 0 {
		return nil, fmt.Errorf("%w: postgres retriever requires configured dimensions", ragconfig.ErrInvalidDimensions)
	}
	return &VectorRetriever{db: pool, config: cfg}, nil
}

// Retrieve 先完成选项校验和查询向量化，再执行 HNSW 候选查询，不在 Go 中重复计算相似度。
func (r *VectorRetriever) Retrieve(ctx context.Context, query string, opts ...eino.Option) ([]*schema.Document, error) {
	collection, topK, threshold := r.config.CollectionID, r.config.TopK, r.config.ScoreThreshold
	options := eino.GetCommonOptions(&eino.Options{Index: &collection, TopK: &topK, ScoreThreshold: &threshold, Embedding: r.config.Embedder}, opts...)
	query = strings.TrimSpace(query)
	if err := validateSearchOptions(ctx, query, options); err != nil {
		return nil, err
	}
	if *options.ScoreThreshold > 1 {
		return nil, ragconfig.ErrInvalidThreshold
	}
	if options.Embedding == nil {
		return nil, ErrMissingEmbedder
	}
	vectors, err := options.Embedding.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed retrieval query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("%w: expected one vector, got %d", ErrInvalidEmbedding, len(vectors))
	}
	queryVector, err := toHalfVector(vectors[0], r.config.Dimensions)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryDocuments(ctx, r.db, vectorSearchSQL, options, retrieval.MatchVector,
		queryVector, strings.TrimSpace(*options.Index), *options.TopK)
}

// toHalfVector 拒绝维度错误、非有限值、半精度溢出和量化后归零的向量。
func toHalfVector(vector []float64, dimensions int) (pgvector.HalfVector, error) {
	if len(vector) != dimensions {
		return pgvector.HalfVector{}, fmt.Errorf("%w: expected %d dimensions, got %d", ErrInvalidEmbedding, dimensions, len(vector))
	}
	values := make([]float32, len(vector))
	nonzero := false
	for i, value := range vector {
		if !finite(value) {
			return pgvector.HalfVector{}, fmt.Errorf("%w: vector[%d] is not finite", ErrInvalidEmbedding, i)
		}
		if math.Abs(value) > 65504 {
			return pgvector.HalfVector{}, fmt.Errorf("%w: vector[%d] exceeds half precision range", ErrInvalidEmbedding, i)
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

// 直接按余弦距离取候选，保留可使用 HNSW 的查询形状；最终分数为 1 - 距离。
// 参数类型由 embedding 列推导，兼容 vector/halfvec，避免转换列本身而使索引失效。
const vectorSearchSQL = `
WITH nearest AS MATERIALIZED (
    SELECT
        chunk_id,
        document_id,
        embedding <=> $1 AS distance
    FROM retrieval_index
    WHERE collection_id = $2
      AND enabled = TRUE
    ORDER BY embedding <=> $1
    LIMIT $3
)
SELECT
    c.chunk_key,
    d.source_key,
    c.content,
    c.context_header,
    c.chunk_index,
    c.start_rune,
    c.end_rune,
    p.chunk_key,
    c.metadata || jsonb_build_object('rag_document_revision', d.revision::text),
    1.0 - n.distance AS score
FROM nearest n
JOIN chunks c
  ON c.collection_id = $2
 AND c.id = n.chunk_id
JOIN documents d ON d.collection_id=c.collection_id AND d.id=c.document_id
LEFT JOIN chunks p ON p.collection_id=c.collection_id AND p.id=c.parent_chunk_id
ORDER BY score DESC, c.chunk_key ASC
`
