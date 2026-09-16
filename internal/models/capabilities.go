package models

import "strings"

// CapabilityMode 描述单项模型能力的配置方式。
//
// auto     由 Humbert 根据 Provider 与 ModelName 做保守推断；
// enabled  用户显式声明支持；
// disabled 用户显式声明不支持。
type CapabilityMode string

const (
	CapabilityAuto     CapabilityMode = "auto"
	CapabilityEnabled  CapabilityMode = "enabled"
	CapabilityDisabled CapabilityMode = "disabled"
)

// CapabilityConfig 是持久化到 models.json 的能力覆盖配置。
// 空字符串兼容旧配置，语义等同 auto。
type CapabilityConfig struct {
	Tools     CapabilityMode `json:"tools,omitempty"`
	Vision    CapabilityMode `json:"vision,omitempty"`
	Files     CapabilityMode `json:"files,omitempty"`
	Reasoning CapabilityMode `json:"reasoning,omitempty"`
	JSON      CapabilityMode `json:"json,omitempty"`
	Audio     CapabilityMode `json:"audio,omitempty"`
}

// Capabilities 是 Runtime 真正使用的有效能力集合。
type Capabilities struct {
	Tools     bool `json:"tools"`
	Vision    bool `json:"vision"`
	Files     bool `json:"files"`
	Reasoning bool `json:"reasoning"`
	JSON      bool `json:"json"`
	Audio     bool `json:"audio"`
}

// EffectiveCapabilities 将自动推断与用户覆盖合并成 Runtime 能力。
func EffectiveCapabilities(providerType ProviderType, modelName string, config CapabilityConfig) Capabilities {
	inferred := inferCapabilities(providerType, modelName)
	return Capabilities{
		Tools:     resolveCapability(config.Tools, inferred.Tools),
		Vision:    resolveCapability(config.Vision, inferred.Vision),
		Files:     resolveCapability(config.Files, inferred.Files),
		Reasoning: resolveCapability(config.Reasoning, inferred.Reasoning),
		JSON:      resolveCapability(config.JSON, inferred.JSON),
		Audio:     resolveCapability(config.Audio, inferred.Audio),
	}
}

func normalizeCapabilityConfig(value CapabilityConfig) (CapabilityConfig, error) {
	var err error
	value.Tools, err = normalizeCapabilityMode(value.Tools)
	if err != nil {
		return CapabilityConfig{}, err
	}
	value.Vision, err = normalizeCapabilityMode(value.Vision)
	if err != nil {
		return CapabilityConfig{}, err
	}
	value.Files, err = normalizeCapabilityMode(value.Files)
	if err != nil {
		return CapabilityConfig{}, err
	}
	value.Reasoning, err = normalizeCapabilityMode(value.Reasoning)
	if err != nil {
		return CapabilityConfig{}, err
	}
	value.JSON, err = normalizeCapabilityMode(value.JSON)
	if err != nil {
		return CapabilityConfig{}, err
	}
	value.Audio, err = normalizeCapabilityMode(value.Audio)
	if err != nil {
		return CapabilityConfig{}, err
	}
	return value, nil
}

func normalizeCapabilityMode(value CapabilityMode) (CapabilityMode, error) {
	value = CapabilityMode(strings.ToLower(strings.TrimSpace(string(value))))
	if value == "" {
		return CapabilityAuto, nil
	}
	switch value {
	case CapabilityAuto, CapabilityEnabled, CapabilityDisabled:
		return value, nil
	default:
		return "", &InvalidCapabilityModeError{Value: string(value)}
	}
}

func resolveCapability(mode CapabilityMode, inferred bool) bool {
	switch mode {
	case CapabilityEnabled:
		return true
	case CapabilityDisabled:
		return false
	default:
		return inferred
	}
}

// inferCapabilities 只承担“Auto”模式的默认推断，不是模型能力数据库。
//
// 设计目标是：
//  1. 旧 Humbert 配置升级后继续允许工具调用；
//  2. 对 Vision/File 等容易导致 Provider 400 的能力保持保守；
//  3. 用户始终可以在 Settings 中显式覆盖。
func inferCapabilities(providerType ProviderType, modelName string) Capabilities {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" {
		return Capabilities{}
	}

	textOnlyUtility := containsAny(name,
		"embedding", "rerank", "moderation", "whisper", "tts", "speech", "dall-e", "image-generation",
	)
	tools := !textOnlyUtility

	vision := containsAny(name,
		"vision", "-vl", "vl-", "llava", "pixtral", "gemma-3", "gemma3",
		"gpt-4o", "gpt-4.1", "gpt-5", "o1", "o3", "o4", "omni",
	)

	reasoning := containsAny(name,
		"reason", "deepseek-r1", "qwq", "o1", "o3", "o4", "gpt-5",
	)

	jsonMode := false
	if providerType == ProviderTypeOpenAI {
		jsonMode = tools
	} else if containsAny(name, "gpt-", "qwen", "deepseek", "llama", "mistral", "gemma") {
		jsonMode = tools
	}

	audio := containsAny(name, "audio", "realtime", "omni-audio")

	// 任意文件输入在 OpenAI-Compatible/Ollama 生态中的支持差异很大，Auto 默认关闭。
	// 即使模型支持 Vision，也不等价于支持 PDF/任意文件内容。
	files := false

	return Capabilities{
		Tools:     tools,
		Vision:    vision,
		Files:     files,
		Reasoning: reasoning,
		JSON:      jsonMode,
		Audio:     audio,
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
