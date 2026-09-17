package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const (
	sessionConfigFileName      = "config.json"
	sessionConfigSchemaVersion = 3
)

// sessionDocument 是每个 Session 独立 config.json 的文件结构。
//
// Session 配置与 Conversation Transcript 明确分工：
//   - config.json：Session 控制面信息，例如 title；
//   - session.jsonl：User/Assistant/ToolResult、Thinking、ToolCall 与 Tree。
//
// Title 修改只原子替换 config.json，绝不会再向 JSONL 追加 session_info Entry。
// CreatedAt/CWD 在 Header 中也存在一份不可变协议快照，便于 session.jsonl 单文件导出；
// 日常 Session 管理则以这里的配置为控制面来源。
type sessionDocument struct {
	SchemaVersion int `json:"schema_version"`

	Session sessionConfig `json:"session"`
}

// sessionConfig 保存一个 Session 的低频配置。
//
// UpdatedAt 不持久化：它由 config.json 与 session.jsonl 的实际修改时间计算，这样每次
// Agent Message append 都不需要额外重写 JSON 配置文件，也不会制造高频第二写路径。
type sessionConfig struct {
	ID string `json:"id"`

	AgentID string `json:"agent_id"`

	Title string `json:"title"`

	CWD string `json:"cwd"`

	CreatedAt time.Time `json:"created_at"`
}

// Store 是 Session Domain 的文件持久化协调层。
//
// 物理布局：
//
//	agents/<agent-id>/sessions/<session-id>/
//	├── config.json
//	└── session.jsonl
//
// Runtime Message 仍统一使用 Eino *schema.Message；消息编码/解码完全委托给
// transcript.Codec。Store 只额外管理每 Session 独立 config.json 和一个可重建的
// session_id -> agent_id 内存索引。
type Store struct {
	transcripts *transcript.Store

	indexMu sync.RWMutex

	sessionAgents map[string]string

	issues map[string]SessionIssue

	configLocks *configLockRegistry
}

type configLockEntry struct {
	mu sync.Mutex

	refs int
}

type configLockRegistry struct {
	mu sync.Mutex

	values map[string]*configLockEntry
}

// NewStore 创建 Session Store，并从现有 Session 目录重建定位索引。
func NewStore(ctx context.Context, transcripts *transcript.Store) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if transcripts == nil {
		return nil, errors.New("Session Store TranscriptStore 不能为空")
	}

	store := &Store{
		transcripts:   transcripts,
		sessionAgents: make(map[string]string),
		issues:        make(map[string]SessionIssue),
		configLocks: &configLockRegistry{
			values: make(map[string]*configLockEntry),
		},
	}
	if err := store.rebuildIndex(ctx); err != nil {
		return nil, fmt.Errorf("重建 Session 索引失败: %w", err)
	}
	return store, nil
}

// ListSessions 返回指定 Agent 的全部 Session。
//
// TranscriptStore 只枚举存在 session.jsonl 的 Session 目录；这里再读取每个独立
// config.json 获取 title 等控制面信息。不存在一个全局 sessions/config.json，因此
// 不同 Session 的创建、重命名不会竞争同一配置文件。
func (s *Store) ListSessions(ctx context.Context, agentID string) ([]Session, error) {
	refs, err := s.transcripts.ListSessionRefs(ctx, agentID)
	if err != nil {
		return nil, translateTranscriptError(err)
	}

	result := make([]Session, 0, len(refs))
	for _, ref := range refs {
		value, err := s.readSessionConfig(ctx, ref.AgentID, ref.ID)
		if err != nil {
			s.recordIssue(ref.ID, ref.AgentID, err)
			continue
		}
		result = append(result, value)
		s.rememberSession(value.ID, value.AgentID)
		s.clearIssue(value.ID)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

// ListSessionIDs 返回指定 Agent 下所有存在 transcript 的 Session ID。
//
// 该清单不读取 config.json，因此 Agent 级删除仍能覆盖并清理已被隔离的损坏 Session。
func (s *Store) ListSessionIDs(ctx context.Context, agentID string) ([]string, error) {
	refs, err := s.transcripts.ListSessionRefs(ctx, agentID)
	if err != nil {
		return nil, translateTranscriptError(err)
	}
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		result = append(result, ref.ID)
	}
	return result, nil
}

// GetSession 根据全局唯一 SessionID 返回 Session 配置投影。
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, ErrSessionNotFound
	}

	agentID, exists := s.lookupAgent(id)
	if !exists {
		if err := s.rebuildIndex(ctx); err != nil {
			return Session{}, fmt.Errorf("刷新 Session 索引失败: %w", err)
		}
		agentID, exists = s.lookupAgent(id)
		if !exists {
			if issue, unavailable := s.lookupIssue(id); unavailable {
				return Session{}, fmt.Errorf("%w: session_id=%s: %s", ErrSessionUnavailable, id, issue.Error)
			}
			return Session{}, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
		}
	}

	value, err := s.readSessionConfig(ctx, agentID, id)
	if err != nil {
		if errors.Is(err, transcript.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			s.forgetSession(id)
		} else {
			s.recordIssue(id, agentID, err)
			return Session{}, fmt.Errorf("%w: session_id=%s: %v", ErrSessionUnavailable, id, err)
		}
		return Session{}, err
	}
	s.clearIssue(id)
	return value, nil
}

