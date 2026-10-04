package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
	indexerpostgres "github.com/sda1-hacker/humbert-agent/internal/rag/indexer/postgres"
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
	ProfileID   string
	ProfileJSON string
	Hybrid      HybridConfig

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
	if err := cfg.Hybrid.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.Search.Validate(); err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, fmt.Errorf("postgres pipeline factory: nil pool")
	}

	if cfg.Search.FinalTopK <= 0 {
		cfg.Search.FinalTopK = search.DefaultConfig().FinalTopK
	}

	return &PipelineFactory{
		pool:   pool,
		config: cfg,
	}, nil
}

func (f *PipelineFactory) ForCollection(collectionID string) (application.SearchEngine, error) {
	return f.ForRequest(application.SearchRequest{CollectionID: collectionID})
}

func (f *PipelineFactory) ForRequest(req application.SearchRequest) (application.SearchEngine, error) {
	collectionID := req.CollectionID
	collectionID = strings.TrimSpace(collectionID)

	if collectionID == "" {
		return nil, ErrMissingCollectionID
	}

	hybridCfg := f.config.Hybrid
	hybridCfg.CollectionID = collectionID
	searchCfg := f.config.Search
	if req.Limit < 0 || req.Limit > MaxTopK {
		return nil, ErrInvalidTopK
	}
	if req.Limit > 0 {
		searchCfg.FinalTopK = req.Limit
	}
	if hybridCfg.TopK == 0 {
		hybridCfg.TopK = DefaultTopK
	}
	if hybridCfg.ChannelTopK == 0 {
		hybridCfg.ChannelTopK = DefaultTopK
	}
	hybridCfg.TopK = max(hybridCfg.TopK, searchCfg.FinalTopK)
	hybridCfg.ChannelTopK = max(hybridCfg.ChannelTopK, hybridCfg.TopK)

	hybrid, err := NewHybridRetriever(f.pool, hybridCfg)
	if err != nil {
		return nil, fmt.Errorf("create hybrid retriever: %w", err)
	}

	var parentLoader search.ParentLoader

	if searchCfg.ExpandParents {
		loader, err := NewParentLoader(f.pool, collectionID)
		if err != nil {
			return nil, fmt.Errorf("create parent loader: %w", err)
		}

		parentLoader = loader
	}

	var vector *VectorRetriever
	var keyword *BM25Retriever
	switch req.Mode {
	case "", "hybrid":
	case "semantic":
		vector, err = NewVectorRetriever(f.pool, VectorConfig{CollectionID: collectionID, Embedder: hybridCfg.Embedder, TopK: hybridCfg.TopK, ScoreThreshold: hybridCfg.VectorThreshold, Dimensions: hybridCfg.Dimensions})
	case "keyword":
		keyword, err = NewBM25Retriever(f.pool, BM25Config{CollectionID: collectionID, TopK: hybridCfg.TopK, ScoreThreshold: hybridCfg.KeywordThreshold})
	default:
		return nil, fmt.Errorf("rag: unknown search mode %q", req.Mode)
	}
	if err != nil {
		return nil, err
	}
	retrieve := func(ctx context.Context, query string) ([]retrieval.SearchResult, retrieval.Diagnostics, error) {
		if f.config.ProfileID != "" {
			if err := indexerpostgres.EnsureCollectionProfile(ctx, f.pool, collectionID, f.config.ProfileID, f.config.ProfileJSON); err != nil {
				return nil, retrieval.Diagnostics{}, err
			}
		}
		if vector != nil {
			results, err := vector.Search(ctx, query)
			return results, retrieval.Diagnostics{ModeUsed: retrieval.MatchVector}, err
		}
		if keyword != nil {
			results, err := keyword.Search(ctx, query)
			return results, retrieval.Diagnostics{ModeUsed: retrieval.MatchKeyword}, err
		}
		return hybrid.SearchWithDiagnostics(ctx, query)
	}

	pipeline, err := search.NewPipelineWithDiagnostics(
		retrieve,
		f.config.Reranker,
		parentLoader,
		searchCfg,
	)

	if err != nil {
		return nil, fmt.Errorf("create search pipeline: %w", err)
	}

	return pipeline, nil
}
