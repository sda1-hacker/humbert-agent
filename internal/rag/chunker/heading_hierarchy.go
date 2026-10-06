package chunker

import "strings"

// HeadingHierarchy 按层级保存当前 Markdown 标题路径。
type HeadingHierarchy struct {
	levels  [6]string
	deepest int
}

// NewHeadingHierarchy 创建一个空的 HeadingHierarchy。
func NewHeadingHierarchy() *HeadingHierarchy {
	return &HeadingHierarchy{}
}

// Observe 读取一个标题，同时清除已经失效的更深层标题。
func (h *HeadingHierarchy) Observe(line string) (int, string) {
	match := MarkdownHeadingPattern.FindStringSubmatch(line)
	if match == nil {
		return 0, ""
	}

	level := len(match[1])
	if level < 1 || level > 6 {
		return 0, ""
	}

	title := strings.TrimSpace(match[2])

	// 当前 level 的标题替换掉旧值。
	h.levels[level-1] = title

	// 一个新的 H2 出现时，旧 H3/H4/H5/H6 已经不属于当前上下文。
	for i := level; i < len(h.levels); i++ {
		h.levels[i] = ""
	}

	// 重新计算当前最深有效层级。
	h.deepest = 0
	for i, value := range h.levels {
		if value != "" {
			h.deepest = i + 1
		}
	}

	return level, title
}

// Breadcrumb 返回以分隔符连接的标题路径，供展示使用。
func (h *HeadingHierarchy) Breadcrumb() string {
	if h.deepest == 0 {
		return ""
	}

	parts := make([]string, 0, h.deepest)

	for i := 0; i < h.deepest; i++ {
		if h.levels[i] != "" {
			parts = append(parts, h.levels[i])
		}
	}

	return strings.Join(parts, " > ")
}

// BreadcrumbWithHashes 返回保留标题层级标记的路径，供检索补充上下文。
func (h *HeadingHierarchy) BreadcrumbWithHashes() string {
	if h.deepest == 0 {
		return ""
	}

	var builder strings.Builder

	for i := 0; i < h.deepest; i++ {
		if h.levels[i] == "" {
			continue
		}

		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}

		builder.WriteString(strings.Repeat("#", i+1))
		builder.WriteByte(' ')
		builder.WriteString(h.levels[i])
	}

	return builder.String()
}

// Depth 返回当前最深有效 Heading Level。
func (h *HeadingHierarchy) Depth() int {
	return h.deepest
}

// Reset 清空整个 Heading 上下文。
func (h *HeadingHierarchy) Reset() {
	for i := range h.levels {
		h.levels[i] = ""
	}

	h.deepest = 0
}
