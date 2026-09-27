package contextengine

import (
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/multimodal"
	"strings"
)

// serializeMessagesForCheckpoint 仅负责跨模型文本投影；摘要生命周期由 Eino 管理。
func serializeMessagesForCheckpoint(messages []*schema.Message, argumentMaxRunes int) string {
	if argumentMaxRunes < 256 {
		argumentMaxRunes = 4096
	}
	var builder strings.Builder
	for _, message := range messages {
		if message == nil {
			continue
		}
		switch message.Role {
		case schema.User:
			builder.WriteString("\n[User]\n")
			builder.WriteString(messageVisibleText(message))
			for _, part := range message.UserInputMultiContent {
				switch part.Type {
				case schema.ChatMessagePartTypeImageURL:
					builder.WriteString("\n")
					builder.WriteString(multimodal.HistoricalImagePlaceholder(part))
					builder.WriteString(" Visual details are unavailable to this text-only checkpoint; retain any observations stated in nearby messages, and do not invent image contents.")
				case schema.ChatMessagePartTypeFileURL:
					builder.WriteString("\n")
					builder.WriteString(multimodal.HistoricalFilePlaceholder(part))
					if extracted := extraStringValue(part.Extra, "extracted_text"); strings.TrimSpace(extracted) != "" {
						builder.WriteString("\n[Extracted file text]\n")
						builder.WriteString(extracted)
					}
				}
			}
		case schema.Assistant:
			if strings.TrimSpace(messageVisibleText(message)) != "" {
				builder.WriteString("\n[Assistant]\n")
				builder.WriteString(messageVisibleText(message))
			}
			for _, call := range message.ToolCalls {
				builder.WriteString("\n[Assistant tool call id=")
				builder.WriteString(call.ID)
				builder.WriteString("]\n")
				builder.WriteString(call.Function.Name)
				builder.WriteString("(")
				builder.WriteString(summarizeToolArguments([]byte(call.Function.Arguments), argumentMaxRunes))
				builder.WriteString(")")
			}
		case schema.Tool:
			builder.WriteString("\n[Tool result id=")
			builder.WriteString(message.ToolCallID)
			builder.WriteString(" name=")
			builder.WriteString(message.ToolName)
			builder.WriteString("]\n")
			builder.WriteString(messageVisibleText(message))
		}
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}
