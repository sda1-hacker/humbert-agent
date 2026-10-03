package chunker

import (
	"regexp"
	"sort"
	"unicode/utf8"
)

// span 表示文本中的一个区间。
// 代码块、表格、latex 公式、链接、图片引用这些内容
// span 本身并不规定 start/end 到底是：byte offset 还是 rune offset
// 要根据具体函数语义判断。
type span struct {
	start int
	end   int
}

// protectedPatterns， protected span 的正则匹配规则
var protectedPatterns = []*regexp.Regexp{
	// 1. latex 公式
	regexp.MustCompile(`(?s)\$\$.*?\$\$`),

	// 2. markdown 图片引用
	regexp.MustCompile(
		`!\[[^\]\n]{0,200}\]\([^)\n]{1,500}\)`,
	),

	// 3. markdown 链接
	regexp.MustCompile(
		`\[[^\]\n]{1,200}\]\([^)\n]{1,500}\)`,
	),

	// 4. Markdown 表格 Header + Separator
	// 例如：
	//     | 城市 | 标准 |
	//     | --- | --- |
	// 这两行应当作为一个完整区域。
	regexp.MustCompile(
		"(?m)[ ]*(?:\\|[^|\\n]*)+\\|[\\r\\n]+" +
			"\\s*(?:\\|\\s*:?-{3,}:?\\s*)+\\|[\\r\\n]+",
	),

	// 5. Markdown 普通表格行
	regexp.MustCompile(
		"(?m)[ ]*(?:\\|[^|\\n]*)+\\|[\\r\\n]+",
	),

	// 6. fenced code block 代码块
	// 例如：
	//     ```go
	//     fmt.Println("hello")
	//     ```
	regexp.MustCompile(
		"(?s)```(?:\\w+)?[\\r\\n].*?```",
	),

	// 7. Markdown inline code 代码行
	// 例如：
	//     `go test ./...`
	regexp.MustCompile(
		"`[^`\\r\\n]+`",
	),
}

// protectedSpans 扫描文本，找到所有不能轻易拆分的区域 --> 找到文本中所有的 protected span
// Go regexp.FindAllStringIndex() 返回的是：byte offset, 而不是rune offset
// 所以这个函数返回的 span 也是 byte offset。
// 如果需要进入 Chunker 的 rune 坐标系统，
// 则调用 protectedSpansRune()。
func protectedSpans(text string) []span {
	// match 是内部临时结构。
	type match struct {
		start int
		end   int
	}

	var all []match

	// ---------------------------------------------------------------------
	// 每一种 Protected Pattern 都独立扫描一次。
	// ---------------------------------------------------------------------

	for _, pattern := range protectedPatterns {
		locations := pattern.FindAllStringIndex(text, -1)

		for _, loc := range locations {
			if len(loc) != 2 {
				continue
			}

			if loc[1]-loc[0] <= 0 {
				continue
			}

			all = append(
				all,
				match{
					start: loc[0],
					end:   loc[1],
				},
			)
		}
	}

	if len(all) == 0 {
		return nil
	}

	// ---------------------------------------------------------------------
	// 同一个区域可能同时命中多个 Pattern。
	// 例如：
	//     ![图片](abc.png)
	// 既可能命中：
	//     Markdown Image
	// 内部：
	//     [图片](abc.png)
	// 又可能命中普通 Markdown Link。
	// 所以需要处理重叠。
	// 排序规则：
	//     第一优先：start 越小越前
	//     第二优先：如果 start 相同，长度越长越前
	// 这样大范围保护块会优先保留。
	// ---------------------------------------------------------------------
	sort.Slice(
		all,
		func(i, j int) bool {
			if all[i].start != all[j].start {
				return all[i].start < all[j].start
			}

			lenI := all[i].end - all[i].start
			lenJ := all[j].end - all[j].start

			return lenI > lenJ
		},
	)

	// ---------------------------------------------------------------------
	// 去掉重叠区域。
	// 假设：
	//     A = [0, 30)
	//     B = [1, 29)
	// A 已经保护整个区域，
	// B 没有必要再保留。
	// ---------------------------------------------------------------------
	result := make(
		[]span,
		0,
		len(all),
	)

	lastEnd := 0

	for _, item := range all {
		if item.start < lastEnd {
			// 和前一个已经保留的 Protected Span 重叠。
			continue
		}

		result = append(
			result,
			span{
				start: item.start,
				end:   item.end,
			},
		)

		lastEnd = item.end
	}

	return result
}

// protectedSpansRune 把 byte offset 的 Protected Span 转成 rune offset
func protectedSpansRune(
	text string,
	byteSpans []span) []span {

	if len(byteSpans) == 0 {
		return nil
	}

	result := make(
		[]span,
		0,
		len(byteSpans),
	)

	// 当前已经扫描到的 rune 位置。
	runeIndex := 0

	// 当前已经扫描到的 byte 位置。
	byteIndex := 0

	for _, item := range byteSpans {
		// 先扫描到 Protected Span 的开始位置。
		for byteIndex < item.start && byteIndex < len(text) {
			_, size := utf8.DecodeRuneInString(text[byteIndex:])
			byteIndex += size
			runeIndex++
		}
		startRune := runeIndex

		// 再扫描完整个 Protected Span。
		for byteIndex < item.end && byteIndex < len(text) {
			_, size := utf8.DecodeRuneInString(text[byteIndex:])
			byteIndex += size
			runeIndex++
		}

		result = append(
			result,
			span{
				start: startRune,
				end:   runeIndex,
			},
		)
	}
	return result
}
