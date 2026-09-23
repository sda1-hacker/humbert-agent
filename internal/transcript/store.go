package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

const (
	sessionDirectoryName      = "sessions"
	sessionTranscriptFileName = "session.jsonl"

	// maxEntryBytes 是单条 JSONL Entry 的硬上限。
	//
	// Tool 自身还应执行更小的输出预算；这里是最后一道 Storage Boundary，防止损坏
	// 文件或异常 Tool Result 让加载 Session 时无限占用内存。
	maxEntryBytes = 8 * 1024 * 1024
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
// 同目录的 config.json 属于 sessions 包的控制面配置，TranscriptStore 不读写它。
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
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return Document{}, err
	}

	unlock := s.locks.lock(path)
	defer unlock()

	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return Document{}, err
	}

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
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return Document{}, err
	}
	unlock := s.locks.lock(path)
	defer unlock()
	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return Document{}, err
	}
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
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return MessageEntryPage{}, err
	}
	unlock := s.locks.lock(path)
	defer unlock()
	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return MessageEntryPage{}, err
	}
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
// 本方法故意不读取 title/config.json。Session 可变配置属于 sessions Store；Transcript
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
// sessions Store 使用该路径保存同目录 config.json。返回路径不代表目录一定存在。
func (s *Store) SessionDirectory(agentID string, sessionID string) (string, error) {
	return s.sessionDirectory(agentID, sessionID)
}

// SessionModifiedAt 返回 session.jsonl 的文件修改时间，用于 Session List 的 UpdatedAt
// 投影。UpdatedAt 不需要为了每条 Message 再重写 config.json。
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
// Session 采用独立目录后，config.json、session.jsonl 以及未来 payloads/ 都属于同一个
// 生命周期。删除 Session 时统一删除该目录，避免遗留孤儿控制面文件。
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
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return RepairResult{}, err
	}

	unlock := s.locks.lock(path)
	defer unlock()

	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return RepairResult{}, err
	}
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

func messageEntryPageFromIndex(
	activeBranch []Entry,
	messageIndexes []int,
	messagePositions map[string]int,
	beforeEntryID string,
	limit int,
) (MessageEntryPage, error) {
	end := len(messageIndexes)
	if beforeEntryID != "" {
		position, exists := messagePositions[beforeEntryID]
		if !exists {
			return MessageEntryPage{}, fmt.Errorf("%w: %s", ErrMessageCursorNotFound, beforeEntryID)
		}
		end = position
	}

	start := 0
	if limit > 0 && end > limit {
		start = end - limit
		// 保持 Assistant ToolCall -> ToolResult(s) -> 最终 Assistant 的事务边界。
		for start > 0 {
			current := activeBranch[messageIndexes[start]].Message
			if current == nil {
				break
			}
			if current.Role == RoleToolResult {
				start--
				continue
			}
			previous := activeBranch[messageIndexes[start-1]].Message
			if current.Role == RoleAssistant && previous != nil && previous.Role == RoleToolResult {
				start--
				continue
			}
			break
		}
	}

	entries := make([]Entry, 0, end-start)
	for _, branchIndex := range messageIndexes[start:end] {
		entries = append(entries, cloneEntry(activeBranch[branchIndex]))
	}
	nextBeforeID := ""
	if start > 0 && len(entries) > 0 {
		nextBeforeID = entries[0].ID
	}
	return MessageEntryPage{
		Entries: entries, StartIndex: start, HasMore: start > 0, NextBeforeID: nextBeforeID,
	}, nil
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

func (s *Store) sessionPath(agentID string, sessionID string) (string, error) {
	directory, err := s.sessionDirectory(agentID, sessionID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, sessionTranscriptFileName)
	if !pathWithinRoot(s.agentsRoot, path) {
		return "", ErrInvalidIdentifier
	}
	return path, nil
}

func (s *Store) agentSessionsDirectory(agentID string) (string, error) {
	if err := validateIdentifier(agentID); err != nil {
		return "", fmt.Errorf("Agent ID 无效: %w", err)
	}
	directory := filepath.Join(s.agentsRoot, agentID, sessionDirectoryName)
	if !pathWithinRoot(s.agentsRoot, directory) {
		return "", ErrInvalidIdentifier
	}
	return directory, nil
}

func (s *Store) sessionDirectory(agentID string, sessionID string) (string, error) {
	if err := validateIdentifier(sessionID); err != nil {
		return "", fmt.Errorf("Session ID 无效: %w", err)
	}
	root, err := s.agentSessionsDirectory(agentID)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, sessionID)
	if !pathWithinRoot(s.agentsRoot, directory) {
		return "", ErrInvalidIdentifier
	}
	return directory, nil
}

