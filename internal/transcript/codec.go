package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// EncodeOptions 是 Eino Runtime Message 写入 Session 时需要补充的持久化上下文。
//
// Content、Reasoning、ToolCalls、FinishReason、TokenUsage 都由 schema.Message 表达，
// 不在这里重复定义。这里只保存 Eino 通用 Message 没有稳定表达、但 Session 需要长期
// 记录的信息，例如实际 Provider/Model、ResponseID 与 ToolResult details。
type EncodeOptions struct {
	API string

	Provider string

	Model string

	ResponseModel string

	ResponseID string

	// ForcedFinishReason 仅用于 Runtime 已经明确知道的本地异常终态，例如 cancelled。
	// 正常模型响应必须留空，直接使用 message.ResponseMeta.FinishReason。
	ForcedFinishReason string

	Details json.RawMessage

	IsError bool

	Cost UsageCost

	Timestamp time.Time
}

// DecodedMessage 是磁盘 Wire Message 恢复后的 Runtime 结果。
type DecodedMessage struct {
	Message *schema.Message

	Options EncodeOptions

	StopReason StopReason
}

// EncodeMessage 将 Eino schema.Message 编码为稳定 JSONL v3 Wire Message。
//
// 这是整个工程中唯一允许执行“Eino Message -> Transcript Message”转换的位置。
func EncodeMessage(message *schema.Message, options EncodeOptions) (AgentMessage, error) {
	if message == nil {
		return AgentMessage{}, errors.New("Eino Message 不能为空")
	}

	timestamp := options.Timestamp.UTC()
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	switch message.Role {
	case schema.User:
		content, err := encodeUserMessageContent(message)
		if err != nil {
			return AgentMessage{}, fmt.Errorf("编码 UserMessage 失败: %w", err)
		}
		return AgentMessage{
			Role:      RoleUser,
			Content:   content,
			Timestamp: timestamp.UnixMilli(),
		}, nil

	case schema.Assistant:
		content, err := encodeAssistantContent(message)
		if err != nil {
			return AgentMessage{}, err
		}
		if len(content) == 0 {
			return AgentMessage{}, errors.New("AssistantMessage 没有可持久化内容")
		}

		provider := strings.TrimSpace(options.Provider)
		model := strings.TrimSpace(options.Model)
		if provider == "" {
			return AgentMessage{}, errors.New("AssistantMessage Provider 不能为空")
		}
		if model == "" {
			return AgentMessage{}, errors.New("AssistantMessage Model 不能为空")
		}

		return AgentMessage{
			Role:          RoleAssistant,
			Content:       content,
			API:           strings.TrimSpace(options.API),
			Provider:      provider,
			Model:         model,
			ResponseModel: strings.TrimSpace(options.ResponseModel),
			Usage:         encodeUsage(message, options.Cost),
			StopReason:    normalizeStopReason(message, options.ForcedFinishReason),
			Timestamp:     timestamp.UnixMilli(),
			ResponseID:    strings.TrimSpace(options.ResponseID),
		}, nil

	case schema.Tool:
		toolCallID := strings.TrimSpace(message.ToolCallID)
		toolName := strings.TrimSpace(message.ToolName)
		if toolCallID == "" {
			return AgentMessage{}, errors.New("ToolResultMessage ToolCallID 不能为空")
		}
		if toolName == "" {
			return AgentMessage{}, errors.New("ToolResultMessage ToolName 不能为空")
		}

		content, err := encodeTextOnlyMessageContent(message)
		if err != nil {
			return AgentMessage{}, fmt.Errorf("编码 ToolResultMessage 失败: %w", err)
		}

		return AgentMessage{
			Role:       RoleToolResult,
			Content:    content,
			Timestamp:  timestamp.UnixMilli(),
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Details:    cloneRawJSON(options.Details),
			IsError:    options.IsError,
		}, nil

	default:
		return AgentMessage{}, fmt.Errorf("Session JSONL 不支持持久化 Eino Role %q", message.Role)
	}
}

