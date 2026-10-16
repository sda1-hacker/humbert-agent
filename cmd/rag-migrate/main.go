// rag-migrate 按配置创建缺失的 RAG 数据库、安装扩展并迁移表和索引。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	ragindexer "github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, logging.SafeErrorText(err, 2048))
		os.Exit(1)
	}
}
func run() error {
	documentsOnly := flag.Bool("documents-only", false, "仅初始化权威文档库，不安装向量或关键词扩展")
	// 与 RAG example 使用同一份文件，避免迁移和导入分别读取不同数据库配置。
	defaultPath := strings.TrimSpace(os.Getenv("HUMBERT_RAG_CONFIG"))
	if defaultPath == "" {
		defaultPath = "rag.example.yaml"
	}
	configPath := flag.String("config", defaultPath, "RAG YAML 路径；默认读取项目根目录 rag.example.yaml，可用 HUMBERT_RAG_CONFIG 指定")
	flag.Parse()
	cfg, err := ragconfig.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}
	// 文档模式不依赖向量；完整 PG 模式在建库前校验并传入唯一的输出维度。
	if !*documentsOnly && (cfg.Embedding.Dimensions <= 0 || cfg.Embedding.Dimensions > ragconfig.PostgresMaxDimensions) {
		return fmt.Errorf("%w: 请填写 embedding.dimensions，PG halfvec HNSW 支持 1..%d", ragconfig.ErrInvalidDimensions, ragconfig.PostgresMaxDimensions)
	}
	cfg.Postgres.Dimensions = cfg.Embedding.Dimensions
	dsn := cfg.Database.DSN()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Postgres.Effective().MigrationTimeout)
	defer cancel()
	if err := ragindexer.EnsureDatabase(ctx, dsn, cfg.Postgres); err != nil {
		return err
	}
	if !*documentsOnly {
		if err := ragindexer.EnsureExtensions(ctx, dsn, cfg.Postgres); err != nil {
			return err
		}
	}
	createPool := ragindexer.NewPool
	if *documentsOnly {
		createPool = ragindexer.NewDocumentPool
	}
	pool, err := createPool(ctx, dsn, cfg.Postgres)
	if err != nil {
		return err
	}
	defer pool.Close()
	migrate := ragindexer.Migrate
	version := ragindexer.SchemaVersion
	if *documentsOnly {
		migrate, version = ragindexer.MigrateDocuments, ragindexer.DocumentSchemaVersion
	}
	if err = migrate(ctx, pool, cfg.Postgres); err != nil {
		return err
	}
	fmt.Printf("RAG schema ready: version %d\n", version)
	return nil
}
