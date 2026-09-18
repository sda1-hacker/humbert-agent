package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// TranscriptRepository 是 Memory Manager 读取当前 Session Tree 的最小边界。
type TranscriptRepository interface {
	LoadTranscript(ctx context.Context, sessionID string) (transcript.Document, error)
}

// TokenEstimator 是 Memory 自动刷新阈值使用的最小 Token 估算边界。
//
// 它与 ContextEngine 的 Estimator 通过结构化方法签名兼容，但 Memory 不反向依赖
// contextengine package，避免两个领域形成循环依赖。
type TokenEstimator interface {
	EstimateText(value string) int
}

// Manager 负责当前 Session 的 Key Facts + Timeline 派生状态。
//
// Memory 不是事实源：ContextFacts 读取失败会记录 Warn 并降级为空，不能阻塞正常对话；
// Refresh 遇到损坏 sidecar 时则基于当前 Transcript 重建并覆盖。模型调用不绑定 Tool，避免
// Memory 维护产生任何副作用。
type Manager struct {
	config config.ContextConfig

	store *Store

	transcripts TranscriptRepository

	estimator TokenEstimator

	logger *logging.Logger

	// refreshLocks 只串行化同一 Session 的“读取 cursor -> 调模型 -> Save”完整刷新周期。
	// Store 自身的锁保证单次文件读写原子，但不足以保护两个并发 Refresh 都从同一个旧
	// cursor 生成摘要；Manager 层锁正好覆盖这一业务事务，不影响不同 Session 并行更新。
	refreshLocksMu sync.Mutex
	refreshLocks   map[string]chan struct{}
}

// NewManager 创建 Session Memory Manager。
func NewManager(
	cfg config.ContextConfig,
	store *Store,
	transcripts TranscriptRepository,
	estimator TokenEstimator,
	logger *logging.Logger,
) (*Manager, error) {
	if store == nil {
		return nil, errors.New("Memory Store 不能为空")
	}
	if transcripts == nil {
		return nil, errors.New("Memory TranscriptRepository 不能为空")
	}
	if estimator == nil {
		return nil, errors.New("Memory TokenEstimator 不能为空")
	}
	if logger == nil {
		return nil, errors.New("Memory Logger 不能为空")
	}
	return &Manager{
		config:       cfg,
		store:        store,
		transcripts:  transcripts,
		estimator:    estimator,
		logger:       logger,
		refreshLocks: make(map[string]chan struct{}),
	}, nil
}

// ContextFacts 返回注入主模型参考上下文的“重要事实”正文。
//
// Memory 是可重建派生状态，因此文件损坏、旧版本等错误不能让用户无法继续聊天。这里会
// 统一记录不含文件正文/Secret 的 Warn 并返回空事实；后续 Refresh 会覆盖重建。
func (m *Manager) ContextFacts(
	ctx context.Context,
	sessionID string,
	activeBranch []transcript.Entry,
) (string, error) {
	document, exists, err := m.store.Load(ctx, sessionID)
	if err != nil {
		m.logger.Warn(
			ctx,
			"Session Memory 不可读取，当前模型上下文将忽略 Memory",
			"operation", "memory.context_facts",
			"session_id", sessionID,
			"error", err,
		)
		return "", nil
	}
	if !exists {
		return "", nil
	}

	// Memory 只能注入它所覆盖 lineage 仍是当前 ActiveBranch 前缀的事实。Retry/Fork 后，
	// memory.json 可能尚未来得及重建；这时宁可暂时不注入 Memory，也不能把已放弃分支的
	// 事实污染下一次模型请求。旧 cursor 只要仍是当前分支的合法前缀，就可以安全使用：
	// cursor 之后的新消息会以 Recent Context 原样提供给模型。
	if _, valid := cursorMatches(document.Cursor, activeBranch); !valid {
		m.logger.Debug(
			ctx,
			"Session Memory Cursor 已不属于当前 ActiveBranch，本次 Context 忽略旧 Memory",
			"operation", "memory.context_facts.stale_cursor",
			"session_id", sessionID,
		)
		return "", nil
	}

	facts, _, err := splitSummary(document.Summary)
	if err != nil {
		m.logger.Warn(
			ctx,
			"Session Memory 格式无效，当前模型上下文将忽略 Memory",
			"operation", "memory.context_facts",
			"session_id", sessionID,
			"error", err,
		)
		return "", nil
	}
	if strings.TrimSpace(facts) == "- 暂无" {
		return "", nil
	}
	result := strings.TrimSpace(facts)
	if document.Sources.EntryCount > 0 {
		result += fmt.Sprintf("\n\n来源范围：%s .. %s（%d 条原始记录）。如需核对细节，可使用 session_history（先 search、再 read）。",
			document.Sources.FirstEntryID, document.Sources.LastEntryID, document.Sources.EntryCount)
	}
	return result, nil
}