// DecodeMessage 将 JSONL v3 Wire Message 恢复为 Eino schema.Message。
//
// Thinking 恢复到 schema.Message.ReasoningContent；ToolCall 恢复到 schema.ToolCalls；
// ToolResult 恢复成标准 schema.Tool Message。
//
// 这里刻意不把历史 Thinking 重新放进 AssistantGenMultiContent.Reasoning。当前
// eino-ext OpenAI/OpenAI-Compatible Adapter 会把 AssistantGenMultiContent 当作普通多模态
// Chat Message Part 编码，而其 Chat Completions 请求并不接受 reasoning part，历史会话一旦
// 包含 Thinking，就会报：
//
//	unsupported chat message part type for AssistantGenMultiContent: reasoning
//
// JSONL 仍完整保留 thinkingSignature 等持久化信息；当前标准 ChatModel Runtime 只通过
// ReasoningContent 回放 Reasoning。以后切换到支持结构化 Reasoning 的 AgenticModel 时，
// 只需要扩展本 Codec，不需要改变 Session JSONL v3。
func DecodeMessage(message *AgentMessage) (DecodedMessage, error) {
	if message == nil {
		return DecodedMessage{}, errors.New("Transcript AgentMessage 不能为空")
	}
	if err := validateAgentMessage(*message); err != nil {
		return DecodedMessage{}, fmt.Errorf("Transcript AgentMessage 无效: %w", err)
	}

	options := EncodeOptions{
		API:           message.API,
		Provider:      message.Provider,
		Model:         message.Model,
		ResponseModel: message.ResponseModel,
		ResponseID:    message.ResponseID,
		Details:       cloneRawJSON(message.Details),
		IsError:       message.IsError,
	}
	if message.Timestamp > 0 {
		options.Timestamp = time.UnixMilli(message.Timestamp).UTC()
	}
	if message.Usage != nil {
		options.Cost = message.Usage.Cost
	}

	switch message.Role {
	case RoleUser:
		userMessage, err := decodeUserMessage(message.Content)
		if err != nil {
			return DecodedMessage{}, err
		}
		return DecodedMessage{
			Message: userMessage,
			Options: options,
		}, nil

	case RoleAssistant:
		result := &schema.Message{Role: schema.Assistant}
		toolCalls := make([]schema.ToolCall, 0)
		var textBuilder strings.Builder
		var reasoningBuilder strings.Builder

		for index, block := range message.Content {
			switch block.Type {
			case ContentText:
				textBuilder.WriteString(block.Text)

			case ContentThinking:
				if block.Redacted {
					// Eino schema.Message 没有通用的 redacted reasoning wire block。
					// 密文继续保留在 Transcript，但不伪装成可发送的 ReasoningContent。
					continue
				}
				reasoningBuilder.WriteString(block.Thinking)

			case ContentToolCall:
				arguments, err := normalizeToolArguments(block.Arguments)
				if err != nil {
					return DecodedMessage{}, fmt.Errorf(
						"content[%d] ToolCall Arguments 无效: %w",
						index,
						err,
					)
				}

				extra := make(map[string]any)
				if value := strings.TrimSpace(block.ThoughtSignature); value != "" {
					extra["thought_signature"] = value
				}
				if value := strings.TrimSpace(block.Namespace); value != "" {
					extra["namespace"] = value
				}
				if len(extra) == 0 {
					extra = nil
				}

				toolCalls = append(toolCalls, schema.ToolCall{
					ID:   block.ID,
					Type: "function",
					Function: schema.FunctionCall{
						Name:      block.Name,
						Arguments: string(arguments),
					},
					Extra: extra,
				})

			default:
				return DecodedMessage{}, fmt.Errorf("Assistant ContentBlock 类型不支持: %q", block.Type)
			}
		}

		result.Content = textBuilder.String()
		result.ReasoningContent = reasoningBuilder.String()
		result.ToolCalls = toolCalls
		result.ResponseMeta = decodeResponseMeta(message)

		return DecodedMessage{
			Message:    result,
			Options:    options,
			StopReason: message.StopReason,
		}, nil

	case RoleToolResult:
		text, err := wireTextContent(message.Content)
		if err != nil {
			return DecodedMessage{}, err
		}
		result := schema.ToolMessage(
			text,
			message.ToolCallID,
			schema.WithToolName(message.ToolName),
		)
		return DecodedMessage{
			Message: result,
			Options: options,
		}, nil

	default:
		return DecodedMessage{}, fmt.Errorf("不支持的 Transcript Message Role: %q", message.Role)
	}
}

