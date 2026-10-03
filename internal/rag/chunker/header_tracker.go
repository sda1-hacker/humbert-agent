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

// defaultHeaderHooks 定义当前默认的 Header Tracking 规则。
// Markdown 表格：
// | 城市 | 标准 |
// | --- | --- |
// 上面两行会成为：
// active table header
// 后续：
// | 东京 | 12000 |
// | 大阪 | 10000 |
// 如果被切到新 Chunk，系统就可以自动把表头补回来。
var defaultHeaderHooks = []headerTrackerHook{
	{
		// 开始规则：
		// Markdown Header Row + Separator Row
		// 例如：
		//	| 城市 | 标准 |
		//	| --- | --- |
		// (?si):
		//	s = . 可以跨行
		//	i = 忽略大小写
		// ^...$ 表示整个 splitUnit 都应该是表头。
		// -------------------------------------------------------------
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

// headerTracker 保存当前 Chunk Merge 过程中正在生效的 Header。
// 举例：
// | 城市 | 标准 |
// | --- | --- |
// | 东京 | 12000 |
// | 大阪 | 10000 |
// 读到前两行之后：
// activeHeaders[15] = "| 城市 | 标准 |\n| --- | --- |\n"
// 后续 Chunk 如果从“大阪”开始，
// 就知道应该把这个 active header 加到新 Chunk 前面。
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

	// pendingExtend：
	// 用于处理“空表头”。
	// 某些转换器可能产生：
	//	||
	//	| --- | --- |
	//	| 城市 | 标准 |
	//	| 东京 | 12000 |
	// 第一行没有列名。
	// 这种情况下先标记 pending，
	// 等第一条真实数据行到来后，
	// 用它替换空表头。
	pendingExtend map[int]bool

	// pendingTableBreak 表示：
	// 当前某条 Table Row 以 paragraph break 结束。
	// 例如：
	//	| 东京 | 12000 |
	//	| 产品 | 价格 |
	// 中间的空行表示第一张表已经结束。
	// 由于 Recursive Splitter 可能把空行包含在前一个 Unit 中，
	// 所以需要延迟到“看到下一个 Unit”时再判断。
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

// update 根据当前 splitUnit 的文本更新 Header 状态。
//
// 这个函数会在 mergeUnits 每处理一个 Unit 时调用一次。
//
// 处理顺序非常重要：
//
//  1. 处理上一 Unit 遗留的 table break
//  2. 检测当前 Header 是否结束
//  3. 检测 Table 列数是否变化
//  4. 修复空 Table Header
//  5. 检测新的 Header
//  6. 清理 ended 状态
func (ht *headerTracker) update(split string) {
	// 每个 Unit 开始时先重置。
	ht.headerEndedThisUnit = false

	// -----------------------------------------------------------------
	// 1. 处理上一 Unit 遗留的 Table Break。
	// -----------------------------------------------------------------
	//
	// 假设：
	//
	//	| 东京 | 12000 |\n\n
	//
	// 上一 Unit 最后已经出现 paragraph break。
	//
	// 如果当前 Unit 又是一个 Table Row：
	//
	//	| 产品 | 价格 |
	//
	// 那说明：
	//
	//	旧表已经结束
	//	新表开始
	//
	// 必须清掉旧 Header。
	// -----------------------------------------------------------------
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

	// -----------------------------------------------------------------
	// 3. Markdown Table 特有处理。
	// -----------------------------------------------------------------
	//
	// 表头可能仍然 active，
	// 但是当前 Table Row 的列数已经变化。
	//
	// 例如：
	//
	//	旧表：
	//	| A | B |
	//
	//	新表：
	//	| X | Y | Z |
	//
	// 即便 Markdown 中间没有非常明显的空白，
	// 列数变化也说明这大概率是另一张表。
	// -----------------------------------------------------------------
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

	// -----------------------------------------------------------------
	// 4. 修复空 Table Header。
	// -----------------------------------------------------------------
	//
	// 某些解析器可能产生：
	//
	//	||
	//	| --- | --- |
	//	| 城市 | 标准 |
	//
	// 此时：
	//
	//	"城市 | 标准"
	//
	// 才是真正有意义的列名。
	//
	// 当前 WeKnora 的处理方式：
	//
	//	使用第一条真实 Table Row
	//	+
	//	原来的 separator line
	//
	// 重建 Header。
	// -----------------------------------------------------------------
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

	// -----------------------------------------------------------------
	// 6. 如果当前已经没有任何 active header，
	// 就清空 endedHeaders。
	//
	// 为什么？
	//
	// 因为后面文档中可能还有第二张、第三张表。
	//
	// ended 只能阻止“当前 Unit 重新启动旧 Header”，
	// 不能永远禁止未来表格。
	// -----------------------------------------------------------------

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

// isEmptyTableHeaderRow 判断 Markdown 表头的第一行
// 是否只有：
//
//	|
//	空格
//	Tab
//
// 例如：
//
//	||
//	| |
//
// 都属于“没有真正列名”的表头。
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

// endTableHeaderOnColumnMismatch 检查：
//
//	当前 Table Row 列数
//
// 是否和：
//
//	Header 列数
//
// 不一致。
//
// 不一致通常表示：
//
//	旧表结束
//	新表开始
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

// splitEndsWithParagraphBreak 判断当前文本是否以：
//
//	\n\n
//
// 或：
//
//	\r\n\r\n
//
// 结束。
func splitEndsWithParagraphBreak(split string) bool {
	trimmed := strings.TrimRight(split, " \t\r")
	return strings.HasSuffix(trimmed, "\n\n") || strings.HasSuffix(trimmed, "\r\n\r\n")
}

// tableRowColumnCount 计算一条 Markdown Table Row
// 有多少列。
//
// 例如：
//
//	| 东京 | 12000 |
//
// strings.Split:
//
//	""
//	" 东京 "
//	" 12000 "
//	""
//
// 去掉头尾空项以后：
//
//	2 列
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

// headerTableColumnCount 获取 Header 的真实列数。
//
// 会跳过：
//
//	空行
//	separator row
//
// 只使用真正的列名行。
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

// headerColumnMismatch 判断：
//
// 当前 Active Header
//
// 和：
//
// nextUnit 的 Table Row
//
// 是否列数不同。
func headerColumnMismatch(headers string, nextUnit string) bool {
	headerColumns := headerTableColumnCount(headers)
	rowColumns := firstTableRowColumnCount(nextUnit)
	return headerColumns > 0 && rowColumns > 0 && headerColumns != rowColumns
}
