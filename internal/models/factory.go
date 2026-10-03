package models

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/sda1-hacker/humbert-agent/internal/credential"

	einomodel "github.com/cloudwego/eino/components/model"
)

// Factory 根据持久化 Model + Provider 配置创建真实的 Eino ChatModel。
//
// Factory 不维护缓存。同一个模型是否复用由 Registry 决定。
//
// 一个非常重要的边界是：Humbert 的模型 TimeoutMS 不能直接映射到
// http.Client.Timeout。Go 的 http.Client.Timeout 覆盖“整个请求生命周期”，包括
// Streaming Response Body 的读取。对于 Thinking 模型，一次 SSE 流持续数分钟是正常
// 情况，如果把 60 秒配置直接放进 http.Client.Timeout，即使模型仍在持续输出 Chunk，
// 第 60 秒也会被客户端强制取消。
//
// 因此 Factory 为 Streaming Model 注入自定义 HTTPClient：
//
//   - http.Client.Timeout 固定为 0，不给正在正常输出的 Stream 设置总时长上限；
//   - Model.TimeoutMS 作为 ResponseHeaderTimeout，即“等待服务端开始响应”的上限；
//   - Turn 生命周期仍由 Runtime context 控制，用户取消/应用关闭可以立即中止请求；
//   - Model 连通性测试仍在 Registry.TestModel 中使用 context.WithTimeout，避免测试永久等待。
//
// 这样既避免 Thinking/长回答在固定 60 秒被误杀，又没有取消必要的连接保护。
type Factory struct {
	credentials *credential.Store
}

func NewFactory(credentials *credential.Store) *Factory {
	return &Factory{credentials: credentials}
}

// Create 统一准备流式连接与凭证，直接使用 Eino 官方 SDK；缓存由 Registry 管理。
// 当前配置只支持三种协议，在这个入口明确映射即可，无需再维护工厂注册表和请求包装。
func (f *Factory) Create(ctx context.Context, resolved ResolvedModel) (einomodel.ToolCallingChatModel, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	providerType := resolved.Provider.Type
	if providerType != ProviderTypeOpenAI && providerType != ProviderTypeOpenAICompatible && providerType != ProviderTypeOllama {
		return nil, fmt.Errorf("不支持的 Provider 类型: %q", resolved.Provider.Type)
	}
	client, err := newStreamingHTTPClient(time.Duration(resolved.Model.TimeoutMS) * time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("创建 Streaming HTTP Client 失败: %w", err)
	}
	apiKey := ""
	if resolved.Provider.Type == ProviderTypeOpenAI {
		apiKey, err = f.requiredCredential(ctx, resolved.Provider)
	} else if resolved.Provider.Type != ProviderTypeOllama && resolved.Provider.CredentialID != "" {
		if f.credentials == nil {
			return nil, errors.New("Provider Credential Store 未初始化")
		}
		apiKey, err = f.credentials.Get(ctx, resolved.Provider.CredentialID)
		if err != nil {
			err = fmt.Errorf("读取 Provider Credential 失败: %w", err)
		}
	}
	if err != nil {
		return nil, err
	}
	if providerType == ProviderTypeOllama {
		value, err := ollama.NewChatModel(ctx, &ollama.ChatModelConfig{BaseURL: resolved.Provider.BaseURL, Model: resolved.Model.ModelName,
			HTTPClient: client, Options: &ollama.Options{NumPredict: resolved.Model.MaxOutputTokens}})
		if err != nil {
			return nil, fmt.Errorf("创建 Ollama ChatModel 失败: %w", err)
		}
		return value, nil
	}
	// 官方与兼容协议的输出预算字段不同，合并公共初始化时仍保留原请求语义。
	maxOutput := resolved.Model.MaxOutputTokens
	cfg := &openai.ChatModelConfig{APIKey: apiKey, BaseURL: resolved.Provider.BaseURL, Model: resolved.Model.ModelName, HTTPClient: client}
	if providerType == ProviderTypeOpenAI {
		cfg.MaxCompletionTokens = &maxOutput
	} else {
		cfg.MaxTokens = &maxOutput
	}
	value, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 %s ChatModel 失败: %w", providerType, err)
	}
	return &reasoningOmittingModel{inner: value}, nil
}

// newStreamingHTTPClient 创建适用于 LLM SSE/Streaming 的 HTTP Client。
//
// responseHeaderTimeout 只限制“等待响应头”的时间，不限制收到响应头以后持续读取 Body
// 的总时长。Transport 从 http.DefaultTransport Clone，保留 Go 默认的 Proxy、Dial、TLS、
// KeepAlive、HTTP/2 等成熟配置，仅覆盖与模型首响应相关的 Timeout。
func newStreamingHTTPClient(responseHeaderTimeout time.Duration) (*http.Client, error) {
	if responseHeaderTimeout <= 0 {
		return nil, errors.New("Response Header Timeout 必须大于 0")
	}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || baseTransport == nil {
		return nil, errors.New("http.DefaultTransport 不是 *http.Transport")
	}
	transport := baseTransport.Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{
		Transport: transport,
		// 必须保持 0。非零值会覆盖整个请求生命周期并在 Streaming Body 仍然活跃时
		// 强制取消，这正是 60 秒 Thinking Stream 报 context deadline exceeded 的根因。
		Timeout: 0,
	}, nil
}

func (f *Factory) requiredCredential(ctx context.Context, provider Provider) (string, error) {
	if provider.CredentialID == "" {
		return "", errors.New("当前 Provider 尚未配置 API Key")
	}
	if f.credentials == nil {
		return "", errors.New("Provider Credential Store 未初始化")
	}
	value, err := f.credentials.Get(ctx, provider.CredentialID)
	if err != nil {
		return "", fmt.Errorf("读取 Provider Credential 失败: %w", err)
	}
	return value, nil
}
