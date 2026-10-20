package chunker

import "regexp"

// NumberedSectionPattern 匹配编号小节，限制层级与标题长度以减少正文误识别。
var NumberedSectionPattern = regexp.MustCompile(`(?m)^[ \t]*(?:\d+(?:\.\d+){1,3}\.?|(?:\d+|[IVX]{1,5})\.)[ \t]+\S.{0,200}$`)

// AllCapsHeadingPattern 匹配短全大写标题，包含常见德语大写字母。
var AllCapsHeadingPattern = regexp.MustCompile(`(?m)^[ \t]*([A-ZÄÖÜ][A-ZÄÖÜ \-]{3,80}):?\s*$`)

// PageFooterPattern 匹配常见中英文、德语页码和页脚。
var PageFooterPattern = regexp.MustCompile(`(?mi)^[ \t]*(?:Seite|Page|页码?)\s+\d+(?:\s*(?:von|of|/)\s*\d+)?[ \t]*$`)

// GermanChapterPattern 匹配德语章节标记。
//
// 例如：
//
//	Kapitel 1: Einführung
//	Abschnitt 2. Installation
//	Teil IV Architektur
var GermanChapterPattern = regexp.MustCompile(`(?m)^[ \t]*(?:Kapitel|Abschnitt|Teil)\s+(?:[0-9]+|[IVX]{1,5})[\.: ].{0,200}$`)

// EnglishChapterPattern 匹配英文章节标记。
//
// 例如：
//
//	Chapter 1: Introduction
//	Section 2. Installation
//	Part III Architecture
var EnglishChapterPattern = regexp.MustCompile(`(?m)^[ \t]*(?:Chapter|Section|Part)\s+(?:[0-9]+|[IVX]{1,5})[\.: ].{0,200}$`)

// ChineseChapterPattern 匹配中文章、节、部分及篇名，支持简繁体与数字编号。
var ChineseChapterPattern = regexp.MustCompile(`(?m)^[ \t]*第[ \t]*[一二三四五六七八九十百千零〇0-9]+[ \t]*(?:章|节|節|部分|篇)[ \t]?.{0,200}$`)

// 这些是普通文本的章节、页码提示；Markdown 语法由 Goldmark 负责。
var plainPatterns = []*regexp.Regexp{NumberedSectionPattern, AllCapsHeadingPattern, GermanChapterPattern, EnglishChapterPattern, ChineseChapterPattern, PageFooterPattern}

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
	return []*regexp.Regexp{GermanChapterPattern, EnglishChapterPattern, ChineseChapterPattern}
}
