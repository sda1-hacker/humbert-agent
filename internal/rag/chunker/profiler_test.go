package chunker

import (
	"math"
	"testing"
)

func TestProfileDocumentMarkdownHeadings(t *testing.T) {
	text := `# 产品手册

## 安装

安装说明。

## 配置

配置说明。

## 部署

部署说明。
`

	profile := ProfileDocument(text)

	if profile.MdHeadingTotal != 4 {
		t.Fatalf("Heading 总数错误: want=4 got=%d", profile.MdHeadingTotal)
	}

	if profile.MdHeadingCounts[1] != 1 {
		t.Fatalf("H1 数量错误: want=1 got=%d", profile.MdHeadingCounts[1])
	}

	if profile.MdHeadingCounts[2] != 3 {
		t.Fatalf("H2 数量错误: want=3 got=%d", profile.MdHeadingCounts[2])
	}

	if profile.DominantHeadingLevel() != 2 {
		t.Fatalf("主 Heading Level 应该为 H2, got=%d", profile.DominantHeadingLevel())
	}
}

func TestDominantHeadingLevelFallbackToDeepest(t *testing.T) {
	profile := &DocProfile{
		MdHeadingTotal: 3,
		MdHeadingCounts: map[int]int{
			1: 1,
			2: 2,
		},
	}

	// 没有任何 Level 达到3次，
	// 所以选择最深出现过的 H2。
	if got := profile.DominantHeadingLevel(); got != 2 {
		t.Fatalf("应该选择最深 Heading H2: got=%d", got)
	}
}

func TestHeadingDensity(t *testing.T) {
	profile := &DocProfile{
		TotalLines:     100,
		MdHeadingTotal: 5,
	}

	got := profile.HeadingDensity()

	if math.Abs(got-0.05) > 0.000001 {
		t.Fatalf("HeadingDensity 错误: want=0.05 got=%f", got)
	}
}

func TestProfileDocumentHeuristicMarkers(t *testing.T) {
	text := `第一章 总则

1.1 适用范围

INTRODUCTION

---

Page 1 of 10

第二章 权限管理

2.1 用户权限
`

	profile := ProfileDocument(text)

	if profile.ChineseChapterCount != 2 {
		t.Fatalf("中文章节数量错误: want=2 got=%d", profile.ChineseChapterCount)
	}

	if profile.NumberedSectionCount != 2 {
		t.Fatalf("编号章节数量错误: want=2 got=%d", profile.NumberedSectionCount)
	}

	if profile.AllCapsShortLineCount != 1 {
		t.Fatalf("全大写标题数量错误: want=1 got=%d", profile.AllCapsShortLineCount)
	}

	if profile.VisualSepCount != 1 {
		t.Fatalf("视觉分隔线数量错误: want=1 got=%d", profile.VisualSepCount)
	}

	if profile.RepeatedFooterCount != 1 {
		t.Fatalf("页脚数量错误: want=1 got=%d", profile.RepeatedFooterCount)
	}

	if profile.HeuristicMarkerTotal() != 6 {
		t.Fatalf("HeuristicMarkerTotal 错误: want=6 got=%d", profile.HeuristicMarkerTotal())
	}
}

func TestProfileDocumentCode(t *testing.T) {
	text := `# Go 示例

这是普通正文。

` + "```go" + `
## 这里不是 Markdown Heading
func main() {
    println("hello")
}
` + "```" + `

## 真实标题

正文。
`

	profile := ProfileDocument(text)

	if !profile.HasCode {
		t.Fatal("应该检测到代码块")
	}

	// 代码块中的：
	//
	//     ## 这里不是 Markdown Heading
	//
	// 不能计入 Heading。
	//
	// 真正的只有：
	//
	//     # Go 示例
	//     ## 真实标题
	if profile.MdHeadingTotal != 2 {
		t.Fatalf("代码块内部 Heading 不应计入统计: got=%d", profile.MdHeadingTotal)
	}

	if profile.CodeRatio <= 0 {
		t.Fatalf("CodeRatio 应大于0: got=%f", profile.CodeRatio)
	}
}

func TestProfileDocumentTable(t *testing.T) {
	text := `普通正文

| 城市 | 标准 |
| --- | --- |
| 东京 | 12000 |
`

	profile := ProfileDocument(text)

	if !profile.HasTables {
		t.Fatal("应该检测到 Markdown Table")
	}
}

func TestProfileDocumentMixedLanguage(t *testing.T) {
	// DetectLanguage 当前判断 mixed 的条件是：
	//
	//     CJK 占比 >= 15%
	//     Latin 占比 >= 15%
	//
	// 所以测试文本必须确保中英文两种脚本都有足够占比。
	//
	// 注意：
	// 我们测试的是当前 WeKnora 的实际规则，
	// 而不是凭直觉认为“同时出现中文和英文就一定是 mixed”。
	text := `
这是一个中文技术文档，用于介绍 RAG 系统的基本工作流程。
This document explains vector search, BM25 and embedding retrieval.
系统会先解析文档，然后进行分块、向量化以及混合检索。
`

	profile := ProfileDocument(text)

	want := []string{
		LangEnglish,
		LangGerman,
		LangChinese,
	}

	if len(profile.DetectedLangs) != len(want) {
		t.Fatalf(
			"Mixed 文档语言数量错误: want=%v got=%v",
			want,
			profile.DetectedLangs,
		)
	}

	for i := range want {
		if profile.DetectedLangs[i] != want[i] {
			t.Fatalf(
				"Mixed Languages 错误: want=%v got=%v",
				want,
				profile.DetectedLangs,
			)
		}
	}
}

func TestProfileDocumentEmpty(t *testing.T) {
	profile := ProfileDocument("")

	if profile == nil {
		t.Fatal("空文本也应该返回非 nil Profile")
	}

	if profile.TotalChars != 0 {
		t.Fatalf("空文档 TotalChars 应为0: got=%d", profile.TotalChars)
	}

	if profile.MdHeadingCounts == nil {
		t.Fatal("MdHeadingCounts 应初始化为空 map")
	}

	// 注意：
	//
	// 空文档在 ProfileDocument 最前面就 return，
	// 所以 DetectedLangs 当前应该保持 nil。
	if profile.DetectedLangs != nil {
		t.Fatalf("空文档 DetectedLangs 应为 nil: got=%v", profile.DetectedLangs)
	}
}

func TestProfileDocumentRuneCount(t *testing.T) {
	text := "中国Go"

	profile := ProfileDocument(text)

	if profile.TotalChars != 4 {
		t.Fatalf("TotalChars 必须使用 rune: want=4 got=%d", profile.TotalChars)
	}
}
