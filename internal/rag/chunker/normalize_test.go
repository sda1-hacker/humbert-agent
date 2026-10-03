package chunker

import "testing"

// TestNormalizeLineEndingsCRLF
// Windows CRLF 应转换成标准 LF。
func TestNormalizeLineEndingsCRLF(t *testing.T) {
	input := "第一行\r\n第二行\r\n第三行"

	got := NormalizeLineEndings(input)

	want := "第一行\n第二行\n第三行"

	if got != want {
		t.Fatalf(
			"CRLF 归一化失败\nwant=%q\ngot =%q",
			want,
			got,
		)
	}
}

// TestNormalizeLineEndingsCR
// 独立 CR 也应该转换成 LF。
func TestNormalizeLineEndingsCR(t *testing.T) {
	input := "第一行\r第二行\r第三行"

	got := NormalizeLineEndings(input)

	want := "第一行\n第二行\n第三行"

	if got != want {
		t.Fatalf(
			"CR 归一化失败\nwant=%q\ngot =%q",
			want,
			got,
		)
	}
}

// TestNormalizeLineEndingsLF
// 已经是标准 LF 的字符串不应该发生变化。
func TestNormalizeLineEndingsLF(t *testing.T) {
	input := "第一行\n第二行\n第三行"

	got := NormalizeLineEndings(input)

	if got != input {
		t.Fatalf(
			"LF 文本不应该发生变化\nwant=%q\ngot =%q",
			input,
			got,
		)
	}
}

// TestRuneLen
// 验证我们的长度单位确实是 Unicode rune，
// 而不是 UTF-8 byte。
func TestRuneLen(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{
			name: "纯中文",
			text: "中国",
			want: 2,
		},
		{
			name: "英文",
			text: "hello",
			want: 5,
		},
		{
			name: "中英文混合",
			text: "中国Go",
			want: 4,
		},
		{
			name: "日文",
			text: "東京",
			want: 2,
		},
		{
			name: "空字符串",
			text: "",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(
			tt.name,
			func(t *testing.T) {
				got := RuneLen(tt.text)

				if got != tt.want {
					t.Fatalf(
						"RuneLen(%q) 错误: want=%d got=%d",
						tt.text,
						tt.want,
						got,
					)
				}
			},
		)
	}
}
