package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
	indexerpostgres "github.com/sda1-hacker/humbert-agent/internal/rag/indexer/postgres"
	tabulaloader "github.com/sda1-hacker/humbert-agent/internal/rag/loader/tabula"
	embeddingopenai "github.com/sda1-hacker/humbert-agent/internal/rag/provider/embedding/openai"
	rerankhttp "github.com/sda1-hacker/humbert-agent/internal/rag/provider/rerank/http"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	retrieverpostgres "github.com/sda1-hacker/humbert-agent/internal/rag/retriever/postgres"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
)

var (
	ErrMissingDatabaseURL         = errors.New("rag: missing database url")
	ErrEmbeddingDimensionMismatch = errors.New("rag: embedding dimension does not match postgres schema")
)

// Config 是整个 RAG Foundation 的 Composition Config。
//
// 这里不重新发明：
//
//	Chunk Config
//	Search Config
//	Rerank Config
//
// 而是直接复用各模块自己的 Config。
//
// Composition Root 的职责只有一个：
//
//	创建对象
//	注入依赖
//	保证同一 Embedder 同时用于 Index + Query
type Config struct {
	DatabaseURL string

	// EnsureSchema=true：
	//
	//     自动 CREATE EXTENSION
	//     自动 EnsureSchema
	//
	// 很适合：
	//
	//     Demo
	//     Local Development
	//
	// 生产环境建议：
	//
	//     false
	//
	// 然后由 Migration / DBA 负责 Schema。
	EnsureSchema bool

	Loader tabulaloader.Config

	Embedding embeddingopenai.Config

	Indexer indexerpostgres.Config

	Hybrid retrieverpostgres.HybridConfig

	// RerankProvider == nil：
	//
	//     不调用远程 Rerank Model。
	//
	// RRF Retrieval 仍然完全可用。
	RerankProvider *rerankhttp.Config

	Rerank rerank.Config

	Search search.Config
}

func DefaultConfig() Config {
	embeddingCfg := embeddingopenai.DefaultConfig()

	// 当前数据库固定：
	//
	//     HALFVEC(1024)
	//
	// 所以默认请求1024维。
	embeddingCfg.Dimensions = indexerpostgres.EmbeddingDimensions

	return Config{
		EnsureSchema: true,

		Loader: tabulaloader.DefaultConfig(),

		Embedding: embeddingCfg,

		Indexer: indexerpostgres.DefaultConfig(),

		Hybrid: retrieverpostgres.DefaultHybridConfig(),

		Rerank: rerank.DefaultConfig(),

		Search: search.DefaultConfig(),
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return ErrMissingDatabaseURL
	}

	if err := c.Embedding.Validate(); err != nil {
		return fmt.Errorf("rag embedding config: %w", err)
	}

	// Dimensions=0 允许某些 Provider 不接受 dimensions 参数。
	//
	// 但是它实际返回的 Vector 最终仍会被：
	//
	//     PGIndexer
	//     VectorRetriever
	//
	// 强校验为1024维。
	if c.Embedding.Dimensions > 0 &&
		c.Embedding.Dimensions != indexerpostgres.EmbeddingDimensions {

		return fmt.Errorf(
			"%w: postgres=%d embedding=%d",
			ErrEmbeddingDimensionMismatch,
			indexerpostgres.EmbeddingDimensions,
			c.Embedding.Dimensions,
		)
	}

	if c.RerankProvider != nil {
		if err := c.RerankProvider.Validate(); err != nil {
			return fmt.Errorf("rag rerank config: %w", err)
		}
	}

	return nil
}

// RAG 是整个系统最终的运行时对象。
//
// 上层通常只需要：
//
//	Ingest()
//	Search()
//	Close()
//
// 不再需要手动理解内部几十个组件。
type RAG struct {
	service *application.Service

	pool *pgxpool.Pool

	embedder embedding.Embedder
}

