package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
)

// SessionDTO 是返回给 Vue 的 Session。
type SessionDTO struct {
	ID string `json:"id"`

	AgentID string `json:"agentID"`

	Title string `json:"title"`

	CreatedAt string `json:"createdAt"`

	UpdatedAt string `json:"updatedAt"`
}

// MessageDTO 是当前聊天前端使用的只读兼容投影。
//
// 真实持久化已经完全切换到 JSONL v3 typed message + Eino schema.Message。本 DTO 不会
// 写回磁盘，只为了在 Process Timeline 前端完成重构前继续兼容现有 toolTrace.js。
type MessageDTO struct {
	MessageNo int64 `json:"messageNo"`

	ID string `json:"id"`

	SessionID string `json:"sessionID"`

	Role string `json:"role"`

	Content string `json:"content"`

	Metadata map[string]any `json:"metadata"`

	Attachments []AttachmentDTO `json:"attachments,omitempty"`

	CreatedAt string `json:"createdAt"`
}

// MessagePageDTO 是聊天界面的游标分页结果。
type MessagePageDTO struct {
	Messages []MessageDTO `json:"messages"`

	HasMore bool `json:"hasMore"`

	NextBeforeID string `json:"nextBeforeID"`
}

// AttachmentDTO 是 UserMessage 附件的安全元数据；二进制通过 ReadAttachment 按需读取。
type AttachmentDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MIMEType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	Kind      string `json:"kind"`
}

// AttachmentContentDTO 是历史附件按需读取结果。
type AttachmentContentDTO struct {
	ID         string `json:"id"`
	Base64Data string `json:"base64Data"`
}

// SessionService 是 Session Domain 的 Wails Adapter。
type SessionService struct {
	core *coreapp.Application
}

// NewSessionService 创建 SessionService。
func NewSessionService(core *coreapp.Application) *SessionService {
	return &SessionService{core: core}
}

// List 返回 Agent 的 Sessions。
func (s *SessionService) List(agentID string) ([]SessionDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	values, err := s.core.Sessions().List(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("读取 Session 列表失败: %w", err)
	}

	result := make([]SessionDTO, 0, len(values))
	for _, value := range values {
		result = append(result, sessionDTO(value))
	}
	return result, nil
}

// Create 创建新 Session。
func (s *SessionService) Create(agentID string, title string) (SessionDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	value, err := s.core.Sessions().Create(ctx, sessions.CreateSessionInput{
		AgentID: agentID,
		Title:   title,
	})
	if err != nil {
		return SessionDTO{}, fmt.Errorf("创建 Session 失败: %w", err)
	}
	return sessionDTO(value), nil
}

// Rename 修改 Session 名称。
func (s *SessionService) Rename(id string, title string) (SessionDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	value, err := s.core.Sessions().Rename(ctx, id, title)
	if err != nil {
		return SessionDTO{}, fmt.Errorf("修改 Session 失败: %w", err)
	}
	return sessionDTO(value), nil
}

// Delete 删除 Session 与完整消息历史。
func (s *SessionService) Delete(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Task、TaskRun 与 Session 的生命周期现在彼此独立。
	//
	// 用户从会话侧栏手动删除一个任务 Session 时，只删除“这段对话”，不会反向删除
	// TaskRun，更不会删除 Task。这样运行历史仍能保留当时的状态、耗时和结果摘要；
	// 对连续任务而言，PersistentSessionID 只是弱引用，下次执行发现 Session 不存在后会
	// 自动创建新的持续会话并更新引用。
	if err := s.core.Runtime().DeleteSession(ctx, id); err != nil {
		return fmt.Errorf("删除 Session 失败: %w", err)
	}

	// Session Scope Rule 只属于当前进程中的临时授权。Session 删除后立即释放对应规则，
	// 防止长时间运行的桌面进程积累已经不可达的授权状态。Session ID 使用 UUID 不会复用，
	// 但主动清理仍能保持清晰生命周期。
	if s.core.Permissions() != nil {
		s.core.Permissions().ClearSessionRules(id)
	}
	return nil
}

// Messages 返回 Session 当前 Active Branch 的最近消息。
//
// SessionManager 返回的 Runtime Message 已经是 Eino *schema.Message。这里仅投影成旧
// Vue DTO，不再从 JSONL metadata 猜测 ToolCall/Thinking。
func (s *SessionService) Messages(sessionID string, limit int) ([]MessageDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	values, err := s.core.Sessions().Messages(ctx, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("读取 Session Message 失败: %w", err)
	}

	result := make([]MessageDTO, 0, len(values))
	for index, value := range values {
		dto, err := messageDTO(value, int64(index+1))
		if err != nil {
			return nil, fmt.Errorf("投影 Message Entry %s 失败: %w", value.EntryID, err)
		}
		result = append(result, dto)
	}
	return result, nil
}

// MessagePage 返回 beforeEntryID 之前的一页消息，不依赖生成的 Wails Binding。
func (s *SessionService) MessagePage(sessionID string, beforeEntryID string, limit int) (MessagePageDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	page, err := s.core.Sessions().MessagePage(ctx, sessionID, beforeEntryID, limit)
	if err != nil {
		return MessagePageDTO{}, fmt.Errorf("分页读取 Session Message 失败: %w", err)
	}

	result := make([]MessageDTO, 0, len(page.Messages))
	for index, value := range page.Messages {
		dto, err := messageDTO(value, int64(page.StartIndex+index+1))
		if err != nil {
			return MessagePageDTO{}, fmt.Errorf("投影 Message Entry %s 失败: %w", value.EntryID, err)
		}
		result = append(result, dto)
	}
	return MessagePageDTO{
		Messages:     result,
		HasMore:      page.HasMore,
		NextBeforeID: page.NextBeforeID,
	}, nil
}

