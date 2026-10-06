package transcript

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store 持久化 Humbert Session JSONL v3。
//
// 文件布局：
//
//	~/.humbert-agent/agents/<agent-id>/sessions/<session-id>/session.jsonl
//
// 并发模型：每个 Session 文件拥有独立进程内 Mutex，不存在全局 Transcript Writer
// Lock。不同 Session 可以并行 fsync；同一 Session 的 Tree append 严格有序。
//
// Store 是文件协议层：它负责 JSONL、Tree、Crash Tail Repair 与路径安全，但不知道
// Eino、Wails、ToolRegistry 或 Agent Runtime。
type Store struct {
	agentsRoot string

	locks *lockRegistry

	cache     *documentCache
	locations *locationCache
}

type lockEntry struct {
	mu sync.Mutex

	refs int
}

type lockRegistry struct {
	mu sync.Mutex

	values map[string]*lockEntry
}

// NewStore 创建 Session Transcript Store。
func NewStore(agentsRoot string) (*Store, error) {
	normalized := strings.TrimSpace(agentsRoot)
	if normalized == "" {
		return nil, errors.New("Transcript Agents Root 不能为空")
	}

	absoluteRoot, err := filepath.Abs(normalized)
	if err != nil {
		return nil, fmt.Errorf("解析 Transcript Agents Root 失败: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)

	if err := os.MkdirAll(absoluteRoot, 0o700); err != nil {
		return nil, fmt.Errorf("创建 Transcript Agents Root 失败: %w", err)
	}

	info, err := os.Lstat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("读取 Transcript Agents Root 状态失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Transcript Agents Root 不能是符号链接")
	}
	if !info.IsDir() {
		return nil, errors.New("Transcript Agents Root 不是目录")
	}

	return &Store{
		agentsRoot: absoluteRoot,
		locks: &lockRegistry{
			values: make(map[string]*lockEntry),
		},
		cache:     newDocumentCache(),
		locations: newLocationCache(),
	}, nil
}

// AgentsRoot 返回 Transcript 内部 Agent Root。
func (s *Store) AgentsRoot() string {
	if s == nil {
		return ""
	}
	return s.agentsRoot
}

// CreateSession 创建新的 v3 Session Transcript。
//
// 物理布局采用“一 Session 一目录”：
//
//	agents/<agent-id>/sessions/<session-id>/session.jsonl
//
// 会话控制面由 sessions 包写入 SQLite，TranscriptStore 不读写元数据库。
// Header 是 JSONL 第一行且不参与 Tree；session.jsonl 使用 O_EXCL 创建，绝不覆盖。
func (s *Store) CreateSession(ctx context.Context, input CreateSessionInput) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := validateIdentifier(input.AgentID); err != nil {
		return fmt.Errorf("Agent ID 无效: %w", err)
	}
	if err := validateIdentifier(input.ID); err != nil {
		return fmt.Errorf("Session ID 无效: %w", err)
	}

	path, err := s.sessionPath(input.AgentID, input.ID)
	if err != nil {
		return err
	}

	unlock := s.locks.lock(path)
	defer unlock()

	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := s.ensureSessionDirectory(input.AgentID, input.ID); err != nil {
		return err
	}

	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	header := SessionHeader{
		Type:      "session",
		Version:   CurrentVersion,
		ID:        input.ID,
		Timestamp: formatTime(createdAt),
		CWD:       strings.TrimSpace(input.CWD),
	}

	line, err := encodeJSONLine(header)
	if err != nil {
		return fmt.Errorf("编码 Session Header 失败: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: session_id=%s", ErrSessionExists, input.ID)
		}
		return fmt.Errorf("创建 Session Transcript 失败: %w", err)
	}

	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	if err := writeFull(file, line); err != nil {
		return fmt.Errorf("写入 Session Header 失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步 Session Header 失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 Session Transcript 失败: %w", err)
	}
	closed = true
	if info, statErr := os.Stat(path); statErr == nil {
		s.cache.putOwned(path, Document{
			Header: header, Entries: []Entry{}, ActiveBranch: []Entry{},
			ContextWindow: ContextWindowIndex{Valid: true, LatestCompactionIndex: -1},
		}, info)
	}
	// The location sidecar is derived and can be created lazily. Its first write
	// happens when a transcript grows past the full-document cache limit.
	return nil
}

