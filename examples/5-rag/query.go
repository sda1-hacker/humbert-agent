package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	eino "github.com/cloudwego/eino/components/retriever"

	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	ragembedding "github.com/sda1-hacker/humbert-agent/internal/rag/embedding"
	ragindexer "github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retriever"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
)

// runQuery 展示：问题 → 查询向量/关键词召回 → 可选精排 → 父块扩展 → 上下文预算 → 返回证据。
// 查询不执行迁移或写入模型档案；不创建回答模型，也不生成自然语言答案。
func runQuery(ctx context.Context, args []string, out io.Writer) error {
	set := flag.NewFlagSet("query", flag.ContinueOnError)
	set.SetOutput(out)
	query := set.String("query", "", "查询问题；semantic/hybrid 会调用配置的 Embedding 模型")
	mode := set.String("mode", "hybrid", "检索方式：hybrid（向量+BM25+RRF）、semantic（向量）、keyword（BM25）")
	limit := set.Int("limit", 0, "最终上下文条数，0 使用 search.final_top_k")
	cfg, err := loadConfig(set, args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*query) == "" || *limit < 0 {
		return errors.New("请提供非空 -query，-limit 必须非负")
	}
	if *mode != "hybrid" && *mode != "semantic" && *mode != "keyword" {
		return errors.New("-mode 只支持 hybrid、semantic 或 keyword")
	}
	if *limit > ragconfig.PostgresMaxTopK {
		return ragconfig.ErrInvalidTopK
	}
	pool, err := openPostgres(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := ragindexer.NewDocumentStore(pool, ragindexer.DocumentConfig{})
	if err != nil {
		return err
	}

	// keyword 完全不创建 Embedding；hybrid 的向量通道权重为 0 时同样跳过。
	var vector, keyword, component eino.Retriever
	if *mode == "semantic" || (*mode == "hybrid" && cfg.Hybrid.Effective().RRF.VectorWeight > 0) {
		embedder, err := ragembedding.New(ctx, cfg.Embedding)
		if err != nil {
			return err
		}
		vectorCfg := cfg.Vector
		vectorCfg.Dimensions = cfg.Embedding.Dimensions
		vectorCfg.Embedder = embedder
		vector, err = retriever.NewVectorRetriever(pool, vectorCfg)
		if err != nil {
			return err
		}
	}
	if *mode != "semantic" {
		keyword, err = retriever.NewBM25Retriever(pool, cfg.Keyword)
		if err != nil {
			return err
		}
	}
	switch *mode {
	case "semantic":
		component = vector
	case "keyword":
		component = keyword
	case "hybrid":
		component, err = retriever.NewHybrid(vector, keyword, cfg.Hybrid)
		if err != nil {
			return err
		}
	}
	// 精排是可选的独立阶段，默认 disabled=true；开启后只请求精排服务，不生成答案。
	var scorer rerank.Scorer
	if !cfg.Rerank.Disabled {
		scorer, err = rerank.NewHTTPClient(*cfg.RerankProvider)
		if err != nil {
			return err
		}
	}
	profile := embeddingProfile(cfg)
	service, err := rag.New(ctx, rag.Dependencies{
		PublishedChunks: store,
		Config: rag.Config{
			Retriever: component, VectorRetriever: vector, KeywordRetriever: keyword,
			RecallTopK: cfg.Hybrid.Effective().TopK, Search: cfg.Search,
			Reranker: rerank.NewEngine(scorer, cfg.Rerank, nil),
			ParentLoader: func(collection string) (search.ParentLoader, error) {
				return retriever.NewParentLoader(pool, collection)
			},
			BeforeSearch: func(ctx context.Context, collection string) error {
				// 模型名相同、维度相同也不代表同一向量空间，必须与入库档案匹配。
				return ragindexer.CheckCollectionProfile(ctx, pool, collection, profile.ID())
			},
		},
	})
	if err != nil {
		return err
	}
	defer service.Close()
	result, err := service.Search(ctx, rag.SearchRequest{
		CollectionID: cfg.CollectionID, Query: *query, Mode: *mode, Limit: *limit,
	})
	if err != nil {
		return err
	}
	return writeJSON(out, result)
}
