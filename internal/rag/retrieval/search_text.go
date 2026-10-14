package retrieval

import (
	"slices"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// SearchTextBuilder 为向量、关键词和精排构造统一文本表示，原文范围由正文模型单独保存。
type SearchTextBuilder struct {
	// TitleKeys 按顺序查找标题，默认使用 title、_title、file_name。
	TitleKeys []string

	// ContextHeaderKey 是 Chunk Transformer
	// 保存 Breadcrumb 的 metadata key。
	ContextHeaderKey string
}

// DefaultSearchTextBuilder 返回当前推荐的检索文本构造规则。
func DefaultSearchTextBuilder() SearchTextBuilder {
	return SearchTextBuilder{TitleKeys: []string{"title", "_title", "file_name"}, ContextHeaderKey: "rag_context_header"}
}

// Build 拼接标题、标题路径与正文；返回值用于模型输入，不用于原文坐标映射。
func (b SearchTextBuilder) Build(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	return BuildSearchText(b.Title(doc), b.ContextHeader(doc), doc.Content)
}

// BuildSearchText 将标题、标题路径和正文拼成模型输入，只去除完全相同的部分。
// 输出是检索表示，不用于计算原文坐标。
func BuildSearchText(title, header, body string) string {
	parts := make([]string, 0, 3)
	for _, value := range []string{title, header, body} {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(parts, value) {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Title 根据配置依次寻找文档标题。
func (b SearchTextBuilder) Title(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	keys := b.TitleKeys

	if len(keys) == 0 {
		keys = DefaultSearchTextBuilder().TitleKeys
	}

	for _, key := range keys {
		if value := metadataString(doc.MetaData, key); value != "" {
			return value
		}
	}

	return ""
}

// ContextHeader 返回 Chunk Breadcrumb。
func (b SearchTextBuilder) ContextHeader(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	key := b.ContextHeaderKey

	if key == "" {
		key = DefaultSearchTextBuilder().ContextHeaderKey
	}

	return metadataString(doc.MetaData, key)
}

// metadataString 只接受字符串元数据，避免把复杂对象意外格式化成检索文本。
func metadataString(metadata map[string]any, key string) string {
	if key == "" {
		return ""
	}
	text, _ := metadata[key].(string)
	return strings.TrimSpace(text)
}
