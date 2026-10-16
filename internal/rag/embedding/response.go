package embedding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxEmbeddingResponseBytes = 32 << 20

// orderedEmbeddingTransport 限制响应大小，并修正 OpenAI SDK 丢弃 index 前的响应顺序。
// 请求协议、鉴权、模型参数及 Eino 回调继续由 eino-ext 处理。
type orderedEmbeddingTransport struct {
	next             http.RoundTripper
	ollamaDimensions int
}

func (t orderedEmbeddingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 当前 eino-ext Ollama 配置未透出 dimensions，在已有传输层补齐原生参数。
	// 仍由 Ollama 执行 MRL 与归一化；请求、鉴权和 Eino 回调继续复用原组件。
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/api/embed") && t.ollamaDimensions > 0 {
		if req.Body == nil {
			return nil, errors.New("missing Ollama embedding request body")
		}
		data, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read Ollama embedding request: %w", err)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(data, &body); err != nil || body == nil {
			return nil, errors.New("invalid Ollama embedding request")
		}
		body["dimensions"], _ = json.Marshal(t.ollamaDimensions)
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
		copy := req.Clone(req.Context())
		copy.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
		copy.Body, _ = copy.GetBody()
		copy.ContentLength = int64(len(data))
		if copy.Header == nil {
			copy.Header = make(http.Header)
		}
		copy.Header.Set("Content-Length", fmt.Sprint(len(data)))
		req = copy
	}
	resp, err := t.next.RoundTrip(req)
	if err == nil && (resp == nil || resp.Body == nil) {
		return nil, errors.New("embedding transport returned an empty response")
	}
	openAI := strings.HasSuffix(req.URL.Path, "/embeddings")
	ollama := strings.HasSuffix(req.URL.Path, "/api/embed")
	if err != nil || req.Method != http.MethodPost || (!openAI && !ollama) {
		return resp, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbeddingResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if len(data) > maxEmbeddingResponseBytes {
		return nil, errors.New("embedding response exceeds size limit")
	}
	if ollama || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 原生协议按输入顺序返回 embeddings 数组，无 index 字段，不套用 OpenAI 重排规则。
		// 错误响应同样有大小上限，具体错误仍由原生 SDK 解析。
		copy := *resp
		copy.Body = io.NopCloser(bytes.NewReader(data))
		return &copy, nil
	}
	// 保留 usage、model 和服务扩展字段，不解码或改写向量浮点值。
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(envelope["data"], &items); err != nil || len(items) == 0 {
		return nil, errors.New("embedding response requires non-empty data")
	}
	ordered := make([]json.RawMessage, len(items))
	for _, item := range items {
		var identity struct {
			Index *int `json:"index"`
		}
		if err := json.Unmarshal(item, &identity); err != nil || identity.Index == nil ||
			*identity.Index < 0 || *identity.Index >= len(items) || ordered[*identity.Index] != nil {
			return nil, errors.New("embedding response has missing, duplicate or invalid index")
		}
		ordered[*identity.Index] = item
	}
	envelope["data"], err = json.Marshal(ordered)
	if err != nil {
		return nil, err
	}
	data, err = json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	copy := *resp
	copy.Header = resp.Header.Clone()
	if copy.Header == nil {
		copy.Header = make(http.Header)
	}
	copy.Header.Set("Content-Length", fmt.Sprint(len(data)))
	copy.ContentLength = int64(len(data))
	copy.Body = io.NopCloser(bytes.NewReader(data))
	return &copy, nil
}