// NewRAG 完成整个 Dependency Composition。
//
// 最重要的不变量：
//
//	同一个 embedder instance
//
// 同时注入：
//
//	PGIndexer
//	HybridRetriever
//
// 从架构层面防止：
//
//	入库用 Model A
//	查询用 Model B
//
// 这种非常隐蔽但致命的问题。
func NewRAG(ctx context.Context, cfg Config) (*RAG, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// -------------------------------------------------------------------------
	// PostgreSQL
	// -------------------------------------------------------------------------

	if cfg.EnsureSchema {
		if err := indexerpostgres.EnsureExtensions(ctx, cfg.DatabaseURL); err != nil {
			return nil, fmt.Errorf("bootstrap postgres extensions: %w", err)
		}
	}

	pool, err := indexerpostgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	success := false

	defer func() {
		if !success {
			pool.Close()
		}
	}()

	if cfg.EnsureSchema {
		if err := indexerpostgres.EnsureSchema(ctx, pool); err != nil {
			return nil, fmt.Errorf("ensure rag postgres schema: %w", err)
		}
	}

	// -------------------------------------------------------------------------
	// Embedding
	// -------------------------------------------------------------------------

	embedder, err := embeddingopenai.New(ctx, cfg.Embedding)
	if err != nil {
		return nil, fmt.Errorf("create embedding provider: %w", err)
	}

	// -------------------------------------------------------------------------
	// Indexer
	//
	// 无论 cfg.Indexer.Embedder 原本是什么，
	// Composition Root 都强制覆盖成上面的唯一 Embedder。
	// -------------------------------------------------------------------------

	indexerCfg := cfg.Indexer
	indexerCfg.Embedder = embedder

	pgIndexer, err := indexerpostgres.NewIndexer(pool, indexerCfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres indexer: %w", err)
	}

	// -------------------------------------------------------------------------
	// Reranker
	// -------------------------------------------------------------------------

	rerankCfg := cfg.Rerank

	var scorer rerank.Scorer

	if cfg.RerankProvider != nil {
		httpScorer, err := rerankhttp.NewClient(*cfg.RerankProvider)
		if err != nil {
			return nil, fmt.Errorf("create rerank provider: %w", err)
		}

		scorer = httpScorer
	} else {
		// Provider 没配置时显式进入 Disabled，
		// Diagnostics 会得到：
		//
		//     OutcomeDisabled
		//
		// 而不是让用户误以为 Rerank Model 出错。
		rerankCfg.Disabled = true
	}

	rerankEngine := rerank.NewEngine(scorer, rerankCfg)

	// -------------------------------------------------------------------------
	// Retrieval
	//
	// 和 Indexer 使用同一个 embedder。
	// -------------------------------------------------------------------------

	hybridCfg := cfg.Hybrid
	hybridCfg.Embedder = embedder

	searchFactory, err := retrieverpostgres.NewPipelineFactory(
		pool,
		retrieverpostgres.PipelineFactoryConfig{
			Hybrid:   hybridCfg,
			Reranker: rerankEngine,
			Search:   cfg.Search,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create search pipeline factory: %w", err)
	}

	// -------------------------------------------------------------------------
	// Loader
	// -------------------------------------------------------------------------

	loader := tabulaloader.NewLoader(cfg.Loader)

	// -------------------------------------------------------------------------
	// Application Facade
	// -------------------------------------------------------------------------

	service := application.NewService(
		loader,
		pgIndexer,
		searchFactory,
	)

	success = true

	return &RAG{
		service:  service,
		pool:     pool,
		embedder: embedder,
	}, nil
}

// Ingest 是最终 ingestion 入口。
func (r *RAG) Ingest(
	ctx context.Context,
	req application.IngestRequest,
) (application.IngestResponse, error) {
	if r == nil || r.service == nil {
		return application.IngestResponse{}, errors.New("rag: runtime is not initialized")
	}

	return r.service.Ingest(ctx, req)
}

// Search 是最终 retrieval 入口。
func (r *RAG) Search(
	ctx context.Context,
	req application.SearchRequest,
) (search.Response, error) {
	if r == nil || r.service == nil {
		return search.Response{}, errors.New("rag: runtime is not initialized")
	}

	return r.service.Search(ctx, req)
}

// Service 暴露 Application Service。
//
// 一般业务代码不需要调用这个方法；
// 它主要用于：
//
//	高级集成
//	自定义 Handler
//	Integration Test
func (r *RAG) Service() *application.Service {
	if r == nil {
		return nil
	}

	return r.service
}

// Embedder 暴露当前统一 Embedder。
//
// 主要用于：
//
//	Debug
//	Smoke Test
//	Evaluation
func (r *RAG) Embedder() embedding.Embedder {
	if r == nil {
		return nil
	}

	return r.embedder
}

// Close 释放数据库连接池。
func (r *RAG) Close() {
	if r == nil {
		return
	}

	if r.pool != nil {
		r.pool.Close()
	}
}
