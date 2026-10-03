package rag

import (
	"errors"
	"testing"

	indexerpostgres "github.com/sda1-hacker/humbert-agent/internal/rag/indexer/postgres"
	embeddingopenai "github.com/sda1-hacker/humbert-agent/internal/rag/provider/embedding/openai"
	rerankhttp "github.com/sda1-hacker/humbert-agent/internal/rag/provider/rerank/http"
)

func TestDefaultConfigUsesPostgresEmbeddingDimension(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Embedding.Dimensions != indexerpostgres.EmbeddingDimensions {
		t.Fatalf(
			"默认 Embedding Dimension 必须与 PostgreSQL 一致: embedding=%d postgres=%d",
			cfg.Embedding.Dimensions,
			indexerpostgres.EmbeddingDimensions,
		)
	}
}

func TestConfigRequiresDatabaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Embedding.Model = "BAAI/bge-m3"

	err := cfg.Validate()

	if !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("缺少 DatabaseURL 应失败: %v", err)
	}
}

func TestConfigRequiresEmbeddingModel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DatabaseURL = "postgres://localhost/test"

	err := cfg.Validate()

	if !errors.Is(err, embeddingopenai.ErrMissingModel) {
		t.Fatalf("缺少 Embedding Model 应失败: %v", err)
	}
}

func TestConfigRejectsDimensionMismatch(t *testing.T) {
	cfg := DefaultConfig()

	cfg.DatabaseURL = "postgres://localhost/test"
	cfg.Embedding.Model = "embedding-model"
	cfg.Embedding.Dimensions = 768

	err := cfg.Validate()

	if !errors.Is(err, ErrEmbeddingDimensionMismatch) {
		t.Fatalf(
			"Embedding / PostgreSQL Dimension 不一致应该失败: %v",
			err,
		)
	}
}

func TestConfigAllowsProviderManagedDimension(t *testing.T) {
	cfg := DefaultConfig()

	cfg.DatabaseURL = "postgres://localhost/test"
	cfg.Embedding.Model = "embedding-model"

	// 0 = 不向 Provider 显式传 dimensions。
	//
	// 真正返回结果仍然会在 Index / Retrieval 时被强校验。
	cfg.Embedding.Dimensions = 0

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Dimensions=0 应该允许: %v", err)
	}
}

func TestConfigValidatesRerankProvider(t *testing.T) {
	cfg := DefaultConfig()

	cfg.DatabaseURL = "postgres://localhost/test"
	cfg.Embedding.Model = "embedding-model"

	rerankCfg := rerankhttp.DefaultConfig()

	// Endpoint / Model 都没有配置。
	cfg.RerankProvider = &rerankCfg

	err := cfg.Validate()

	if !errors.Is(err, rerankhttp.ErrMissingEndpoint) {
		t.Fatalf(
			"非法 Rerank Provider Config 应被 Composition Root 检查: %v",
			err,
		)
	}
}
