package runtime

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrProviderContentBlocked 表示上游模型服务的内容安全系统拒绝了本次输入或输出。
	//
	// 这类错误不是 Humbert Tool/Session/JSONL 逻辑错误，也不应该把已经被 Provider
	// 判定为不可交付的 partial output 继续写入 Session。Runtime 会保留用户消息和此前
	// 已完成的合法 Agent Step，但丢弃本次被拦截的未完成 Assistant Stream。
	ErrProviderContentBlocked = errors.New(
		"模型服务内容安全检查拦截了本次输入或输出",
	)
)

const providerContentBlockedUserMessage = "模型服务的内容安全检查拦截了本次输入或输出。请调整问题表述后重试，或切换到其他可用模型。"

// classifyProviderError 把常见 Provider 原始错误归类成 Humbert 稳定错误类型。
//
// 目前 OpenAI-Compatible Provider 对审核失败的错误文案并不统一。阿里云/千问常见
// data_inspection_failed 与“Input/Output data may contain inappropriate content”；部分
// 兼容网关还会改变大小写、空格甚至返回存在拼写问题的 message。因此这里只依赖
// 稳定语义片段做分类，而不比较完整错误字符串。
func classifyProviderError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrProviderContentBlocked) {
		return err
	}
	if !isProviderContentBlockedText(err.Error()) {
		return err
	}
	return fmt.Errorf(
		"%w: %v",
		ErrProviderContentBlocked,
		err,
	)
}

// isProviderContentBlockedText 判断错误文本是否属于内容安全拒绝。
//
// 不把单独的“content”或“safety”当成命中条件，避免把普通业务错误误分类。
func isProviderContentBlockedText(value string) bool {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == "" {
		return false
	}

	if strings.Contains(text, "data_inspection_failed") ||
		strings.Contains(text, "datainspectionfailed") {
		return true
	}

	if strings.Contains(text, "content security warning") ||
		strings.Contains(text, "custom_role_blocked") ||
		strings.Contains(text, "customroleblocked") {
		return true
	}

	// 一些 OpenAI-Compatible 网关会把官方文案拼写错误地透传出来，例如
	// "inappropriatntent"。只要同时出现 input/output/data 与 inappropriat 词干，就
	// 可以可靠判断为同一类审核拒绝。
	if strings.Contains(text, "inappropriat") &&
		(strings.Contains(text, "input data") ||
			strings.Contains(text, "output data") ||
			strings.Contains(text, "input or output data")) {
		return true
	}

	return false
}

// providerErrorForUser 返回适合直接展示给终端用户的错误文本。
//
// Provider 原始错误仍然进入结构化日志，便于开发排查；UI 不直接显示冗长 SDK 包装、
// NodeRunError 路径或第三方网关内部文案。
func providerErrorForUser(err error) string {
	if errors.Is(err, ErrProviderContentBlocked) {
		return providerContentBlockedUserMessage
	}
	return ""
}
