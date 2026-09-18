package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const sessionHistoryToolName = "session_history"

const (
	sessionHistoryActionSearch = "search"
	sessionHistoryActionRead   = "read"
)

// HistoryRepository 是会话历史与上下文资源工具读取 Session Domain 的最小边界。
type HistoryRepository interface {
	LoadTranscript(ctx context.Context, sessionID string) (transcript.Document, error)
}

// SessionHistoryFactory 把“定位旧历史”和“读取旧历史”收敛为一个内部工具。
//
// 两个动作共享同一份完整会话记录，但职责仍然分开：search 只返回少量定位信息，read
// 才按 entry_id 读取原文。这样既减少一个 Tool Schema，又不会因为一次搜索把大量旧历史
// 重新塞回模型工作窗口。
type SessionHistoryFactory struct {
	repository HistoryRepository
}

func NewSessionHistoryFactory(repository HistoryRepository) (*SessionHistoryFactory, error) {
	if repository == nil {
		return nil, errors.New("Session History Repository 不能为空")
	}
	return &SessionHistoryFactory{repository: repository}, nil
}

func (f *SessionHistoryFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: sessionHistoryToolName, Risk: humberttools.RiskRead, Internal: true}
}

// SessionHistoryInput 使用 action 区分搜索和读取，避免为两个高度相关的只读动作暴露两份
// Tool Definition。不同 action 只读取各自需要的字段，其余字段会被忽略。
type SessionHistoryInput struct {
	Action string `json:"action" jsonschema:"description=操作类型：search 用关键词定位旧历史；read 用 entry_id 读取原始历史。"`

	Query      string `json:"query,omitempty" jsonschema:"description=action=search 时要查找的关键词或短语。"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=action=search 时最多返回多少条匹配，默认 8，最大 20。"`

	EntryID string `json:"entry_id,omitempty" jsonschema:"description=action=read 时要读取的 entry_id，可来自 search 结果或上下文检查点来源。"`
	Before  int    `json:"before,omitempty" jsonschema:"description=action=read 时同时读取目标之前多少条当前分支记录，默认 1，最大 5。"`
	After   int    `json:"after,omitempty" jsonschema:"description=action=read 时同时读取目标之后多少条当前分支记录，默认 2，最大 8。"`
	Offset  int    `json:"offset,omitempty" jsonschema:"description=action=read 时目标 entry 从第几个 Unicode 字符开始读取，默认 0。"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=action=read 时目标 entry 最多读取多少字符，默认 12000，最大 20000。"`
}

type SessionHistoryMatch struct {
	EntryID   string `json:"entry_id"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp"`
	Snippet   string `json:"snippet"`
}

type SessionHistoryEntry struct {
	EntryID    string `json:"entry_id"`
	Type       string `json:"type"`
	Role       string `json:"role,omitempty"`
	Timestamp  string `json:"timestamp"`
	Offset     int    `json:"offset,omitempty"`
	End        int    `json:"end,omitempty"`
	TotalChars int    `json:"total_chars,omitempty"`
	More       bool   `json:"more,omitempty"`
	Content    string `json:"content"`
}

type SessionHistoryOutput struct {
	Action  string                `json:"action"`
	Matches []SessionHistoryMatch `json:"matches,omitempty"`
	Entries []SessionHistoryEntry `json:"entries,omitempty"`
}

func (f *SessionHistoryFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(sessionHistoryToolName,
		"按需恢复当前会话较早的原始历史。action=search 先用关键词返回少量 entry_id 和摘要；action=read 再读取指定 entry_id 及少量相邻上下文。完整 session.jsonl 始终是事实来源，不要为了查旧细节一次读取大段历史。",
		func(callCtx context.Context, input *SessionHistoryInput) (*SessionHistoryOutput, error) {
			if input == nil {
				return nil, errors.New("session_history 输入不能为空")
			}
			action := strings.ToLower(strings.TrimSpace(input.Action))
			switch action {
			case sessionHistoryActionSearch:
				matches, err := f.search(callCtx, scope, input)
				if err != nil {
					return nil, err
				}
				return &SessionHistoryOutput{Action: action, Matches: matches}, nil
			case sessionHistoryActionRead:
				entries, err := f.read(callCtx, scope, input)
				if err != nil {
					return nil, err
				}
				return &SessionHistoryOutput{Action: action, Entries: entries}, nil
			default:
				return nil, fmt.Errorf("action 必须是 %q 或 %q", sessionHistoryActionSearch, sessionHistoryActionRead)
			}
		})
}

