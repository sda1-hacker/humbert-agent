package sessions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	defaultSessionTitle     = "新会话"
	maxSessionTitleLength   = 200
	maxUserMessageBytes     = 256 * 1024
	defaultMessageLimit     = 200
	maxMessageLimit         = 1000
	defaultMessagePageLimit = 80
	maxMessagePageLimit     = 200
)

// Service 是 Humbert 唯一的 SessionManager。
//
// 消息运行时类型统一使用 Eino *schema.Message。Service 不定义自己的 Message
// Content/Thinking/ToolCall 模型，也不接受任意 metadata map。
//
// 持久化只有三条清晰入口：AppendUserMessage、AppendAssistantMessage、AppendToolResult。
// 三者最终都经过 Store -> transcript.EncodeMessage -> JSONL v3 Tree。
type Service struct {
	store *Store

	agents *agents.Service

	workspaces *workspace.Manager

	logger *logging.Logger
}

// NewService 创建 SessionManager。
func NewService(
	store *Store,
	agentService *agents.Service,
	workspaceManager *workspace.Manager,
	logger *logging.Logger,
) *Service {
	return &Service{
		store:      store,
		agents:     agentService,
		workspaces: workspaceManager,
		logger:     logger,
	}
}

// List 返回指定 Agent 的全部 Session。
func (s *Service) List(ctx context.Context, agentID string) ([]Session, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("Agent ID 不能为空")
	}
	if _, err := s.agents.Get(ctx, agentID); err != nil {
		return nil, err
	}
	return s.store.ListSessions(ctx, agentID)
}

// ListIDs 返回 Agent 下包括已隔离损坏项在内的 Session ID，用于 Aggregate 删除清理。
//
// 这里刻意不要求 Agent 仍为 active：上一次删除在写入 deleting 标记后中断时，重试仍需
// 取得完整 ID 清单并清理 Session 级权限规则。
func (s *Service) ListIDs(ctx context.Context, agentID string) ([]string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("Agent ID 不能为空")
	}
	return s.store.ListSessionIDs(ctx, agentID)
}

// Get 返回指定 Session。
func (s *Service) Get(ctx context.Context, sessionID string) (Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Session{}, errors.New("Session ID 不能为空")
	}
	value, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	if _, err := s.agents.Get(ctx, value.AgentID); err != nil {
		return Session{}, err
	}
	return value, nil
}

// Create 创建新的独立 Session 目录。
//
// Humbert 当前产品约束是一 Agent 对应一个 Workspace，因此 Session 只保存
// AgentID。Workspace 在创建时由 Agent Profile 解析并冻结到 Session CWD。
func (s *Service) Create(ctx context.Context, input CreateSessionInput) (Session, error) {
	if s.store == nil || s.agents == nil || s.workspaces == nil || s.logger == nil {
		return Session{}, errors.New("SessionService 尚未正确初始化")
	}

	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		return Session{}, errors.New("Agent ID 不能为空")
	}
	title, err := normalizeTitle(input.Title)
	if err != nil {
		return Session{}, err
	}

	var value Session
	err = s.agents.WithActiveAgent(ctx, agentID, func(agentInfo agents.AgentInfo) error {
		agentWorkspace, resolveErr := s.workspaces.Resolve(
			ctx,
			agentInfo.Agent.ID,
			agentInfo.Agent.WorkspaceMode,
			agentInfo.Agent.WorkspacePath,
		)
		if resolveErr != nil {
			return fmt.Errorf("解析 Agent Workspace 失败: %w", resolveErr)
		}

		now := time.Now().UTC()
		value = Session{
			ID:        uuid.NewString(),
			AgentID:   agentID,
			Title:     title,
			CWD:       agentWorkspace.RootDir,
			CreatedAt: now,
			UpdatedAt: now,
		}
		return s.store.CreateSession(ctx, value)
	})
	if err != nil {
		return Session{}, err
	}

	s.logger.Info(
		ctx,
		"Session 已创建",
		"operation", "session.create",
		"session_id", value.ID,
		"agent_id", value.AgentID,
	)
	return value, nil
}

// Rename 原子更新该 Session 自己的 config.json，不写 Conversation Tree。
func (s *Service) Rename(ctx context.Context, sessionID string, title string) (Session, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Session{}, errors.New("Session ID 不能为空")
	}

	normalized, err := normalizeTitle(title)
	if err != nil {
		return Session{}, err
	}
	if err := s.store.RenameSession(ctx, sessionID, normalized); err != nil {
		return Session{}, err
	}
	return s.store.GetSession(ctx, sessionID)
}

// SetArchived 修改会话归档状态，不影响对话树。
func (s *Service) SetArchived(ctx context.Context, sessionID string, archived bool) (Session, error) {
	if _, err := s.Get(ctx, sessionID); err != nil {
		return Session{}, err
	}
	if err := s.store.SetArchived(ctx, sessionID, archived); err != nil {
		return Session{}, err
	}
	return s.store.GetSession(ctx, sessionID)
}

