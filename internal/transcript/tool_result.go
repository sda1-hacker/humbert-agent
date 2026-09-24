package transcript

import "strings"

// ToolResultSucceeded 统一判断一次工具调用是否确实完成。旧会话中的权限拒绝曾以普通
// ToolResult 保存，因此仍需识别其固定文案；新结果应优先写入 IsError。
func ToolResultSucceeded(message *AgentMessage) bool {
	if message == nil || message.IsError {
		return false
	}
	var text strings.Builder
	for _, block := range message.Content {
		if block.Type == ContentText {
			text.WriteString(block.Text)
		}
	}
	return !ToolResultRejected(text.String())
}

// ToolResultRejected 兼容已落盘的权限拒绝结果，也供 Runtime 标记新结果。
func ToolResultRejected(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return (strings.HasPrefix(value, "工具 \"") && strings.Contains(value, "已被 humbert permission policy 拒绝，未执行任何操作")) ||
		(strings.HasPrefix(value, "用户拒绝了工具 \"") && strings.Contains(value, "的本次调用，未执行任何操作"))
}
