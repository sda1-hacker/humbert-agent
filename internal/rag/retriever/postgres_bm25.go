package retriever

import (
	"context"
	"fmt"
	"strings"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgxpool"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

var _ eino.Retriever = (*BM25Retriever)(nil)

// BM25Retriever 仅适配 Eino 的 Retrieve 调用；BM25 算法、分词和索引维护由 pg_search 执行。
// 对应索引在 indexer/postgres_schema.go 的 retrieval_index_search_idx 中创建。
type BM25Retriever struct {
	db     queryer
	config ragconfig.BM25
}

// NewBM25Retriever 使用已安装 pg_search 并完成迁移的数据库，连接池由装配方关闭。
func NewBM25Retriever(pool *pgxpool.Pool, cfg ragconfig.BM25) (*BM25Retriever, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, fmt.Errorf("postgres bm25 retriever: nil pool")
	}
	if cfg.TopK == 0 {
		cfg.TopK = ragconfig.DefaultTopK
	}
	return &BM25Retriever{db: pool, config: cfg}, nil
}

// Retrieve 直接执行关键词 SQL，不调用向量模型；参数绑定避免拼接用户输入。
func (r *BM25Retriever) Retrieve(ctx context.Context, query string, opts ...eino.Option) ([]*schema.Document, error) {
	collection, topK, threshold := r.config.CollectionID, r.config.TopK, r.config.ScoreThreshold
	options := eino.GetCommonOptions(&eino.Options{Index: &collection, TopK: &topK, ScoreThreshold: &threshold}, opts...)
	query = strings.TrimSpace(query)
	if err := validateSearchOptions(ctx, query, options); err != nil {
		return nil, err
	}
	return queryDocuments(ctx, r.db, bm25SearchSQL, options, retrieval.MatchKeyword,
		strings.TrimSpace(*options.Index), query, *options.TopK)
}

// ||| 使用 ParadeDB 的关键词匹配，pdb.score(id) 返回 BM25 相关性分数。
// 仅检索当前知识库中启用的子块，再关联权威文档以保留当前版本和原文范围。
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
