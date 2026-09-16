package models

import "time"

// ProviderType 标识模型服务提供方的协议类型。
//
// v0.1 暂时只支持三种：
//
//   - openai：OpenAI 官方 API；
//   - openai_compatible：任何兼容 OpenAI Chat Completions API 的服务；
//   - ollama：本地或远程 Ollama。
//
// OpenAI-Compatible 可以覆盖 DeepSeek、Qwen、vLLM、LM Studio 等服务，
// 因此在 v0.1 阶段没有必要为每一家厂商单独创建 Provider 类型。
type ProviderType string

const (
	ProviderTypeOpenAI ProviderType = "openai"

	ProviderTypeOpenAICompatible ProviderType = "openai_compatible"

	ProviderTypeOllama ProviderType = "ollama"
)

// Provider 描述一个模型服务提供方。
//
// CredentialID 只保存对 CredentialStore 的引用，
// API Key 本身永远不会进入 providers.json。
type Provider struct {
	ID string `json:"id"`

	Name string `json:"name"`

	Type ProviderType `json:"type"`

	BaseURL string `json:"base_url"`

	CredentialID string `json:"credential_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`

	UpdatedAt time.Time `json:"updated_at"`
}

// Model 描述一个可以被 Agent 使用的具体模型。
//
// Provider 和 Model 分离的原因是一个 Provider 通常可以提供多个模型，例如：
//
//	OpenAI Provider
//	    ├── gpt-5
//	    ├── gpt-5-mini
//	    └── gpt-4.1
//
// API Key 和 BaseURL 属于 Provider，而 ModelName、Timeout 等属于 Model。
type Model struct {
	ID string `json:"id"`

	ProviderID string `json:"provider_id"`

	ModelName string `json:"model_name"`

	DisplayName string `json:"display_name"`

	TimeoutMS int `json:"timeout_ms"`

	// ContextWindow 是该模型实例允许的最大上下文 Token 数。
	//
	// Humbert 不根据模型名称猜测该值，因为 OpenAI-Compatible/Ollama 的同名模型
	// 可能以不同上下文窗口启动。该值由用户在模型配置中明确维护，是 ContextEngine
	// 计算自动压缩阈值和 UI 环形进度的事实来源。
	ContextWindow int `json:"context_window"`

	// MaxOutputTokens 是单次模型响应预留的最大输出 Token 数。
	//
	// ContextEngine 会把它纳入安全余量，避免输入上下文已经占满窗口后 Provider
	// 无法为 Assistant 输出保留空间。它描述预算，不强制改变 Provider 自身的
	// max_tokens/max_completion_tokens 参数。
	MaxOutputTokens int `json:"max_output_tokens"`

	Capabilities CapabilityConfig `json:"capabilities,omitempty"`

	Enabled bool `json:"enabled"`

	CreatedAt time.Time `json:"created_at"`

	UpdatedAt time.Time `json:"updated_at"`
}

// ModelInfo 是 UI 和 Registry 查询列表时使用的完整模型信息。
//
// 它额外包含 Provider 信息，但不会暴露 CredentialID。
type ModelInfo struct {
	Model Model

	ProviderName string

	ProviderType ProviderType
}

// ResolvedModel 是 ModelFactory 创建真实 Eino ChatModel 时需要的不可变配置。
//
// Registry 在一次 Resolve 操作中读取完整 Provider + Model，
// 避免 Factory 自己再次读取文件配置。
type ResolvedModel struct {
	Provider Provider

	Model Model
}

// CreateProviderInput 是创建 Provider 时的业务输入。
//
// APIKey 只在调用期间存在于内存中，
// Registry 保存完成后不会继续持有该字符串。
type CreateProviderInput struct {
	Name string

	Type ProviderType

	BaseURL string

	APIKey string
}

// UpdateProviderInput 描述 Provider 修改请求。
//
// UpdateAPIKey 用于区分：
//
//	APIKey == "" + UpdateAPIKey == false
//	    → 保留已有 API Key
//
//	APIKey == "" + UpdateAPIKey == true
//	    → 删除已有 API Key
//
//	APIKey != "" + UpdateAPIKey == true
//	    → 替换 API Key
//
// 这是为了避免前端编辑 Provider 时必须读取并回显敏感信息。
type UpdateProviderInput struct {
	Name string

	Type ProviderType

	BaseURL string

	APIKey string

	UpdateAPIKey bool
}

// CreateModelInput 描述创建模型所需的业务参数。
type CreateModelInput struct {
	ProviderID string

	ModelName string

	DisplayName string

	TimeoutMS int

	ContextWindow int

	MaxOutputTokens int

	Capabilities CapabilityConfig

	Enabled bool
}

// UpdateModelInput 描述修改模型所需的业务参数。
type UpdateModelInput struct {
	ProviderID string

	ModelName string

	DisplayName string

	TimeoutMS int

	ContextWindow int

	MaxOutputTokens int

	Capabilities CapabilityConfig

	Enabled bool
}

// TestResult 是一次真实模型连通性测试的结果。
//
// 测试会真正发送一次最小模型请求，因此可能产生极少量 Token 消耗。
type TestResult struct {
	Success bool

	DurationMS int64

	ResponsePreview string
}
