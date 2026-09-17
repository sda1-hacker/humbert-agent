package models

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
	"unicode"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"

	defaultOllamaBaseURL = "http://127.0.0.1:11434"

	defaultModelTimeoutMS = 600000

	minModelTimeoutMS = 1000

	maxModelTimeoutMS = 300000

	// defaultModelContextWindow 为旧 models.json 与新建模型提供保守默认值。
	// 128k 是当前主流长上下文模型较常见的能力档位；用户仍应按实际部署值修改。
	defaultModelContextWindow = 128 * 1024

	// defaultModelMaxOutputTokens 是默认输出预算，同时作为 Provider 请求的输出硬上限。
	defaultModelMaxOutputTokens = 8 * 1024

	minModelContextWindow = 4 * 1024
	maxModelContextWindow = 2 * 1024 * 1024

	minModelMaxOutputTokens = 256
)

func normalizeCreateProviderInput(
	input CreateProviderInput,
) (CreateProviderInput, error) {
	name, err := normalizeRequiredText(
		input.Name,
		"Provider 名称",
		100,
	)
	if err != nil {
		return CreateProviderInput{}, err
	}

	baseURL, err := normalizeProviderBaseURL(
		input.Type,
		input.BaseURL,
	)
	if err != nil {
		return CreateProviderInput{}, err
	}

	return CreateProviderInput{
		Name: name,

		Type: input.Type,

		BaseURL: baseURL,

		APIKey: strings.TrimSpace(
			input.APIKey,
		),
	}, nil
}

func normalizeUpdateProviderInput(
	input UpdateProviderInput,
) (UpdateProviderInput, error) {
	normalized, err :=
		normalizeCreateProviderInput(
			CreateProviderInput{
				Name: input.Name,

				Type: input.Type,

				BaseURL: input.BaseURL,

				APIKey: input.APIKey,
			},
		)
	if err != nil {
		return UpdateProviderInput{}, err
	}

	return UpdateProviderInput{
		Name: normalized.Name,

		Type: normalized.Type,

		BaseURL: normalized.BaseURL,

		APIKey: normalized.APIKey,

		UpdateAPIKey: input.UpdateAPIKey,
	}, nil
}

func normalizeCreateModelInput(
	input CreateModelInput,
) (CreateModelInput, error) {
	providerID, err :=
		normalizeRequiredText(
			input.ProviderID,
			"Provider ID",
			128,
		)
	if err != nil {
		return CreateModelInput{}, err
	}

	modelName, err :=
		normalizeRequiredText(
			input.ModelName,
			"模型名称",
			256,
		)
	if err != nil {
		return CreateModelInput{}, err
	}

	displayName :=
		strings.TrimSpace(
			input.DisplayName,
		)

	if displayName == "" {
		displayName = modelName
	}

	displayName, err =
		normalizeRequiredText(
			displayName,
			"模型显示名称",
			128,
		)
	if err != nil {
		return CreateModelInput{}, err
	}

	timeoutMS := input.TimeoutMS

	if timeoutMS == 0 {
		timeoutMS =
			defaultModelTimeoutMS
	}

	if timeoutMS < minModelTimeoutMS ||
		timeoutMS > maxModelTimeoutMS {
		return CreateModelInput{}, fmt.Errorf(
			"模型超时时间必须位于 %d-%d 毫秒之间",
			minModelTimeoutMS,
			maxModelTimeoutMS,
		)
	}

	contextWindow := input.ContextWindow
	if contextWindow == 0 {
		contextWindow = defaultModelContextWindow
	}
	if contextWindow < minModelContextWindow || contextWindow > maxModelContextWindow {
		return CreateModelInput{}, fmt.Errorf(
			"模型 Context Window 必须位于 %d-%d Token 之间",
			minModelContextWindow,
			maxModelContextWindow,
		)
	}

	maxOutputTokens := input.MaxOutputTokens
	if maxOutputTokens == 0 {
		maxOutputTokens = defaultModelMaxOutputTokens
	}
	if maxOutputTokens < minModelMaxOutputTokens {
		return CreateModelInput{}, fmt.Errorf(
			"模型 Max Output Tokens 不能小于 %d",
			minModelMaxOutputTokens,
		)
	}
	if maxOutputTokens >= contextWindow {
		return CreateModelInput{}, errors.New(
			"模型 Max Output Tokens 必须小于 Context Window",
		)
	}

	capabilities, err := normalizeCapabilityConfig(input.Capabilities)
	if err != nil {
		return CreateModelInput{}, err
	}

	return CreateModelInput{
		ProviderID: providerID,

		ModelName: modelName,

		DisplayName: displayName,

		TimeoutMS: timeoutMS,

		ContextWindow: contextWindow,

		MaxOutputTokens: maxOutputTokens,

		Capabilities: capabilities,

		Enabled: input.Enabled,
	}, nil
}

