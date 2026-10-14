package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino/components/embedding"
)

var (
	ErrMissingModel      = errors.New("openai embedding: missing model")
	ErrInvalidDimensions = errors.New("openai embedding: invalid dimensions")
	ErrInvalidTimeout    = errors.New("openai embedding: invalid timeout")
)

// OpenAIConfig 描述兼容 OpenAI 协议的向量服务及请求设置。
type OpenAIConfig struct {
	InputBudget Budget
	APIKey      string

	// BaseURL 是服务的 API 基础地址；完整 embeddings 路径由底层客户端拼接。
	BaseURL string

	Model         string
	ModelRevision string

	// Dimensions 大于零时向模型请求指定维度，否则使用服务默认维度。
	Dimensions int

	Timeout time.Duration

	// HTTPClient 非空时使用调用方客户端，否则按 Timeout 创建客户端。
	HTTPClient *http.Client
}

// DefaultOpenAIConfig 返回请求超时等默认设置，模型与凭据由调用方提供。
func DefaultOpenAIConfig() OpenAIConfig {
	return OpenAIConfig{Timeout: 30 * time.Second}
}

// Validate 检查模型、地址、维度与请求超时。
func (c OpenAIConfig) Validate() error {
	if strings.TrimSpace(c.BaseURL) != "" {
		u, err := url.Parse(strings.TrimSpace(c.BaseURL))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("openai embedding: BaseURL must be an HTTP(S) API base without embedded credentials, query or fragment")
		}
	}
	if err := c.InputBudget.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Model) == "" {
		return ErrMissingModel
	}

	if c.Dimensions < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidDimensions, c.Dimensions)
	}

	if c.Timeout < 0 {
		return fmt.Errorf("%w: %s", ErrInvalidTimeout, c.Timeout)
	}

	return nil
}

// NewOpenAI 校验配置并创建 Eino 原生 Embedder，复用 eino-ext 的协议与回调实现。
func NewOpenAI(ctx context.Context, cfg OpenAIConfig) (embedding.Embedder, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Model = strings.TrimSpace(cfg.Model)

	encodingFormat := einoopenai.EmbeddingEncodingFormatFloat

	einoCfg := &einoopenai.EmbeddingConfig{
		APIKey:         cfg.APIKey,
		BaseURL:        cfg.BaseURL,
		Model:          cfg.Model,
		Timeout:        cfg.Timeout,
		HTTPClient:     cfg.HTTPClient,
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

	return Limit(embedder, cfg.InputBudget), nil
}
