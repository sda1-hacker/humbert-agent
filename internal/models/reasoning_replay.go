package models

import (
	"context"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// reasoningOmittingModel 是 OpenAI Chat Completions 协议的最终出站边界。
// 兼容接口的 reasoning_content 并非通用输入字段，不能仅依赖 Runtime 投影：
// Eino 在同一轮工具循环中还会产生新的 Assistant Message。
type reasoningOmittingModel struct {
	inner einomodel.ToolCallingChatModel
}

var _ einomodel.ToolCallingChatModel = (*reasoningOmittingModel)(nil)

func (m *reasoningOmittingModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	return m.inner.Generate(ctx, withoutReasoning(input), opts...)
}

func (m *reasoningOmittingModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.inner.Stream(ctx, withoutReasoning(input), opts...)
}

func (m *reasoningOmittingModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &reasoningOmittingModel{inner: bound}, nil
}

// withoutReasoning 只复制需要修改的消息；Transcript 与 Eino State 保持原样。
func withoutReasoning(messages []*schema.Message) []*schema.Message {
	result := make([]*schema.Message, len(messages))
	for index, message := range messages {
		if message == nil || message.Role != schema.Assistant {
			result[index] = message
			continue
		}
		clone := *message
		clone.ReasoningContent = ""
		if len(message.AssistantGenMultiContent) > 0 {
			parts := make([]schema.MessageOutputPart, 0, len(message.AssistantGenMultiContent))
			for _, part := range message.AssistantGenMultiContent {
				if part.Type != schema.ChatMessagePartTypeReasoning {
					parts = append(parts, part)
				}
			}
			clone.AssistantGenMultiContent = parts
		}
		result[index] = &clone
	}
	return result
}
