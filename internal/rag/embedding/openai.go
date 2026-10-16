package embedding

import (
	"context"
	"fmt"

	einoopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino/components/embedding"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// newOpenAI 只组装 Eino 原生组件；配置与取消检查由统一 New 入口完成。
func newOpenAI(ctx context.Context, cfg ragconfig.Embedding) (embedding.Embedder, error) {
	encodingFormat := einoopenai.EmbeddingEncodingFormatFloat

	einoCfg := &einoopenai.EmbeddingConfig{
		APIKey:         cfg.APIKey,
		BaseURL:        cfg.BaseURL,
		Model:          cfg.Model,
		Timeout:        cfg.Timeout,
		HTTPClient:     newHTTPClient(cfg),
		EncodingFormat: &encodingFormat,
	}

	if cfg.Dimensions > 0 {
		dimensions := cfg.Dimensions
		einoCfg.Dimensions = &dimensions
	}

	embedder, err := einoopenai.NewEmbedder(ctx, einoCfg)
	if err != nil {
		return nil, fmt.Errorf("create openai compatible embedder: %w", err)
	}

	return &limitedEmbedder{delegate: embedder, budget: cfg.InputBudget, dimensions: cfg.Dimensions}, nil
}
