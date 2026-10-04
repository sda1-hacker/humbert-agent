package openai

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
	"github.com/sda1-hacker/humbert-agent/internal/rag/embeddinginput"
)

var (
	ErrMissingModel      = errors.New("openai embedding: missing model")
	ErrInvalidDimensions = errors.New("openai embedding: invalid dimensions")
	ErrInvalidTimeout    = errors.New("openai embedding: invalid timeout")
)

// Config 描述一个 OpenAI-compatible Embedding Endpoint。
//
// 这个 Adapter 不只可以连接 OpenAI 官方服务。
//
// 只要服务兼容：
//
//	POST /v1/embeddings
//
// 就可以使用，例如：
//
//	OpenAI
//	vLLM
//	LocalAI
//	一些企业内部 OpenAI-compatible gateway
//
// 对于 vLLM:
//
//	BaseURL = "http://127.0.0.1:8001/v1"
//	APIKey  = "EMPTY"
//	Model   = "BAAI/bge-m3"
type Config struct {
	InputBudget embeddinginput.Budget
	APIKey      string

	// BaseURL 必须是 OpenAI API Base。
	//
	// vLLM 通常：
	//
	//     http://127.0.0.1:8001/v1
	//
	// 不是：
	//
	//     http://127.0.0.1:8001/v1/embeddings
	BaseURL string

	Model         string
	ModelRevision string

	// Dimensions <= 0 表示不向 Provider 显式传 dimensions。
	//
	// 我们当前 PostgreSQL 固定：
	//
	//     halfvec(1024)
	//
	// 因此 Composition Root 默认会设置：
	//
	//     1024
	Dimensions int

	Timeout time.Duration

	// HTTPClient 非 nil 时优先使用。
	//
	// 这允许：
	//
	//     自定义 Transport
	//     Proxy
	//     TLS
	//     Trace
	//     测试 httptest.Server
	HTTPClient *http.Client
}

func DefaultConfig() Config {
	return Config{
		Timeout: 30 * time.Second,
	}
}

func (c Config) Validate() error {
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

// New 创建 Eino embedding.Embedder。
//
// 这里没有自己重写 /v1/embeddings HTTP Client，
// 而是直接复用 Eino 官方 eino-ext OpenAI Embedder。
//
// 这样可以继续获得：
//
//	Eino callback
//	embedding.WithModel()
//	OpenAI-compatible protocol
//
// 等能力。
func New(ctx context.Context, cfg Config) (embedding.Embedder, error) {
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

	return embeddinginput.Limit(embedder, cfg.InputBudget), nil
}