// Refresh 根据当前 ActiveBranch 更新 Session Memory。
//
// force=false 时仅在“新增 User Turn >= 配置阈值”或“新增历史 Token >= 配置阈值”时更新；
// force=true 用于“压缩并更新”和压缩后的维护。若旧 cursor 不再属于当前分支，则自动从
// 当前分支重建，不把旧 Branch 的事实带进新 Memory。
func (m *Manager) Refresh(
	ctx context.Context,
	sessionID string,
	model einomodel.BaseChatModel,
	force bool,
) (RefreshResult, error) {
	if ctx == nil {
		return RefreshResult{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return RefreshResult{}, fmt.Errorf("刷新 Session Memory 被取消: %w", err)
	}
	if strings.TrimSpace(sessionID) == "" {
		return RefreshResult{}, errors.New("Session ID 不能为空")
	}
	if model == nil {
		return RefreshResult{}, errors.New("Memory Model 不能为空")
	}

	// 同一 Session 的 Refresh 必须覆盖整个外部模型调用周期串行化。否则两个调用都可能
	// 读取相同旧 cursor，后完成者会覆盖先完成者的新摘要。锁不创建 goroutine，等待过程
	// 仍受调用方 ctx 控制；拿到锁后再次检查 ctx，避免已取消请求继续执行磁盘/网络 IO。
	refreshLock := m.refreshLockFor(sessionID)
	select {
	case <-ctx.Done():
		return RefreshResult{}, fmt.Errorf("等待 Session Memory 刷新锁被取消: %w", ctx.Err())
	case <-refreshLock:
	}
	defer func() { refreshLock <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return RefreshResult{}, fmt.Errorf("刷新 Session Memory 被取消: %w", err)
	}

	startedAt := time.Now()
	document, err := m.transcripts.LoadTranscript(ctx, sessionID)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("读取 Session Transcript 失败: %w", err)
	}
	if len(document.ActiveBranch) == 0 || strings.TrimSpace(document.LeafID) == "" {
		return RefreshResult{}, nil
	}

	// 先尝试读取现有 sidecar；损坏时将其视为可重建的派生状态。Manager 已持有当前
	// Session 的刷新事务锁，因此后续模型调用与 Save 不会被另一刷新周期覆盖。
	current, exists, loadErr := m.store.Load(ctx, sessionID)
	if loadErr != nil {
		m.logger.Warn(
			ctx,
			"检测到不可用 Session Memory，将从当前 ActiveBranch 重建",
			"operation", "memory.refresh.rebuild_corrupt",
			"session_id", sessionID,
			"error", loadErr,
		)
		exists = false
		current = Document{}
	}

	result, next, shouldSave, err := m.prepareAndGenerate(ctx, document, current, exists, model, force)
	if err != nil {
		return RefreshResult{}, err
	}
	if !shouldSave {
		return result, nil
	}
	if err := m.store.Save(ctx, sessionID, next); err != nil {
		return RefreshResult{}, fmt.Errorf("保存 Session Memory 失败: %w", err)
	}

	m.logger.Info(
		ctx,
		"Session Memory 已更新",
		"operation", "memory.refresh",
		"session_id", sessionID,
		"rebuilt", result.Rebuilt,
		"pending_user_turns", result.PendingUserTurns,
		"pending_tokens", result.PendingTokens,
		logging.Duration(startedAt),
	)
	return result, nil
}