// Issues 返回当前被隔离的 Session，供启动日志与诊断界面使用。
func (s *Store) Issues() []SessionIssue {
	s.indexMu.RLock()
	defer s.indexMu.RUnlock()
	result := make([]SessionIssue, 0, len(s.issues))
	for _, issue := range s.issues {
		result = append(result, issue)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AgentID != result[j].AgentID {
			return result[i].AgentID < result[j].AgentID
		}
		return result[i].SessionID < result[j].SessionID
	})
	return result
}

// CreateSession 创建独立 Session 目录中的 session.jsonl 与 config.json。
//
// Transcript 先创建协议文件，随后使用 atomicfile 写 config.json。第二步失败时会补偿
// 删除整个 Session 目录；进程在两步之间退出时，下次读取从合法 Header 恢复缺失配置。
func (s *Store) CreateSession(ctx context.Context, value Session) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("创建 Session 被取消: %w", err)
	}
	path, err := s.configPath(value.AgentID, value.ID)
	if err != nil {
		return err
	}
	// 创建与缺失配置恢复使用同一把锁，避免并发列表读取把新会话恢复为默认标题。
	unlock := s.configLocks.lock(path)
	defer unlock()

	if err := s.transcripts.CreateSession(ctx, transcript.CreateSessionInput{
		ID:        value.ID,
		AgentID:   value.AgentID,
		CWD:       value.CWD,
		CreatedAt: value.CreatedAt,
	}); err != nil {
		return translateTranscriptError(err)
	}

	document := sessionDocument{
		SchemaVersion: sessionConfigSchemaVersion,
		Session: sessionConfig{
			ID:        value.ID,
			AgentID:   value.AgentID,
			Title:     value.Title,
			CWD:       value.CWD,
			CreatedAt: value.CreatedAt.UTC(),
		},
	}
	if err := atomicfile.WriteJSON(ctx, path, 0o600, document); err != nil {
		rollbackErr := s.transcripts.DeleteSession(context.WithoutCancel(ctx), value.AgentID, value.ID)
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("创建 Session config.json 失败: %w", err),
				fmt.Errorf("回滚 Session 数据目录失败: %w", rollbackErr),
			)
		}
		return fmt.Errorf("创建 Session config.json 失败: %w", err)
	}

	s.rememberSession(value.ID, value.AgentID)
	return nil
}

// RenameSession 原子更新单个 Session 的 config.json。
//
// Rename 不再产生 Session Tree Entry，因此改名不会污染模型上下文，也不会让
// session.jsonl 出现与 Agent 对话无关的 session_info 记录。
func (s *Store) RenameSession(ctx context.Context, id string, title string) error {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return err
	}

	path, err := s.configPath(session.AgentID, id)
	if err != nil {
		return err
	}
	unlock := s.configLocks.lock(path)
	defer unlock()

	document, err := s.readSessionDocument(ctx, path, session.AgentID, id)
	if err != nil {
		return err
	}
	document.Session.Title = title

	if err := atomicfile.WriteJSON(ctx, path, 0o600, document); err != nil {
		return fmt.Errorf("更新 Session config.json 失败: %w", err)
	}
	return nil
}

// DeleteSession 删除 Session 整个目录，包括 config.json、session.jsonl 与未来 sidecar。
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return err
	}

	path, err := s.configPath(session.AgentID, id)
	if err != nil {
		return err
	}
	unlock := s.configLocks.lock(path)
	defer unlock()

	if err := s.transcripts.DeleteSession(ctx, session.AgentID, id); err != nil {
		return translateTranscriptError(err)
	}
	s.forgetSession(id)
	return nil
}

