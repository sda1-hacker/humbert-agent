package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/searchindex"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// SessionDTO 是返回给 Vue 的 Session。
type SessionDTO struct {
	ID string `json:"id"`

	AgentID string `json:"agentID"`

	Title    string `json:"title"`
	Archived bool   `json:"archived"`

	CreatedAt string `json:"createdAt"`

	UpdatedAt string `json:"updatedAt"`
}

// MessageDTO 是聊天界面的唯一只读协议，由持久化消息投影得到，不写回历史。
type MessageDTO struct {
	MessageNo int64 `json:"messageNo"`

	ID string `json:"id"`

	SessionID string `json:"sessionID"`

	Role string `json:"role"`

	Content string `json:"content"`

	Metadata MessageMetadataDTO `json:"metadata"`

	Attachments []AttachmentDTO `json:"attachments,omitempty"`

	CreatedAt string `json:"createdAt"`
}

// MessageMetadataDTO 显式约束 UI 需要的字段，避免各入口自行拼接无类型 metadata。
type MessageMetadataDTO struct {
	Reasoning     string            `json:"reasoning_content,omitempty"`
	ToolCalls     []schema.ToolCall `json:"tool_calls,omitempty"`
	ProviderID    string            `json:"provider_id,omitempty"`
	ModelName     string            `json:"model_name,omitempty"`
	ResponseModel string            `json:"response_model,omitempty"`
	ResponseID    string            `json:"response_id,omitempty"`
	StopReason    string            `json:"stop_reason,omitempty"`
	Incomplete    bool              `json:"incomplete,omitempty"`
	ToolCallID    string            `json:"tool_call_id,omitempty"`
	ToolName      string            `json:"tool_name,omitempty"`
	IsError       bool              `json:"is_error,omitempty"`
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
	core   *coreapp.Application
	search *searchindex.Controller
}

type SessionSearchDTO struct {
	Results  []searchindex.Result `json:"results"`
	Updating bool                 `json:"updating"`
	Error    string               `json:"error,omitempty"`
}

// NewSessionService 创建 SessionService。
func NewSessionService(core *coreapp.Application) *SessionService {
	return &SessionService{core: core, search: searchindex.NewController(filepath.Join(core.Config().Paths.CacheDir, "conversation-search.sqlite"))}
}

func (s *SessionService) ServiceShutdown() error { return s.search.Close() }

// Search 先读可丢弃的 SQLite 投影；过期检查在后台执行。前端按 updating 轮询，
// 因此首轮建索引不会把一次搜索请求挂起到两分钟。
func (s *SessionService) Search(query string) (SessionSearchDTO, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SessionSearchDTO{Results: []searchindex.Result{}}, nil
	}
	if len([]rune(query)) > 200 {
		return SessionSearchDTO{}, fmt.Errorf("搜索词不能超过 200 字")
	}
	index, status, release, err := s.search.Acquire("sessions", func(ctx context.Context, index *searchindex.Index) error {
		return searchindex.RefreshSessions(ctx, index, s.core.Agents(), s.core.Sessions())
	})
	if err != nil {
		return SessionSearchDTO{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := index.Search(ctx, query, 50)
	if err != nil {
		return SessionSearchDTO{}, err
	}
	return SessionSearchDTO{Results: results, Updating: status.Updating, Error: status.Error}, nil
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

func (s *SessionService) SetArchived(id string, archived bool) (SessionDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := s.core.Sessions().SetArchived(ctx, id, archived)
	if err != nil {
		return SessionDTO{}, fmt.Errorf("更新会话归档状态失败: %w", err)
	}
	return sessionDTO(value), nil
}

// Delete 删除 Session 与完整消息历史。
func (s *SessionService) Delete(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 删除普通对话时这里只会移除 Session；如果 TaskRun 引用了它，Task Manager 会在
	// 同一个调度临界区内同时删除全部对应运行历史并解除连续任务引用。
	if _, err := s.core.Tasks().DeleteConversation(ctx, id); err != nil {
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

// MessageWindow locates a search hit through the transcript location index and
// reads only its nearby active-branch entries.
func (s *SessionService) MessageWindow(sessionID, entryID string) (MessagePageDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entries, err := s.core.Sessions().ReadActiveBranchRange(ctx, sessionID, entryID, 80, 80)
	if err != nil {
		return MessagePageDTO{}, err
	}
	messages := make([]MessageDTO, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != transcript.EntryMessage || entry.Message == nil {
			continue
		}
		decoded, err := transcript.DecodeMessage(entry.Message)
		if err != nil {
			return MessagePageDTO{}, err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			return MessagePageDTO{}, err
		}
		value := sessions.Message{EntryID: entry.ID, ParentID: entry.ParentID, SessionID: sessionID, Message: decoded.Message, Persistence: decoded.Options, StopReason: decoded.StopReason, CreatedAt: createdAt}
		dto, err := messageDTO(value, int64(len(messages)+1))
		if err != nil {
			return MessagePageDTO{}, err
		}
		messages = append(messages, dto)
	}
	if len(messages) == 0 {
		return MessagePageDTO{}, fmt.Errorf("目标消息不存在")
	}
	beforeID := messages[0].ID
	older, err := s.core.Sessions().MessagePage(ctx, sessionID, beforeID, 1)
	if err != nil {
		return MessagePageDTO{}, err
	}
	return MessagePageDTO{Messages: messages, HasMore: len(older.Messages) > 0, NextBeforeID: beforeID}, nil
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
		Archived:  value.Archived,
		CreatedAt: value.CreatedAt.Format(time.RFC3339),
		UpdatedAt: value.UpdatedAt.Format(time.RFC3339),
	}
}

func messageDTO(value sessions.Message, messageNo int64) (MessageDTO, error) {
	if value.Message == nil {
		return MessageDTO{}, fmt.Errorf("Eino Message 为空")
	}

	role := string(value.Message.Role)
	metadata := MessageMetadataDTO{}
	content := runtimeMessageText(value.Message)
	attachments := runtimeMessageAttachments(value.Message)
	switch value.Message.Role {
	case schema.User:
	case schema.Assistant:
		metadata.Reasoning = runtimeMessageReasoning(value.Message)
		metadata.ToolCalls = value.Message.ToolCalls
		metadata.ProviderID = value.Persistence.Provider
		metadata.ModelName = value.Persistence.Model
		metadata.ResponseModel = value.Persistence.ResponseModel
		metadata.ResponseID = value.Persistence.ResponseID
		metadata.StopReason = string(value.StopReason)
		switch value.StopReason {
		case "length", "error", "aborted", "deferred":
			metadata.Incomplete = true
		}
	case schema.Tool:
		metadata.ToolCallID = value.Message.ToolCallID
		metadata.ToolName = value.Message.ToolName
		metadata.IsError = value.Persistence.IsError
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
