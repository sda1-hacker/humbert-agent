package chunker

import "testing"

// TestChunkEmbeddingContentWithoutHeader
// 测试没有 ContextHeader 时，EmbeddingContent 只返回正文。
func TestChunkEmbeddingContentWithoutHeader(t *testing.T) {
	chunk := Chunk{
		Content: "\n\n东京住宿最高不得超过12000日元。\n\n",
	}

	got := chunk.EmbeddingContent()

	want := "东京住宿最高不得超过12000日元。"

	if got != want {
		t.Fatalf(
			"EmbeddingContent 不符合预期\nwant: %q\ngot:  %q",
			want,
			got,
		)
	}
}

// TestChunkEmbeddingContentWithHeader
// 测试存在 ContextHeader 时：
//
//	Header
//	+
//	空行
//	+
//	Content
//
// 是否正确拼接。
func TestChunkEmbeddingContentWithHeader(t *testing.T) {
	chunk := Chunk{
		ContextHeader: "# 差旅制度\n## 日本地区\n### 住宿",
		Content:       "\n东京住宿最高不得超过12000日元。\n",
	}

	got := chunk.EmbeddingContent()

	want := "# 差旅制度\n## 日本地区\n### 住宿\n\n" +
		"东京住宿最高不得超过12000日元。"

	if got != want {
		t.Fatalf(
			"EmbeddingContent 不符合预期\nwant:\n%s\n\ngot:\n%s",
			want,
			got,
		)
	}
}

// TestChunkRuneOffsetInvariant
// 验证我们整个系统后面都会依赖的一个重要约定：
//
//	End - Start == RuneLen(Content)
//
// 注意：
//
// Chunk 结构体本身不会主动强制这个规则，
//
// 因为后面的表格表头补全等逻辑存在“合成内容”，
// 算法层必须自己维护正确的位置关系。
//
// 这个测试主要帮助我们从第一天建立正确认知。
func TestChunkRuneOffsetInvariant(t *testing.T) {
	content := "东京住宿标准"

	chunk := Chunk{
		Content: content,

		// 假设这段文字从原文第 10 个 rune 开始。
		Start: 10,

		End: 10 + RuneLen(content),
	}

	got := chunk.End - chunk.Start
	want := RuneLen(chunk.Content)

	if got != want {
		t.Fatalf(
			"Chunk rune offset 不满足约定: end-start=%d, content runes=%d",
			got,
			want,
		)
	}
}
