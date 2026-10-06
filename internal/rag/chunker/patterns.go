package chunker

import "regexp"

// 结构信号按可信度分配优先级，供文档画像统计与启发式边界选择使用。
const (
	PrioFormFeed       = 100
	PrioNumberedHead   = 90
	PrioChapterMarker  = 85
	PrioAllCapsHeading = 70
	PrioVisualSep      = 60
	PrioPageFooter     = 50
	PrioBlankBlock     = 40
)

// MarkdownHeadingPattern 匹配 ATX 标题，两个捕获组分别保存井号和标题正文。
var MarkdownHeadingPattern = regexp.MustCompile(
	`(?m)^(#{1,6})\s+(.+?)\s*#*\s*$`,
)

// FormFeedPattern 匹配 ASCII Form Feed：
//
//	\f
//
// 某些 PDF → Text / OCR 工具会使用它表示分页。
// 对 Heuristic Chunking 来说，它是非常强的结构边界。
var FormFeedPattern = regexp.MustCompile(`\f`)

// NumberedSectionPattern 匹配编号小节，限制层级与标题长度以减少正文误识别。
var NumberedSectionPattern = regexp.MustCompile(
	`(?m)^[ \t]*(?:\d+(?:\.\d+){1,3}\.?|(?:\d+|[IVX]{1,5})\.)[ \t]+\S.{0,200}$`,
)

// AllCapsHeadingPattern 匹配短全大写标题，包含常见德语大写字母。
var AllCapsHeadingPattern = regexp.MustCompile(
	`(?m)^[ \t]*([A-ZÄÖÜ][A-ZÄÖÜ \-]{3,80}):?\s*$`,
)

// VisualSeparatorPattern 匹配星号、下划线和横线等视觉分隔线。
var VisualSeparatorPattern = regexp.MustCompile(
	`(?m)^[ \t]*(?:-{3,}|={3,}|\*{3,}|_{3,})[ \t]*$`,
)

// ExcessiveBlanksPattern 匹配三个或更多连续换行。
var ExcessiveBlanksPattern = regexp.MustCompile(`\n{3,}`)

// PageFooterPattern 匹配常见中英文、德语页码和页脚。
var PageFooterPattern = regexp.MustCompile(
	`(?mi)^[ \t]*(?:Seite|Page|页码?)\s+\d+(?:\s*(?:von|of|/)\s*\d+)?[ \t]*$`,
)

// GermanChapterPattern 匹配德语章节标记。
//
// 例如：
//
//	Kapitel 1: Einführung
//	Abschnitt 2. Installation
//	Teil IV Architektur
var GermanChapterPattern = regexp.MustCompile(
	`(?m)^[ \t]*(?:Kapitel|Abschnitt|Teil)\s+(?:[0-9]+|[IVX]{1,5})[\.: ].{0,200}$`,
)

// EnglishChapterPattern 匹配英文章节标记。
//
// 例如：
//
//	Chapter 1: Introduction
//	Section 2. Installation
//	Part III Architecture
var EnglishChapterPattern = regexp.MustCompile(
	`(?m)^[ \t]*(?:Chapter|Section|Part)\s+(?:[0-9]+|[IVX]{1,5})[\.: ].{0,200}$`,
)

// ChineseChapterPattern 匹配中文章、节、部分及篇名，支持简繁体与数字编号。
var ChineseChapterPattern = regexp.MustCompile(
	`(?m)^[ \t]*第[ \t]*[一二三四五六七八九十百千零〇0-9]+[ \t]*(?:章|节|節|部分|篇)[ \t]?.{0,200}$`,
)

// SentenceSeparators 返回适合各语言的句末分隔符；英文要求标点后有空格，避免误切版本号和域名。
func SentenceSeparators(lang string) []string {
	switch lang {
	case LangChinese:
		return []string{"。", "！", "？", "；", "\n"}
	case LangGerman, LangEnglish:
		return []string{". ", "! ", "? ", "; ", "\n"}
	default:
		return []string{"。", "！", "？", "；", ". ", "! ", "? ", "; ", "\n"}
	}
}

// ChapterPatternsForLangs 根据语言提示选择章节正则；未指定或无法识别时启用全部规则。
func ChapterPatternsForLangs(languages []string) []*regexp.Regexp {
	if len(languages) == 0 {
		return allChapterPatterns()
	}

	var patterns []*regexp.Regexp

	for _, lang := range languages {
		switch lang {
		case LangGerman:
			patterns = append(patterns, GermanChapterPattern)
		case LangEnglish:
			patterns = append(patterns, EnglishChapterPattern)
		case LangChinese:
			patterns = append(patterns, ChineseChapterPattern)
		}
	}

	if len(patterns) == 0 {
		return allChapterPatterns()
	}

	return patterns
}

// allChapterPatterns 统一返回所有章节匹配规则。
func allChapterPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		GermanChapterPattern,
		EnglishChapterPattern,
		ChineseChapterPattern,
	}
}