// AppendMessage 把 Eino Message 编码成 JSONL v3 Wire Message，再追加到当前 Tree Leaf。
//
// config.json 不参与消息写入，因此 Agent Streaming/Tool Calling 不会产生第二个高频
// writer。Session UpdatedAt 由 session.jsonl 的文件修改时间即时投影。
func (s *Store) AppendMessage(
	ctx context.Context,
	sessionID string,
	message *schema.Message,
	options transcript.EncodeOptions,
) (Message, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return Message{}, err
	}

	wireMessage, err := transcript.EncodeMessage(message, options)
	if err != nil {
		return Message{}, fmt.Errorf("编码 Session Message 失败: %w", err)
	}

	entry, err := s.transcripts.AppendMessage(ctx, session.AgentID, sessionID, wireMessage)
	if err != nil {
		return Message{}, translateTranscriptError(err)
	}

	createdAt, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return Message{}, fmt.Errorf("解析新 Message Entry 时间失败: %w", err)
	}

	decoded, err := transcript.DecodeMessage(entry.Message)
	if err != nil {
		return Message{}, fmt.Errorf("校验刚写入的 Message 失败: %w", err)
	}

	return Message{
		EntryID:     entry.ID,
		ParentID:    entry.ParentID,
		SessionID:   sessionID,
		AgentID:     session.AgentID,
		Message:     decoded.Message,
		Persistence: decoded.Options,
		StopReason:  decoded.StopReason,
		CreatedAt:   createdAt.UTC(),
	}, nil
}

// ListMessages 返回当前 Active Branch 上的 Eino Runtime Message。
func (s *Store) ListMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	page, err := s.ListMessagePage(ctx, sessionID, "", limit)
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

// ListMessagePage 返回 beforeEntryID 之前的一页 Active Branch Message。
//
// beforeEntryID 为空时从分支末尾读取。游标只接受当前 Active Branch 上的 Message
// Entry，避免在分支变化后静默返回错误窗口。
func (s *Store) ListMessagePage(
	ctx context.Context,
	sessionID string,
	beforeEntryID string,
	limit int,
) (MessagePage, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return MessagePage{}, err
	}

	entryPage, err := s.transcripts.LoadMessagePage(ctx, session.AgentID, sessionID, beforeEntryID, limit)
	if err != nil {
		return MessagePage{}, translateTranscriptError(err)
	}
	pageMessages := make([]Message, 0, len(entryPage.Entries))
	for _, entry := range entryPage.Entries {
		decoded, err := transcript.DecodeMessage(entry.Message)
		if err != nil {
			return MessagePage{}, fmt.Errorf("恢复 Message Entry %s 失败: %w", entry.ID, err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			return MessagePage{}, fmt.Errorf("解析 Message Entry %s 时间失败: %w", entry.ID, err)
		}
		pageMessages = append(pageMessages, Message{
			EntryID:     entry.ID,
			ParentID:    entry.ParentID,
			SessionID:   sessionID,
			AgentID:     session.AgentID,
			Message:     decoded.Message,
			Persistence: decoded.Options,
			StopReason:  decoded.StopReason,
			CreatedAt:   createdAt.UTC(),
		})
	}
	return MessagePage{
		Messages:     pageMessages,
		StartIndex:   entryPage.StartIndex,
		HasMore:      entryPage.HasMore,
		NextBeforeID: entryPage.NextBeforeID,
	}, nil
}

// LoadTranscript 返回指定 Session 的完整 JSONL Tree 内存投影。
//
// 该方法只提供给 ContextEngine/Memory 等需要理解 Compaction 与 ActiveBranch 的内部
// 领域模块。普通聊天 UI 仍应使用 ListMessages，避免上层依赖 Transcript Wire Schema。
func (s *Store) LoadTranscript(ctx context.Context, sessionID string) (transcript.Document, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return transcript.Document{}, err
	}
	document, err := s.transcripts.LoadSession(ctx, session.AgentID, sessionID)
	if err != nil {
		return transcript.Document{}, translateTranscriptError(err)
	}
	return document, nil
}

// AppendCompaction 把 ContextEngine 生成的 CompactionEntry 追加到 Session Tree。
func (s *Store) AppendCompaction(
	ctx context.Context,
	sessionID string,
	input transcript.AppendCompactionInput,
) (transcript.Entry, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return transcript.Entry{}, err
	}
	entry, err := s.transcripts.AppendCompaction(ctx, session.AgentID, sessionID, input)
	if err != nil {
		return transcript.Entry{}, translateTranscriptError(err)
	}
	return entry, nil
}