const attachmentURLPrefix = "humbert-attachment://"

func encodeUserMessageContent(message *schema.Message) ([]ContentBlock, error) {
	if message == nil {
		return nil, errors.New("Message 不能为空")
	}
	if len(message.UserInputMultiContent) == 0 {
		if strings.TrimSpace(message.Content) == "" {
			return nil, errors.New("UserMessage 内容不能为空")
		}
		return []ContentBlock{{Type: ContentText, Text: message.Content}}, nil
	}
	blocks := make([]ContentBlock, 0, len(message.UserInputMultiContent))
	for index, part := range message.UserInputMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			if part.Text != "" {
				blocks = append(blocks, ContentBlock{Type: ContentText, Text: part.Text})
			}
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil || part.Image.URL == nil {
				return nil, fmt.Errorf("UserInputMultiContent[%d] image 缺少 sidecar URL", index)
			}
			id := strings.TrimPrefix(strings.TrimSpace(*part.Image.URL), attachmentURLPrefix)
			if id == "" || id == *part.Image.URL {
				return nil, fmt.Errorf("UserInputMultiContent[%d] image URL 不是 Humbert sidecar 引用", index)
			}
			blocks = append(blocks, ContentBlock{Type: ContentImage, AttachmentID: id, Name: extraString(part.Extra, "name"), MIMEType: part.Image.MIMEType, SizeBytes: extraInt64(part.Extra, "size_bytes")})
		case schema.ChatMessagePartTypeFileURL:
			if part.File == nil || part.File.URL == nil {
				return nil, fmt.Errorf("UserInputMultiContent[%d] file 缺少 sidecar URL", index)
			}
			id := strings.TrimPrefix(strings.TrimSpace(*part.File.URL), attachmentURLPrefix)
			if id == "" || id == *part.File.URL {
				return nil, fmt.Errorf("UserInputMultiContent[%d] file URL 不是 Humbert sidecar 引用", index)
			}
			name := strings.TrimSpace(part.File.Name)
			if name == "" {
				name = extraString(part.Extra, "name")
			}
			blocks = append(blocks, ContentBlock{Type: ContentFile, AttachmentID: id, Name: name, MIMEType: part.File.MIMEType, SizeBytes: extraInt64(part.Extra, "size_bytes")})
		default:
			return nil, fmt.Errorf("UserInputMultiContent[%d] 类型暂不支持持久化: %q", index, part.Type)
		}
	}
	if len(blocks) == 0 {
		return nil, errors.New("UserMessage 没有可持久化内容")
	}
	return blocks, nil
}

