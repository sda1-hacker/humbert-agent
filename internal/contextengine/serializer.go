package contextengine

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const compactionSystemPrompt = `你是 Humbert 的内部上下文压缩器。你的输出只会作为后续模型调用的内部 checkpoint，不是给用户看的回复。

请根据“之前的 checkpoint（如果有）”与“本次即将被压缩的原始历史”，生成一份可让 Agent 无缝继续当前任务的 Markdown checkpoint。

必须严格使用以下标题，且不要增加前言或结尾：
## Goal
## Constraints & Preferences
## Progress
### Done
### In Progress
### Blocked
## Key Decisions
## Next Steps
## Critical Context

要求：
- 保留当前目标、用户明确约束、已完成工作、正在进行的工作、阻塞、关键决定及原因、下一步和继续任务不可丢失的数据。
- 工具结果只提炼与继续任务有关的事实，不复制大段文件或网页正文。
- Assistant reasoning 只能用于理解已经形成的决定和进展，不得逐字复制思维过程。
- 如果之前 checkpoint 与新历史冲突，以时间更晚、明确确认的信息为准。
- 不调用工具，不向用户提问，不评价压缩行为。`

// serializeCompactionPlan 把即将离开主 Context 的 Transcript 区域转换成摘要模型输入。
//
// Transcript 原文永不截断；这里只限制单个消息块以及整个摘要请求的字符数，防止大型
// 用户输入、附件文本或 ToolResult 让压缩请求本身再次超过 Context Window。截断按 rune
// 边界执行，不会产生非法 UTF-8。
func serializeCompactionPlan(plan Plan, maxChars int) string {
	return serializeCompactionPlanWithLimit(plan, maxChars, maxChars*8)
}

func serializeCompactionPlanWithLimit(plan Plan, maxChars int, maxTotalChars int) string {
	if maxChars < 256 {
		maxChars = 256
	}
	if maxTotalChars < 512 {
		maxTotalChars = 512
	}

	var builder strings.Builder
	if strings.TrimSpace(plan.PreviousSummary) != "" {
		builder.WriteString("[Previous checkpoint]\n")
		builder.WriteString(truncateText(strings.TrimSpace(plan.PreviousSummary), maxChars*2))
		builder.WriteString("\n\n[New history to merge]\n")
	}

	for _, entry := range plan.ToSummarize {
		if entry.Message == nil {
			continue
		}
		message := entry.Message
		switch message.Role {
		case transcript.RoleUser:
			builder.WriteString("\n[User]\n")
			builder.WriteString(truncateText(wireVisibleText(message.Content), maxChars))
			for _, block := range message.Content {
				switch block.Type {
				case transcript.ContentImage:
					builder.WriteString("\n[Image attachment: ")
					builder.WriteString(attachmentLabel(block))
					builder.WriteString("]")
				case transcript.ContentFile:
					builder.WriteString("\n[File attachment: ")
					builder.WriteString(attachmentLabel(block))
					builder.WriteString("]\n")
					builder.WriteString(truncateText(block.ExtractedText, maxChars))
				}
			}
			builder.WriteByte('\n')

		case transcript.RoleAssistant:
			for _, block := range message.Content {
				switch block.Type {
				case transcript.ContentThinking:
					if block.Redacted || strings.TrimSpace(block.Thinking) == "" {
						continue
					}
					builder.WriteString("\n[Assistant reasoning]\n")
					builder.WriteString(truncateText(block.Thinking, maxChars))
					builder.WriteByte('\n')
				case transcript.ContentText:
					builder.WriteString("\n[Assistant]\n")
					builder.WriteString(truncateText(block.Text, maxChars))
					builder.WriteByte('\n')
				case transcript.ContentToolCall:
					builder.WriteString("\n[Assistant tool call]\n")
					builder.WriteString(block.Name)
					builder.WriteString("(")
					builder.WriteString(summarizeToolArguments(block.Arguments, maxChars))
					builder.WriteString(")\n")
				}
			}

		case transcript.RoleToolResult:
			builder.WriteString("\n[Tool result: ")
			builder.WriteString(message.ToolName)
			if message.IsError {
				builder.WriteString("; error")
			}
			builder.WriteString("]\n")
			builder.WriteString(truncateText(wireVisibleText(message.Content), maxChars))
			builder.WriteByte('\n')
		}
	}

	return truncateCompactionPayload(strings.TrimSpace(builder.String()), maxTotalChars)
}