// SessionDirectory 返回指定 Session 的受控数据目录。
//
// Memory Store 使用该目录写 memory.json。目录路径由 TranscriptStore 在 Humbert
// agents root 下推导并校验，调用方不得把用户输入直接拼接到文件路径。
func (s *Store) SessionDirectory(ctx context.Context, sessionID string) (string, error) {
	if ctx == nil {
		return "", errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("解析 Session 目录被取消: %w", err)
	}
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	directory, err := s.transcripts.SessionDirectory(session.AgentID, sessionID)
	if err != nil {
		return "", translateTranscriptError(err)
	}
	return directory, nil
}

// rebuildIndex 从 Agent Session 目录重新构建 SessionID -> AgentID 索引。
func (s *Store) rebuildIndex(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("重建 Session 索引被取消: %w", err)
	}

	root := s.transcripts.AgentsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("读取 Agent Transcript Root 失败: %w", err)
	}

	newAgents := make(map[string]string)
	newIssues := make(map[string]SessionIssue)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("重建 Session 索引被取消: %w", err)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Agent Transcript 目录不能是符号链接: %s", entry.Name())
		}
		if !entry.IsDir() {
			continue
		}

		refs, err := s.transcripts.ListSessionRefs(ctx, entry.Name())
		if err != nil {
			return err
		}
		for _, ref := range refs {
			// 缺失配置可由合法 Transcript Header 恢复；损坏或不匹配的配置会被隔离，
			// 不能让单个 Session 阻塞整个应用启动。
			value, err := s.readSessionConfig(ctx, ref.AgentID, ref.ID)
			if err != nil {
				newIssues[ref.ID] = SessionIssue{SessionID: ref.ID, AgentID: ref.AgentID, Error: err.Error()}
				continue
			}
			if existing, exists := newAgents[value.ID]; exists && existing != value.AgentID {
				return fmt.Errorf(
					"发现重复 Session ID %s，分别属于 Agent %s 和 %s",
					value.ID,
					existing,
					value.AgentID,
				)
			}
			newAgents[value.ID] = value.AgentID
		}
	}

	s.indexMu.Lock()
	s.sessionAgents = newAgents
	s.issues = newIssues
	s.indexMu.Unlock()
	return nil
}

func (s *Store) readSessionConfig(ctx context.Context, agentID string, sessionID string) (Session, error) {
	path, err := s.configPath(agentID, sessionID)
	if err != nil {
		return Session{}, err
	}
	unlock := s.configLocks.lock(path)
	defer unlock()

	document, err := s.readSessionDocument(ctx, path, agentID, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		document, err = s.recoverMissingSessionConfig(ctx, path, agentID, sessionID)
	}
	if err != nil {
		return Session{}, err
	}

	transcriptModifiedAt, err := s.transcripts.SessionModifiedAt(ctx, agentID, sessionID)
	if err != nil {
		return Session{}, translateTranscriptError(err)
	}
	configInfo, err := os.Stat(path)
	if err != nil {
		return Session{}, fmt.Errorf("读取 Session config.json 修改时间失败: %w", err)
	}

	updatedAt := document.Session.CreatedAt.UTC()
	if transcriptModifiedAt.After(updatedAt) {
		updatedAt = transcriptModifiedAt
	}
	if configInfo.ModTime().UTC().After(updatedAt) {
		updatedAt = configInfo.ModTime().UTC()
	}

	return Session{
		ID:        document.Session.ID,
		AgentID:   document.Session.AgentID,
		Title:     document.Session.Title,
		CWD:       document.Session.CWD,
		CreatedAt: document.Session.CreatedAt.UTC(),
		UpdatedAt: updatedAt,
	}, nil
}