func decodeUserMessage(blocks []ContentBlock) (*schema.Message, error) {
	if len(blocks) == 0 {
		return nil, errors.New("UserMessage content 为空")
	}
	parts := make([]schema.MessageInputPart, 0, len(blocks))
	textOnly := true
	for index, block := range blocks {
		switch block.Type {
		case ContentText:
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: block.Text})
		case ContentImage:
			textOnly = false
			url := attachmentURLPrefix + block.AttachmentID
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{URL: &url, MIMEType: block.MIMEType}}, Extra: map[string]any{"name": block.Name, "size_bytes": block.SizeBytes, "attachment_id": block.AttachmentID}})
		case ContentFile:
			textOnly = false
			url := attachmentURLPrefix + block.AttachmentID
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeFileURL, File: &schema.MessageInputFile{MessagePartCommon: schema.MessagePartCommon{URL: &url, MIMEType: block.MIMEType}, Name: block.Name}, Extra: map[string]any{"name": block.Name, "size_bytes": block.SizeBytes, "attachment_id": block.AttachmentID}})
		default:
			return nil, fmt.Errorf("UserMessage content[%d] 类型不支持: %q", index, block.Type)
		}
	}
	if textOnly {
		var b strings.Builder
		for _, part := range parts {
			b.WriteString(part.Text)
		}
		return schema.UserMessage(b.String()), nil
	}
	return &schema.Message{Role: schema.User, UserInputMultiContent: parts}, nil
}

func extraInt64(extra map[string]any, key string) int64 {
	if extra == nil {
		return 0
	}
	value, ok := extra[key]
	if !ok {
		return 0
	}
	switch n := value.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		v, _ := n.Int64()
		return v
	default:
		return 0
	}
}

func encodeTextOnlyMessageContent(message *schema.Message) ([]ContentBlock, error) {
	if message == nil {
		return nil, errors.New("Message 不能为空")
	}
	if message.Content == "" {
		return nil, errors.New("Message text 内容不能为空")
	}
	return []ContentBlock{{Type: ContentText, Text: message.Content}}, nil
}

func encodeAssistantContent(message *schema.Message) ([]ContentBlock, error) {
	blocks := make([]ContentBlock, 0, len(message.AssistantGenMultiContent)+len(message.ToolCalls)+2)
	hasStructuredText := false
	hasStructuredReasoning := false

	for index, part := range message.AssistantGenMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			if part.Text == "" {
				continue
			}
			hasStructuredText = true
			blocks = append(blocks, ContentBlock{Type: ContentText, Text: part.Text})

		case schema.ChatMessagePartTypeReasoning:
			if part.Reasoning == nil || part.Reasoning.Text == "" {
				continue
			}
			hasStructuredReasoning = true
			blocks = append(blocks, ContentBlock{
				Type:              ContentThinking,
				Thinking:          part.Reasoning.Text,
				ThinkingSignature: part.Reasoning.Signature,
			})

		default:
			return nil, fmt.Errorf(
				"AssistantGenMultiContent[%d] 类型 %q 暂未纳入 Session v3 协议",
				index,
				part.Type,
			)
		}
	}

	// 部分 Provider 目前只填写兼容字段 Content / ReasoningContent。只有结构化字段不存在
	// 时才回退，避免同一内容被持久化两次。
	if !hasStructuredReasoning && message.ReasoningContent != "" {
		blocks = append([]ContentBlock{{
			Type:     ContentThinking,
			Thinking: message.ReasoningContent,
		}}, blocks...)
	}
	if !hasStructuredText && message.Content != "" {
		blocks = append(blocks, ContentBlock{Type: ContentText, Text: message.Content})
	}

	// schema.Message 的 ToolCalls 与 AssistantGenMultiContent 是两个独立字段，无法表达
	// ToolCall 与文本块之间任意交错。当前 Eino ChatModel 协议中 ToolCall 是 Assistant
	// Step 的终止动作，因此稳定追加在生成内容之后。
	for index, call := range message.ToolCalls {
		id := strings.TrimSpace(call.ID)
		name := strings.TrimSpace(call.Function.Name)
		if id == "" {
			return nil, fmt.Errorf("ToolCalls[%d] ID 不能为空", index)
		}
		if name == "" {
			return nil, fmt.Errorf("ToolCalls[%d] Function.Name 不能为空", index)
		}

		arguments, err := normalizeToolArguments([]byte(call.Function.Arguments))
		if err != nil {
			return nil, fmt.Errorf("ToolCalls[%d] Arguments 无效: %w", index, err)
		}

		blocks = append(blocks, ContentBlock{
			Type:             ContentToolCall,
			ID:               id,
			Name:             name,
			Arguments:        arguments,
			ThoughtSignature: extraString(call.Extra, "thought_signature", "thoughtSignature"),
			Namespace:        extraString(call.Extra, "namespace"),
		})
	}

	return blocks, nil
}

