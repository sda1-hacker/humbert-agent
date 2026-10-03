package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
)

var _ application.SearchFactory = (*PipelineFactory)(nil)

// PipelineFactoryConfig 保存整个 Search Pipeline 的默认配置。
//
// Hybrid:
//
//	Dense + BM25 + RRF
//
// Reranker:
//
//	可为 nil。
//	nil 时保留 Hybrid Retrieval 顺序。
//
// Search:
//
//	FinalTopK
//	Parent Expansion
type PipelineFactoryConfig struct {
	Hybrid HybridConfig

	Reranker *rerank.Engine

	Search search.Config
}

// PipelineFactory 根据 CollectionID 创建一套绑定 Collection 的 Search Pipeline。
//
// 当前创建成本很低：
//
//	不建立新数据库连接
//	不创建新模型 Client
//
// 只是创建几个持有共享 pool / embedder 的轻量 struct。
//
// 所以第一版不需要为了它引入缓存和生命周期管理。
type PipelineFactory struct {
	pool   *pgxpool.Pool
	config PipelineFactoryConfig
}

func NewPipelineFactory(
	pool *pgxpool.Pool,
	cfg PipelineFactoryConfig,
) (*PipelineFactory, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres pipeline factory: nil pool")
	}

	if cfg.Search.FinalTopK <= 0 {
		cfg.Search = search.DefaultConfig()
	}

	return &PipelineFactory{
		pool:   pool,
		config: cfg,
	}, nil
}

func (f *PipelineFactory) ForCollection(collectionID string) (application.SearchEngine, error) {
	collectionID = strings.TrimSpace(collectionID)

	if collectionID == "" {
		return nil, ErrMissingCollectionID
	}

	hybridCfg := f.config.Hybrid
	hybridCfg.CollectionID = collectionID

	hybrid, err := NewHybridRetriever(f.pool, hybridCfg)
	if err != nil {
		return nil, fmt.Errorf("create hybrid retriever: %w", err)
	}

	var parentLoader search.ParentLoader

	if f.config.Search.ExpandParents {
		loader, err := NewParentLoader(f.pool, collectionID)
		if err != nil {
			return nil, fmt.Errorf("create parent loader: %w", err)
		}

		parentLoader = loader
	}

	retrieve := func(ctx context.Context, query string) ([]retrieval.SearchResult, error) {
		return hybrid.Search(ctx, query)
	}

	pipeline, err := search.NewPipeline(
		retrieve,
		f.config.Reranker,
		parentLoader,
		f.config.Search,
	)

	if err != nil {
		return nil, fmt.Errorf("create search pipeline: %w", err)
	}

	return pipeline, nil
}
