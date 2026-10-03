package chunker

import "regexp"

// Heuristic Splitter 后面会把不同结构信号当作候选分割边界。
//
// 不同信号的可信度并不一样，因此 WeKnora 为它们定义了优先级。
//
// 例如：
//
//	\f              PDF 换页符
//
// 通常比：
//
//	----------------
//
// 这种视觉分隔线更能说明“这里真的进入了一个新的结构区域”。
//
// 当前这一阶段 Profiler 主要负责统计这些信号，
// 真正使用 priority 进行边界选择会在 Heuristic Splitter 阶段实现。
const (
	PrioFormFeed       = 100
	PrioNumberedHead   = 90
	PrioChapterMarker  = 85
	PrioAllCapsHeading = 70
	PrioVisualSep      = 60
	PrioPageFooter     = 50
	PrioBlankBlock     = 40
)

// MarkdownHeadingPattern 匹配 ATX 风格 Markdown Heading。
//
// 可以匹配：
//
//	# 一级标题
//	## 二级标题
//	### 三级标题
//
// 也支持 Markdown 中合法的尾部 #：
//
//	## 配置 ##
//
// Capture Group：
//
//	m[1] = "##"
//	m[2] = "配置"
//
// 后面通过 len(m[1]) 就可以得到 Heading Level。
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

// NumberedSectionPattern 匹配带编号的小节标题。
//
// 可以匹配：
//
//  1. Introduction
//  2. Methods
//     IV. Results
//     1.1 Installation
//     2.3.1 用户权限
//
// 多级编号：
//
//	1.1
//	1.2.3
//
// 最多支持四层数字结构。
//
// 最后的标题文本限制在大约 200 字符以内，
// 防止把很长的正文行错误识别成章节标题。
var NumberedSectionPattern = regexp.MustCompile(
	`(?m)^[ \t]*(?:\d+(?:\.\d+){1,3}\.?|(?:\d+|[IVX]{1,5})\.)[ \t]+\S.{0,200}$`,
)

// AllCapsHeadingPattern 匹配全大写短标题。
//
// 例如：
//
//	INTRODUCTION
//	SYSTEM ARCHITECTURE
//	INSTALLATION:
//
// 很多 PDF 或纯文本并没有 Markdown #，
// 但是章节标题会通过全大写来表示。
//
// 要求：
//
//	以大写字母开头
//	至少有一定长度
//	最大约 80 字符
//
// 同时支持德语 Ä / Ö / Ü。
var AllCapsHeadingPattern = regexp.MustCompile(
	`(?m)^[ \t]*([A-ZÄÖÜ][A-ZÄÖÜ \-]{3,80}):?\s*$`,
)

// VisualSeparatorPattern 匹配视觉分隔线。
//
// 例如：
//
//	---
//	=====
//	*****
//	______
//
// 注意：
//
// Markdown 中 "---" 也可能是 Horizontal Rule。
// 对 Heuristic Splitter 来说，同样可以视为一个潜在章节边界。
var VisualSeparatorPattern = regexp.MustCompile(
	`(?m)^[ \t]*(?:-{3,}|={3,}|\*{3,}|_{3,})[ \t]*$`,
)

// ExcessiveBlanksPattern 匹配连续三个或更多换行。
//
// 例如：
//
//	第一部分
//
//
//	第二部分
//
// 这种空白通常意味着比普通 "\n\n" 更强的章节断点。
var ExcessiveBlanksPattern = regexp.MustCompile(`\n{3,}`)

// PageFooterPattern 匹配常见页码 / 页脚形式。
//
// 例如：
//
//	Page 3
//	Page 3 of 10
//	Seite 3 von 10
//	页码 3
//	页 3 / 10
//
// (?mi)：
//
//	m = 多行
//	i = 大小写不敏感
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

// ChineseChapterPattern 匹配中文章节标题。
//
// 支持：
//
//	第一章
//	第一章 总则
//	第3节
//	第 3 节
//	第二部分
//	第十篇
//
// 同时兼容简体“节”和繁体“節”。
var ChineseChapterPattern = regexp.MustCompile(
	`(?m)^[ \t]*第[ \t]*[一二三四五六七八九十百千零〇0-9]+[ \t]*(?:章|节|節|部分|篇)[ \t]?.{0,200}$`,
)

// SentenceSeparators 返回不同语言适合使用的句子级分隔符。
//
// 当前 Profiler 不会调用它，
// 但 Heading / Heuristic Splitter 后面都会用到。
//
// 中文：
//
//	。！？；
//
// 英文 / 德文：
//
//	". "
//	"! "
//	"? "
//	"; "
//
// 为什么英文后面要求空格？
//
// 因为直接使用 "." 很容易把：
//
//	v1.2.3
//	3.14159
//	foo.bar
//
// 错误当成一句话结束。
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

// ChapterPatternsForLangs 根据语言提示选择章节规则。
//
// 例如：
//
//	Languages = []string{"zh"}
//
// 就只需要检测：
//
//	ChineseChapterPattern
//
// 而不需要再尝试英文和德文规则。
//
// 如果 languages 为空或者都是未知语言，
// 则返回所有规则，保证自动模式不会漏掉结构。
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