// Delete 删除一个 Session 及完整 Tree 历史。
func (s *Service) Delete(ctx context.Context, sessionID string) error {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteSession(ctx, sessionID); err != nil {
		return err
	}

	s.logger.Info(
		ctx,
		"Session 已删除",
		"operation", "session.delete",
		"session_id", sessionID,
		"agent_id", session.AgentID,
	)
	return nil
}

// AppendUserMessage 创建标准 Eino UserMessage 并持久化。
//
// 用户输入必须先落盘，再由 Resolver 从 Active Branch 构造模型上下文；不再额外维护
// pending user message 或第二套临时历史。
func (s *Service) AppendUserMessage(ctx context.Context, sessionID string, content string) (Message, error) {
	return s.appendUserInput(ctx, sessionID, UserInput{Text: content})
}

// AppendUserInput 保存文本 + 图片/文件附件。
func (s *Service) AppendUserInput(ctx context.Context, sessionID string, input UserInput) (Message, error) {
	return s.appendUserInput(ctx, sessionID, input)
}

// PrepareUserMessage 在 Runtime 持有会话占用锁时保存或复用一次用户输入。
// 重试只允许复用当前分支最后一条尚无回复的相同 UserMessage，不能引用其他会话或旧轮次。
func (s *Service) PrepareUserMessage(ctx context.Context, sessionID string, input UserInput, retryID string) (Message, error) {
	retryID = strings.TrimSpace(retryID)
	if retryID == "" {
		return s.AppendUserInput(ctx, sessionID, input)
	}
	messages, err := s.store.ListMessages(ctx, sessionID, 1)
	if err != nil {
		return Message{}, err
	}
	if len(messages) == 1 {
		last := messages[0]
		if last.EntryID == retryID && last.Message != nil && last.Message.Role == schema.User {
			matches, matchErr := s.userInputMatchesStoredMessage(ctx, sessionID, input, last.Message)
			if matchErr != nil {
				return Message{}, fmt.Errorf("校验重试用户输入失败: %w", matchErr)
			}
			if matches {
				return last, nil
			}
			return Message{}, errors.New("重试输入与已保存的用户消息不一致，请刷新会话")
		}
	}
	return Message{}, errors.New("重试消息已不再是当前会话最后一条未回复的用户消息，请刷新会话")
}

// AppendAssistantMessage 持久化一个已经完成的 Eino Assistant Step。
//
// message 必须是合并后的终态 schema.Message。Streaming Delta 只能发给 EventBus，
// 不能调用本方法。Thinking/ToolCalls/Usage/FinishReason 直接从 schema.Message 读取。
func (s *Service) AppendAssistantMessage(
	ctx context.Context,
	sessionID string,
	message *schema.Message,
	persistence AssistantPersistence,
) (Message, error) {
	if message == nil {
		return Message{}, errors.New("Assistant Message 不能为空")
	}
	if message.Role != schema.Assistant {
		return Message{}, fmt.Errorf("%w: AppendAssistantMessage 只接受 assistant role，实际为 %q", ErrInvalidMessageRole, message.Role)
	}

	return s.append(ctx, sessionID, message, transcript.EncodeOptions{
		API:                strings.TrimSpace(persistence.API),
		Provider:           strings.TrimSpace(persistence.Provider),
		Model:              strings.TrimSpace(persistence.Model),
		ResponseModel:      strings.TrimSpace(persistence.ResponseModel),
		ResponseID:         strings.TrimSpace(persistence.ResponseID),
		ForcedFinishReason: strings.TrimSpace(persistence.ForcedFinishReason),
		Cost:               persistence.Cost,
	})
}

// AppendToolResult 持久化一个已经完成的 Eino Tool Message。
func (s *Service) AppendToolResult(
	ctx context.Context,
	sessionID string,
	message *schema.Message,
	persistence ToolResultPersistence,
) (Message, error) {
	if message == nil {
		return Message{}, errors.New("Tool Result Message 不能为空")
	}
	if message.Role != schema.Tool {
		return Message{}, fmt.Errorf("%w: AppendToolResult 只接受 tool role，实际为 %q", ErrInvalidMessageRole, message.Role)
	}

	return s.append(ctx, sessionID, message, transcript.EncodeOptions{
		Details: cloneRawJSON(persistence.Details),
		IsError: persistence.IsError,
	})
}

// Messages 返回当前 Active Branch 上最近的消息记录。
func (s *Service) Messages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("Session ID 不能为空")
	}
	if limit == 0 {
		limit = defaultMessageLimit
	}
	if limit < 1 || limit > maxMessageLimit {
		return nil, fmt.Errorf("Message limit 必须位于 1-%d 之间", maxMessageLimit)
	}
	return s.store.ListMessages(ctx, sessionID, limit)
}

