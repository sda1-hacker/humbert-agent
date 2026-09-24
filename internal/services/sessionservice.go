package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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
	core           *coreapp.Application
	searchMu       sync.Mutex
	search         *searchindex.Index
	searchCtx      context.Context
	searchCancel   context.CancelFunc
	searchWorkers  sync.WaitGroup
	searchReaders  sync.WaitGroup
	searchUpdating bool
	searchChecked  time.Time
	searchError    string
}

type SessionSearchDTO struct {
	Results  []searchindex.Result `json:"results"`
	Updating bool                 `json:"updating"`
	Error    string               `json:"error,omitempty"`
}

// NewSessionService 创建 SessionService。
func NewSessionService(core *coreapp.Application) *SessionService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SessionService{core: core, searchCtx: ctx, searchCancel: cancel}
}

func (s *SessionService) ServiceShutdown() error {
	s.searchMu.Lock()
	s.searchCancel()
	s.searchMu.Unlock()
	s.searchWorkers.Wait()
	s.searchReaders.Wait()
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if s.search != nil {
		index := s.search
		s.search = nil
		return index.Close()
	}
	return nil
}

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
	s.searchMu.Lock()
	if err := s.searchCtx.Err(); err != nil {
		s.searchMu.Unlock()
		return SessionSearchDTO{}, err
	}
	if s.search == nil {
		index, err := searchindex.Open(filepath.Join(s.core.Config().Paths.CacheDir, "conversation-search.sqlite"))
		if err != nil {
			s.searchMu.Unlock()
			return SessionSearchDTO{}, err
		}
		s.search = index
	}
	index := s.search
	if s.searchCtx.Err() == nil && !s.searchUpdating && time.Since(s.searchChecked) > 10*time.Second {
		s.searchUpdating = true
		s.searchError = ""
		s.searchWorkers.Add(1)
		go s.refreshSearchIndex(index)
	}
	updating, lastError := s.searchUpdating, s.searchError
	s.searchReaders.Add(1)
	s.searchMu.Unlock()
	defer s.searchReaders.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := index.Search(ctx, query, 50)
	if err != nil {
		return SessionSearchDTO{}, err
	}
	return SessionSearchDTO{Results: results, Updating: updating, Error: lastError}, nil
}

func (s *SessionService) refreshSearchIndex(index *searchindex.Index) {
	defer s.searchWorkers.Done()
	ctx, cancel := context.WithTimeout(s.searchCtx, 2*time.Minute)
	defer cancel()
	err := s.updateSearchIndex(ctx, index)
	s.searchMu.Lock()
	s.searchUpdating = false
	s.searchChecked = time.Now()
	s.searchError = ""
	if err != nil && s.searchCtx.Err() == nil {
		s.searchError = err.Error()
	}
	s.searchMu.Unlock()
}

// updateSearchIndex 只重建 revision 改变的会话；JSONL 始终是权威数据。
func (s *SessionService) updateSearchIndex(ctx context.Context, index *searchindex.Index) error {
	agents, err := s.core.Agents().List(ctx)
	if err != nil {
		return err
	}
	keep := make(map[string]bool)
	for _, agent := range agents {
		sessions, err := s.core.Sessions().List(ctx, agent.Agent.ID)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			keep[session.ID] = true
			revision := session.UpdatedAt.UnixNano()
			cached, exists, err := index.CachedSession(ctx, session.ID)
			if err != nil {
				return err
			}
			indexed := searchindex.Session{ID: session.ID, AgentID: session.AgentID, Title: session.Title, Archived: session.Archived, Revision: revision}
			if exists && cached.Revision == revision {
				if cached.Title != indexed.Title || cached.Archived != indexed.Archived || cached.AgentID != indexed.AgentID {
					if err := index.UpdateSessionMetadata(ctx, indexed); err != nil {
						return err
					}
				}
				continue
			}
			if err := index.ReplaceStream(ctx, indexed, func(add func(searchindex.Message) error) error {
				var insertErr error
				visitErr := s.core.Sessions().VisitActiveBranchReverse(ctx, session.ID, func(entry transcript.Entry) bool {
					if entry.Type != transcript.EntryMessage || entry.Message == nil {
						return true
					}
					role := entry.Message.Role
					if role != transcript.RoleUser && role != transcript.RoleAssistant {
						return true
					}
					var content strings.Builder
					for _, block := range entry.Message.Content {
						if block.Type == transcript.ContentText {
							content.WriteString(block.Text)
							content.WriteByte('\n')
						}
						if block.Type == transcript.ContentFile {
							content.WriteString(block.ExtractedText)
							content.WriteByte('\n')
						}
					}
					insertErr = add(searchindex.Message{EntryID: entry.ID, Role: string(role), Timestamp: entry.Timestamp, Content: content.String()})
					return insertErr == nil
				})
				if visitErr != nil {
					return visitErr
				}
				return insertErr
			}); err != nil {
				return err
			}
		}
	}
	if err := index.Prune(ctx, keep); err != nil {
		return err
	}
	return nil
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
