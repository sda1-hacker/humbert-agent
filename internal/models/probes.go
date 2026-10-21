package models

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

var ErrProbeMismatch = errors.New("模型未通过该项能力测试")

// ProviderConnection 只在 Go 装配层读取凭据；不绑定到 Wails，也不返回给页面。
func (r *Registry) ProviderConnection(ctx context.Context, id string) (Provider, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, err := r.store.GetProvider(ctx, id)
	if err != nil {
		return Provider{}, "", err
	}
	key := ""
	if p.Type != ProviderTypeOllama && p.CredentialID != "" {
		key, err = r.factory.requiredCredential(ctx, p)
	}
	return p, key, err
}

// DiscoverModels 仅查询已保存连接的元数据。限制时长、响应大小和条数，拒绝重定向携带凭据。
// 不根据列表猜测窗口和能力，也不会发起推理、下载模型或覆盖已有模型。
func (r *Registry) DiscoverModels(ctx context.Context, id string) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	p, key, err := r.ProviderConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	path := "/models"
	if p.Type == ProviderTypeOllama {
		path = "/api/tags"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, errors.New("供应商地址无效")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("无法读取模型列表，请检查供应商地址、网络或本地服务")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("读取模型列表失败（HTTP %d），可以继续使用预设或自定义模型", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, errors.New("模型列表读取失败或响应过大")
	}
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, errors.New("供应商返回的模型列表格式无效")
	}
	if p.Type == ProviderTypeOllama && response.Models == nil || p.Type != ProviderTypeOllama && response.Data == nil {
		return nil, errors.New("供应商返回的模型列表格式无效")
	}
	names := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && len(name) <= 256 && len(names) < 1000 {
			names[name] = true
		}
	}
	if p.Type == ProviderTypeOllama {
		for _, model := range response.Models {
			add(model.Name)
		}
	} else {
		for _, model := range response.Data {
			add(model.ID)
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

// ProbeModel 只测试指定的单项协议能力，不运行 Agent 或真实工具。
// 使用短输出预算和独立超时；失败仅代表本次验证失败，不自动改写能力配置。
func (r *Registry) ProbeModel(ctx context.Context, id, kind string) (TestResult, error) {
	if ctx == nil {
		return TestResult{}, errors.New("context.Context 不能为空")
	}
	if kind != "text" && kind != "tools" && kind != "vision" && kind != "stream" {
		return TestResult{}, errors.New("未知的模型测试项目")
	}
	r.mu.RLock()
	resolved, err := r.store.ResolveModel(ctx, id)
	if err != nil {
		r.mu.RUnlock()
		return TestResult{}, err
	}
	if !resolved.Model.Enabled {
		r.mu.RUnlock()
		return TestResult{}, ErrModelDisabled
	}
	resolved.Model.MaxOutputTokens = min(resolved.Model.MaxOutputTokens, 1024)
	timeout := time.Duration(resolved.Model.TimeoutMS) * time.Millisecond
	if kind != "text" {
		timeout = min(timeout, 2*time.Minute)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := newStreamingHTTPClient(time.Duration(resolved.Model.TimeoutMS) * time.Millisecond)
	if err != nil {
		r.mu.RUnlock()
		return TestResult{}, err
	}
	defer client.CloseIdleConnections()
	instance, err := r.factory.create(ctx, resolved, client)
	r.mu.RUnlock() // 凭据读取与配置冻结处于同一临界区，网络请求不持锁。
	if err != nil {
		return TestResult{}, err
	}
	input := []*schema.Message{schema.UserMessage("Reply with exactly OK.")}
	if kind == "tools" {
		instance, err = instance.WithTools([]*schema.ToolInfo{{Name: "humbert_connection_probe", Desc: "A harmless connection probe. Call this function once with no arguments.", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}})
		if err != nil {
			return TestResult{}, err
		}
		input[0].Content = "Call humbert_connection_probe exactly once with no arguments. Do not answer with text."
	}
	if kind == "vision" {
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, img); err != nil {
			return TestResult{}, err
		}
		encoded := base64.StdEncoding.EncodeToString(buffer.Bytes())
		input = []*schema.Message{{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: "What is the main color in this image? Reply with one color word."},
			{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &encoded, MIMEType: "image/png"}}},
		}}}
	}
	started := time.Now()
	var text strings.Builder
	if kind == "stream" {
		stream, streamErr := instance.Stream(ctx, input)
		if streamErr != nil {
			return TestResult{}, streamErr
		}
		defer stream.Close()
		for {
			chunk, readErr := stream.Recv()
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return TestResult{}, readErr
			}
			if chunk != nil {
				text.WriteString(chunk.Content)
			}
		}
	} else {
		answer, callErr := instance.Generate(ctx, input)
		if callErr != nil {
			return TestResult{}, callErr
		}
		if answer == nil {
			return TestResult{}, fmt.Errorf("%w: 模型返回空响应", ErrProbeMismatch)
		}
		if kind == "tools" {
			if len(answer.ToolCalls) != 1 || answer.ToolCalls[0].Function.Name != "humbert_connection_probe" {
				return TestResult{}, fmt.Errorf("%w: 模型未返回预期的工具调用", ErrProbeMismatch)
			}
			var args map[string]any
			if raw := strings.TrimSpace(answer.ToolCalls[0].Function.Arguments); raw != "" {
				if json.Unmarshal([]byte(raw), &args) != nil || args == nil || len(args) != 0 {
					return TestResult{}, fmt.Errorf("%w: 工具调用参数格式不正确", ErrProbeMismatch)
				}
			}
			text.WriteString("tool call verified")
		} else {
			text.WriteString(answer.Content)
		}
	}
	value := strings.TrimSpace(text.String())
	if value == "" {
		return TestResult{}, fmt.Errorf("%w: 模型没有返回可验证的文本", ErrProbeMismatch)
	}
	// 要求明确的颜色词，避免把“not red”等否定回答误判成视觉能力已验证。
	colorWord := strings.ToLower(strings.Trim(value, " \t\r\n.!。！\"'"))
	if kind == "vision" && colorWord != "red" && colorWord != "红" && colorWord != "红色" {
		return TestResult{}, fmt.Errorf("%w: 图片请求已返回，但未正确识别测试图片的颜色", ErrProbeMismatch)
	}
	preview := []rune(value)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	return TestResult{Success: true, DurationMS: time.Since(started).Milliseconds(), ResponsePreview: string(preview)}, nil
}
