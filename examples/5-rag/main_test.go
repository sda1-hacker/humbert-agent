package main

import (
	"errors"
	"math"
	"testing"
	"time"

	indexerpostgres "github.com/sda1-hacker/humbert-agent/internal/rag/indexer/postgres"
	embeddingopenai "github.com/sda1-hacker/humbert-agent/internal/rag/provider/embedding/openai"
	rerankhttp "github.com/sda1-hacker/humbert-agent/internal/rag/provider/rerank/http"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// TestDefaultConfigUsesPostgresEmbeddingDimension 验证默认请求维度与数据库 halfvec 维度一致。
func TestDefaultConfigUsesPostgresEmbeddingDimension(t *testing.T) {
	cfg := defaultPostgresConfig()

	if cfg.Embedding.Dimensions != indexerpostgres.EmbeddingDimensions {
		t.Fatalf(
			"默认 Embedding Dimension 必须与 PostgreSQL 一致: embedding=%d postgres=%d",
			cfg.Embedding.Dimensions,
			indexerpostgres.EmbeddingDimensions,
		)
	}
}

// TestConfigRequiresDatabaseURL 验证缺少数据库地址时在初始化前明确失败。
func TestConfigRequiresDatabaseURL(t *testing.T) {
	cfg := defaultPostgresConfig()
	cfg.Embedding.Model = "BAAI/bge-m3"

	err := cfg.Validate()

	if !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("缺少 DatabaseURL 应失败: %v", err)
	}
}

// TestConfigRequiresEmbeddingModel 验证模型名称必须配置，即使请求维度使用默认值。
func TestConfigRequiresEmbeddingModel(t *testing.T) {
	cfg := defaultPostgresConfig()
	cfg.DatabaseURL = "postgres://localhost/test"

	err := cfg.Validate()

	if !errors.Is(err, embeddingopenai.ErrMissingModel) {
		t.Fatalf("缺少 Embedding Model 应失败: %v", err)
	}
}

// TestConfigRejectsDimensionMismatch 验证请求指定的维度不能与数据库列冲突。
func TestConfigRejectsDimensionMismatch(t *testing.T) {
	cfg := defaultPostgresConfig()

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

// TestConfigAllowsProviderManagedDimension 验证省略请求 dimensions 时允许由模型决定输出，再在写入/查询时校验。
func TestConfigAllowsProviderManagedDimension(t *testing.T) {
	cfg := defaultPostgresConfig()

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

// TestConfigValidatesRerankProvider 验证开启精排的配置必须提供合法地址与模型。
func TestConfigValidatesRerankProvider(t *testing.T) {
	cfg := defaultPostgresConfig()

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

// TestEmbeddingProfileIgnoresCredentialsAndRequestSettings 验证档案包含模型权重和输入规则，但不包含密钥、超时或请求维度省略。
func TestEmbeddingProfileIgnoresCredentialsAndRequestSettings(t *testing.T) {
	c := defaultPostgresConfig()
	c.Embedding.Model = "model-a"
	before := c.EmbeddingProfile().ID()
	c.Embedding.APIKey = "rotated-key"
	c.Embedding.Timeout = time.Minute
	c.Embedding.Dimensions = 0
	if c.EmbeddingProfile().ID() != before {
		t.Fatal("密钥、超时或省略请求维度不应改变向量空间档案")
	}
	c.Embedding.ModelRevision = "new-weights"
	if c.EmbeddingProfile().ID() == before {
		t.Fatal("模型权重版本必须进入向量空间档案")
	}
	c.Embedding.ModelRevision = ""
	c.InputBuilder.TitleKeys = []string{"custom-title"}
	if c.EmbeddingProfile().ID() == before {
		t.Fatal("检索文本规则必须进入向量空间档案")
	}
}

// TestEvaluateMergedParentEvidence 验证同父块合并后相关子块不丢失，重复证据只算一次。
func TestEvaluateMergedParentEvidence(t *testing.T) {
	results := []retrieval.SearchResult{{
		ChunkID: "a", ContextChunkID: "parent",
		Evidence: []retrieval.HitEvidence{{ChunkID: "a"}, {ChunkID: "b"}, {ChunkID: "x"}, {ChunkID: "b"}},
	}}
	m := evaluate(results, map[string]bool{"a": true, "b": true})
	if m.Retrieved != 3 || m.Hits != 2 || m.Recall != 1 || math.Abs(m.Precision-2.0/3) > 1e-9 {
		t.Fatalf("一条父块上下文含三条子块证据，应为召回率 100%%、准确率 66.67%%：%+v", m)
	}
}

// TestEvaluateContextDoesNotInventHits 验证父块中包含相关内容不等于召回了该子块。
func TestEvaluateContextDoesNotInventHits(t *testing.T) {
	results := []retrieval.SearchResult{{ChunkID: "a", ContextChunkID: "parent", ContextContent: "包含子块 a 和 b 的内容"}}
	m := evaluate(results, map[string]bool{"a": true, "b": true})
	if m.Retrieved != 1 || m.Hits != 1 || m.Recall != 0.5 || m.Precision != 1 {
		t.Fatalf("只有子块 a 被召回，应为召回率 50%%、准确率 100%%：%+v", m)
	}
	m = evaluate(nil, map[string]bool{"a": true})
	if m.Recall != 0 || m.Precision != 0 {
		t.Fatalf("没有结果时指标应为零：%+v", m)
	}
}