// ReadAttachment 按需读取 Session sidecar。前端只在图片进入历史视图时调用。
func (s *SessionService) ReadAttachment(sessionID string, attachmentID string) (AttachmentContentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := s.core.Sessions().ReadAttachment(ctx, sessionID, attachmentID)
	if err != nil {
		return AttachmentContentDTO{}, fmt.Errorf("读取附件失败: %w", err)
	}
	return AttachmentContentDTO{ID: attachmentID, Base64Data: base64.StdEncoding.EncodeToString(data)}, nil
}

func sessionDTO(value sessions.Session) SessionDTO {
	return SessionDTO{
		ID:        value.ID,
		AgentID:   value.AgentID,
		Title:     value.Title,
		CreatedAt: value.CreatedAt.Format(time.RFC3339),
		UpdatedAt: value.UpdatedAt.Format(time.RFC3339),
	}
}

func messageDTO(value sessions.Message, messageNo int64) (MessageDTO, error) {
	if value.Message == nil {
		return MessageDTO{}, fmt.Errorf("Eino Message 为空")
	}

	role := ""
	metadata := make(map[string]any)
	content := runtimeMessageText(value.Message)
	attachments := runtimeMessageAttachments(value.Message)

	switch value.Message.Role {
	case schema.User:
		role = "user"

	case schema.Assistant:
		role = "assistant"
		if reasoning := runtimeMessageReasoning(value.Message); reasoning != "" {
			metadata["reasoning_content"] = reasoning
		}
		if len(value.Message.ToolCalls) > 0 {
			toolCalls := make([]map[string]any, 0, len(value.Message.ToolCalls))
			for _, call := range value.Message.ToolCalls {
				toolCalls = append(toolCalls, map[string]any{
					"id":   call.ID,
					"type": call.Type,
					"function": map[string]any{
						"name":      call.Function.Name,
						"arguments": call.Function.Arguments,
					},
				})
			}
			metadata["tool_calls"] = toolCalls
		}
		if value.Persistence.Provider != "" {
			metadata["provider_id"] = value.Persistence.Provider
		}
		if value.Persistence.Model != "" {
			metadata["model_name"] = value.Persistence.Model
		}
		if value.Persistence.ResponseModel != "" {
			metadata["response_model"] = value.Persistence.ResponseModel
		}
		if value.Persistence.ResponseID != "" {
			metadata["response_id"] = value.Persistence.ResponseID
		}
		if value.StopReason != "" {
			metadata["stop_reason"] = string(value.StopReason)

			switch value.StopReason {
			case "length", "error", "aborted", "deferred":
				// 这些终态都表示本次 Assistant Step 没有正常完成。前端只需要一个
				// 明确的展示标记，不需要把 Runtime Error 文本复制进 Session JSONL。
				metadata["incomplete"] = true
			}
		}

	case schema.Tool:
		role = "tool"
		metadata["tool_call_id"] = value.Message.ToolCallID
		metadata["tool_name"] = value.Message.ToolName
		metadata["is_error"] = value.Persistence.IsError

	default:
		return MessageDTO{}, fmt.Errorf("不支持的 Eino Message Role: %q", value.Message.Role)
	}

	return MessageDTO{
		MessageNo:   messageNo,
		ID:          value.EntryID,
		SessionID:   value.SessionID,
		Role:        role,
		Content:     content,
		Metadata:    metadata,
		Attachments: attachments,
		CreatedAt:   value.CreatedAt.Format(time.RFC3339),
	}, nil
}

func runtimeMessageText(message *schema.Message) string {
	if message == nil {
		return ""
	}
	if message.Content != "" {
		return message.Content
	}

	var builder strings.Builder
	if message.Role == schema.User {
		for _, part := range message.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText {
				builder.WriteString(part.Text)
			}
		}
		return builder.String()
	}
	for _, part := range message.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func runtimeMessageAttachments(message *schema.Message) []AttachmentDTO {
	if message == nil || message.Role != schema.User {
		return nil
	}
	result := make([]AttachmentDTO, 0)
	for _, part := range message.UserInputMultiContent {
		kind := ""
		name := ""
		mime := ""
		switch part.Type {
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil {
				continue
			}
			kind = "image"
			mime = part.Image.MIMEType
			name = stringExtra(part.Extra, "name")
		case schema.ChatMessagePartTypeFileURL:
			if part.File == nil {
				continue
			}
			kind = "file"
			mime = part.File.MIMEType
			name = part.File.Name
			if name == "" {
				name = stringExtra(part.Extra, "name")
			}
		default:
			continue
		}
		id := stringExtra(part.Extra, "attachment_id")
		if id == "" {
			continue
		}
		result = append(result, AttachmentDTO{ID: id, Name: name, MIMEType: mime, SizeBytes: int64Extra(part.Extra, "size_bytes"), Kind: kind})
	}
	return result
}

func stringExtra(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
func int64Extra(values map[string]any, key string) int64 {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func runtimeMessageReasoning(message *schema.Message) string {
	if message == nil {
		return ""
	}
	if message.ReasoningContent != "" {
		return message.ReasoningContent
	}

	var builder strings.Builder
	for _, part := range message.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeReasoning && part.Reasoning != nil {
			builder.WriteString(part.Reasoning.Text)
		}
	}
	return builder.String()
}
