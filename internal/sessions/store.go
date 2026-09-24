package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// Store 协调 Session 控制面 SQLite 与消息 JSONL。
//
// 物理布局：
//
//	agents/session-metadata.sqlite
//	agents/<agent-id>/sessions/<session-id>/
//	└── session.jsonl（消息事实）
//
// Runtime Message 仍统一使用 Eino *schema.Message；消息编码/解码完全委托给
// transcript.Codec。SQLite 只保存会话标题、归档、所属 Agent 与排序时间。
type Store struct {
	transcripts *transcript.Store
	catalog     *sessionCatalog
	lifecycleMu sync.Mutex

	indexMu sync.RWMutex

	issues map[string]SessionIssue

	sessionLocks *sessionLockRegistry
}

type sessionLockEntry struct {
	mu sync.Mutex

	refs int
}

type sessionLockRegistry struct {
	mu sync.Mutex

	values map[string]*sessionLockEntry
}

// NewStore 创建 Session Store，并从 JSONL 修复缺失元数据及活动时间。
func NewStore(ctx context.Context, transcripts *transcript.Store) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if transcripts == nil {
		return nil, errors.New("Session Store TranscriptStore 不能为空")
	}

	catalog, err := openSessionCatalog(transcripts.AgentsRoot())
	if err != nil {
		return nil, err
	}
	store := &Store{
		transcripts: transcripts,
		catalog:     catalog,
		issues:      make(map[string]SessionIssue),
		sessionLocks: &sessionLockRegistry{
			values: make(map[string]*sessionLockEntry),
		},
	}
	if err := store.rebuildIndex(ctx); err != nil {
		_ = catalog.close()
		return nil, fmt.Errorf("重建 Session 索引失败: %w", err)
	}
	return store, nil
}

// Close 释放会话元数据库连接；应在所有 Session 操作结束后调用。
func (s *Store) Close() error { return s.catalog.close() }

// ListSessions 从 SQLite 索引读取，不逐个打开 Session 目录或配置。
func (s *Store) ListSessions(ctx context.Context, agentID string) ([]Session, error) {
	return s.catalog.list(ctx, agentID)
}

// ListSessionIDs 返回指定 Agent 下所有存在 transcript 的 Session ID。
//
// 该清单不读取 SQLite，因此 Agent 级删除仍能覆盖并清理损坏 Session。
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

	value, err := s.catalog.get(ctx, id)
	if errors.Is(err, ErrSessionNotFound) {
		if err := s.rebuildIndex(ctx); err != nil {
			return Session{}, fmt.Errorf("刷新 Session 索引失败: %w", err)
		}
		value, err = s.catalog.get(ctx, id)
		if errors.Is(err, ErrSessionNotFound) {
			if issue, unavailable := s.lookupIssue(id); unavailable {
				return Session{}, fmt.Errorf("%w: session_id=%s: %s", ErrSessionUnavailable, id, issue.Error)
			}
			return Session{}, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
		}
	}
	if err != nil {
		return Session{}, err
	}
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

// CreateSession 创建 JSONL，再写入 SQLite 控制面。第二步失败时回滚目录。
func (s *Store) CreateSession(ctx context.Context, value Session) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("创建 Session 被取消: %w", err)
	}
	unlock := s.sessionLocks.lock(value.ID)
	defer unlock()

	if err := s.transcripts.CreateSession(ctx, transcript.CreateSessionInput{
		ID:        value.ID,
		AgentID:   value.AgentID,
		CWD:       value.CWD,
		CreatedAt: value.CreatedAt,
	}); err != nil {
		return translateTranscriptError(err)
	}

	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.CreatedAt
	if modified, err := s.transcripts.SessionModifiedAt(ctx, value.AgentID, value.ID); err == nil && modified.After(value.UpdatedAt) {
		value.UpdatedAt = modified
	}
	if err := s.catalog.insert(ctx, value); err != nil {
		rollbackErr := s.transcripts.DeleteSession(context.WithoutCancel(ctx), value.AgentID, value.ID)
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("写入 Session 元数据库失败: %w", err),
				fmt.Errorf("回滚 Session 数据目录失败: %w", rollbackErr),
			)
		}
		return fmt.Errorf("写入 Session 元数据库失败: %w", err)
	}

	s.clearIssue(value.ID)
	return nil
}

// RenameSession 只更新 SQLite 控制面，不追加 Session Tree Entry。
func (s *Store) RenameSession(ctx context.Context, id string, title string) error {
	_, err := s.GetSession(ctx, id)
	if err != nil {
		return err
	}

	unlock := s.sessionLocks.lock(id)
	defer unlock()

	return s.catalog.rename(ctx, id, title)
}

// SetArchived updates only the session control plane; its transcript remains intact.
func (s *Store) SetArchived(ctx context.Context, id string, archived bool) error {
	_, err := s.GetSession(ctx, id)
	if err != nil {
		return err
	}
	unlock := s.sessionLocks.lock(id)
	defer unlock()
	return s.catalog.setArchived(ctx, id, archived)
}

// DeleteSession 删除 Session 整个目录和对应的 SQLite 元数据。
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return err
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	unlock := s.sessionLocks.lock(id)
	defer unlock()

	if err := s.transcripts.DeleteSession(ctx, session.AgentID, id); err != nil {
		return translateTranscriptError(err)
	}
	if err := s.catalog.delete(ctx, id); err != nil {
		return err
	}
	s.clearIssue(id)
	return nil
}

