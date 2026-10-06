package chunker

import (
	"regexp"
	"sort"
	"strings"
)

// 追踪 markdown 中的 table header
// 包括空表头修复、段落断表以及列数变化时结束旧表。

// headerTrackerHook 描述一种“上下文 Header”的识别规则。
// 实际只有一种：Markdown Table Header
// 为了以后能够继续扩展其他需要跨 Chunk 保留的上下文。
// 一个 Hook 包含：
// startPattern： 什么时候开始跟踪 Header
// endPattern： 什么时候结束跟踪 Header
// priority： 多种 Header 同时存在时的优先级
type headerTrackerHook struct {
	startPattern *regexp.Regexp
	endPattern   *regexp.Regexp
	priority     int
}

// markdownTableHookPriority 是 Markdown Table Header Hook 的优先级。
const markdownTableHookPriority = 15

// 默认跟踪 Markdown 表头，跨块时自动补充列名和分隔行。
var defaultHeaderHooks = []headerTrackerHook{
	{
		// 表头必须同时包含列名行和 Markdown 分隔行。
		startPattern: regexp.MustCompile(
			`(?si)^\s*` +
				`(?:\|[^|\n]*)+[\r\n]+` +
				`\s*(?:\|\s*:?-{3,}:?\s*)+\|?[\r\n]+$`,
		),

		// 结束规则：
		// 1. 空白
		// 或：
		// 2. 当前内容不是以 "|" 或 whitespace 开头
		// 意味着已经离开 Markdown Table。
		endPattern: regexp.MustCompile(
			`(?si)^\s*$|^\s*[^|\s].*$`,
		),
		priority: markdownTableHookPriority,
	},
}

// tableRowPattern 匹配一条 Markdown 表格行。
// 例如：
// | 东京 | 12000 |
// | 大阪 | 10000 |
// 注意它只负责判断“是不是 Table Row”，
// 不判断是不是 Header Separator。
var tableRowPattern = regexp.MustCompile(
	`(?m)^\s*(?:\|[^|\n]*)+\|\s*$`,
)

// headerTracker 跟踪当前有效的表头，在跨块时补充列名。
type headerTracker struct {
	// hooks 是所有 Header 规则。
	hooks []headerTrackerHook

	// activeHeaders：
	// priority -> header text
	// 当前仍然处于生效状态的 Header。
	activeHeaders map[int]string

	// endedHeaders：
	// 某个 Header 在本轮 update 中已经结束。
	// 防止同一个 splitUnit：
	// 先结束旧 Header
	// 又马上被识别成同一个 Header
	endedHeaders map[int]bool

	// pendingExtend 表示空表头需要使用下一行的真实列名补全。
	pendingExtend map[int]bool

	// pendingTableBreak 记录表格后的段落结束信号，由下一段决定是否清除旧表头。
	pendingTableBreak bool

	// headerEndedThisUnit 是给 mergeUnits 使用的信号。
	// 表示：
	//	当前 Unit 开始之前，
	//	旧 Table Header 已经结束。
	// mergeUnits 看到它以后，
	// 会先把旧 current flush，
	// 避免两张不同的表混在同一个 Chunk。
	headerEndedThisUnit bool
}

// newHeaderTracker 创建 Header Tracker。
func newHeaderTracker() *headerTracker {
	return &headerTracker{
		hooks:         defaultHeaderHooks,
		activeHeaders: make(map[int]string),
		endedHeaders:  make(map[int]bool),
		pendingExtend: make(map[int]bool),
	}
}

// update 根据当前单元更新表头，返回是否开始新表。
func (ht *headerTracker) update(split string) {
	// 每个 Unit 开始时先重置。
	ht.headerEndedThisUnit = false

	// 上一段出现空行且当前是表格行时，结束旧表头，避免串入下一张表。
	if ht.pendingTableBreak {
		ht.pendingTableBreak = false

		if _, active := ht.activeHeaders[markdownTableHookPriority]; active {
			if firstTableRowColumnCount(split) > 0 {
				// 下一个 Unit 是另一张表。
				ht.clearTableHeader()
				// 通知 Merge：
				// 先 flush 上一张表的内容。
				ht.headerEndedThisUnit = true
			} else {
				// 下一块不是 Table Row。
				// 正常结束旧表。
				ht.clearTableHeader()
			}
		}
	}

	// -----------------------------------------------------------------
	// 2. 检测已有 Header 是否遇到了 endPattern。
	// -----------------------------------------------------------------
	for _, hook := range ht.hooks {
		if _, active := ht.activeHeaders[hook.priority]; !active {
			continue
		}

		if hook.endPattern.MatchString(split) {
			ht.endedHeaders[hook.priority] = true
			delete(ht.activeHeaders, hook.priority)
			delete(ht.pendingExtend, hook.priority)
		}
	}

	// 当前表格行列数变化时结束旧表头。
	if _, active :=
		ht.activeHeaders[markdownTableHookPriority]; active {

		if !ht.pendingExtend[markdownTableHookPriority] {
			if splitEndsWithParagraphBreak(split) {
				// 当前 Table Row 后面存在空段落。
				// 暂时不立刻结束，
				// 等看到下一个 Unit 再确认。
				ht.pendingTableBreak = true
			} else {
				ht.endTableHeaderOnColumnMismatch(split)
			}
		}
	}

	// 空表头使用下一条真实表格行补齐列名，并保留原分隔行。
	for priority := range ht.pendingExtend {
		if _, active :=
			ht.activeHeaders[priority]; active &&
			tableRowPattern.MatchString(split) {
			separator := extractSeparatorLine(ht.activeHeaders[priority])
			ht.activeHeaders[priority] = split + separator
		}
		delete(ht.pendingExtend, priority)
	}

	// -----------------------------------------------------------------
	// 5. 检测新的 Header Start。
	// -----------------------------------------------------------------

	for _, hook := range ht.hooks {
		if _, active := ht.activeHeaders[hook.priority]; active {
			continue
		}

		// 当前 Unit 里刚结束的 Header，
		// 不允许马上重新识别。
		if ht.endedHeaders[hook.priority] {
			continue
		}

		header := hook.startPattern.FindString(split)

		if header == "" {
			continue
		}

		ht.activeHeaders[hook.priority] = header

		// 是否属于：
		//
		//	||
		//	| --- | --- |
		//
		// 这种空表头。
		if isEmptyTableHeaderRow(header) {
			ht.pendingExtend[hook.priority] = true
		}
	}

	// 已结束表头仅用于当前单元防止重复启动，不阻止后续表格。

	if len(ht.activeHeaders) == 0 {
		for priority := range ht.endedHeaders {
			delete(ht.endedHeaders, priority)
		}
	}
}

