package chunker

import (
	"unicode"
	"unicode/utf8"
)

// Chunker 内部使用的粗粒度语言标识。
//
// 这里没有试图识别所有语言。
//
// 原因是这套语言检测并不是用来：
//
//	做机器翻译
//	展示“文档语言”
//
// 而是用于：
//
//  1. 选择合适的句子分隔符
//  2. 选择章节识别规则
//  3. 粗略估算 Token 数量
//
// 所以当前只关心：
//
//	英文
//	德文
//	中文/CJK
//	混合文本
const (
	LangEnglish = "en"
	LangGerman  = "de"
	LangChinese = "zh"
	LangMixed   = "mixed"
)

// charsPerToken 是每个语言大约“多少字符对应一个 Token”。
//
// 当前数值和 WeKnora 保持一致：
//
//	English  ≈ 4.0 chars/token
//	German   ≈ 4.5 chars/token
//	Chinese  ≈ 1.7 chars/token
//	Mixed    ≈ 3.0 chars/token
//
// 这里只是近似估算，不是真实 tokenizer。
//
// 我们故意不引入 tiktoken 等重量依赖，
// 因为 Chunker 只是需要一个保守的预算值，
// 不需要做到 tokenizer 级别完全精确。
var charsPerToken = map[string]float64{
	LangEnglish: 4.0,
	LangGerman:  4.5,
	LangChinese: 1.7,
	LangMixed:   3.0,
}

// ApproxTokenCount 粗略估算字符串 Token 数量。
//
// 例如：
//
//	ApproxTokenCount("hello world", LangEnglish)
//
// 实际上计算：
//
//	rune 数 / 4.0
//
// 最终四舍五入。
func ApproxTokenCount(text, lang string) int {
	if text == "" {
		return 0
	}

	return ApproxTokenCountFromRuneLen(utf8.RuneCountInString(text), lang)
}

// ApproxTokenCountFromRuneLen 是 ApproxTokenCount 的无额外 rune 扫描版本。
//
// 如果调用方已经计算过：
//
//	runeLen
//
// 就不要再次扫描字符串。
//
// 后面 Chunk Preview、统计每个 Chunk Token 时会很有用。
func ApproxTokenCountFromRuneLen(runeLen int, lang string) int {
	if runeLen <= 0 {
		return 0
	}

	ratio, ok := charsPerToken[lang]
	if !ok {
		ratio = charsPerToken[LangMixed]
	}

	approx := float64(runeLen) / ratio

	if approx < 1 {
		return 1
	}

	return int(approx + 0.5)
}

// DetectLanguage 对文本做一个“便宜但够用”的语言判断。
//
// 注意：
//
// 这不是专业 Language Identification。
//
// 它只统计两类字符：
//
//	CJK
//	Latin
//
// CJK 包含：
//
//	汉字
//	韩文
//	平假名
//	片假名
//
// 所以 LangChinese 在这里更准确地说其实是：
//
//	“CJK 类文本”
//
// 但为了和 WeKnora 保持一致，仍然命名为 zh。
//
// -----------------------------------------------------------------------------
// 判定规则：
//
// 如果 CJK 和 Latin 都 >= 15%：
//
//	mixed
//
// 如果 CJK > 30%：
//
//	zh
//
// 否则：
//
//	如果有德语 Umlaut 或常见德语功能词
//	    de
//
//	否则
//	    en
//
// -----------------------------------------------------------------------------
func DetectLanguage(text string) string {
	if text == "" {
		return LangMixed
	}

	var cjk, latin, umlaut int

	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hangul, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r):
			cjk++

		case isGermanUmlaut(r):
			umlaut++
			latin++

		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			latin++
		}
	}

	total := cjk + latin

	// 文本只有数字、标点、空白等内容。
	if total == 0 {
		return LangMixed
	}

	cjkRatio := float64(cjk) / float64(total)
	latinRatio := float64(latin) / float64(total)

	// 两种脚本都有明显占比，视为混合文本。
	if cjkRatio >= 0.15 && latinRatio >= 0.15 {
		return LangMixed
	}

	if cjkRatio > 0.3 {
		return LangChinese
	}

	if umlaut > 0 || hasGermanWords(text) {
		return LangGerman
	}

	return LangEnglish
}

// isGermanUmlaut 判断是否是典型德语字符。
func isGermanUmlaut(r rune) bool {
	switch r {
	case 'ä', 'ö', 'ü', 'Ä', 'Ö', 'Ü', 'ß':
		return true
	default:
		return false
	}
}

// hasGermanWords 做一个非常轻量的德语 Stop Word 检测。
//
// 检测的并不是：
//
//	“这篇文章是否真正是德语”
//
// 而只是：
//
//	“Latin 文本有没有明显德语信号？”
//
// 当前只扫描文本开头最多 512 byte，避免为非常大的文档
// 做没有必要的完整字符串匹配。
func hasGermanWords(text string) bool {
	const sampleSize = 512

	if len(text) > sampleSize {
		text = text[:sampleSize]
	}

	germanWords := []string{
		" der ",
		" die ",
		" das ",
		" und ",
		" ist ",
		" nicht ",
		" mit ",
		" auf ",
	}

	for _, word := range germanWords {
		if containsLower(text, word) {
			return true
		}
	}

	return false
}

// containsLower 做 ASCII 大小写不敏感查找。
//
// 为什么没有直接：
//
//	strings.Contains(strings.ToLower(...))
//
// WeKnora 当前实现刻意使用这个轻量方法，
// 避免为了几个简单 ASCII Stop Word 创建完整小写字符串。
func containsLower(haystack, needle string) bool {
	if len(haystack) < len(needle) {
		return false
	}

	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true

		for j := 0; j < len(needle); j++ {
			ch := haystack[i+j]

			if ch >= 'A' && ch <= 'Z' {
				ch += 'a' - 'A'
			}

			if ch != needle[j] {
				match = false
				break
			}
		}

		if match {
			return true
		}
	}

	return false
}

// CharsForTokenLimit 把模型 TokenLimit 转换成大致字符预算。
//
// 例如：
//
//	TokenLimit = 512
//	Language   = zh
//
// 大约：
//
//	512 * 1.7 * 0.9
//	≈ 783 chars
//
// 其中：
//
//	0.9
//
// 是 10% 安全余量。
//
// 为什么要留安全余量？
//
// 因为：
//
//	chars/token
//
// 本身就是估算值。
// 宁可 Chunk 小一点，也不要因为估算误差让 Embedding API
// 超出硬 Token Limit。
func CharsForTokenLimit(tokens int, lang string) int {
	if tokens <= 0 {
		return 0
	}

	ratio, ok := charsPerToken[lang]
	if !ok {
		ratio = charsPerToken[LangMixed]
	}

	return int(float64(tokens) * ratio * 0.9)
}