func (f *SessionHistoryFactory) search(ctx context.Context, scope humberttools.Scope, input *SessionHistoryInput) ([]SessionHistoryMatch, error) {
	query := strings.ToLower(strings.TrimSpace(input.Query))
	if query == "" {
		return nil, errors.New("action=search 时 query 不能为空")
	}
	limit := input.MaxResults
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}

	document, err := f.repository.LoadTranscript(ctx, scope.SessionID)
	if err != nil {
		return nil, err
	}
	matches := make([]SessionHistoryMatch, 0, limit)
	// 从最近历史向前搜索，更符合 Agent 恢复当前任务的需要。
	for i := len(document.ActiveBranch) - 1; i >= 0 && len(matches) < limit; i-- {
		entry := document.ActiveBranch[i]
		text, role := historyEntryText(entry)
		if text == "" || !strings.Contains(strings.ToLower(text), query) {
			continue
		}
		matches = append(matches, SessionHistoryMatch{
			EntryID:   entry.ID,
			Role:      role,
			Timestamp: entry.Timestamp,
			Snippet:   historySnippet(text, 600),
		})
	}
	return matches, nil
}

func (f *SessionHistoryFactory) read(ctx context.Context, scope humberttools.Scope, input *SessionHistoryInput) ([]SessionHistoryEntry, error) {
	id := strings.TrimSpace(input.EntryID)
	if id == "" {
		return nil, errors.New("action=read 时 entry_id 不能为空")
	}
	before, after := input.Before, input.After
	if before == 0 {
		before = 1
	}
	if after == 0 {
		after = 2
	}
	if before < 0 || before > 5 || after < 0 || after > 8 {
		return nil, errors.New("before/after 超出允许范围")
	}
	if input.Offset < 0 {
		return nil, errors.New("offset 不能小于 0")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 12000
	}
	if limit < 1 || limit > 20000 {
		return nil, errors.New("limit 必须在 1-20000 之间")
	}

	document, err := f.repository.LoadTranscript(ctx, scope.SessionID)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range document.ActiveBranch {
		if document.ActiveBranch[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, fmt.Errorf("entry_id 不在当前有效会话分支中: %s", id)
	}

	start, end := index-before, index+after+1
	if start < 0 {
		start = 0
	}
	if end > len(document.ActiveBranch) {
		end = len(document.ActiveBranch)
	}
	entries := make([]SessionHistoryEntry, 0, end-start)
	for _, entry := range document.ActiveBranch[start:end] {
		text, role := historyEntryText(entry)
		runes := []rune(text)
		offset, entryLimit := 0, 2000
		if entry.ID == id {
			offset, entryLimit = input.Offset, limit
		}
		if offset > len(runes) {
			return nil, fmt.Errorf("offset=%d 超过历史记录 %s 长度 %d", offset, id, len(runes))
		}
		entryEnd := offset + entryLimit
		if entryEnd > len(runes) {
			entryEnd = len(runes)
		}
		content := string(runes[offset:entryEnd])
		if entry.ID != id && entryEnd < len(runes) {
			content += "\n...[相邻历史仅显示摘要；需要完整内容时，请再次调用 session_history，并使用 action=read、entry_id=该记录 ID]"
		}
		entries = append(entries, SessionHistoryEntry{
			EntryID: entry.ID, Type: string(entry.Type), Role: role, Timestamp: entry.Timestamp,
			Offset: offset, End: entryEnd, TotalChars: len(runes), More: entryEnd < len(runes), Content: content,
		})
	}
	return entries, nil
}

func historyEntryText(entry transcript.Entry) (string, string) {
	if entry.Type == transcript.EntryCompaction {
		return strings.TrimSpace(entry.Summary), "checkpoint"
	}
	if entry.Message == nil {
		return "", ""
	}
	message := entry.Message
	var builder strings.Builder
	for _, block := range message.Content {
		switch block.Type {
		case transcript.ContentText:
			builder.WriteString(block.Text)
			builder.WriteByte('\n')
		case transcript.ContentFile:
			builder.WriteString("[file " + block.Name + "]\n")
			builder.WriteString(block.ExtractedText)
			builder.WriteByte('\n')
		case transcript.ContentImage:
			builder.WriteString("[image attachment]\n")
		case transcript.ContentToolCall:
			builder.WriteString("[tool call " + block.Name + " id=" + block.ID + "]\n")
		}
	}
	if message.Role == transcript.RoleToolResult {
		builder.WriteString("[tool result " + message.ToolName + " id=" + message.ToolCallID + "]\n")
	}
	return strings.TrimSpace(builder.String()), string(message.Role)
}

func historySnippet(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes]) + "\n...[历史搜索结果已截断；先使用 entry_id 定位，再用 session_history action=read 按需读取原文]"
}