// AppendMessage 将一条完整 AgentMessage 作为新的 Tree Node 追加到当前 Leaf。
//
// Runtime Streaming Delta 不调用本方法；只有一个 Assistant Step 进入终态后才会形成
// 完整 AgentMessage 并一次性落盘，因此 JSONL 中不会出现 pending Message。
func (s *Store) AppendMessage(
	ctx context.Context,
	agentID string,
	sessionID string,
	message AgentMessage,
) (Entry, error) {
	if err := validateAgentMessage(message); err != nil {
		return Entry{}, err
	}

	entry := Entry{
		Type:      EntryMessage,
		ID:        uuid.NewString(),
		Timestamp: formatTime(time.Now().UTC()),
		Message:   &message,
	}

	return s.appendEntry(ctx, agentID, sessionID, entry, "")
}

// AppendCompaction 将已经生成并校验的 Context Checkpoint 追加到当前 Session Leaf。
//
// Compaction 不删除任何历史 Message。旧消息仍完整保留在 JSONL 中，ContextEngine 只在
// 投影模型上下文时使用 Summary + FirstKeptEntryID 替代更早历史。摘要生成属于外部模型 IO，
// 调用方必须携带 Prepare 阶段观察到的 ExpectedLeafID；本方法在文件锁内同时校验 Leaf
// 完全一致和 FirstKeptEntryID 仍属于当前 ActiveBranch。任一条件变化都返回
// ErrCompactionStale，调用方应丢弃旧摘要并重新准备。
func (s *Store) AppendCompaction(
	ctx context.Context,
	agentID string,
	sessionID string,
	input AppendCompactionInput,
) (Entry, error) {
	if strings.TrimSpace(input.ExpectedLeafID) == "" {
		return Entry{}, errors.New("Compaction ExpectedLeafID 不能为空")
	}

	entry := Entry{
		Type:             EntryCompaction,
		ID:               uuid.NewString(),
		Timestamp:        formatTime(time.Now().UTC()),
		Summary:          strings.TrimSpace(input.Summary),
		FirstKeptEntryID: strings.TrimSpace(input.FirstKeptEntryID),
		TokensBefore:     input.TokensBefore,
		TokensAfter:      input.TokensAfter,
		Details:          &input.Details,
	}

	return s.appendEntry(ctx, agentID, sessionID, entry, strings.TrimSpace(input.ExpectedLeafID))
}

// LoadSession 加载完整 Session，并重建当前 Active Branch。
//
// 加载前会保守修复最后一条 crash 残行；中间损坏绝不自动跳过。
func (s *Store) LoadSession(
	ctx context.Context,
	agentID string,
	sessionID string,
) (Document, error) {
	path, unlock, err := s.lockExistingSession(agentID, sessionID)
	if err != nil {
		return Document{}, err
	}
	defer unlock()

	document, _, repair, err := s.documentForReadLocked(ctx, path, sessionID)
	if err != nil {
		return Document{}, err
	}
	document = cloneDocument(document)
	document.Repair = repair
	return document, nil
}

// LoadContextSession 返回只读的当前分支视图，供一次模型请求构建 Context。
//
// Entry 及其嵌套 payload 均属于进程内缓存，调用方不得修改。Append 只会添加新的
// Entry，不会改写既有 Entry；这里截断 slice capacity，避免调用方 append 到缓存
// 的底层数组。对外需要可修改的完整历史时仍使用 LoadSession 的深拷贝。
func (s *Store) LoadContextSession(
	ctx context.Context,
	agentID string,
	sessionID string,
) (Document, error) {
	path, unlock, err := s.lockExistingSession(agentID, sessionID)
	if err != nil {
		return Document{}, err
	}
	defer unlock()
	info, err := os.Stat(path)
	if err != nil {
		return Document{}, err
	}
	if info.Size() > defaultDocumentCacheBytes {
		return s.largeContextSessionLocked(ctx, path, sessionID)
	}
	_, cached := s.cache.readOnly(path, info)
	document, _, _, err := s.documentForReadLocked(ctx, path, sessionID)
	if err != nil {
		return Document{}, err
	}
	branch := document.ActiveBranch
	stats := ReadStats{CacheHit: cached}
	if !cached {
		stats = ReadStats{BytesRead: info.Size()}
	}
	return Document{
		Header:        document.Header,
		ActiveBranch:  branch[:len(branch):len(branch)],
		LeafID:        document.LeafID,
		ContextWindow: document.ContextWindow,
		ReadStats:     stats,
	}, nil
}

