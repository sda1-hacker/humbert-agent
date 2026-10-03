package chunker

import "strings"

// HeadingHierarchy 用来维护当前 Markdown 标题上下文。
//
// Markdown 最多支持 H1 ~ H6，所以这里固定维护 6 个槽位。
//
// 例如文档:
//
//	# 产品手册
//	## 安装
//	### Linux
//
// 内部状态大致是:
//
//	levels[0] = "产品手册"
//	levels[1] = "安装"
//	levels[2] = "Linux"
//
// deepest = 3
//
// 当后面遇到:
//
//	## 配置
//
// H2 被替换，同时所有更深层级都必须失效:
//
//	levels[0] = "产品手册"
//	levels[1] = "配置"
//	levels[2] = ""
//
// deepest = 2
type HeadingHierarchy struct {
	levels  [6]string
	deepest int
}

// NewHeadingHierarchy 创建一个空的 HeadingHierarchy。
func NewHeadingHierarchy() *HeadingHierarchy {
	return &HeadingHierarchy{}
}

// Observe 尝试把一行 Markdown 当成 Heading 处理。
//
// 如果识别成功:
//
//	返回 level 和去掉 # 后的标题正文。
//
// 例如:
//
//	## Installation
//
// 返回:
//
//	level = 2
//	title = "Installation"
//
// 如果不是 Heading:
//
//	返回 0, ""。
//
// 注意:
// 这个函数自己不知道 fenced code 的状态。
// 调用方必须保证不要把代码块里的 "# xxx" 传进来。
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

// Breadcrumb 返回人类更容易阅读的标题路径。
//
// 例如:
//
//	Chapter 1 > Installation > Linux
//
// 这个形式主要适合:
//
//	日志
//	Debug
//	Preview UI
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

// BreadcrumbWithHashes 返回适合 RAG ContextHeader 使用的 Markdown 标题路径。
//
// 例如:
//
//	# 产品手册
//	## 安装
//	### Linux
//
// 我们的 Chunk.ContextHeader 就使用这种形式。
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
