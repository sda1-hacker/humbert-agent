package chunker

import "testing"

// TestProtectedSpans 验证几种核心 Markdown 结构
// 都能够被识别为 Protected Span。
func TestProtectedSpans(t *testing.T) {

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "LaTeX块公式",

			text: "前文\n$$\nE = mc^2\n$$\n后文",

			want: "$$\nE = mc^2\n$$",
		},

		{
			name: "Markdown图片",

			text: "前文 ![架构图](images/arch.png) 后文",

			want: "![架构图](images/arch.png)",
		},

		{
			name: "Markdown链接",

			text: "访问 [OpenAI](https://openai.com) 获取详情",

			want: "[OpenAI](https://openai.com)",
		},

		{
			name: "InlineCode",

			text: "请执行 `go test ./...` 运行测试",

			want: "`go test ./...`",
		},

		{
			name: "FencedCode",

			text: "前文\n```go\nfmt.Println(\"hello\")\n```\n后文",

			want: "```go\nfmt.Println(\"hello\")\n```",
		},

		{
			name: "MarkdownTableRow",

			text: "前文\n| 东京 | 12000 |\n后文",

			want: "| 东京 | 12000 |\n",
		},
	}

	for _, tt := range tests {

		t.Run(
			tt.name,
			func(t *testing.T) {

				spans :=
					protectedSpans(
						tt.text,
					)

				if len(spans) == 0 {
					t.Fatalf(
						"没有找到 Protected Span",
					)
				}

				found := false

				for _, s := range spans {

					got :=
						tt.text[s.start:s.end]

					if got == tt.want {
						found = true
						break
					}
				}

				if !found {
					t.Fatalf(
						"没有找到期望的 Protected Span\nwant=%q\nspans=%v",
						tt.want,
						spans,
					)
				}
			},
		)
	}
}

// TestProtectedSpansImageDoesNotProduceNestedLink
//
// Markdown Image：
//
//	![图片](a.png)
//
// 内部同时存在：
//
//	[图片](a.png)
//
// 普通 Link pattern 也可能命中。
//
// 但最终必须去掉重叠，
// 只保留完整 Image Span。
func TestProtectedSpansImageDoesNotProduceNestedLink(
	t *testing.T,
) {

	text :=
		"![图片](a.png)"

	spans :=
		protectedSpans(text)

	if len(spans) != 1 {
		t.Fatalf(
			"图片应该最终只有一个 Protected Span, got=%d",
			len(spans),
		)
	}

	got :=
		text[spans[0].start:spans[0].end]

	if got != text {
		t.Fatalf(
			"应该保护完整图片引用: want=%q got=%q",
			text,
			got,
		)
	}
}

// TestProtectedSpansRune
// 专门验证中文情况下 byte offset → rune offset 转换。
func TestProtectedSpansRune(t *testing.T) {

	prefix :=
		"这是中文前缀"

	code :=
		"`go test`"

	text :=
		prefix +
			code +
			"后文"

	byteSpans :=
		protectedSpans(text)

	if len(byteSpans) != 1 {
		t.Fatalf(
			"预期一个 Protected Span, got=%d",
			len(byteSpans),
		)
	}

	runeSpans :=
		protectedSpansRune(
			text,
			byteSpans,
		)

	if len(runeSpans) != 1 {
		t.Fatalf(
			"预期一个 Rune Span",
		)
	}

	wantStart :=
		RuneLen(prefix)

	wantEnd :=
		wantStart +
			RuneLen(code)

	if runeSpans[0].start != wantStart {
		t.Fatalf(
			"Rune start 错误: want=%d got=%d",
			wantStart,
			runeSpans[0].start,
		)
	}

	if runeSpans[0].end != wantEnd {
		t.Fatalf(
			"Rune end 错误: want=%d got=%d",
			wantEnd,
			runeSpans[0].end,
		)
	}
}
