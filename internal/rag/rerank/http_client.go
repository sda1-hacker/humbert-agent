package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

const (
	maxResponseBodySize = 4 << 20 // 4 MiB
)

var (
	ErrEmptyQuery           = errors.New("rerank http: empty query")
	ErrInvalidResponse      = errors.New("rerank http: invalid response")
	ErrInvalidScore         = errors.New("rerank http: invalid relevance score")
	ErrResponseBodyTooLarge = errors.New("rerank http: response body too large")
)

var _ Scorer = (*HTTPClient)(nil)

// HTTPClient 精排 HTTP 客户端，按输入顺序还原服务返回的相关性分数。
type HTTPClient struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
	headers  map[string]string
}

// NewHTTPClient 校验配置并创建精排客户端，优先使用调用方提供的 HTTPClient。
func NewHTTPClient(cfg ragconfig.RerankHTTP) (*HTTPClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// 复制客户端避免修改调用方共享状态，并为没有超时的自定义客户端补齐边界。
	client := http.Client{}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	if client.Timeout == 0 {
		client.Timeout = cfg.Timeout
		if client.Timeout == 0 {
			client.Timeout = ragconfig.DefaultRequestTimeout
		}
	}

	headers := make(map[string]string, len(cfg.Headers))

	for key, value := range cfg.Headers {
		headers[key] = value
	}

	return &HTTPClient{
		endpoint: strings.TrimSpace(cfg.Endpoint),
		apiKey:   strings.TrimSpace(cfg.APIKey),
		model:    strings.TrimSpace(cfg.Model),
		client:   &client,
		headers:  headers,
	}, nil
}

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

type rerankResult struct {
	Index          *int     `json:"index"`
	RelevanceScore *float64 `json:"relevance_score"`
}

// Score 请求精排分数，并按响应中的 index 对齐输入顺序；不能直接使用服务返回的排序。
func (c *HTTPClient) Score(ctx context.Context, query string, passages []string) ([]float64, error) {
	query = strings.TrimSpace(query)

	if query == "" {
		return nil, ErrEmptyQuery
	}

	if len(passages) == 0 {
		return []float64{}, nil
	}

	requestPayload := rerankRequest{
		Model:     c.model,
		Query:     query,
		Documents: passages,

		// Engine 需要每一个 Passage 的 ModelScore，
		// 所以必须要求 Provider 返回全部结果。
		TopN: len(passages),
	}

	body, err := json.Marshal(requestPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	for key, value := range c.headers {
		if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Content-Type") {
			continue
		}

		request.Header.Set(key, value)
	}

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute rerank request: %w", err)
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read rerank response: %w", err)
	}

	if len(rawBody) > maxResponseBodySize {
		return nil, ErrResponseBodyTooLarge
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		// 服务错误正文可能回显请求或凭据，不放进日志/上层诊断。
		return nil, fmt.Errorf("rerank http status %d", response.StatusCode)
	}

	var decoded rerankResponse

	if err := json.Unmarshal(rawBody, &decoded); err != nil {
		return nil, fmt.Errorf("%w: decode json: %v", ErrInvalidResponse, err)
	}

	// 因为 request.top_n == len(passages)，
	// 所以必须拿回每一个 Passage 的分数。
	if len(decoded.Results) != len(passages) {
		return nil, fmt.Errorf("%w: expected %d results, got %d", ErrInvalidResponse, len(passages), len(decoded.Results))
	}

	scores := make([]float64, len(passages))
	seen := make([]bool, len(passages))

	for _, item := range decoded.Results {
		if item.Index == nil || item.RelevanceScore == nil {
			return nil, fmt.Errorf("%w: missing index or relevance_score", ErrInvalidResponse)
		}
		index, score := *item.Index, *item.RelevanceScore
		if index < 0 || index >= len(passages) {
			return nil, fmt.Errorf("%w: result index %d out of range", ErrInvalidResponse, index)
		}

		if seen[index] {
			return nil, fmt.Errorf("%w: duplicate result index %d", ErrInvalidResponse, index)
		}

		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {

			return nil, fmt.Errorf("%w: result[%d]=%v", ErrInvalidScore, index, score)
		}

		scores[index] = score
		seen[index] = true
	}

	for index, found := range seen {
		if !found {
			return nil, fmt.Errorf("%w: missing result index %d", ErrInvalidResponse, index)
		}
	}

	return scores, nil
}