// LoadMessagePage 从 Active Branch 的 Message 索引读取一页 Wire Entry。
// 缓存命中时工作量与页大小相关，不需要克隆或筛选完整 Document。
func (s *Store) LoadMessagePage(
	ctx context.Context,
	agentID string,
	sessionID string,
	beforeEntryID string,
	limit int,
) (MessageEntryPage, error) {
	path, unlock, err := s.lockExistingSession(agentID, sessionID)
	if err != nil {
		return MessageEntryPage{}, err
	}
	defer unlock()
	info, err := os.Stat(path)
	if err != nil {
		return MessageEntryPage{}, err
	}
	if info.Size() > defaultDocumentCacheBytes {
		return s.largeMessagePageLocked(ctx, path, sessionID, beforeEntryID, limit)
	}

	document, info, _, err := s.documentForReadLocked(ctx, path, sessionID)
	if err != nil {
		return MessageEntryPage{}, err
	}
	beforeEntryID = strings.TrimSpace(beforeEntryID)
	if page, cached, pageErr := s.cache.messagePage(path, info, beforeEntryID, limit); cached {
		return page, pageErr
	}
	indexes, positions := indexMessages(document.ActiveBranch)
	return messageEntryPageFromIndex(document.ActiveBranch, indexes, positions, beforeEntryID, limit)
}

// lockExistingSession 为读取/修复取得同一 Session 的锁并检查真实文件。
// 成功后由调用方 defer unlock；校验失败在此归还锁，不能让不存在或不安全的路径泄漏锁项。
func (s *Store) lockExistingSession(agentID, sessionID string) (string, func(), error) {
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return "", nil, err
	}
	unlock := s.locks.lock(path)
	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		unlock()
		return "", nil, err
	}
	return path, unlock, nil
}

func (s *Store) documentForReadLocked(
	ctx context.Context,
	path string,
	sessionID string,
) (Document, os.FileInfo, RepairResult, error) {
	repair, err := repairTailLocked(ctx, path)
	if err != nil {
		return Document{}, nil, RepairResult{}, err
	}
	if repair.Repaired {
		s.cache.invalidate(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Document{}, nil, RepairResult{}, fmt.Errorf("读取 Session Transcript 状态失败: %w", err)
	}
	if document, ok := s.cache.readOnly(path, info); ok {
		return document, info, repair, nil
	}
	document, err := loadLocked(ctx, path, sessionID)
	if err != nil {
		return Document{}, nil, RepairResult{}, err
	}
	s.cache.putOwned(path, document, info)
	return document, info, repair, nil
}

// ListSessionRefs 返回指定 Agent 下存在有效 session.jsonl 的物理 Session 引用。
//
// 本方法故意不读取会话标题或 SQLite 元数据。Session 可变配置属于 sessions Store；Transcript
// 只负责枚举自己的 JSONL 数据，用于 Agent 删除保护、模型历史引用扫描等低频路径。
func (s *Store) ListSessionRefs(ctx context.Context, agentID string) ([]SessionRef, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if err := validateIdentifier(agentID); err != nil {
		return nil, fmt.Errorf("Agent ID 无效: %w", err)
	}

	directory, err := s.agentSessionsDirectory(agentID)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SessionRef{}, nil
		}
		return nil, fmt.Errorf("读取 Agent Session 目录失败: %w", err)
	}

	result := make([]SessionRef, 0, len(entries))
	for _, entry := range entries {
		if err := validateContext(ctx); err != nil {
			return nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Session 目录不能是符号链接: %s", entry.Name())
		}
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()
		if err := validateIdentifier(sessionID); err != nil {
			return nil, fmt.Errorf("发现非法 Session 目录名 %q: %w", sessionID, err)
		}

		path, err := s.sessionPath(agentID, sessionID)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// 目录可能是一次未完成创建留下的控制面残留；它不是有效 Transcript。
				continue
			}
			return nil, fmt.Errorf("检查 Session %s Transcript 失败: %w", sessionID, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Session %s Transcript 不是安全普通文件", sessionID)
		}

		result = append(result, SessionRef{ID: sessionID, AgentID: agentID})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, nil
}

