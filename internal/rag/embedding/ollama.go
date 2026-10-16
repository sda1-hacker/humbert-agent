package embedding

import (
	"context"
	"fmt"

	einoOllama "github.com/cloudwego/eino-ext/components/embedding/ollama"
	"github.com/cloudwego/eino/components/embedding"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// newOllama 使用原生 /api/embed；只组装组件，配置在统一 New 入口校验。
func newOllama(ctx context.Context, cfg ragconfig.Embedding) (embedding.Embedder, error) {
	// 必须显式关闭服务端截断，否则向量可能只表示正文前半段，仍被当作完整分块入库。
	truncate := false
	nativeCfg := &einoOllama.EmbeddingConfig{
		BaseURL: cfg.BaseURL, Model: cfg.Model, Timeout: cfg.Timeout,
		HTTPClient: newHTTPClient(cfg), Truncate: &truncate,
	}
	if cfg.KeepAlive != nil {
		duration := *cfg.KeepAlive
		nativeCfg.KeepAlive = &duration
	}
	embedder, err := einoOllama.NewEmbedder(ctx, nativeCfg)
	if err != nil {
		return nil, fmt.Errorf("create native Ollama embedder: %w", err)
	}
	// 上游当前会忽略调用级 Model，且用 %v 包装错误；固定模型并通过 context 保留取消原因。
	return &limitedEmbedder{delegate: embedder, budget: cfg.InputBudget, fixedModel: cfg.Model, timeout: cfg.Timeout, dimensions: cfg.Dimensions}, nil
}
