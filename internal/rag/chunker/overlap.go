package chunker

import "strings"

// 表示在 overlap 之前观察多少个 rune
// 为什么需要 Lookbehind？
// 如果 Overlap = 80，正好在第 80 个字符之前存在 \n\n
// 如果窗口从第二个 \n 开始，就看不到完整的 "\n\n"。
// 所以向前额外看几个字符。当前最长关心 \r\n\r\n
const semanticOverlapLookbehind = 4

// semanticOverlapBoundary 表示候选语义边界。
// start/end 都是当前 overlap window 中的 rune offset。
// priority：
// 1 = paragraph
// 2 = line
// 3 = sentence
// 数字越小，优先级越高。
type semanticOverlapBoundary struct {
	start    int
	end      int
	priority int
}

// unitsText 把 splitUnit 文本按顺序拼起来。
func unitsText(units []splitUnit) string {
	var builder strings.Builder
	for _, unit := range units {
		builder.WriteString(unit.text)
	}
	return builder.String()
}

// computeOverlap 在空间预算内选择上一块的连续原文尾部，优先从语义边界开始。
func computeOverlap(current []splitUnit, chunkOverlap int, chunkSize int, nextLen int) ([]splitUnit, int) {

	if chunkOverlap <= 0 {
		return nil, 0
	}

	// 重叠长度受下一单元剩余空间限制，不能使新块超过目标大小。

	maxOverlap := chunkOverlap

	remaining := chunkSize - nextLen

	if remaining < maxOverlap {
		maxOverlap = remaining
	}

	if maxOverlap <= 0 {
		return nil, 0
	}

	// -------------------------------------------------------------
	// 获取尾部窗口。
	// 多拿 semanticOverlapLookbehind 个字符，
	// 是为了识别刚好被 window 边界截断的 separator。
	// -------------------------------------------------------------

	window := semanticOverlapWindow(current, maxOverlap+semanticOverlapLookbehind)

	if len(window) == 0 {
		return nil, 0
	}

	windowText := unitsText(window)

	// 窗口前部仅用于识别边界，最终重叠不能超过允许长度。

	originalWindowStart := RuneLen(windowText) - maxOverlap

	if originalWindowStart < 0 {
		originalWindowStart = 0
	}

	// 优先寻找段落、换行和句末边界。

	boundaryEnd, ok := findSemanticOverlapBoundaryEndingAtOrAfter(windowText, originalWindowStart)

	if !ok {
		// 没有合理语义边界：
		// 不 overlap。
		// 这正是和：
		//	直接复制最后80字
		// 最大的区别。
		return nil, 0
	}

	// 重叠从分隔符之后的真实内容开始。

	overlap := trimUnitsPrefix(window, boundaryEnd)

	overlapLen := 0

	for _, unit := range overlap {
		overlapLen += RuneLen(unit.text)
	}

	// 最后做一次防御性校验。
	if overlapLen <= 0 || overlapLen > maxOverlap || strings.TrimSpace(unitsText(overlap)) == "" {
		return nil, 0
	}

	return overlap, overlapLen
}

// semanticOverlapWindow 提取连续原文尾部；合成表头和不连续区间会阻断窗口。
func semanticOverlapWindow(current []splitUnit, maxLen int) []splitUnit {

	if maxLen <= 0 || len(current) == 0 {
		return nil
	}

	remaining := maxLen

	reversed := make([]splitUnit, 0, len(current))

	// 从最后一个 Unit 往前扫描。
	for i := len(current) - 1; i >= 0 && remaining > 0; i-- {
		unit := current[i]
		unitLen := RuneLen(unit.text)

		if unitLen == 0 {
			continue
		}

		// 合成表头或非连续原文是硬屏障，不能跨过它们建立原文重叠。

		if unit.start == unit.end || unit.end-unit.start != unitLen {
			break
		}

		if unitLen <= remaining {
			reversed = append(reversed, unit)
			remaining -= unitLen
			continue
		}

		// ---------------------------------------------------------
		// 当前 Unit 比 remaining 大。
		//
		// 只截取最后 remaining rune。
		// ---------------------------------------------------------

		runes := []rune(unit.text)

		start := unitLen - remaining

		reversed = append(
			reversed,
			splitUnit{
				text:  string(runes[start:]),
				start: unit.start + start,
				end:   unit.end,
			},
		)

		remaining = 0
	}

	if len(reversed) == 0 {
		return nil
	}

	// 刚才是从后往前取得的，
	// 现在重新恢复正常顺序。
	window := make([]splitUnit, len(reversed))

	for i := range reversed {
		window[len(reversed)-1-i] = reversed[i]
	}

	return window
}

