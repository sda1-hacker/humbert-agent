package chunker

import (
	"strings"
	"unicode/utf8"
)

// NormalizeLineEndings 统一文档中的换行符
// 不同来源产生的文本可能使用不同换行规则，windows \r\n，早期的 mac \r，linux/unix \n
func NormalizeLineEndings(text string) string {
	// 绝大多数正常文本本来就是 \n。
	//
	// 先做这个快速判断，可以避免没有 \r 时
	// 创建多余的新字符串。
	if !strings.Contains(text, "\r") {
		return text
	}

	text = strings.ReplaceAll(text, "\r\n", "\n")

	return strings.ReplaceAll(text, "\r", "\n")
}

// RuneLen 返回字符串中的 Unicode rune 数量。
// 因为后面的 Chunker 中会非常频繁地计算比如：ChunkSize、Start、End、Protected Span、Overlap, 所以我们需要提供一个这样的工具
func RuneLen(text string) int {
	return utf8.RuneCountInString(text)
}
