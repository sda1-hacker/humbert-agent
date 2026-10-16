package retrieval

import (
	"slices"
	"strings"

	"github.com/cloudwego/eino/schema"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// BuildInput 根据统一配置构造模型输入；原文及其字符范围仍单独保留。
func BuildInput(builder ragconfig.SearchText, doc *schema.Document) string {
	if doc == nil {
		return ""
	}
	builder = builder.Effective()
	return BuildSearchText(Title(builder, doc), metadataString(doc.MetaData, builder.ContextHeaderKey), doc.Content)
}

// Title 按配置顺序查找标题，避免不同索引通道各自维护标题规则。
func Title(builder ragconfig.SearchText, doc *schema.Document) string {
	if doc == nil {
		return ""
	}
	for _, key := range builder.Effective().TitleKeys {
		if value := metadataString(doc.MetaData, key); value != "" {
			return value
		}
	}
	return ""
}

// BuildSearchText 只去除完全相同的标题、路径和正文；拼接结果不用于计算原文坐标。
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

// metadataString 只读取字符串，防止把任意对象格式化后混入模型输入。
func metadataString(metadata map[string]any, key string) string {
	if key == "" {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}
