package embedding

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino/components/embedding"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// New 统一校验并选择 eino-ext 模型组件，不在创建时发起模型请求。
// 导入和查询应共享同一个 Embedder，避免使用不同的向量空间。
func New(ctx context.Context, cfg ragconfig.Embedding) (embedding.Embedder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.Effective()
	if cfg.Provider == ragconfig.ProviderOllama {
		return newOllama(ctx, cfg)
	}
	return newOpenAI(ctx, cfg)
}

// newHTTPClient 不修改调用方共享客户端；两种协议都限制响应大小。
func newHTTPClient(cfg ragconfig.Embedding) *http.Client {
	client := http.Client{Timeout: cfg.Timeout}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
		if client.Timeout == 0 {
			client.Timeout = cfg.Timeout
		}
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = retryEmbeddingTransport{
		next:     orderedEmbeddingTransport{next: transport, ollamaDimensions: cfg.Dimensions},
		attempts: cfg.RetryAttempts, interval: cfg.RetryInterval,
	}
	return &client
}

// retryEmbeddingTransport 只重试无副作用的向量请求；不叠加 SDK 之外的整篇任务重试。
// 响应读取也在重试范围内，连接半途断开不会返回残缺向量；鉴权、参数、格式错误直接返回。
type retryEmbeddingTransport struct {
	next     http.RoundTripper
	attempts int
	interval time.Duration
}

func (t retryEmbeddingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	eligible := req.Method == http.MethodPost && (strings.HasSuffix(req.URL.Path, "/embeddings") || strings.HasSuffix(req.URL.Path, "/api/embed"))
	delay := t.interval
	for attempt := 1; ; attempt++ {
		if err := req.Context().Err(); err != nil {
			if req.Body != nil {
				req.Body.Close()
			}
			return nil, err
		}
		resp, err := t.next.RoundTrip(req)
		retry := false
		if err != nil {
			var network net.Error
			retry = !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
				((errors.As(err, &network) && (network.Timeout() || network.Temporary())) ||
					errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
					errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE))
		} else if resp != nil {
			switch resp.StatusCode {
			case 408, 429, 500, 502, 503, 504:
				retry = true
			}
		}
		if !eligible || !retry || attempt >= t.attempts || req.GetBody == nil {
			return resp, err
		}
		wait := delay
		if resp != nil {
			// 尊重 Retry-After；超过单次退避上限时直接交还错误，不提前重发限流请求。
			if value := resp.Header.Get("Retry-After"); value != "" {
				if seconds, e := strconv.ParseInt(value, 10, 64); e == nil && seconds > 10 {
					return resp, err
				} else if e == nil && seconds >= 0 {
					wait = max(wait, time.Duration(seconds)*time.Second)
				} else if at, e := http.ParseTime(value); e == nil {
					if time.Until(at) > 10*time.Second {
						return resp, err
					}
					wait = max(wait, time.Until(at))
				}
			}
			if resp.Body != nil {
				resp.Body.Close()
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
		copy := req.Clone(req.Context())
		copy.Body, err = req.GetBody()
		if err != nil {
			return nil, err
		}
		req = copy
		delay = min(delay*2, 10*time.Second)
	}
}
