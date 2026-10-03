package searchcontent

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Builder 负责构造真正送给：
//
//	Embedding
//	BM25
//
// 的检索文本。
//
// 我们始终保持两个世界分离：
//
//	schema.Document.Content
//	    = 权威 Chunk 原文
//
//	SearchContent
//	    = 为检索优化后的表示
//
// 当前基本结构：
//
//	title
//
//	context header
//
//	chunk body
//
// 例如：
//
//	员工差旅制度
//
//	# 差旅制度
//	## 日本地区
//	### 酒店标准
//
//	东京地区住宿标准为……
type Builder struct {
	// TitleKeys 按顺序寻找 Document title。
	//
	// 默认：
	//
	//     title
	//     _title
	//     file_name
	//
	// 前面的业务 title 优先级最高。
	TitleKeys []string

	// ContextHeaderKey 是 Chunk Transformer
	// 保存 Breadcrumb 的 metadata key。
	ContextHeaderKey string
}

// DefaultBuilder 返回当前推荐的检索文本构造规则。
func DefaultBuilder() Builder {
	return Builder{
		TitleKeys: []string{
			"title",
			"_title",
			"file_name",
		},
		ContextHeaderKey: "rag_context_header",
	}
}

// Build 构造用于 Embedding / BM25 的 SearchContent。
//
// 注意这里会 TrimSpace。
//
// 这是安全的，因为 SearchContent 本来就是：
//
//	retrieval representation
//
// 不承担 Source Offset 映射职责。
//
// 真正原文仍然保存在：
//
//	schema.Document.Content
func (b Builder) Build(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	title := b.Title(doc)
	header := b.ContextHeader(doc)
	body := strings.TrimSpace(doc.Content)

	parts := make([]string, 0, 3)

	appendUnique := func(value string) {
		value = strings.TrimSpace(value)

		if value == "" {
			return
		}

		// 只做“完整部分完全相同”的去重。
		//
		// 不做模糊处理：
		//
		//     title = "产品手册"
		//
		//     header = "# 产品手册\n## 安装"
		//
		// 两者并不完全相同，
		// 因此都会保留。
		//
		// 这种轻微 title reinforcement
		// 对 retrieval 通常反而有帮助。
		for _, existing := range parts {
			if existing == value {
				return
			}
		}

		parts = append(parts, value)
	}

	appendUnique(title)
	appendUnique(header)
	appendUnique(body)

	return strings.Join(parts, "\n\n")
}

// Title 根据配置依次寻找文档标题。
func (b Builder) Title(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	keys := b.TitleKeys

	if len(keys) == 0 {
		keys = DefaultBuilder().TitleKeys
	}

	for _, key := range keys {
		if value := metadataString(doc.MetaData, key); value != "" {
			return value
		}
	}

	return ""
}

// ContextHeader 返回 Chunk Breadcrumb。
func (b Builder) ContextHeader(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	key := b.ContextHeaderKey

	if key == "" {
		key = DefaultBuilder().ContextHeaderKey
	}

	return metadataString(doc.MetaData, key)
}

// metadataString 只接受真正 string metadata。
//
// 不使用 fmt.Sprint 的原因是：
//
//	[]string{"a", "b"}
//	map[string]any{...}
//
// 这种复杂对象不应该因为一个通用格式化操作
// 意外进入 Embedding 文本。
func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 || key == "" {
		return ""
	}

	value, ok := metadata[key]
	if !ok {
		return ""
	}

	text, ok := value.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(text)
}
