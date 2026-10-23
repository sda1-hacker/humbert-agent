package models

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ReasoningReplayMode 只控制请求中是否回传原生推理，不控制模型是否开启 thinking。
// Auto 跟随共享预设；用户可为自建网关或新模型显式指定，无需修改厂商名单。
type ReasoningReplayMode string

const (
	ReasoningReplayAuto        ReasoningReplayMode = "auto"
	ReasoningReplayInclude     ReasoningReplayMode = "include"
	ReasoningReplayCurrentTurn ReasoningReplayMode = "current_turn"
	ReasoningReplayOmit        ReasoningReplayMode = "omit"
)

func normalizeReasoningReplay(mode ReasoningReplayMode) (ReasoningReplayMode, error) {
	mode = ReasoningReplayMode(strings.ToLower(strings.TrimSpace(string(mode))))
	if mode == "" {
		mode = ReasoningReplayAuto
	}
	switch mode {
	case ReasoningReplayAuto, ReasoningReplayInclude, ReasoningReplayCurrentTurn, ReasoningReplayOmit:
		return mode, nil
	default:
		return "", fmt.Errorf("推理回放策略无效: %q，只允许 auto/include/current_turn/omit", mode)
	}
}

// EffectiveReasoningReplay 让 DTO、冻结快照和实际请求读取同一份有效协议。
func EffectiveReasoningReplay(provider ProviderType, modelName string, override ReasoningReplayMode) ReasoningReplayMode {
	mode, err := normalizeReasoningReplay(override)
	if err != nil {
		return ReasoningReplayOmit
	}
	if mode != ReasoningReplayAuto {
		return mode
	}
	if preset, ok := presetForModel(provider, modelName); ok && preset.ReasoningReplay != "" {
		return preset.ReasoningReplay
	}
	if mode := presetCatalog.Defaults.ReasoningReplay[provider]; mode != "" {
		return mode
	}
	return ReasoningReplayOmit
}

// reasoningModel 是各协议共用的最终出站边界。
// Eino 同轮工具循环会产生新的 Assistant，故不能只在历史投影中应用回放策略。
type reasoningModel struct {
	inner  einomodel.ToolCallingChatModel
	policy ReasoningReplayMode
}

var _ einomodel.ToolCallingChatModel = (*reasoningModel)(nil)

func (m *reasoningModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	return m.inner.Generate(ctx, reasoningForRequest(input, m.policy), opts...)
}

func (m *reasoningModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.inner.Stream(ctx, reasoningForRequest(input, m.policy), opts...)
}

func (m *reasoningModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &reasoningModel{inner: bound, policy: m.policy}, nil
}

// reasoningForRequest 只修改副本。文本/推理块统一为 Content/ReasoningContent，
// 与 Transcript Codec 重放表示一致，避免直播消息为数组、重放消息为字符串而改变请求。
func reasoningForRequest(messages []*schema.Message, policy ReasoningReplayMode) []*schema.Message {
	latestUser := -1
	if policy == ReasoningReplayCurrentTurn {
		for index, message := range messages {
			if message != nil && message.Role == schema.User {
				latestUser = index
			}
		}
	}
	result := make([]*schema.Message, len(messages))
	for index, message := range messages {
		if message == nil || message.Role != schema.Assistant {
			result[index] = message
			continue
		}
		clone := *message
		replay := policy == ReasoningReplayInclude || policy == ReasoningReplayCurrentTurn && index > latestUser
		if len(message.ToolCalls) > 0 {
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
					text.WriteString(part.Text)
					parts = append(parts, part)
				default:
					textOnly = false
					parts = append(parts, part)
				}
			}
			clone.AssistantGenMultiContent = parts
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
		result[index] = &clone
	}
	return result
}
