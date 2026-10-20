package knowledge

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/embedding"
	"github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retriever"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
)

type engine struct {
	*rag.Service
	pool    *pgxpool.Pool
	store   *indexer.Indexer
	sources *rag.LocalSourceStore
	config  ragconfig.Config
}

// buildEngine 仅在用户初始化、上传或检索时连接外部服务；应用启动不依赖 PostgreSQL 在线。
// 更换存储时替换这里的 Eino 组件，知识库页面、Agent 工具与核心导入流程无需改写。
func (m *Manager) buildEngine(ctx context.Context, initialize bool) (_ *engine, err error) {
	values := clone(m.state.Values)
	for key, id := range m.state.Credentials {
		value, e := m.host.Credentials.Get(ctx, id)
		if e != nil {
			return nil, e
		}
		if key == "rerank_provider.headers" {
			var headers map[string]string
			if err := json.Unmarshal([]byte(value), &headers); err != nil {
				return nil, err
			}
			setValue(values, key, headers)
		} else {
			setValue(values, key, value)
		}
	}
	values["collection_id"] = "desktop"
	cfg, err := ragconfig.FromValues(values)
	if err != nil {
		return nil, err
	}
	cfg.SourceDirectory = filepath.Join(m.directory, "sources")
	cfg.Loader.AssetDirectory = filepath.Join(m.directory, "images")
	cfg.Loader.ParseOptions.CacheDirectory = filepath.Join(m.directory, "parse-cache")
	cfg.Loader.ImageModels = m.imageModels
	cfg.Postgres.Dimensions = cfg.Embedding.Dimensions // 建表、索引和查询只使用同一维度来源。
	if err := cfg.ValidatePostgres(); err != nil {
		return nil, err
	}
	dsn := cfg.Database.DSN()
	if initialize {
		if err := indexer.EnsureDatabase(ctx, dsn, cfg.Postgres); err != nil {
			return nil, err
		}
		if err := indexer.EnsureExtensions(ctx, dsn, cfg.Postgres); err != nil {
			return nil, err
		}
	}
	pool, err := indexer.NewPool(ctx, dsn, cfg.Postgres)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			pool.Close()
		}
	}()
	if initialize {
		if err = indexer.Migrate(ctx, pool, cfg.Postgres); err != nil {
			return nil, err
		}
	}
	if err = indexer.CheckSchema(ctx, pool, cfg.Embedding.Dimensions); err != nil {
		return nil, err
	}
	sources, err := rag.NewLocalSourceStore(cfg.SourceDirectory, cfg.Loader.ParseOptions.MaxInputBytes)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = sources.Close()
		}
	}()
	embedder, err := embedding.New(ctx, cfg.Embedding)
	if err != nil {
		return nil, err
	}
	profile := embedding.NewProfile(cfg.Embedding, cfg.InputBuilder)
	ic := cfg.Indexer
	ic.Embedder = embedder
	ic.Dimensions = cfg.Embedding.Dimensions
	ic.InputBudget = cfg.Embedding.InputBudget
	ic.ProfileID, ic.ProfileJSON = profile.ID(), profile.JSON()
	ic.VersionedDocuments = true
	store, err := indexer.NewIndexer(pool, ic)
	if err != nil {
		return nil, err
	}
	vc := cfg.Vector
	vc.Embedder, vc.Dimensions = embedder, cfg.Embedding.Dimensions
	vector, err := retriever.NewVectorRetriever(pool, vc)
	if err != nil {
		return nil, err
	}
	keyword, err := retriever.NewBM25Retriever(pool, cfg.Keyword)
	if err != nil {
		return nil, err
	}
	hybrid, err := retriever.NewHybrid(vector, keyword, cfg.Hybrid)
	if err != nil {
		return nil, err
	}
	var scorer rerank.Scorer
	if !cfg.Rerank.Disabled {
		scorer, err = rerank.NewHTTPClient(*cfg.RerankProvider)
		if err != nil {
			return nil, err
		}
	}
	loader := rag.NewLoader(cfg.Loader)
	service, err := rag.New(ctx, rag.Dependencies{
		Loader: loader, Images: loader, ImageStates: store, Indexer: store, Lifecycle: store, Documents: store,
		Sources: sources, ChunkReader: store, PublishedChunks: store,
		Config: rag.Config{Ingest: cfg.Ingest, InputBudget: cfg.Embedding.InputBudget, InputBuilder: cfg.InputBuilder,
			Retriever: hybrid, VectorRetriever: vector, KeywordRetriever: keyword, RecallTopK: cfg.Hybrid.TopK,
			Search: cfg.Search, Reranker: rerank.NewEngine(scorer, cfg.Rerank, nil),
			ParentLoader: func(collection string) (search.ParentLoader, error) {
				return retriever.NewParentLoader(pool, collection)
			},
			BeforeSearch: func(ctx context.Context, collection string) error {
				return indexer.CheckCollectionProfile(ctx, pool, collection, profile.ID())
			},
		},
		Close: func() { _ = sources.Close(); pool.Close() },
	})
	if err != nil {
		return nil, err
	}
	return &engine{service, pool, store, sources, cfg}, nil
}