// MessagePage 返回聊天界面使用的一页历史消息。
func (s *Service) MessagePage(
	ctx context.Context,
	sessionID string,
	beforeEntryID string,
	limit int,
) (MessagePage, error) {
	if strings.TrimSpace(sessionID) == "" {
		return MessagePage{}, errors.New("Session ID 不能为空")
	}
	if limit == 0 {
		limit = defaultMessagePageLimit
	}
	if limit < 1 || limit > maxMessagePageLimit {
		return MessagePage{}, fmt.Errorf("Message page limit 必须位于 1-%d 之间", maxMessagePageLimit)
	}
	return s.store.ListMessagePage(ctx, sessionID, beforeEntryID, limit)
}

// BuildContext 返回当前 Active Branch 对应的 Eino Runtime Messages。
// JSONL Wire -> schema.Message 的恢复只在 transcript.DecodeMessage 中实现；附件统一在完整
// Message 列表上水合，以便应用与 Runtime Provider 请求相同的历史图片重放窗口。
func (s *Service) BuildContext(
	ctx context.Context,
	sessionID string,
	limit int,
) ([]*schema.Message, error) {
	messages, err := s.Messages(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}

	result := make([]*schema.Message, 0, len(messages))
	for _, stored := range messages {
		if stored.Message == nil {
			return nil, fmt.Errorf("Session Entry %s 恢复得到空 Eino Message", stored.EntryID)
		}
		result = append(result, stored.Message)
	}
	return s.HydrateMessages(ctx, sessionID, result)
}

// LoadTranscript 返回当前 Session 的完整 Tree 投影，供 ContextEngine 与 Session Memory 使用。
//
// 这是内部领域 API，不应直接暴露给 Wails。这样 Context/Memory 能复用 Session Store
// 的 sessionID -> agentID 安全定位，同时普通 UI 仍只看到稳定 DTO。
func (s *Service) LoadTranscript(ctx context.Context, sessionID string) (transcript.Document, error) {
	document, err := s.store.LoadTranscript(ctx, sessionID)
	if err != nil {
		return transcript.Document{}, err
	}
	return document, nil
}

// LoadContextTranscript 只供 ContextEngine 读取；返回的分支和消息 payload 是只读视图。
func (s *Service) LoadContextTranscript(ctx context.Context, sessionID string) (transcript.Document, error) {
	return s.store.LoadContextTranscript(ctx, sessionID)
}

func (s *Service) VisitActiveBranchReverse(ctx context.Context, sessionID string, visit func(transcript.Entry) bool) error {
	return s.store.VisitActiveBranchReverse(ctx, sessionID, visit)
}

func (s *Service) ReadActiveBranchRange(ctx context.Context, sessionID, entryID string, before, after int) ([]transcript.Entry, error) {
	return s.store.ReadActiveBranchRange(ctx, sessionID, entryID, before, after)
}

// AppendCompaction 持久化一次已经生成的 Context Checkpoint。
func (s *Service) AppendCompaction(
	ctx context.Context,
	sessionID string,
	input transcript.AppendCompactionInput,
) (transcript.Entry, error) {
	entry, err := s.store.AppendCompaction(ctx, sessionID, input)
	if err != nil {
		return transcript.Entry{}, err
	}

	s.logger.Info(
		ctx,
		"Session Context 已压缩",
		"operation", "session.compaction.append",
		"session_id", sessionID,
		"entry_id", entry.ID,
		"first_kept_entry_id", entry.FirstKeptEntryID,
		"tokens_before", entry.TokensBefore,
		"tokens_after", entry.TokensAfter,
	)
	return entry, nil
}

// SessionDirectory 返回 Session 的受控数据目录，供同生命周期 sidecar 状态使用。
func (s *Service) SessionDirectory(ctx context.Context, sessionID string) (string, error) {
	return s.store.SessionDirectory(ctx, sessionID)
}

func (s *Service) append(
	ctx context.Context,
	sessionID string,
	message *schema.Message,
	options transcript.EncodeOptions,
) (Message, error) {
	value, err := s.store.AppendMessage(ctx, sessionID, message, options)
	if err != nil {
		return Message{}, err
	}

	s.logger.Debug(
		ctx,
		"Session Message 已追加",
		"operation", "session.message.append",
		"session_id", sessionID,
		"entry_id", value.EntryID,
		"role", string(message.Role),
	)
	return value, nil
}

func normalizeTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = defaultSessionTitle
	}
	if len([]rune(title)) > maxSessionTitleLength {
		return "", fmt.Errorf("Session 标题长度不能超过 %d", maxSessionTitleLength)
	}
	return title, nil
}

func cloneRawJSON(input []byte) []byte {
	if len(input) == 0 {
		return nil
	}
	result := make([]byte, len(input))
	copy(result, input)
	return result
}