// getHeaders 返回当前所有 Active Header。
//
// 如果未来同时存在多种 Header Hook，
// 按 priority 从高到低排列。
func (ht *headerTracker) getHeaders() string {
	if len(ht.activeHeaders) == 0 {
		return ""
	}

	type entry struct {
		priority int
		text     string
	}

	entries := make([]entry, 0, len(ht.activeHeaders))

	for priority, text := range ht.activeHeaders {

		entries = append(
			entries,
			entry{
				priority: priority,
				text:     text,
			},
		)
	}

	sort.Slice(
		entries,
		func(i, j int) bool {
			return entries[i].priority >
				entries[j].priority
		},
	)

	parts := make([]string, len(entries))

	for i, e := range entries {
		parts[i] = e.text
	}

	return strings.Join(parts, "\n")
}

// isEmptyTableHeaderRow 判断表头是否没有有效列名。
func isEmptyTableHeaderRow(
	header string,
) bool {

	index := strings.IndexByte(header, '\n')

	if index < 0 {
		return false
	}

	firstRow := strings.TrimSpace(header[:index])

	for _, r := range firstRow {
		if r != '|' && r != ' ' && r != '\t' {
			return false
		}
	}

	return true
}

// extractSeparatorLine 从 Markdown Table Header 中
// 提取：
//
//	| --- | --- |
//
// 这一行。
func extractSeparatorLine(header string) string {

	for _, line := range strings.Split(header, "\n") {
		if strings.Contains(line, "---") {
			return line + "\n"
		}
	}

	return ""
}

// clearTableHeader 清除当前 Markdown Table Header。
func (ht *headerTracker) clearTableHeader() {
	ht.endedHeaders[markdownTableHookPriority] = true
	delete(ht.activeHeaders, markdownTableHookPriority)
	delete(ht.pendingExtend, markdownTableHookPriority)
}

// endTableHeaderOnColumnMismatch 在当前行与表头列数不一致时结束旧表头。
func (ht *headerTracker) endTableHeaderOnColumnMismatch(
	split string,
) {

	header, ok := ht.activeHeaders[markdownTableHookPriority]

	if !ok {
		return
	}

	rowColumns := firstTableRowColumnCount(split)

	headerColumns := headerTableColumnCount(header)

	if rowColumns > 0 && headerColumns > 0 && rowColumns != headerColumns {
		ht.clearTableHeader()
		ht.headerEndedThisUnit = true
	}
}

// splitEndsWithParagraphBreak 判断文本是否以空行结束。
func splitEndsWithParagraphBreak(split string) bool {
	trimmed := strings.TrimRight(split, " \t\r")
	return strings.HasSuffix(trimmed, "\n\n") || strings.HasSuffix(trimmed, "\r\n\r\n")
}

// tableRowColumnCount 计算表格列数，忽略转义竖线和代码中的竖线。
func tableRowColumnCount(line string) int {

	line = strings.TrimSpace(line)

	if !strings.HasPrefix(line, "|") {
		return 0
	}

	parts := strings.Split(line, "|")

	if len(parts) > 0 && strings.TrimSpace(parts[0]) == "" {
		parts = parts[1:]
	}

	if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}

	return len(parts)
}

// firstTableRowColumnCount 从一段文本中找到
// 第一条 Markdown Table Row，并返回它的列数。
func firstTableRowColumnCount(text string) int {

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if tableRowPattern.MatchString(line) {
			return tableRowColumnCount(line)
		}
	}

	return 0
}

// headerTableColumnCount 从表头中找到真实列数。
func headerTableColumnCount(header string) int {
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "---") {
			continue
		}
		if n := tableRowColumnCount(line); n > 0 {
			return n
		}
	}

	return 0
}

// headerColumnMismatch 判断正文第一行的列数是否与待补充表头冲突。
func headerColumnMismatch(headers string, nextUnit string) bool {
	headerColumns := headerTableColumnCount(headers)
	rowColumns := firstTableRowColumnCount(nextUnit)
	return headerColumns > 0 && rowColumns > 0 && headerColumns != rowColumns
}
