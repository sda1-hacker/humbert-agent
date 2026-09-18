package contextengine

import (
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// RetainedState 是跨工作窗口持续存在的“当前会话工作状态”。它不是完整历史，也不是长期
// 记忆；它只保存让下一工作窗口继续当前任务所需的目标、约束、进度、决定和待办。
// 当前实现从格式固定的 durable checkpoint 确定性解析，因此不增加额外模型调用。
type RetainedState struct {
	Available bool `json:"available"`

	Goal        string `json:"goal,omitempty"`
	Constraints string `json:"constraints,omitempty"`
	Done        string `json:"done,omitempty"`
	InProgress  string `json:"inProgress,omitempty"`
	Blocked     string `json:"blocked,omitempty"`
	Decisions   string `json:"decisions,omitempty"`
	NextSteps   string `json:"nextSteps,omitempty"`
	Critical    string `json:"critical,omitempty"`

	ReadFiles     []string `json:"readFiles,omitempty"`
	ModifiedFiles []string `json:"modifiedFiles,omitempty"`

	SourceFirstEntryID string `json:"sourceFirstEntryID,omitempty"`
	SourceLastEntryID  string `json:"sourceLastEntryID,omitempty"`
	SourceEntryCount   int    `json:"sourceEntryCount,omitempty"`
}

func retainedStateFromCompaction(entry *transcript.Entry) RetainedState {
	if entry == nil || entry.Type != transcript.EntryCompaction || strings.TrimSpace(entry.Summary) == "" {
		return RetainedState{}
	}
	sections := checkpointSections(entry.Summary)
	state := RetainedState{
		Available:   true,
		Goal:        sections["## Goal"],
		Constraints: sections["## Constraints & Preferences"],
		Done:        sections["### Done"],
		InProgress:  sections["### In Progress"],
		Blocked:     sections["### Blocked"],
		Decisions:   sections["## Key Decisions"],
		NextSteps:   sections["## Next Steps"],
		Critical:    sections["## Critical Context"],
	}
	if entry.Details != nil {
		state.ReadFiles = append([]string(nil), entry.Details.ReadFiles...)
		state.ModifiedFiles = append([]string(nil), entry.Details.ModifiedFiles...)
		state.SourceFirstEntryID = entry.Details.SourceFirstEntryID
		state.SourceLastEntryID = entry.Details.SourceLastEntryID
		state.SourceEntryCount = entry.Details.SourceEntryCount
	}
	return state
}

func checkpointSections(summary string) map[string]string {
	headings := []string{
		"## Goal",
		"## Constraints & Preferences",
		"## Progress",
		"### Done",
		"### In Progress",
		"### Blocked",
		"## Key Decisions",
		"## Next Steps",
		"## Critical Context",
	}
	positions := make([]int, len(headings))
	for i, heading := range headings {
		positions[i] = strings.Index(summary, heading)
	}
	result := make(map[string]string, len(headings))
	for i, heading := range headings {
		start := positions[i]
		if start < 0 {
			continue
		}
		start += len(heading)
		end := len(summary)
		for j := i + 1; j < len(headings); j++ {
			if positions[j] >= 0 && positions[j] > start {
				end = positions[j]
				break
			}
		}
		result[heading] = strings.TrimSpace(summary[start:end])
	}
	return result
}

// localCheckpoint 从已经持久化的会话保留状态生成模型无关的兜底检查点。它不声称覆盖
// 新发生但尚未被语义压缩的细节，因此只在远程压缩失败时与最近原始历史配合使用。
func (s RetainedState) localCheckpoint() string {
	if !s.Available {
		return ""
	}
	parts := []struct{ heading, value string }{
		{"## Goal", s.Goal},
		{"## Constraints & Preferences", s.Constraints},
		{"## Progress\n### Done", s.Done},
		{"### In Progress", s.InProgress},
		{"### Blocked", s.Blocked},
		{"## Key Decisions", s.Decisions},
		{"## Next Steps", s.NextSteps},
		{"## Critical Context", s.Critical},
	}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(part.heading)
		builder.WriteByte('\n')
		value := strings.TrimSpace(part.value)
		if value == "" {
			value = "- 暂无"
		}
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

// localFallbackCheckpoint 在压缩模型不可用时生成一个确定性的应急检查点。
// 它明确声明自己不是完整语义摘要，只保留已有检查点和一小段可读线索；完整旧历史仍由
// session.jsonl 保存，并可通过 session_history 追回，因此不会把“摘要缺失”
// 伪装成“事实已经丢失”。
func localFallbackCheckpoint(previous string, messages []*schema.Message, maxChars int) string {
	if maxChars < 2048 {
		maxChars = 4096
	}
	sections := checkpointSections(previous)
	get := func(heading string) string {
		value := strings.TrimSpace(sections[heading])
		if value == "" {
			return "- 暂无"
		}
		return value
	}

	var evidence strings.Builder
	evidenceRunes := 0
	for _, message := range messages {
		if message == nil {
			continue
		}
		var label string
		switch message.Role {
		case schema.User:
			label = "用户"
		case schema.Assistant:
			label = "助手"
		case schema.Tool:
			label = "工具结果"
		default:
			continue
		}
		text := strings.TrimSpace(messageVisibleText(message))
		if text == "" && message.Role == schema.Assistant && len(message.ToolCalls) > 0 {
			names := make([]string, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				if name := strings.TrimSpace(call.Function.Name); name != "" {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				text = "调用工具: " + strings.Join(names, ", ")
			}
		}
		if text == "" {
			continue
		}
		text = truncateText(text, 600)
		line := "- [" + label + "] " + text + "\n"
		lineRunes := len([]rune(line))
		if evidenceRunes+lineRunes > maxChars/2 {
			break
		}
		evidence.WriteString(line)
		evidenceRunes += lineRunes
	}
	if evidence.Len() == 0 {
		evidence.WriteString("- 本次新增历史未能由压缩模型归纳；需要旧细节时请回查完整会话记录。\n")
	}

	critical := get("## Critical Context")
	if critical == "- 暂无" {
		critical = ""
	}
	if critical != "" {
		critical += "\n"
	}
	critical += "- [应急检查点] 压缩模型本次不可用或失败。下面只提供原始历史线索，不应视为完整摘要；需要精确细节时必须使用 session_history 回查。\n" + strings.TrimSpace(evidence.String())

	var builder strings.Builder
	builder.WriteString("## Goal\n" + get("## Goal") + "\n")
	builder.WriteString("## Constraints & Preferences\n" + get("## Constraints & Preferences") + "\n")
	builder.WriteString("## Progress\n")
	builder.WriteString("### Done\n" + get("### Done") + "\n")
	builder.WriteString("### In Progress\n" + get("### In Progress") + "\n")
	builder.WriteString("### Blocked\n" + get("### Blocked") + "\n")
	builder.WriteString("## Key Decisions\n" + get("## Key Decisions") + "\n")
	builder.WriteString("## Next Steps\n" + get("## Next Steps") + "\n")
	builder.WriteString("## Critical Context\n" + strings.TrimSpace(critical))
	return strings.TrimSpace(builder.String())
}
