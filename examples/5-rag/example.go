// Package ragexample 集中两个示例的固定 RAG 参数与装配，不读取 RAG YAML 或命令行参数。
// 图片模型复用应用配置与凭据，不在这里重复填写。
// 从仓库根目录依次运行：
//
//	go run ./examples/5-rag/upload
//	go run ./examples/5-rag/query
//
// 需要 PostgreSQL 14+、pgvector 0.6.2+、pg_search 0.20.10+，以及已安装模型的本地 Ollama。
// 图片理解复用应用的视觉模型；精排可选，仍不接回答模型或 Agent。
package ragexample

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	appconfig "github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/credential"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	ragindexer "github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
)

const (
	CollectionID = "rag-simple-example"                   // 上传和查询使用同一个知识库。
	FilePath     = "/Users/sda1_hacker/Desktop/电网一张图.pdf" // 改成 PDF、Word 等本地文件路径即可验证解析。
	Question     = "一张图"                                  // 固定查询问题。
)

// Config 集中写死实验参数；其余限额沿用正式 RAG 配置的默认值。
func Config() ragconfig.Config {
	cfg := ragconfig.Default()
	// 使用独立的示例库，避免把实验数据写入已有业务库；首次上传自动创建缺失的库。
	cfg.Database = ragconfig.Database{
		Host:     "localhost",
		Port:     5432,
		Name:     "humbert_rag_example",
		User:     "root",
		Password: "123456",
		SSLMode:  "disable", // 仅本地测试账户。
	}
	cfg.CollectionID = CollectionID
	cfg.SourceDirectory = ".rag-example/sources" // 保留上传原件，数据库只保存引用及分块。
	cfg.Loader.ParseOptions.CacheDirectory = ".rag-example/parse-cache"
	cfg.Loader.AssetDirectory = ".rag-example/images"
	// 开启后会调用“设置 → 多媒体”选择的真实图片模型；false 只保存图片、索引正文。
	// 本示例不读取 rag.example.yaml，模型配置和凭据复用应用的 Model Registry。
	cfg.Loader.RecognizeImages = true
	cfg.Loader.ImageRecognition.Provider = ragconfig.ProviderAgent
	// cfg.Loader.ParseOptions.OCRLanguage = "chi_sim+eng" // 仅本地 Tesseract 使用。
	cfg.Embedding.Provider = ragconfig.ProviderOllama
	cfg.Embedding.BaseURL = "http://localhost:11434"
	cfg.Embedding.Model = "embeddinggemma-2:740m"
	cfg.Embedding.Dimensions = 128 // 唯一维度配置：建表、入库和查询都取这里的值。
	cfg.Postgres.Dimensions = cfg.Embedding.Dimensions
	// 解析和切分沿用统一的 WeKnora 预设，不为当前文件强制标题策略。
	// 跨页表格可选接入与 WeKnora 相同的结构化服务；空值仍用 Tabula，不会自动发送文档。
	// cfg.Loader.ParseOptions.PDF.PaddleOCRVLEndpoint = "http://localhost:8080"
	cfg.Chunking.ParentChunkSize = 4096              // 父块提供上下文；修改切分参数后重新 upload。
	cfg.Chunking.ChildChunkSize = 384                // 子块参与检索，过小易丢语义，过大易带入无关正文。
	cfg.Chunking.Splitter.ChunkOverlap = 80          // 父子模式中正值开启重叠，实际子块重叠取子块大小的 1/5；0 关闭。
	cfg.Hybrid.TopK, cfg.Hybrid.ChannelTopK = 50, 50 // 融合候选数、每路候选数。
	cfg.Hybrid.RRF.VectorWeight, cfg.Hybrid.RRF.KeywordWeight = 0.7, 0.3
	cfg.Vector.ScoreThreshold, cfg.Keyword.ScoreThreshold = 0.15, 0.30 // 两种原始分数的尺度不同。
	cfg.Search.FinalTopK = 3                                           // 最终返回子块数，也是评测中的 K。
	cfg.Search.CollapseSameParent = false                              // 评测按子块计数，保留每个子块的独立排名。
	// 本地模型首次加载可能较慢，整次检索的超时也要覆盖模型加载时间。
	cfg.Search.Timeout, cfg.Hybrid.Timeout = 2*time.Minute, 2*time.Minute
	cfg.Hybrid.FailurePolicy = ragconfig.FailureStrict // 双路都成功才算验证通过。
	cfg.Rerank.Disabled = true                         // 用户决定是否精排；false 时同时填写下面的服务配置。
	// cfg.RerankProvider = &ragconfig.RerankHTTP{Endpoint: "精排服务完整 URL", Model: "精排模型名"}
	return cfg
}