func normalizeUpdateModelInput(
	input UpdateModelInput,
) (UpdateModelInput, error) {
	normalized, err :=
		normalizeCreateModelInput(
			CreateModelInput(input),
		)
	if err != nil {
		return UpdateModelInput{}, err
	}

	return UpdateModelInput(
		normalized,
	), nil
}

func normalizeProviderBaseURL(
	providerType ProviderType,
	raw string,
) (string, error) {
	raw = strings.TrimSpace(raw)

	switch providerType {
	case ProviderTypeOpenAI:
		// v0.1 的 openai 类型只表示 OpenAI 官方服务。
		//
		// 自定义 OpenAI-compatible 服务应该选择
		// openai_compatible，避免用户误把 API Key 发送到错误地址。
		return defaultOpenAIBaseURL, nil

	case ProviderTypeOpenAICompatible:
		if raw == "" {
			return "", errors.New(
				"OpenAI-Compatible Provider 必须配置 Base URL",
			)
		}

		return normalizeRemoteURL(
			raw,
			true,
		)

	case ProviderTypeOllama:
		if raw == "" {
			raw =
				defaultOllamaBaseURL
		}

		return normalizeRemoteURL(
			raw,
			true,
		)

	default:
		return "", fmt.Errorf(
			"不支持的 Provider 类型: %q",
			providerType,
		)
	}
}

// normalizeRemoteURL 校验模型服务地址。
//
// 为了避免 API Key 通过明文 HTTP 发送到公网：
//
//   - HTTPS 可以访问任意正常 Host；
//   - HTTP 只允许 localhost / loopback IP。
//
// 同时拒绝 URL UserInfo、Query、Fragment，避免敏感参数进入日志、
// 配置文件或者 HTTP Proxy。
func normalizeRemoteURL(
	raw string,
	allowLoopbackHTTP bool,
) (string, error) {
	parsed, err :=
		url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf(
			"解析 Provider Base URL 失败: %w",
			err,
		)
	}

	if !parsed.IsAbs() {
		return "", errors.New(
			"Provider Base URL 必须是绝对 URL ",
		)
	}

	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowLoopbackHTTP ||
			!isLoopbackHost(
				parsed.Hostname(),
			) {
			return "", errors.New(
				"HTTP 模型服务只允许 localhost 或 loopback 地址，公网服务必须使用 HTTPS",
			)
		}

	default:
		return "", fmt.Errorf(
			"Provider Base URL 不支持协议 %q，只允许 http/https ",
			parsed.Scheme,
		)
	}

	if strings.TrimSpace(
		parsed.Hostname(),
	) == "" {
		return "", errors.New(
			"Provider Base URL 缺少 Host ",
		)
	}

	if parsed.User != nil {
		return "", errors.New(
			"Provider Base URL 不允许包含用户名或密码 ",
		)
	}

	if parsed.RawQuery != "" {
		return "", errors.New(
			"Provider Base URL 不允许包含 Query 参数 ",
		)
	}

	if parsed.Fragment != "" {
		return "", errors.New(
			"Provider Base URL 不允许包含 Fragment ",
		)
	}

	for _, segment := range strings.Split(
		parsed.Path,
		"/",
	) {
		if segment == ".." {
			return "", errors.New(
				"Provider Base URL 路径中不能包含 ",
			)
		}
	}

	cleanPath :=
		path.Clean(parsed.Path)

	if cleanPath == "." {
		cleanPath = ""
	}

	parsed.Path =
		strings.TrimSuffix(
			cleanPath,
			"/",
		)

	return strings.TrimSuffix(
		parsed.String(),
		"/",
	), nil
}

func isLoopbackHost(
	host string,
) bool {
	host =
		strings.TrimSpace(
			strings.ToLower(host),
		)

	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil &&
		ip.IsLoopback()
}

func normalizeRequiredText(
	value string,
	field string,
	maxLength int,
) (string, error) {
	value =
		strings.TrimSpace(value)

	if value == "" {
		return "", fmt.Errorf(
			"%s不能为空",
			field,
		)
	}

	if len(value) > maxLength {
		return "", fmt.Errorf(
			"%s长度不能超过 %d",
			field,
			maxLength,
		)
	}

	for _, char := range value {
		if unicode.IsControl(char) {
			return "", fmt.Errorf(
				"%s不能包含控制字符",
				field,
			)
		}
	}

	return value, nil
}