func (s *Store) ensureSessionDirectory(agentID string, sessionID string) error {
	agentDirectory := filepath.Join(s.agentsRoot, agentID)
	if !pathWithinRoot(s.agentsRoot, agentDirectory) {
		return ErrInvalidIdentifier
	}
	if err := ensureRealDirectory(agentDirectory); err != nil {
		return fmt.Errorf("准备 Agent Transcript 目录失败: %w", err)
	}

	root, err := s.agentSessionsDirectory(agentID)
	if err != nil {
		return err
	}
	if err := ensureRealDirectory(root); err != nil {
		return fmt.Errorf("准备 Agent Session Root 失败: %w", err)
	}

	directory, err := s.sessionDirectory(agentID, sessionID)
	if err != nil {
		return err
	}
	if err := ensureRealDirectory(directory); err != nil {
		return fmt.Errorf("准备 Session 数据目录失败: %w", err)
	}
	return nil
}

func (s *Store) validateExistingSessionPath(agentID string, sessionID string, path string) error {
	root, err := s.agentSessionsDirectory(agentID)
	if err != nil {
		return err
	}
	if err := validateRealDirectory(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("验证 Agent Session Root 失败: %w", err)
	}

	directory, err := s.sessionDirectory(agentID, sessionID)
	if err != nil {
		return err
	}
	if err := validateRealDirectory(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("验证 Session 数据目录失败: %w", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("读取 Session Transcript 状态失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Session Transcript 不能是符号链接")
	}
	if !info.Mode().IsRegular() {
		return errors.New("Session Transcript 不是普通文件")
	}
	return nil
}

func ensureRealDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return validateRealDirectory(path)
}

func validateRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("目录不能是符号链接: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("路径不是目录: %s", path)
	}
	return nil
}

func validateIdentifier(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return ErrInvalidIdentifier
	}
	if strings.ContainsRune(value, '\x00') || filepath.IsAbs(value) {
		return ErrInvalidIdentifier
	}
	if strings.ContainsAny(value, `/\\`) || filepath.Base(value) != value {
		return ErrInvalidIdentifier
	}
	return nil
}

