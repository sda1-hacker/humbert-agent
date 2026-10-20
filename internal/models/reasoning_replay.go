package models

import (
	"context"
	"encoding/json"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// SupportsReasoningReplay 描述当前模型的已知协议，而非能否生成 thinking。
// MiMo 与 DeepSeek 当前协议要求工具链回传前序 reasoning_content；未知兼容模型仍保守省略。
func (s RuntimeSnapshot) SupportsReasoningReplay() bool {
	return supportsReasoningReplay(s.ProviderType, s.ModelName)
}

func supportsReasoningReplay(provider ProviderType, modelName string) bool {
	if provider == ProviderTypeOllama {
		return true
	}
	if provider != ProviderTypeOpenAICompatible {
		return false
	}
	// 只识别官方已文档化的模型 ID；预设新增模型也必须遵守对应协议，不能一概丢弃推理字段。
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case "mimo-v2.6-pro", "mimo-v2.6-flash", "mimo-v2.6-pro-ultraspeed", "mimo-v2.5-pro", "mimo-v2.5":
		return true
	case "deepseek-flash", "deepseek-v4-pro":
		return true
	default:
		return false
	}
}

// reasoningModel 是 OpenAI Chat Completions 的最终出站边界。
// Eino 同轮工具循环会产生新的 Assistant，故不能只在历史投影中应用回放策略。
type reasoningModel struct {
	inner  einomodel.ToolCallingChatModel
	replay bool
}

var _ einomodel.ToolCallingChatModel = (*reasoningModel)(nil)

func (m *reasoningModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	return m.inner.Generate(ctx, reasoningForRequest(input, m.replay), opts...)
}

func (m *reasoningModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.inner.Stream(ctx, reasoningForRequest(input, m.replay), opts...)
}

func (m *reasoningModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &reasoningModel{inner: bound, replay: m.replay}, nil
}

// reasoningForRequest 只修改副本。MiMo 的文本/推理块统一为 Content/ReasoningContent，
// 与 Transcript Codec 重放表示一致，避免直播消息为数组、重放消息为字符串而改变请求。
func reasoningForRequest(messages []*schema.Message, replay bool) []*schema.Message {
	result := make([]*schema.Message, len(messages))
	for index, message := range messages {
		if message == nil || message.Role != schema.Assistant {
			result[index] = message
			continue
		}
		clone := *message
		if replay && len(message.ToolCalls) > 0 {
			clone.ToolCalls = append([]schema.ToolCall(nil), message.ToolCalls...)
			for i := range clone.ToolCalls {
				// Index 仅用于合并 SSE delta；普通 Assistant 历史靠稳定 ID 关联。
				clone.ToolCalls[i].Index = nil
				arguments := strings.TrimSpace(clone.ToolCalls[i].Function.Arguments)
				if arguments == "" {
					arguments = "{}"
				}
				// 对合法 Object 使用与 JSONL RawMessage 相同的编码，避免空白/HTML
				// 转义在落盘后改变历史前缀。不修正模型生成的非法参数。
				if strings.HasPrefix(arguments, "{") {
					if encoded, err := json.Marshal(json.RawMessage(arguments)); err == nil {
						clone.ToolCalls[i].Function.Arguments = string(encoded)
					}
				}
			}
		}
		if !replay {
			clone.ReasoningContent = ""
		}
		if len(message.AssistantGenMultiContent) > 0 {
			parts := make([]schema.MessageOutputPart, 0, len(message.AssistantGenMultiContent))
			var reasoning, text strings.Builder
			textOnly := true
			for _, part := range message.AssistantGenMultiContent {
				switch part.Type {
				case schema.ChatMessagePartTypeReasoning:
					if replay && part.Reasoning != nil {
						reasoning.WriteString(part.Reasoning.Text)
					}
				case schema.ChatMessagePartTypeText:
					if replay {
						text.WriteString(part.Text)
					}
					parts = append(parts, part)
				default:
					textOnly = false
					parts = append(parts, part)
				}
			}
			clone.AssistantGenMultiContent = parts
			if replay {
				// 结构化和兼容字段通常是同一份内容，只保留一次，优先结构化原文。
				if reasoning.Len() > 0 {
					clone.ReasoningContent = reasoning.String()
				}
				if textOnly {
					if text.Len() > 0 {
						clone.Content = text.String()
					}
					clone.AssistantGenMultiContent = nil
				}
			}
		}
		result[index] = &clone
	}
	return result
}