func encodeUsage(message *schema.Message, cost UsageCost) *Usage {
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		if cost == (UsageCost{}) {
			return nil
		}
		return &Usage{Cost: cost}
	}

	usage := message.ResponseMeta.Usage
	return &Usage{
		Input:       usage.PromptTokens,
		Output:      usage.CompletionTokens,
		CacheRead:   usage.PromptTokenDetails.CachedTokens,
		CacheWrite:  0,
		Reasoning:   usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens: usage.TotalTokens,
		Cost:        cost,
	}
}

func decodeResponseMeta(message *AgentMessage) *schema.ResponseMeta {
	if message == nil || message.Role != RoleAssistant {
		return nil
	}

	meta := &schema.ResponseMeta{
		FinishReason: einoFinishReason(message.StopReason),
	}
	if message.Usage != nil {
		meta.Usage = &schema.TokenUsage{
			PromptTokens: message.Usage.Input,
			PromptTokenDetails: schema.PromptTokenDetails{
				CachedTokens: message.Usage.CacheRead,
			},
			CompletionTokens: message.Usage.Output,
			CompletionTokensDetails: schema.CompletionTokensDetails{
				ReasoningTokens: message.Usage.Reasoning,
			},
			TotalTokens: message.Usage.TotalTokens,
		}
	}
	return meta
}

func normalizeStopReason(message *schema.Message, forced string) StopReason {
	if value := strings.TrimSpace(forced); value != "" {
		return stopReasonFromRaw(value)
	}
	if message != nil && len(message.ToolCalls) > 0 {
		return StopReasonToolUse
	}
	if message != nil && message.ResponseMeta != nil {
		return stopReasonFromRaw(message.ResponseMeta.FinishReason)
	}
	return StopReasonStop
}

func stopReasonFromRaw(raw string) StopReason {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "tool_calls", "tool_call", "tool_use", "tooluse":
		return StopReasonToolUse
	case "length", "max_tokens", "max_output_tokens":
		return StopReasonLength
	case "error", "content_filter", "failed", "failure":
		return StopReasonError
	case "aborted", "cancelled", "canceled":
		return StopReasonAborted
	case "deferred":
		return StopReasonDeferred
	default:
		return StopReasonStop
	}
}

func einoFinishReason(reason StopReason) string {
	switch reason {
	case StopReasonToolUse:
		return "tool_calls"
	case StopReasonLength:
		return "length"
	case StopReasonError:
		return "error"
	case StopReasonAborted:
		return "aborted"
	case StopReasonDeferred:
		return "deferred"
	default:
		return "stop"
	}
}

func wireTextContent(blocks []ContentBlock) (string, error) {
	var builder strings.Builder
	for index, block := range blocks {
		if block.Type != ContentText {
			return "", fmt.Errorf("只允许 text block，content[%d] 实际为 %q", index, block.Type)
		}
		builder.WriteString(block.Text)
	}
	if builder.Len() == 0 {
		return "", errors.New("Message text 内容为空")
	}
	return builder.String(), nil
}

func normalizeToolArguments(input []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if !json.Valid(trimmed) {
		return nil, errors.New("不是合法 JSON")
	}
	if trimmed[0] != '{' {
		return nil, errors.New("必须是 JSON Object")
	}
	result := make(json.RawMessage, len(trimmed))
	copy(result, trimmed)
	return result, nil
}

func extraString(extra map[string]any, keys ...string) string {
	if extra == nil {
		return ""
	}
	for _, key := range keys {
		value, exists := extra[key]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func cloneRawJSON(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return nil
	}
	result := make(json.RawMessage, len(input))
	copy(result, input)
	return result
}
