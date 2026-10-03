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

// computeOverlap 从上一 Chunk 的 current 中
// 选择应该保留到下一 Chunk 的语义尾部。
// 参数：
//
//	current
//	    上一个 Chunk 当前包含的 Unit
//
//	chunkOverlap
//	    配置的最大 overlap
//
//	chunkSize
//	    Chunk 总预算
//
//	nextLen
//	    下一条即将加入 Unit 的长度
//
// 为什么需要 nextLen？
//
// 因为：
//
//	Overlap + NextUnit
//
// 也必须尽量不超过 ChunkSize。
func computeOverlap(current []splitUnit, chunkOverlap int, chunkSize int, nextLen int) ([]splitUnit, int) {

	if chunkOverlap <= 0 {
		return nil, 0
	}

	// -------------------------------------------------------------
	// overlap 自己也占下一 Chunk 的空间。
	//
	// 假设：
	//
	//	ChunkSize = 512
	//	Overlap   = 80
	//	NextUnit  = 480
	//
	// 那显然不能：
	//
	//	80 + 480 = 560
	//
	// 所以 overlap 最多只能：
	//
	//	512 - 480 = 32
	// -------------------------------------------------------------

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

	// -------------------------------------------------------------
	// 真正允许 overlap 的窗口开始位置。
	//
	// 例如：
	//
	//	window 总长 = 84
	//	maxOverlap = 80
	//
	// 前 4 个字符属于：
	//
	//	lookbehind
	//
	// 我们可以利用它识别边界，
	// 但最终 overlap 绝不能超过 80。
	// -------------------------------------------------------------

	originalWindowStart := RuneLen(windowText) - maxOverlap

	if originalWindowStart < 0 {
		originalWindowStart = 0
	}

	// -------------------------------------------------------------
	// 找：
	//
	//	paragraph
	//	line
	//	sentence
	//
	// 语义边界。
	// -------------------------------------------------------------

	boundaryEnd, ok := findSemanticOverlapBoundaryEndingAtOrAfter(windowText, originalWindowStart)

	if !ok {
		// 没有合理语义边界：
		// 不 overlap。
		// 这正是和：
		//	直接复制最后80字
		// 最大的区别。
		return nil, 0
	}

	// -------------------------------------------------------------
	// overlap 从 separator 后面开始。
	//
	// 例如：
	//
	//	第一段。\n\n第二段正文
	//	       ↑
	//
	// 选中 paragraph boundary 后，
	//
	// overlap：
	//
	//	第二段正文
	// -------------------------------------------------------------

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

// semanticOverlapWindow 从 current 尾部截取最多 maxLen rune。
//
// 这里有一个非常重要的能力：
//
// 可以从某个 splitUnit 的“中间”开始截。
//
// 例如 current 最后一个 Unit 是：
//
//	一整段 300 rune 的普通文本
//
// overlap 只有 80。
//
// 不能因为：
//
//	Unit 300 > 80
//
// 就完全没有 overlap。
//
// 应该截取该 Unit 最后 80 rune，
// 然后再寻找语义边界。
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

		// ---------------------------------------------------------
		// Synthetic Unit 是硬屏障。
		//
		// 后面 Table Header 自动补全时，
		// 会创建：
		//
		//	start == end
		//
		// 但是：
		//
		//	text != ""
		//
		// 的 Unit。
		//
		// 它并不真正来自原文，
		// 不能跨过它继续寻找 source-backed overlap。
		//
		// 另外：
		//
		//	end - start != RuneLen(text)
		//
		// 也意味着它不是一个纯原文 Unit。
		// ---------------------------------------------------------

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

// findSemanticOverlapBoundaryEndingAtOrAfter
// 在文本中选择最佳 Semantic Boundary。
//
// 规则：
//
// 优先级：
//
//  1. paragraph
//  2. line
//  3. sentence
//
// 同一优先级：
//
//	选择最靠前的那个。
//
// 为什么是“最靠前”？
//
// 因为 boundary 越靠前：
//
//	保留下来的尾部越长
//
// 在不超过 overlap limit 的前提下，
// 尽量保留更多上下文。
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

	// -------------------------------------------------------------
	// Semantic boundary 不能位于 Protected Span 内。
	//
	// 例如：
	//
	//	`foo. bar`
	//
	// "." 虽然看起来是英文句号，
	// 但它位于 inline code 里面，
	// 不能作为 overlap 边界。
	// -------------------------------------------------------------

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

	// boundary 后面必须还有真正内容。
	//
	// 如果 separator 已经处于文本最后：
	//
	//	xxxx。
	//	     ↑
	//
	// 那 overlap 会是空文本，
	// 没有意义。
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

	// -------------------------------------------------------------
	// Priority 1：
	//
	// Paragraph Break
	//
	//	\n\n
	//
	// 或：
	//
	//	\r\n\r\n
	// -------------------------------------------------------------

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

	// -------------------------------------------------------------
	// Priority 2：
	//
	// Line Break
	//
	// 已经属于 paragraph break 的 newline
	// 不再重复作为低优先级候选。
	// -------------------------------------------------------------

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

	// -------------------------------------------------------------
	// Priority 3：
	//
	// Sentence Ending
	//
	// 中文：
	//
	//	。
	//	？
	//	！
	//
	// 英文：
	//
	//	". "
	//	"? "
	//	"! "
	//
	// 英文必须要求后面存在空格，
	// 避免把：
	//
	//	v1.2.3
	//	foo.bar
	//
	// 里的 "." 错当成句末。
	// -------------------------------------------------------------

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
