package rerankhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
)

const (
	defaultTimeout      = 30 * time.Second
	maxResponseBodySize = 4 << 20 // 4 MiB
)

var (
	ErrMissingEndpoint      = errors.New("rerank http: missing endpoint")
	ErrMissingModel         = errors.New("rerank http: missing model")
	ErrInvalidEndpoint      = errors.New("rerank http: invalid endpoint")
	ErrEmptyQuery           = errors.New("rerank http: empty query")
	ErrInvalidResponse      = errors.New("rerank http: invalid response")
	ErrInvalidScore         = errors.New("rerank http: invalid relevance score")
	ErrResponseBodyTooLarge = errors.New("rerank http: response body too large")
)

var _ rerank.Scorer = (*Client)(nil)

// Config 描述一个 Jina/Cohere-compatible Rerank Endpoint。
//
// 当前推荐直接传完整 URL，例如：
//
// vLLM:
//
//	http://127.0.0.1:8002/v1/rerank
//
// Jina:
//
//	https://api.jina.ai/v1/rerank
//
// Cohere:
//
//	https://api.cohere.com/v2/rerank
//
// 请求核心字段均为：
//
//	model
//	query
//	documents
//	top_n
//
// 响应核心字段均为：
//
//	results[].index
//	results[].relevance_score
type Config struct {
	Endpoint string
	APIKey   string
	Model    string

	Timeout time.Duration

	HTTPClient *http.Client

	// Headers 用于企业 Gateway 等场景。
	//
	// Authorization / Content-Type 由 Adapter 自己负责，
	// 不建议通过这里覆盖。
	Headers map[string]string
}

func DefaultConfig() Config {
	return Config{
		Timeout: defaultTimeout,
	}
}

func (c Config) Validate() error {
	endpoint := strings.TrimSpace(c.Endpoint)

	if endpoint == "" {
		return ErrMissingEndpoint
	}

	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEndpoint, err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: unsupported scheme %q", ErrInvalidEndpoint, parsed.Scheme)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidEndpoint)
	}

	if strings.TrimSpace(c.Model) == "" {
		return ErrMissingModel
	}

	if c.Timeout < 0 {
		return fmt.Errorf("rerank http: invalid timeout %s", c.Timeout)
	}

	return nil
}

type Client struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
	headers  map[string]string
}

func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	client := cfg.HTTPClient

	if client == nil {
		timeout := cfg.Timeout

		if timeout == 0 {
			timeout = defaultTimeout
		}

		client = &http.Client{
			Timeout: timeout,
		}
	}

	headers := make(map[string]string, len(cfg.Headers))

	for key, value := range cfg.Headers {
		headers[key] = value
	}

	return &Client{
		endpoint: strings.TrimSpace(cfg.Endpoint),
		apiKey:   strings.TrimSpace(cfg.APIKey),
		model:    strings.TrimSpace(cfg.Model),
		client:   client,
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
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// Score 实现 rerank.Scorer。
//
// 一个很重要的行为：
//
// Provider 返回 results 通常已经按照 relevance_score DESC 排序。
//
// 但我们的 rerank.Engine 要求：
//
//	scores[i]
//
// 必须对应：
//
//	passages[i]
//
// 所以这里不能直接：
//
//	append(response.Results[i].RelevanceScore)
//
// 而必须根据：
//
//	result.Index
//
// 把 score 放回原输入位置。
func (c *Client) Score(ctx context.Context, query string, passages []string) ([]float64, error) {
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

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	for key, value := range c.headers {
		if strings.EqualFold(key, "Authorization") ||
			strings.EqualFold(key, "Content-Type") {
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

	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {

		message := strings.TrimSpace(string(rawBody))

		if len(message) > 1000 {
			message = message[:1000]
		}

		return nil, fmt.Errorf(
			"rerank http status %d: %s",
			response.StatusCode,
			message,
		)
	}

	var decoded rerankResponse

	if err := json.Unmarshal(rawBody, &decoded); err != nil {
		return nil, fmt.Errorf("%w: decode json: %v", ErrInvalidResponse, err)
	}

	// 因为 request.top_n == len(passages)，
	// 所以必须拿回每一个 Passage 的分数。
	if len(decoded.Results) != len(passages) {
		return nil, fmt.Errorf(
			"%w: expected %d results, got %d",
			ErrInvalidResponse,
			len(passages),
			len(decoded.Results),
		)
	}

	scores := make([]float64, len(passages))
	seen := make([]bool, len(passages))

	for _, item := range decoded.Results {
		if item.Index < 0 || item.Index >= len(passages) {
			return nil, fmt.Errorf(
				"%w: result index %d out of range",
				ErrInvalidResponse,
				item.Index,
			)
		}

		if seen[item.Index] {
			return nil, fmt.Errorf(
				"%w: duplicate result index %d",
				ErrInvalidResponse,
				item.Index,
			)
		}

		if math.IsNaN(item.RelevanceScore) ||
			math.IsInf(item.RelevanceScore, 0) ||
			item.RelevanceScore < 0 ||
			item.RelevanceScore > 1 {

			return nil, fmt.Errorf(
				"%w: result[%d]=%v",
				ErrInvalidScore,
				item.Index,
				item.RelevanceScore,
			)
		}

		scores[item.Index] = item.RelevanceScore
		seen[item.Index] = true
	}

	for index, found := range seen {
		if !found {
			return nil, fmt.Errorf(
				"%w: missing result index %d",
				ErrInvalidResponse,
				index,
			)
		}
	}

	return scores, nil
}