// NewLoader 只装配示例需要的模型注册表，不启动 Agent、MCP 或后台任务。
// 正式桌面入口可直接把现有 app.Models() 注入 Loader.ImageModels，无需重复初始化。
func NewLoader(ctx context.Context, cfg ragconfig.Loader) (*rag.Loader, error) {
	if cfg.RecognizeImages && cfg.ImageRecognition.Effective().Provider == ragconfig.ProviderAgent && cfg.ImageText == nil && cfg.ImageModels == nil {
		appCfg, err := appconfig.Load()
		if err != nil {
			return nil, err
		}
		store, err := models.NewStore(ctx, appCfg.Paths.ProvidersFile, appCfg.Paths.ModelsFile)
		if err != nil {
			return nil, err
		}
		credentials, err := credential.NewSystem(appCfg.Paths.SecretsDir)
		if err != nil {
			return nil, err
		}
		cfg.ImageModels = models.NewRegistry(store, credentials, logging.NewBootstrap())
	}
	return rag.NewLoader(cfg), nil
}

// NewReranker 关闭时不创建客户端、不请求模型；启用时校验服务配置，错误不能伪装成已精排。
func NewReranker(cfg ragconfig.Config) (*rerank.Engine, error) {
	if cfg.Rerank.Disabled {
		return rerank.NewEngine(nil, cfg.Rerank, nil), nil
	}
	if cfg.RerankProvider == nil {
		return nil, fmt.Errorf("开启精排时需要配置 RerankProvider 的地址和模型")
	}
	scorer, err := rerank.NewHTTPClient(*cfg.RerankProvider)
	if err != nil {
		return nil, err
	}
	return rerank.NewEngine(scorer, cfg.Rerank, nil), nil
}

// OpenPostgres 仅上传入口初始化数据库；查询入口只连接并检查已有表和向量维度。
func OpenPostgres(ctx context.Context, cfg ragconfig.Config, initialize bool) (*pgxpool.Pool, error) {
	if err := cfg.ValidatePostgres(); err != nil {
		return nil, err
	}
	dsn := cfg.Database.DSN()
	if initialize {
		if err := ragindexer.EnsureDatabase(ctx, dsn, cfg.Postgres); err != nil {
			return nil, err
		}
		fmt.Printf("数据库已就绪：%s:%d/%s（不存在时自动创建）\n", cfg.Database.Host, cfg.Database.Port, cfg.Database.Name)
		if err := ragindexer.EnsureExtensions(ctx, dsn, cfg.Postgres); err != nil {
			return nil, err
		}
	}
	pool, err := ragindexer.NewPool(ctx, dsn, cfg.Postgres)
	if err != nil {
		return nil, err
	}
	if initialize {
		err = ragindexer.Migrate(ctx, pool, cfg.Postgres)
	}
	if err == nil {
		err = ragindexer.CheckSchema(ctx, pool, cfg.Embedding.Dimensions)
	}
	if err != nil {
		pool.Close()
		return nil, err
	}
	if initialize {
		fmt.Println("RAG 数据表、向量索引和 BM25 索引已就绪")
	}
	return pool, nil
}

// Run 让 Ctrl+C 同时取消解析、模型和 SQL；业务函数返回前完成清理，再退出进程。
func Run(action func(context.Context) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	err := action(ctx)
	cancel()
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, logging.SafeErrorText(err, 2048))
		os.Exit(1)
	}
}

func PrintJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