func (m *Manager) prepareAndGenerate(
	ctx context.Context,
	transcriptDocument transcript.Document,
	current Document,
	exists bool,
	model einomodel.BaseChatModel,
	force bool,
) (RefreshResult, Document, bool, error) {
	branch := transcriptDocument.ActiveBranch
	startIndex := 0
	rebuilt := !exists
	previousSummary := ""
	previousArtifacts := Artifacts{}

	if exists {
		if coveredIndex, valid := cursorMatches(current.Cursor, branch); valid {
			startIndex = coveredIndex + 1
			previousSummary = current.Summary
			previousArtifacts = current.Artifacts
			rebuilt = false
		} else {
			rebuilt = true
		}
	}

	segment := branch[startIndex:]
	if rebuilt {
		segment = rebuildSegment(branch)
	}

	pendingTurns := countUserTurns(segment)
	serialized, artifacts := serializeSegment(segment, m.config.SerializerMaxChars)
	pendingTokens := m.estimator.EstimateText(serialized)

	result := RefreshResult{
		Rebuilt:          rebuilt,
		CoveredLeafID:    transcriptDocument.LeafID,
		PendingUserTurns: pendingTurns,
		PendingTokens:    pendingTokens,
	}
	if !force && pendingTurns < m.config.MemoryTurnInterval && pendingTokens < m.config.MemoryTokenInterval {
		return result, current, false, nil
	}
	if strings.TrimSpace(serialized) == "" {
		// 没有新的语义消息时不调用模型。force 常在只有 control entry 的情况下发生；旧
		// Memory 已存在时可以仅推进 cursor，使后续增量判断仍然准确。
		if exists && !rebuilt {
			next := current
			next.Cursor = Cursor{CoveredLeafID: transcriptDocument.LeafID, LineageHash: lineageHash(branch)}
			next.UpdatedAt = time.Now().UTC()
			result.Updated = true
			return result, next, true, nil
		}
		return result, current, false, nil
	}

	var prompt strings.Builder
	if strings.TrimSpace(previousSummary) != "" && !rebuilt {
		prompt.WriteString("[已有 Session Memory]\n")
		prompt.WriteString(truncate(previousSummary, m.config.SerializerMaxChars*2))
		prompt.WriteString("\n\n[新增会话片段]\n")
	} else {
		prompt.WriteString("[当前 Session 会话片段]\n")
	}
	prompt.WriteString(serialized)

	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(m.config.OperationTimeoutMS)*time.Millisecond)
	defer cancel()
	promptText := truncateMiddle(prompt.String(), m.config.SerializerMaxChars*8)
	response, err := model.Generate(operationCtx, []*schema.Message{
		schema.SystemMessage(memorySystemPrompt),
		schema.UserMessage(promptText),
	})
	if err != nil {
		return RefreshResult{}, Document{}, false, fmt.Errorf("调用模型更新 Session Memory 失败: %w", err)
	}
	if response == nil {
		return RefreshResult{}, Document{}, false, errors.New("Memory 模型返回空 Message")
	}
	summary, err := normalizeSummary(messageText(response))
	if err != nil {
		return RefreshResult{}, Document{}, false, fmt.Errorf("Memory 模型返回格式无效: %w", err)
	}

	result.Updated = true
	next := Document{
		Version:   CurrentVersion,
		SessionID: transcriptDocument.Header.ID,
		Cursor: Cursor{
			CoveredLeafID: transcriptDocument.LeafID,
			LineageHash:   lineageHash(branch),
		},
		Summary:   summary,
		Artifacts: mergeArtifacts(previousArtifacts, artifacts, rebuilt),
		Sources:   memorySourceRange(branch),
		UpdatedAt: time.Now().UTC(),
	}
	return result, next, true, nil
}

func memorySourceRange(branch []transcript.Entry) SourceRange {
	result := SourceRange{}
	for _, entry := range branch {
		if entry.Type != transcript.EntryMessage || entry.Message == nil {
			continue
		}
		if result.FirstEntryID == "" {
			result.FirstEntryID = entry.ID
		}
		result.LastEntryID = entry.ID
		result.EntryCount++
	}
	return result
}

// rebuildSegment 为丢失/失效 memory.json 构造受控的重建输入。
//
// 若分支从未压缩，Memory 可以读取完整 ActiveBranch；若存在 Compaction，则使用“最新
// checkpoint + firstKept 之后的 recent raw history + checkpoint 后新消息”。由于
// CompactionEntry 在物理 Tree 中位于 retained history 之后，必须跳过它原来的物理位置，
// 否则同一个 checkpoint 会在重建 prompt 中出现两次。
func rebuildSegment(branch []transcript.Entry) []transcript.Entry {
	index := latestCompactionIndex(branch)
	if index < 0 {
		return append([]transcript.Entry(nil), branch...)
	}

	checkpoint := branch[index]
	firstKept := findEntryIndex(branch, checkpoint.FirstKeptEntryID)
	if firstKept < 0 || firstKept >= index {
		// 非法 checkpoint 不应该由正常 TranscriptStore 产生；Memory 是派生状态，
		// 此处宁可回退完整分支，也不能悄悄丢失可能仍有价值的会话信息。
		return append([]transcript.Entry(nil), branch...)
	}

	result := make([]transcript.Entry, 0, len(branch)-firstKept)
	result = append(result, checkpoint)
	for i := firstKept; i < len(branch); i++ {
		if i == index {
			continue
		}
		result = append(result, branch[i])
	}
	return result
}

