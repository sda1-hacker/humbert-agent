package chunker

import (
	"unicode"
	"unicode/utf8"
)

// 语言标识用于切分规则与近似 token 预算，仅区分当前支持的语言和混合文本。
const (
	LangEnglish = "en"
	LangGerman  = "de"
	LangChinese = "zh"
	LangMixed   = "mixed"
)

// charsPerToken 是语言相关的近似字符比例，不是模型的真实分词结果。
var charsPerToken = map[string]float64{
	LangEnglish: 4.0,
	LangGerman:  4.5,
	LangChinese: 1.7,
	LangMixed:   3.0,
}

// ApproxTokenCount 根据字符数量与语言比例近似估算 token 数。
func ApproxTokenCount(text, lang string) int {
	if text == "" {
		return 0
	}

	return ApproxTokenCountFromRuneLen(utf8.RuneCountInString(text), lang)
}

// ApproxTokenCountFromRuneLen 使用已知字符数估算 token，避免重复扫描正文。
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

// DetectLanguage 根据字符分布粗略判断语言，用于切分和预算估算。
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

// hasGermanWords 只扫描前 512 字节的常见德语词，作为拉丁文本的辅助语言信号。
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

// containsLower 执行 ASCII 大小写不敏感查找，不创建全文小写副本。
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

// CharsForTokenLimit 将近似 token 预算换算成字符预算。
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
