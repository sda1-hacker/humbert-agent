package multimodal

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

// MessageText 读取消息正文，保留兼容字段的优先级与空白，不混入 reasoning。
func MessageText(message *schema.Message) string {
	if message == nil {
		return ""
	}
	if message.Content != "" {
		return message.Content
	}
	return MessagePartsText(message)
}

// MessagePartsText 仅投影结构化文本；上下文可在空白兼容字段时显式选择此路径。
func MessagePartsText(message *schema.Message) string {
	if message == nil {
		return ""
	}
	var b strings.Builder
	if message.Role == schema.User {
		for _, part := range message.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText {
				b.WriteString(part.Text)
			}
		}
	} else {
		for _, part := range message.AssistantGenMultiContent {
			if part.Type == schema.ChatMessagePartTypeText {
				b.WriteString(part.Text)
			}
		}
	}
	return b.String()
}

// MessageReasoning 供界面投影使用；是否向模型重放由调用方策略决定。
func MessageReasoning(message *schema.Message) string {
	if message == nil {
		return ""
	}
	if message.ReasoningContent != "" {
		return message.ReasoningContent
	}
	var b strings.Builder
	for _, part := range message.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeReasoning && part.Reasoning != nil {
			b.WriteString(part.Reasoning.Text)
		}
	}
	return b.String()
}
