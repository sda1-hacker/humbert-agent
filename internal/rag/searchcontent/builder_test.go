package searchcontent

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestBuilderTitleHeaderBody(t *testing.T) {
	builder := DefaultBuilder()

	doc := &schema.Document{
		Content: "东京地区住宿标准为每晚 12000 日元。",
		MetaData: map[string]any{
			"title":              "员工差旅制度",
			"rag_context_header": "# 差旅制度\n## 日本地区",
		},
	}

	got := builder.Build(doc)

	want := "员工差旅制度\n\n" +
		"# 差旅制度\n## 日本地区\n\n" +
		"东京地区住宿标准为每晚 12000 日元。"

	if got != want {
		t.Fatalf("SearchContent 错误\nwant=%q\ngot =%q", want, got)
	}
}

func TestBuilderTitleFallback(t *testing.T) {
	builder := DefaultBuilder()

	doc := &schema.Document{
		Content: "正文。",
		MetaData: map[string]any{
			"_title":    "Loader Title",
			"file_name": "manual.pdf",
		},
	}

	if got := builder.Title(doc); got != "Loader Title" {
		t.Fatalf("应该优先使用 _title: got=%q", got)
	}
}

func TestBuilderFileNameFallback(t *testing.T) {
	builder := DefaultBuilder()

	doc := &schema.Document{
		Content: "正文。",
		MetaData: map[string]any{
			"file_name": "manual.pdf",
		},
	}

	if got := builder.Title(doc); got != "manual.pdf" {
		t.Fatalf("应该 fallback 到 file_name: got=%q", got)
	}
}

func TestBuilderWithoutMetadata(t *testing.T) {
	builder := DefaultBuilder()

	doc := &schema.Document{
		Content: "  只有正文。  ",
	}

	if got := builder.Build(doc); got != "只有正文。" {
		t.Fatalf("只有正文时 SearchContent 错误: %q", got)
	}
}

func TestBuilderAvoidsExactDuplicate(t *testing.T) {
	builder := DefaultBuilder()

	doc := &schema.Document{
		Content: "完全一样",
		MetaData: map[string]any{
			"title": "完全一样",
		},
	}

	if got := builder.Build(doc); got != "完全一样" {
		t.Fatalf("完全相同部分应该去重: %q", got)
	}
}

func TestBuilderIgnoresNonStringMetadata(t *testing.T) {
	builder := DefaultBuilder()

	doc := &schema.Document{
		Content: "正文。",
		MetaData: map[string]any{
			"title": []string{"错误", "类型"},
		},
	}

	if got := builder.Build(doc); got != "正文。" {
		t.Fatalf("非 string metadata 不应该进入 SearchContent: %q", got)
	}
}

func TestBuilderNilDocument(t *testing.T) {
	builder := DefaultBuilder()

	if got := builder.Build(nil); got != "" {
		t.Fatalf("nil Document 应返回空字符串: %q", got)
	}
}