// recoverMissingSessionConfig 只恢复不存在的配置，不覆盖已有、损坏或符号链接配置。
// Header 保存不可变身份、工作目录和创建时间；丢失的标题无法恢复，明确使用恢复标题。
// 调用方必须持有 configLocks；完整加载同时校验 JSONL，保留所有原始消息。
func (s *Store) recoverMissingSessionConfig(ctx context.Context, path, agentID, sessionID string) (sessionDocument, error) {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return sessionDocument{}, fmt.Errorf("检查待恢复 Session 配置失败: %w", err)
		}
		return sessionDocument{}, errors.New("Session 配置已存在，拒绝覆盖恢复")
	}
	loaded, err := s.transcripts.LoadSession(ctx, agentID, sessionID)
	if err != nil {
		return sessionDocument{}, fmt.Errorf("校验待恢复 Session Transcript 失败: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, loaded.Header.Timestamp)
	if err != nil {
		return sessionDocument{}, fmt.Errorf("解析待恢复 Session 创建时间失败: %w", err)
	}
	document := sessionDocument{
		SchemaVersion: sessionConfigSchemaVersion,
		Session: sessionConfig{
			ID: sessionID, AgentID: agentID, Title: "恢复的会话",
			CWD: loaded.Header.CWD, CreatedAt: createdAt.UTC(),
		},
	}
	if err := atomicfile.WriteJSON(ctx, path, 0o600, document); err != nil {
		return sessionDocument{}, fmt.Errorf("恢复 Session config.json 失败: %w", err)
	}
	return document, nil
}

func (s *Store) readSessionDocument(
	ctx context.Context,
	path string,
	expectedAgentID string,
	expectedSessionID string,
) (sessionDocument, error) {
	var document sessionDocument
	if err := atomicfile.ReadJSON(ctx, path, &document); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionDocument{}, fmt.Errorf("Session config.json 不存在: %w", err)
		}
		return sessionDocument{}, fmt.Errorf("读取 Session config.json 失败: %w", err)
	}
	if document.SchemaVersion != sessionConfigSchemaVersion {
		return sessionDocument{}, fmt.Errorf(
			"Session config.json schema_version 不支持: %d",
			document.SchemaVersion,
		)
	}
	if document.Session.ID != expectedSessionID {
		return sessionDocument{}, fmt.Errorf(
			"Session config.json ID 与目录不一致: directory=%s config=%s",
			expectedSessionID,
			document.Session.ID,
		)
	}
	if document.Session.AgentID != expectedAgentID {
		return sessionDocument{}, fmt.Errorf(
			"Session config.json AgentID 与目录不一致: directory=%s config=%s",
			expectedAgentID,
			document.Session.AgentID,
		)
	}
	if strings.TrimSpace(document.Session.Title) == "" {
		return sessionDocument{}, errors.New("Session config.json title 不能为空")
	}
	if document.Session.CreatedAt.IsZero() {
		return sessionDocument{}, errors.New("Session config.json created_at 不能为空")
	}

	return document, nil
}

func (s *Store) configPath(agentID string, sessionID string) (string, error) {
	directory, err := s.transcripts.SessionDirectory(agentID, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, sessionConfigFileName), nil
}

func (s *Store) lookupAgent(sessionID string) (string, bool) {
	s.indexMu.RLock()
	defer s.indexMu.RUnlock()
	agentID, exists := s.sessionAgents[sessionID]
	return agentID, exists
}

func (s *Store) rememberSession(sessionID string, agentID string) {
	s.indexMu.Lock()
	s.sessionAgents[sessionID] = agentID
	delete(s.issues, sessionID)
	s.indexMu.Unlock()
}

func (s *Store) forgetSession(sessionID string) {
	s.indexMu.Lock()
	delete(s.sessionAgents, sessionID)
	delete(s.issues, sessionID)
	s.indexMu.Unlock()
}

func (s *Store) recordIssue(sessionID string, agentID string, err error) {
	s.indexMu.Lock()
	delete(s.sessionAgents, sessionID)
	s.issues[sessionID] = SessionIssue{SessionID: sessionID, AgentID: agentID, Error: err.Error()}
	s.indexMu.Unlock()
}

func (s *Store) clearIssue(sessionID string) {
	s.indexMu.Lock()
	delete(s.issues, sessionID)
	s.indexMu.Unlock()
}

func (s *Store) lookupIssue(sessionID string) (SessionIssue, bool) {
	s.indexMu.RLock()
	defer s.indexMu.RUnlock()
	issue, exists := s.issues[sessionID]
	return issue, exists
}

func translateTranscriptError(err error) error {
	if errors.Is(err, transcript.ErrSessionNotFound) {
		return fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}
	if errors.Is(err, transcript.ErrMessageCursorNotFound) {
		return fmt.Errorf("%w: %v", ErrMessageCursorNotFound, err)
	}
	return err
}

func (r *configLockRegistry) lock(key string) func() {
	r.mu.Lock()
	entry := r.values[key]
	if entry == nil {
		entry = &configLockEntry{}
		r.values[key] = entry
	}
	entry.refs++
	r.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		r.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(r.values, key)
		}
		r.mu.Unlock()
	}
}