// findSemanticOverlapBoundary 是方便测试和内部使用的入口。
func findSemanticOverlapBoundary(text string) (int, bool) {

	return findSemanticOverlapBoundaryEndingAtOrAfter(text, 0)
}

// findSemanticOverlapBoundaryEndingAtOrAfter 查找指定位置之后的段落、换行或句末边界。
func findSemanticOverlapBoundaryEndingAtOrAfter(text string, minEnd int) (int, bool) {

	runes := []rune(text)

	if len(runes) == 0 {
		return 0, false
	}

	if minEnd < 0 {
		minEnd = 0
	}

	if minEnd > len(runes) {
		return 0, false
	}

	// 代码、链接等保护区域内部不能作为重叠起点。

	protected := protectedSpansRune(text, protectedSpans(text))

	insideProtected := func(pos int) bool {
		for _, p := range protected {
			if pos < p.start {
				return false
			}

			if pos >= p.start && pos < p.end {
				return true
			}
		}

		return false
	}

	// 分隔符后须有真实内容，否则重叠为空。
	hasMeaningfulTail := func(end int) bool {
		return end >= 0 && end < len(runes) && strings.TrimSpace(string(runes[end:])) != ""
	}

	var best semanticOverlapBoundary

	found := false

	// consider 统一处理一个候选 boundary。
	consider := func(start int, end int, priority int) {
		if start < 0 || end <= start || end < minEnd || end > len(runes) || insideProtected(start) || !hasMeaningfulTail(end) {
			return
		}

		candidate := semanticOverlapBoundary{
			start:    start,
			end:      end,
			priority: priority,
		}

		if !found || candidate.priority < best.priority || (candidate.priority == best.priority && candidate.start < best.start) {
			best = candidate
			found = true
		}
	}

	// 第一优先级：段落空行。

	paragraphRune := make([]bool, len(runes))

	for i := 0; i < len(runes); i++ {
		switch {
		case i+3 < len(runes) &&
			runes[i] == '\r' &&
			runes[i+1] == '\n' &&
			runes[i+2] == '\r' &&
			runes[i+3] == '\n':

			consider(i, i+4, 1)

			for j := i; j < i+4; j++ {
				paragraphRune[j] = true
			}

			i += 3

		case i+1 < len(runes) &&
			runes[i] == '\n' &&
			runes[i+1] == '\n':

			consider(i, i+2, 1)

			paragraphRune[i] = true
			paragraphRune[i+1] = true

			i++
		}
	}

	// 第二优先级：普通换行，排除已归为段落分隔的换行。

	for i := 0; i < len(runes); i++ {

		if paragraphRune[i] {
			continue
		}

		if runes[i] == '\r' && i+1 < len(runes) && runes[i+1] == '\n' && !paragraphRune[i+1] {
			consider(i, i+2, 2)
			i++
			continue
		}

		if runes[i] == '\n' {
			consider(i, i+1, 2)
		}
	}

	// 第三优先级：句末标点；英文标点后须有空格，避免误切版本号或域名。

	for i := 0; i < len(runes); i++ {

		switch runes[i] {

		case '。', '？', '！':
			consider(i, i+1, 3)

		case '.', '?', '!':
			if i+1 < len(runes) && runes[i+1] == ' ' {
				consider(i, i+2, 3)
			}
		}
	}

	if !found {
		return 0, false
	}

	return best.end, true
}

// trimUnitsPrefix 从 []splitUnit 开头删除 prefixLen 个 source rune。
//
// 如果删除位置位于某个 Unit 中间，
// 就切掉该 Unit 的前半部分，同时修正 start。
func trimUnitsPrefix(units []splitUnit, prefixLen int) []splitUnit {

	if prefixLen <= 0 {
		result := make([]splitUnit, len(units))
		copy(result, units)
		return result
	}

	remaining := prefixLen

	result := make([]splitUnit, 0, len(units))

	for _, unit := range units {

		unitLen := RuneLen(unit.text)

		if remaining >= unitLen {
			remaining -= unitLen
			continue
		}

		if remaining > 0 {
			runes := []rune(unit.text)
			unit.text = string(runes[remaining:])
			unit.start += remaining
			remaining = 0
		}

		result = append(result, unit)
	}

	return result
}