// SessionDirectory 返回受 Humbert 数据根约束的 Session 目录路径。
//
// sessions Store 使用该路径定位会话附属文件。返回路径不代表目录一定存在。
func (s *Store) SessionDirectory(agentID string, sessionID string) (string, error) {
	return s.sessionDirectory(agentID, sessionID)
}

// LoadHeader 只读取 JSONL 第一行，供缺失会话元数据时恢复不可变身份。
// 不扫描或解码长会话的历史消息。
func (s *Store) LoadHeader(ctx context.Context, agentID, sessionID string) (SessionHeader, error) {
	if err := validateContext(ctx); err != nil {
		return SessionHeader{}, err
	}
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return SessionHeader{}, err
	}
	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return SessionHeader{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return SessionHeader{}, err
	}
	defer file.Close()
	line, err := bufio.NewReader(io.LimitReader(file, maxEntryBytes+2)).ReadBytes('\n')
	if err != nil || len(line) == 0 || len(line) > maxEntryBytes+1 {
		return SessionHeader{}, &CorruptionError{Line: 1, Offset: 0, Reason: "Session Header 缺失或超出长度限制"}
	}
	var header SessionHeader
	if err := decodeStrictJSON(bytes.TrimSuffix(line, []byte{'\n'}), &header); err != nil {
		return SessionHeader{}, &CorruptionError{Line: 1, Offset: 0, Reason: "Session Header JSON 无法解析"}
	}
	if err := validateHeader(header, sessionID); err != nil {
		return SessionHeader{}, &CorruptionError{Line: 1, Offset: 0, Reason: err.Error()}
	}
	return header, nil
}

// SessionModifiedAt 返回 session.jsonl 的文件修改时间，用于 Session List 的 UpdatedAt
// 投影。UpdatedAt 不需要为了每条 Message 再重写 JSON 配置。
func (s *Store) SessionModifiedAt(ctx context.Context, agentID string, sessionID string) (time.Time, error) {
	if err := validateContext(ctx); err != nil {
		return time.Time{}, err
	}
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return time.Time{}, err
	}
	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, fmt.Errorf("读取 Session Transcript 修改时间失败: %w", err)
	}
	return info.ModTime().UTC(), nil
}

// DeleteSession 删除整个 Session 数据目录。
//
// Session 采用独立目录后，session.jsonl、附件及旧版遗留文件属于同一个
// 生命周期。删除 Session 时统一删除该目录，避免遗留孤儿文件。
func (s *Store) DeleteSession(ctx context.Context, agentID string, sessionID string) error {
	if err := validateContext(ctx); err != nil {
		return err
	}

	directory, err := s.sessionDirectory(agentID, sessionID)
	if err != nil {
		return err
	}
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return err
	}

	unlock := s.locks.lock(path)
	defer unlock()

	info, err := os.Lstat(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: session_id=%s", ErrSessionNotFound, sessionID)
		}
		return fmt.Errorf("读取 Session 目录状态失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("Session 目录不是安全真实目录")
	}
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("删除 Session 数据目录失败: %w", err)
	}
	s.cache.invalidate(path)
	s.locations.invalidate(path)
	return nil
}

// RepairSession 显式执行 Tail Repair。
func (s *Store) RepairSession(
	ctx context.Context,
	agentID string,
	sessionID string,
) (RepairResult, error) {
	path, unlock, err := s.lockExistingSession(agentID, sessionID)
	if err != nil {
		return RepairResult{}, err
	}

	defer unlock()

	result, err := repairTailLocked(ctx, path)
	if result.Repaired {
		s.cache.invalidate(path)
		s.locations.invalidate(path)
	}
	return result, err
}