// refreshLockFor 返回一个进程内、按 Session 隔离的 Memory 刷新事务锁。
func (m *Manager) refreshLockFor(sessionID string) chan struct{} {
	m.refreshLocksMu.Lock()
	defer m.refreshLocksMu.Unlock()
	if lock, ok := m.refreshLocks[sessionID]; ok {
		return lock
	}
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	m.refreshLocks[sessionID] = lock
	return lock
}

func serializeSegment(entries []transcript.Entry, maxChars int) (string, Artifacts) {
	if maxChars < 256 {
		maxChars = 256
	}
	var builder strings.Builder
	artifacts := Artifacts{}
	for _, entry := range entries {
		if entry.Type == transcript.EntryCompaction && strings.TrimSpace(entry.Summary) != "" {
			builder.WriteString("\n[Context checkpoint]\n")
			builder.WriteString(truncate(entry.Summary, maxChars*2))
			builder.WriteByte('\n')

			// memory.json 可能被删除或损坏后重建。CompactionDetails 是从原始 ToolCall
			// 参数确定性提取出的产物信息，因此重建时必须一并吸收，不能只依赖 checkpoint
			// 自然语言是否碰巧提到了某个文件。
			if entry.Details != nil {
				artifacts.ReadFiles = append(artifacts.ReadFiles, entry.Details.ReadFiles...)
				artifacts.ModifiedFiles = append(artifacts.ModifiedFiles, entry.Details.ModifiedFiles...)
			}
			continue
		}
		if entry.Message == nil {
			continue
		}
		message := entry.Message
		switch message.Role {
		case transcript.RoleUser:
			builder.WriteString("\n[User]\n")
			builder.WriteString(truncate(visibleText(message.Content), maxChars))
			for _, block := range message.Content {
				switch block.Type {
				case transcript.ContentImage:
					builder.WriteString("\n[Image attachment: ")
					builder.WriteString(memoryAttachmentLabel(block))
					builder.WriteString("]")
				case transcript.ContentFile:
					builder.WriteString("\n[File attachment: ")
					builder.WriteString(memoryAttachmentLabel(block))
					builder.WriteString("]\n")
					builder.WriteString(truncate(block.ExtractedText, maxChars))
				}
			}
			builder.WriteByte('\n')
		case transcript.RoleAssistant:
			text := visibleText(message.Content)
			if strings.TrimSpace(text) != "" {
				builder.WriteString("\n[Assistant outcome]\n")
				builder.WriteString(truncate(text, maxChars))
				builder.WriteByte('\n')
			}
			for _, block := range message.Content {
				if block.Type != transcript.ContentToolCall {
					continue
				}
				builder.WriteString("\n[Tool call]\n")
				builder.WriteString(block.Name)
				builder.WriteString("(")
				builder.WriteString(compactArguments(block.Arguments, min(maxChars, 1024)))
				builder.WriteString(")\n")
				collectArtifact(&artifacts, block.Name, block.Arguments)
			}
		case transcript.RoleToolResult:
			// Memory 不保存成功 ToolResult 正文。失败结果只保留短错误语义，便于记住真正
			// 的阻塞，但仍不复制可能包含文件正文或 Secret 的完整输出。
			if message.IsError {
				builder.WriteString("\n[Tool error: ")
				builder.WriteString(message.ToolName)
				builder.WriteString("]\n")
				builder.WriteString(truncate(visibleText(message.Content), min(maxChars, 512)))
				builder.WriteByte('\n')
			}
		}
	}
	return truncateMiddle(strings.TrimSpace(builder.String()), maxChars*8), normalizeArtifacts(artifacts)
}