// PurgeAgentMetadata 在 Agent 目录删除成功后清除其会话目录索引。
func (s *Store) PurgeAgentMetadata(ctx context.Context, agentID string) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if err := s.catalog.deleteAgent(ctx, agentID); err != nil {
		return err
	}
	s.indexMu.Lock()
	for id, issue := range s.issues {
		if issue.AgentID == agentID {
			delete(s.issues, id)
		}
	}
	s.indexMu.Unlock()
	return nil
}

// AppendMessage 把 Eino Message 编码成 JSONL v3 Wire Message，再追加到当前 Tree Leaf。
//
// 元数据库只保存更新时间；消息内容仍只有 JSONL 一份。
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
	// JSONL 已经提交；即使更新时间投影失败，重启后的重建也会以文件 mtime 修复。
	if modified, statErr := s.transcripts.SessionModifiedAt(ctx, session.AgentID, sessionID); statErr == nil {
		_ = s.catalog.touch(ctx, sessionID, modified)
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

// LoadContextTranscript 返回 Transcript 缓存中的只读 ActiveBranch，避免每轮构建模型
// Context 时深拷贝完整 Tree。调用方不得修改返回的 Entry 或嵌套 payload。
func (s *Store) LoadContextTranscript(ctx context.Context, sessionID string) (transcript.Document, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return transcript.Document{}, err
	}
	document, err := s.transcripts.LoadContextSession(ctx, session.AgentID, sessionID)
	if err != nil {
		return transcript.Document{}, translateTranscriptError(err)
	}
	return document, nil
}

func (s *Store) VisitActiveBranchReverse(ctx context.Context, sessionID string, visit func(transcript.Entry) bool) error {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	return translateTranscriptError(s.transcripts.VisitActiveBranchReverse(ctx, session.AgentID, sessionID, visit))
}

func (s *Store) ReadActiveBranchRange(ctx context.Context, sessionID, entryID string, before, after int) ([]transcript.Entry, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	entries, err := s.transcripts.ReadActiveBranchRange(ctx, session.AgentID, sessionID, entryID, before, after)
	if err != nil {
		return nil, translateTranscriptError(err)
	}
	return entries, nil
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
	if modified, statErr := s.transcripts.SessionModifiedAt(ctx, session.AgentID, sessionID); statErr == nil {
		_ = s.catalog.touch(ctx, sessionID, modified)
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

// rebuildIndex 只从 JSONL 头部恢复缺失元数据，并用文件 mtime 修复活动时间。
func (s *Store) rebuildIndex(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
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

	seen := make(map[string]string)
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
			if existing, exists := seen[ref.ID]; exists && existing != ref.AgentID {
				return fmt.Errorf(
					"发现重复 Session ID %s，分别属于 Agent %s 和 %s",
					ref.ID,
					existing,
					ref.AgentID,
				)
			}
			seen[ref.ID] = ref.AgentID
			value, err := s.catalog.get(ctx, ref.ID)
			if errors.Is(err, ErrSessionNotFound) {
				value, err = s.recoverSessionFromHeader(ctx, ref.AgentID, ref.ID)
				if err != nil {
					newIssues[ref.ID] = SessionIssue{SessionID: ref.ID, AgentID: ref.AgentID, Error: err.Error()}
					continue
				}
				if err := s.catalog.insert(ctx, value); err != nil {
					return fmt.Errorf("恢复 Session %s 元数据失败: %w", ref.ID, err)
				}
			}
			if err != nil {
				return fmt.Errorf("读取 Session %s 元数据失败: %w", ref.ID, err)
			}
			if value.AgentID != ref.AgentID {
				return fmt.Errorf("Session %s 元数据所属 Agent 与目录不一致", ref.ID)
			}
			if modified, err := s.transcripts.SessionModifiedAt(ctx, ref.AgentID, ref.ID); err == nil {
				if err := s.catalog.touch(ctx, ref.ID, modified); err != nil {
					return err
				}
			}
		}
	}
	if err := s.catalog.prune(ctx, seen); err != nil {
		return err
	}

	s.indexMu.Lock()
	s.issues = newIssues
	s.indexMu.Unlock()
	return nil
}

// recoverSessionFromHeader 仅在 SQLite 缺少记录时读取 JSONL 第一行。
// Header 不含旧标题和归档信息，因此使用明确的恢复标题。
func (s *Store) recoverSessionFromHeader(ctx context.Context, agentID, sessionID string) (Session, error) {
	header, err := s.transcripts.LoadHeader(ctx, agentID, sessionID)
	if err != nil {
		return Session{}, translateTranscriptError(err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, header.Timestamp)
	if err != nil {
		return Session{}, fmt.Errorf("解析 Session Header 创建时间失败: %w", err)
	}
	modifiedAt, err := s.transcripts.SessionModifiedAt(ctx, agentID, sessionID)
	if err != nil {
		return Session{}, translateTranscriptError(err)
	}
	createdAt = createdAt.UTC()
	if modifiedAt.Before(createdAt) {
		modifiedAt = createdAt
	}
	return Session{
		ID: sessionID, AgentID: agentID, Title: "恢复的会话", CWD: header.CWD,
		CreatedAt: createdAt, UpdatedAt: modifiedAt,
	}, nil
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

func (r *sessionLockRegistry) lock(key string) func() {
	r.mu.Lock()
	entry := r.values[key]
	if entry == nil {
		entry = &sessionLockEntry{}
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