func pathWithinRoot(root string, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateAgentMessage(message AgentMessage) error {
	if message.Timestamp <= 0 {
		return errors.New("AgentMessage timestamp 必须大于 0")
	}
	if len(message.Content) == 0 {
		return errors.New("AgentMessage content 不能为空")
	}

	for index, block := range message.Content {
		if err := validateContentBlock(block); err != nil {
			return fmt.Errorf("AgentMessage content[%d] 无效: %w", index, err)
		}
	}

	switch message.Role {
	case RoleUser:
		for _, block := range message.Content {
			if block.Type != ContentText && block.Type != ContentImage && block.Type != ContentFile {
				return errors.New("UserMessage 只允许 text/image/file ContentBlock")
			}
		}

	case RoleAssistant:
		if strings.TrimSpace(message.Model) == "" {
			return errors.New("AssistantMessage model 不能为空")
		}
		if strings.TrimSpace(message.Provider) == "" {
			return errors.New("AssistantMessage provider 不能为空")
		}
		if !validStopReason(message.StopReason) {
			return fmt.Errorf("AssistantMessage stopReason 无效: %q", message.StopReason)
		}

	case RoleToolResult:
		if strings.TrimSpace(message.ToolCallID) == "" {
			return errors.New("ToolResultMessage toolCallId 不能为空")
		}
		if strings.TrimSpace(message.ToolName) == "" {
			return errors.New("ToolResultMessage toolName 不能为空")
		}
		for _, block := range message.Content {
			if block.Type != ContentText {
				return errors.New("ToolResultMessage 当前只允许 text ContentBlock")
			}
		}

	default:
		return fmt.Errorf("AgentMessage role 不支持: %q", message.Role)
	}

	return nil
}

func validateContentBlock(block ContentBlock) error {
	switch block.Type {
	case ContentText:
		if block.Text == "" {
			return errors.New("text block 内容不能为空")
		}

	case ContentImage, ContentFile:
		if strings.TrimSpace(block.AttachmentID) == "" {
			return errors.New("attachmentId 不能为空")
		}
		if strings.ContainsAny(block.AttachmentID, `/\`) {
			return errors.New("attachmentId 非法")
		}
		if strings.TrimSpace(block.Name) == "" {
			return errors.New("attachment name 不能为空")
		}
		if strings.TrimSpace(block.MIMEType) == "" {
			return errors.New("attachment mimeType 不能为空")
		}
		if block.SizeBytes < 0 {
			return errors.New("attachment sizeBytes 非法")
		}
		if block.Type == ContentFile && strings.TrimSpace(block.ExtractedText) == "" {
			return errors.New("file attachment 缺少 extractedText")
		}
		if block.Type == ContentImage && block.ExtractedText != "" {
			return errors.New("image attachment 不允许 extractedText")
		}

	case ContentThinking:
		if block.Thinking == "" && !block.Redacted {
			return errors.New("thinking block 内容不能为空")
		}

	case ContentToolCall:
		if strings.TrimSpace(block.ID) == "" {
			return errors.New("toolCall id 不能为空")
		}
		if strings.TrimSpace(block.Name) == "" {
			return errors.New("toolCall name 不能为空")
		}
		if len(block.Arguments) == 0 || !json.Valid(block.Arguments) {
			return errors.New("toolCall arguments 必须是合法 JSON")
		}
		trimmed := bytes.TrimSpace(block.Arguments)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return errors.New("toolCall arguments 必须是 JSON Object")
		}

	default:
		return fmt.Errorf("ContentBlock type 不支持: %q", block.Type)
	}
	return nil
}

func validStopReason(reason StopReason) bool {
	switch reason {
	case StopReasonStop,
		StopReasonLength,
		StopReasonToolUse,
		StopReasonError,
		StopReasonAborted,
		StopReasonDeferred:
		return true
	default:
		return false
	}
}

func validateEntryPayload(entry Entry) error {
	if strings.TrimSpace(entry.ID) == "" {
		return errors.New("Session Entry id 不能为空")
	}
	if _, err := parseTime(entry.Timestamp); err != nil {
		return fmt.Errorf("Session Entry timestamp 无效: %w", err)
	}

	switch entry.Type {
	case EntryMessage:
		if entry.Message == nil {
			return errors.New("message Entry 缺少 message")
		}
		return validateAgentMessage(*entry.Message)

	case EntryModelChange:
		if strings.TrimSpace(entry.Provider) == "" || strings.TrimSpace(entry.ModelID) == "" {
			return errors.New("model_change provider/modelId 不能为空")
		}
		return nil

	case EntryThinkingLevelChange:
		if strings.TrimSpace(entry.ThinkingLevel) == "" {
			return errors.New("thinking_level_change thinkingLevel 不能为空")
		}
		return nil

	case EntryCompaction:
		if strings.TrimSpace(entry.Summary) == "" {
			return errors.New("compaction summary 不能为空")
		}
		if strings.TrimSpace(entry.FirstKeptEntryID) == "" {
			return errors.New("compaction firstKeptEntryId 不能为空")
		}
		if entry.TokensBefore <= 0 {
			return errors.New("compaction tokensBefore 必须大于 0")
		}
		if entry.TokensAfter < 0 {
			return errors.New("compaction tokensAfter 不能小于 0")
		}
		if entry.Details == nil || strings.TrimSpace(entry.Details.Reason) == "" {
			return errors.New("compaction details.reason 不能为空")
		}
		return nil

	case EntryBranchSummary,
		EntryCustom,
		EntryCustomMessage,
		EntryLabel:
		return nil

	default:
		return fmt.Errorf("未知 Session Entry Type: %q", entry.Type)
	}
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

func encodeJSONLine(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > maxEntryBytes {
		return nil, fmt.Errorf("单条 JSONL Entry 超过 %d bytes", maxEntryBytes)
	}
	return append(data, '\n'), nil
}

func repairTailLocked(ctx context.Context, path string) (RepairResult, error) {
	if err := validateContext(ctx); err != nil {
		return RepairResult{}, err
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return RepairResult{}, fmt.Errorf("打开 Transcript Repair 文件失败: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return RepairResult{}, fmt.Errorf("读取 Transcript 文件状态失败: %w", err)
	}
	if info.Size() == 0 {
		return RepairResult{}, &CorruptionError{Line: 1, Offset: 0, Reason: "缺少 Session Header"}
	}

	// 正常关闭的每条 JSONL 写入都以换行结束。绝大多数读取/追加无需为了确认“没有 crash
	// tail”先完整扫描一次；后续 loadLocked 仍会逐行严格校验全部 JSON 和 Tree 关系，因此
	// 这里的 O(1) 快路径不会掩盖中间损坏。只有末字节不是换行时才进入下面的修复扫描。
	lastByte := []byte{0}
	if _, err := file.ReadAt(lastByte, info.Size()-1); err != nil {
		return RepairResult{}, fmt.Errorf("读取 Transcript 尾字节失败: %w", err)
	}
	if lastByte[0] == '\n' {
		return RepairResult{}, nil
	}

	reader := bufio.NewReaderSize(file, 64*1024)
	var offset int64
	var lastGoodOffset int64
	lineNumber := 0

	for {
		if err := validateContext(ctx); err != nil {
			return RepairResult{}, err
		}

		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if len(line) == 0 && readErr != nil {
			return RepairResult{}, fmt.Errorf("读取 Transcript Repair 数据失败: %w", readErr)
		}

		lineNumber++
		hasNewline := line[len(line)-1] == '\n'
		payload := line
		if hasNewline {
			payload = line[:len(line)-1]
		}
		if len(payload) > maxEntryBytes {
			return RepairResult{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "单行 Entry 超过最大限制"}
		}

		if !json.Valid(payload) {
			if errors.Is(readErr, io.EOF) && !hasNewline && lastGoodOffset > 0 {
				truncated := info.Size() - lastGoodOffset
				if err := file.Truncate(lastGoodOffset); err != nil {
					return RepairResult{}, fmt.Errorf("截断损坏 Transcript Tail 失败: %w", err)
				}
				if err := file.Sync(); err != nil {
					return RepairResult{}, fmt.Errorf("同步 Transcript Tail Repair 失败: %w", err)
				}
				return RepairResult{Repaired: true, TruncatedBytes: truncated}, nil
			}
			reason := "存在非尾部无效 JSON"
			if lineNumber == 1 {
				reason = "Session Header 不完整"
			}
			return RepairResult{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: reason}
		}

		offset += int64(len(line))
		lastGoodOffset = offset

		if errors.Is(readErr, io.EOF) {
			if hasNewline {
				break
			}
			if _, err := file.Seek(0, io.SeekEnd); err != nil {
				return RepairResult{}, fmt.Errorf("定位 Transcript 尾部失败: %w", err)
			}
			if err := writeFull(file, []byte{'\n'}); err != nil {
				return RepairResult{}, fmt.Errorf("补写 Transcript 尾部换行失败: %w", err)
			}
			if err := file.Sync(); err != nil {
				return RepairResult{}, fmt.Errorf("同步 Transcript 尾部换行失败: %w", err)
			}
			return RepairResult{Repaired: true, AddedFinalNewline: true}, nil
		}
		if readErr != nil {
			return RepairResult{}, fmt.Errorf("读取 Transcript Repair 数据失败: %w", readErr)
		}
	}

	return RepairResult{}, nil
}

func loadLocked(ctx context.Context, path string, sessionID string) (Document, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Document{}, fmt.Errorf("%w: session_id=%s", ErrSessionNotFound, sessionID)
		}
		return Document{}, fmt.Errorf("打开 Session Transcript 失败: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64*1024)
	lineNumber := 0
	var offset int64

	var header SessionHeader
	entries := make([]Entry, 0, 64)
	entryByID := make(map[string]Entry)
	leafID := ""

	for {
		if err := validateContext(ctx); err != nil {
			return Document{}, err
		}

		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if len(line) == 0 && readErr != nil {
			return Document{}, fmt.Errorf("读取 Session Transcript 失败: %w", readErr)
		}

		lineNumber++
		if len(line) > maxEntryBytes+1 {
			return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "单行 Entry 超过最大限制"}
		}

		payload := bytes.TrimSuffix(line, []byte{'\n'})
		if lineNumber == 1 {
			if err := decodeStrictJSON(payload, &header); err != nil {
				return Document{}, &CorruptionError{Line: 1, Offset: 0, Reason: "Session Header JSON 无法解析"}
			}
			if err := validateHeader(header, sessionID); err != nil {
				return Document{}, &CorruptionError{Line: 1, Offset: 0, Reason: err.Error()}
			}
		} else {
			var entry Entry
			if err := decodeStrictJSON(payload, &entry); err != nil {
				return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "Session Entry JSON 无法解析"}
			}
			if err := validateEntryPayload(entry); err != nil {
				return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: err.Error()}
			}
			if _, exists := entryByID[entry.ID]; exists {
				return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "Session Entry id 重复"}
			}
			if entry.ParentID != nil {
				if _, exists := entryByID[*entry.ParentID]; !exists {
					return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "parentId 指向不存在或尚未出现的 Entry"}
				}
			}

			entryByID[entry.ID] = entry
			entries = append(entries, entry)
			leafID = entry.ID
		}

		offset += int64(len(line))
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return Document{}, fmt.Errorf("读取 Session Transcript 失败: %w", readErr)
		}
	}

	if lineNumber == 0 {
		return Document{}, &CorruptionError{Line: 1, Offset: 0, Reason: "缺少 Session Header"}
	}

	activeBranch, err := buildActiveBranch(entryByID, leafID)
	if err != nil {
		return Document{}, err
	}

	return Document{
		Header:        header,
		Entries:       entries,
		ActiveBranch:  activeBranch,
		LeafID:        leafID,
		ContextWindow: contextWindowIndex(activeBranch),
	}, nil
}

func contextWindowIndex(branch []Entry) ContextWindowIndex {
	result := ContextWindowIndex{Valid: true, LatestCompactionIndex: -1}
	positions := make(map[string]int, len(branch))
	for index, entry := range branch {
		positions[entry.ID] = index
		if entry.Type != EntryCompaction {
			continue
		}
		result.Generation++
		result.LatestCompactionIndex = index
		firstKeptIndex, found := positions[entry.FirstKeptEntryID]
		if !found || firstKeptIndex >= index {
			result.FirstKeptIndex = -1
		} else {
			result.FirstKeptIndex = firstKeptIndex
		}
	}
	return result
}

func validateHeader(header SessionHeader, expectedSessionID string) error {
	if header.Type != "session" {
		return errors.New("第一行不是 Session Header")
	}
	if header.Version != CurrentVersion {
		return fmt.Errorf("只支持 Session JSONL v%d，实际为 v%d", CurrentVersion, header.Version)
	}
	if header.ID != expectedSessionID {
		return errors.New("Session Header id 与文件名不一致")
	}
	if _, err := parseTime(header.Timestamp); err != nil {
		return errors.New("Session Header timestamp 无效")
	}
	return nil
}

func buildActiveBranch(entryByID map[string]Entry, leafID string) ([]Entry, error) {
	if leafID == "" {
		return []Entry{}, nil
	}

	reversed := make([]Entry, 0, len(entryByID))
	visited := make(map[string]struct{}, len(entryByID))
	currentID := leafID

	for currentID != "" {
		if _, exists := visited[currentID]; exists {
			return nil, fmt.Errorf("%w: Session Tree 存在 parent cycle", ErrCorrupted)
		}
		visited[currentID] = struct{}{}

		entry, exists := entryByID[currentID]
		if !exists {
			return nil, fmt.Errorf("%w: Active Branch Entry %s 不存在", ErrCorrupted, currentID)
		}
		reversed = append(reversed, entry)

		if entry.ParentID == nil {
			break
		}
		currentID = *entry.ParentID
	}

	result := make([]Entry, len(reversed))
	for index := range reversed {
		result[len(reversed)-1-index] = reversed[index]
	}
	return result, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return err
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON 包含多余值")
		}
		return fmt.Errorf("JSON 尾部无效: %w", err)
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
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