func memoryAttachmentLabel(block transcript.ContentBlock) string {
	name := strings.TrimSpace(block.Name)
	if name == "" {
		name = "attachment"
	}
	if mimeType := strings.TrimSpace(block.MIMEType); mimeType != "" {
		return name + "; MIME: " + mimeType
	}
	return name
}

func truncateMiddle(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	marker := "\n\n...[middle history omitted for memory budget]...\n\n"
	markerRunes := []rune(marker)
	if maxRunes <= len(markerRunes)+2 {
		return truncate(value, maxRunes)
	}
	runes := []rune(value)
	available := maxRunes - len(markerRunes)
	prefix := available / 3
	suffix := available - prefix
	return string(runes[:prefix]) + marker + string(runes[len(runes)-suffix:])
}

func countUserTurns(entries []transcript.Entry) int {
	count := 0
	for _, entry := range entries {
		if entry.Message != nil && entry.Message.Role == transcript.RoleUser {
			count++
		}
	}
	return count
}

func latestCompactionIndex(branch []transcript.Entry) int {
	for index := len(branch) - 1; index >= 0; index-- {
		if branch[index].Type == transcript.EntryCompaction && strings.TrimSpace(branch[index].FirstKeptEntryID) != "" {
			return index
		}
	}
	return -1
}

func findEntryIndex(branch []transcript.Entry, id string) int {
	for index := range branch {
		if branch[index].ID == id {
			return index
		}
	}
	return -1
}

func visibleText(blocks []transcript.ContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type == transcript.ContentText {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

func messageText(message *schema.Message) string {
	if message == nil {
		return ""
	}
	if strings.TrimSpace(message.Content) != "" {
		return message.Content
	}
	var builder strings.Builder
	for _, part := range message.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func compactArguments(arguments json.RawMessage, maxChars int) string {
	var raw any
	if err := json.Unmarshal(arguments, &raw); err != nil {
		return "{}"
	}
	sanitized := sanitizeMemoryArgument(raw, "")
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return "{}"
	}
	return truncate(string(encoded), maxChars)
}

// sanitizeMemoryArgument 递归清理 Tool Arguments 中不应进入 Session Memory 的字段。
//
// 参数来自 ToolCall，未来接入 MCP 后等同于外部输入，不能假设 key 大小写或嵌套层级稳定。
// 对正文类字段和常见 Credential 字段统一替换为固定标记；保留 path/query 等低敏感结构化
// 参数，使 Memory 仍能理解“做了什么”，但不会把文件正文、Token 或 Cookie 长期写入 sidecar。
func sanitizeMemoryArgument(value any, key string) any {
	if memoryArgumentShouldOmit(key) {
		return "[omitted]"
	}

	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			result[childKey] = sanitizeMemoryArgument(childValue, childKey)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, childValue := range typed {
			result[index] = sanitizeMemoryArgument(childValue, "")
		}
		return result
	default:
		return value
	}
}

func memoryArgumentShouldOmit(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(normalized)
	switch normalized {
	case "content", "old_text", "new_text", "password", "passwd", "token", "access_token",
		"refresh_token", "api_key", "apikey", "authorization", "cookie", "set_cookie", "secret",
		"client_secret", "credential", "credentials":
		return true
	default:
		return false
	}
}

func collectArtifact(artifacts *Artifacts, toolName string, arguments json.RawMessage) {
	if artifacts == nil {
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(arguments, &raw); err != nil {
		return
	}
	path, _ := raw["path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file":
		artifacts.ReadFiles = append(artifacts.ReadFiles, path)
	case "write_file", "edit_file":
		artifacts.ModifiedFiles = append(artifacts.ModifiedFiles, path)
	}
}

func mergeArtifacts(previous Artifacts, current Artifacts, rebuilt bool) Artifacts {
	if rebuilt {
		return normalizeArtifacts(current)
	}
	return normalizeArtifacts(Artifacts{
		ReadFiles:     append(append([]string(nil), previous.ReadFiles...), current.ReadFiles...),
		ModifiedFiles: append(append([]string(nil), previous.ModifiedFiles...), current.ModifiedFiles...),
	})
}

func normalizeArtifacts(value Artifacts) Artifacts {
	value.ReadFiles = uniqueSorted(value.ReadFiles)
	value.ModifiedFiles = uniqueSorted(value.ModifiedFiles)
	return value
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func truncate(value string, maxRunes int) string {
	if maxRunes <= 0 || value == "" || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes]) + "\n...[truncated for memory]"
}