// appendEntry 将新节点挂到当前 Leaf 后追加。
//
// 当前 Leaf 定义为文件中最后一次 append 的 Tree Entry。未来实现 Branch/Retry 时只需
// 增加允许调用方显式指定 parentId 的 API，底层 Tree Schema 不需要再升级。
func (s *Store) appendEntry(
	ctx context.Context,
	agentID string,
	sessionID string,
	entry Entry,
	expectedLeafID string,
) (Entry, error) {
	if err := validateContext(ctx); err != nil {
		return Entry{}, err
	}
	if err := validateIdentifier(agentID); err != nil {
		return Entry{}, fmt.Errorf("Agent ID 无效: %w", err)
	}
	if err := validateIdentifier(sessionID); err != nil {
		return Entry{}, fmt.Errorf("Session ID 无效: %w", err)
	}
	if err := validateEntryPayload(entry); err != nil {
		return Entry{}, err
	}

	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return Entry{}, err
	}

	unlock := s.locks.lock(path)
	defer unlock()

	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Entry{}, err
	}
	var document Document
	var location *locationIndex
	if info.Size() > defaultDocumentCacheBytes {
		// The full cache cannot retain this document; validate append from the
		// lightweight location index instead of reparsing all message bodies.
		document, location, err = s.largeMetadataLocked(ctx, path, sessionID)
		if err == nil {
			info, err = os.Stat(path)
		}
	} else {
		document, info, _, err = s.documentForReadLocked(ctx, path, sessionID)
	}
	if err != nil {
		return Entry{}, err
	}

	// Compaction 的摘要是在文件锁之外生成的。调用方提供 ExpectedLeafID 时，这里在真正
	// append 前执行 compare-and-append 检查；即使 firstKeptEntryId 仍属于同一 ActiveBranch，
	// 只要期间追加过新节点，也拒绝提交旧摘要。普通 Message append 不使用该约束。
	if expectedLeafID != "" && document.LeafID != expectedLeafID {
		return Entry{}, fmt.Errorf(
			"%w: expected_leaf_id=%s actual_leaf_id=%s",
			ErrCompactionStale,
			expectedLeafID,
			document.LeafID,
		)
	}

	if entry.Type == EntryCompaction {
		if err := validateCompactionAgainstDocument(entry, document); err != nil {
			return Entry{}, err
		}
	}

	if document.LeafID != "" {
		parent := document.LeafID
		entry.ParentID = &parent
	} else {
		entry.ParentID = nil
	}

	line, err := encodeJSONLine(entry)
	if err != nil {
		return Entry{}, fmt.Errorf("编码 Session Entry 失败: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Entry{}, fmt.Errorf("打开 Session Transcript 追加失败: %w", err)
	}

	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	if err := writeFull(file, line); err != nil {
		return Entry{}, fmt.Errorf("追加 Session Entry 失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		return Entry{}, fmt.Errorf("同步 Session Entry 失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return Entry{}, fmt.Errorf("关闭 Session Transcript 失败: %w", err)
	}
	closed = true

	// Cache and location metadata must own their payloads. The caller still
	// holds the input message and may reuse or mutate its slices after append.
	storedEntry := cloneEntry(entry)
	if updatedInfo, statErr := os.Stat(path); statErr == nil {
		s.advanceLocationLocked(path, info, updatedInfo, storedEntry, int64(len(line)), location)
		if !s.cache.advance(path, info, updatedInfo, storedEntry) {
			s.cache.invalidate(path)
		}
	} else {
		s.cache.invalidate(path)
	}
	return entry, nil
}

// validateCompactionAgainstDocument 在真正 append 前验证压缩切点仍然属于当前分支。
//
// 摘要生成是外部模型 IO，期间 Session 理论上可能因为 Retry/Fork 或其他控制面操作改变
// ActiveBranch。这里不接受“FirstKeptEntryID 只存在于历史 Entries”的弱校验，必须确认它
// 位于当前 ActiveBranch，才能保证新 CompactionEntry 不引用被放弃分支。
func validateCompactionAgainstDocument(entry Entry, document Document) error {
	if entry.Type != EntryCompaction {
		return nil
	}

	firstKept := strings.TrimSpace(entry.FirstKeptEntryID)
	if firstKept == "" {
		return errors.New("Compaction firstKeptEntryId 不能为空")
	}

	found := false
	for _, branchEntry := range document.ActiveBranch {
		if branchEntry.ID == firstKept {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%w: firstKeptEntryId=%s 已不在当前 ActiveBranch", ErrCompactionStale, firstKept)
	}

	return nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Session Transcript 操作被取消: %w", err)
	}
	return nil
}

func (r *lockRegistry) lock(key string) func() {
	r.mu.Lock()
	entry := r.values[key]
	if entry == nil {
		entry = &lockEntry{}
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
