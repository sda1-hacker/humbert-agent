package chunker

import "testing"

func TestMarkdownHeadingPattern(t *testing.T) {
	tests := []struct {
		text      string
		wantMatch bool
		wantLevel int
	}{
		{"# Title", true, 1},
		{"## Installation", true, 2},
		{"### 配置 ###", true, 3},
		{"###### Deep", true, 6},
		{"####### Too Deep", false, 0},
		{"normal text", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			match := MarkdownHeadingPattern.FindStringSubmatch(tt.text)

			if (match != nil) != tt.wantMatch {
				t.Fatalf("匹配结果错误: text=%q match=%v", tt.text, match != nil)
			}

			if match != nil && len(match[1]) != tt.wantLevel {
				t.Fatalf("Heading Level 错误: want=%d got=%d", tt.wantLevel, len(match[1]))
			}
		})
	}
}

func TestNumberedSectionPattern(t *testing.T) {
	matches := []string{
		"1. Introduction",
		"IV. Results",
		"1.1 Installation",
		"2.3.1 用户权限",
	}

	for _, text := range matches {
		if !NumberedSectionPattern.MatchString(text) {
			t.Fatalf("应该匹配编号章节: %q", text)
		}
	}

	if NumberedSectionPattern.MatchString("这是普通正文") {
		t.Fatal("普通正文不应该匹配 NumberedSectionPattern")
	}
}

func TestChapterPatterns(t *testing.T) {
	if !ChineseChapterPattern.MatchString("第一章 总则") {
		t.Fatal("中文章节识别失败")
	}

	if !ChineseChapterPattern.MatchString("第 3 节 权限管理") {
		t.Fatal("带空格的中文章节识别失败")
	}

	if !EnglishChapterPattern.MatchString("Chapter 2: Installation") {
		t.Fatal("英文章节识别失败")
	}

	if !GermanChapterPattern.MatchString("Kapitel 2: Installation") {
		t.Fatal("德文章节识别失败")
	}
}

func TestVisualSeparatorPattern(t *testing.T) {
	for _, text := range []string{"---", "=====", "*****", "______"} {
		if !VisualSeparatorPattern.MatchString(text) {
			t.Fatalf("视觉分隔线识别失败: %q", text)
		}
	}
}

func TestSentenceSeparators(t *testing.T) {
	zh := SentenceSeparators(LangChinese)

	if zh[0] != "。" {
		t.Fatalf("中文句子分隔符错误: %v", zh)
	}

	en := SentenceSeparators(LangEnglish)

	if en[0] != ". " {
		t.Fatalf("英文句子分隔符错误: %v", en)
	}
}

func TestChapterPatternsForLangs(t *testing.T) {
	patterns := ChapterPatternsForLangs([]string{LangChinese})

	if len(patterns) != 1 {
		t.Fatalf("zh 应该只返回一套章节规则, got=%d", len(patterns))
	}

	if !patterns[0].MatchString("第一章 总则") {
		t.Fatal("返回的 zh pattern 不正确")
	}

	all := ChapterPatternsForLangs(nil)

	if len(all) != 3 {
		t.Fatalf("空语言提示应该返回所有章节规则, got=%d", len(all))
	}
}