func attachmentLabel(block transcript.ContentBlock) string {
	name := strings.TrimSpace(block.Name)
	if name == "" {
		name = "attachment"
	}
	label := name
	if mimeType := strings.TrimSpace(block.MIMEType); mimeType != "" {
		label += "; MIME: " + mimeType
	}
	if block.SizeBytes > 0 {
		label += fmt.Sprintf("; %d bytes", block.SizeBytes)
	}
	return label
}

// truncateCompactionPayload 为整个摘要输入设置硬边界，并同时保留开头的旧 checkpoint
// 与结尾的最新历史。只做单块截断不能阻止大量短消息共同撑爆摘要模型上下文。
func truncateCompactionPayload(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	marker := "\n\n...[middle history omitted for compaction budget]...\n\n"
	markerRunes := []rune(marker)
	if maxRunes <= len(markerRunes)+2 {
		return truncateText(value, maxRunes)
	}
	runes := []rune(value)
	available := maxRunes - len(markerRunes)
	prefix := available / 3
	suffix := available - prefix
	return string(runes[:prefix]) + marker + string(runes[len(runes)-suffix:])
}

func compactionSerializationLimit(maxChars int, contextWindow int) int {
	limit := maxChars * 8
	if limit < 512 {
		limit = 512
	}
	// 非 ASCII 最坏接近 1 rune/token。只让序列化历史占窗口一半，为系统提示、摘要输出
	// 和 Provider framing 留出空间。
	if contextWindow > 0 && contextWindow/2 < limit {
		limit = contextWindow / 2
	}
	if limit < 512 {
		limit = 512
	}
	return limit
}

func wireVisibleText(blocks []transcript.ContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type == transcript.ContentText {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

func truncateText(value string, maxRunes int) string {
	if maxRunes <= 0 || value == "" {
		return value
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes]) + "\n...[truncated for compaction]"
}

// normalizeSummaryResult 校验摘要模型返回了最小可恢复结构。
func normalizeSummaryResult(value string) (string, error) {
	value = stripWholeMarkdownFence(value)
	if value == "" {
		return "", fmt.Errorf("压缩模型返回空 checkpoint")
	}

	required := []string{
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
	last := -1
	for position, heading := range required {
		index := strings.Index(value, heading)
		if index < 0 {
			return "", fmt.Errorf("压缩 checkpoint 缺少标题 %q", heading)
		}
		if position == 0 && index != 0 {
			return "", fmt.Errorf("压缩 checkpoint 必须以 %q 开始", heading)
		}
		if index <= last {
			return "", fmt.Errorf("压缩 checkpoint 标题顺序无效: %q", heading)
		}
		last = index
	}
	return value, nil
}

// stripWholeMarkdownFence 兼容模型把整个结构化结果额外包在 Markdown code fence 中的情况。
//
// 只移除“整个响应恰好是一层 fence”的外壳，不处理正文内部代码块，避免为了容错而改变
// checkpoint 的真实内容。该函数不猜测或补写缺失标题，结构仍由 normalizeSummaryResult
// 严格校验。
func stripWholeMarkdownFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") || !strings.HasSuffix(value, "```") {
		return value
	}
	firstLineEnd := strings.IndexByte(value, '\n')
	if firstLineEnd < 0 {
		return value
	}
	inner := strings.TrimSpace(value[firstLineEnd+1 : len(value)-3])
	return inner
}

// summarizeToolArguments 把 Tool 参数清理成适合内部 Compaction 模型读取的短 JSON。
//
// Compaction 的目的只是恢复“调用了什么、针对哪个资源、结果如何”，不需要再次向摘要模型
// 暴露 write_file.content、替换正文或 Credential。参数未来也可能来自 MCP 等外部 Tool，因此
// 必须递归处理嵌套对象和不同大小写/分隔符写法；无法解析的参数宁可退化为 `{}`，也不把
// 未校验原文继续传播到内部摘要请求。
func summarizeToolArguments(arguments json.RawMessage, maxRunes int) string {
	var value any
	if err := json.Unmarshal(arguments, &value); err != nil {
		return "{}"
	}
	value = sanitizeCompactionArgument(value, "")
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return truncateText(string(encoded), maxRunes)
}

func sanitizeCompactionArgument(value any, key string) any {
	if compactionArgumentShouldOmit(key) {
		return "[omitted]"
	}

	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			result[childKey] = sanitizeCompactionArgument(childValue, childKey)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, childValue := range typed {
			result[index] = sanitizeCompactionArgument(childValue, "")
		}
		return result
	default:
		return value
	}
}

func compactionArgumentShouldOmit(key string) bool {
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
