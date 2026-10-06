package chunker

import (
	"sort"
	"strings"
)

// boundary 保存结构边界的字符位置与优先级；同一位置保留最可信的信号。
type boundary struct {
	runeStart int
	priority  int
}

// splitByHeuristics 根据章节、分页和分隔线划分结构块，再按大小合并与递归拆分。
func splitByHeuristics(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
	if text == "" {
		return nil
	}

	runes := []rune(text)
	totalRunes := len(runes)

	// 短文档直接递归切分，避免因结构信号产生过多碎片。
	if totalRunes <= cfg.ChunkSize {
		return SplitText(text, cfg)
	}

	// 第一步：
	//
	// 扫描全文，找到所有可能的结构边界。
	bounds := findHeuristicBoundaries(text, cfg.Languages)

	// 仅删除严格位于代码、公式等保护区域内部的边界，保留区域两端。
	protected := protectedSpansRune(text, protectedSpans(text))
	if len(protected) > 0 {
		bounds = dropBoundsInsideSpans(bounds, protected)
	}

	// 没找到任何有效结构边界。
	//
	// 说明 Heuristic Strategy 对当前文档没有帮助，
	// 直接退回成熟的 Legacy Splitter。
	if len(bounds) == 0 {
		return SplitText(text, cfg)
	}

	// 补充文档末尾边界，让最后一段也进入统一合并循环。
	bounds = append(bounds, boundary{runeStart: totalRunes})

	// 补充文档起点，保留首个章节前的前言。
	if bounds[0].runeStart != 0 {
		bounds = append([]boundary{{runeStart: 0}}, bounds...)
	}

	var out []Chunk

	seq := 0

	// chunkStart：
	//
	// 当前正在累计的 Chunk 从哪里开始。
	chunkStart := bounds[0].runeStart

	// curEnd：
	//
	// 当前已经累计到哪个 Boundary。
	curEnd := chunkStart

	// 当前块至少积累到目标大小的四分之一或 50 字符，减少极小碎片。
	minChunkSize := cfg.ChunkSize / 4
	if minChunkSize < 50 {
		minChunkSize = 50
	}

	// 按大小逐个装入结构块，超出预算时输出当前块。

	for i := 1; i < len(bounds); i++ {
		nextEnd := bounds[i].runeStart

		// 这里检查单个结构块长度，不是累计块长度。
		blockLen := nextEnd - curEnd

		// 单个结构块过大时交给递归切分，复用保护区域与表头处理。

		if blockLen > cfg.ChunkSize {
			// 在超大 Block 前面可能已经累计了一些正常 Block。
			//
			// 先把这些内容输出。
			if curEnd-chunkStart > 0 {
				out = appendChunk(out, runes, chunkStart, curEnd, &seq)
				chunkStart = curEnd
			}

			// 单独处理这个巨大结构 Block。
			out = appendOversizeBlock(out, runes, curEnd, nextEnd, cfg, &seq)

			curEnd = nextEnd
			chunkStart = nextEnd
			continue
		}

		// accumulated 表示：
		//
		// 如果把当前 Block 也加入当前 Chunk，
		// Chunk 总长度会是多少。
		accumulated := nextEnd - chunkStart

		// 累计大小超过预算且已达到最小块大小时输出。

		if accumulated > cfg.ChunkSize && curEnd-chunkStart >= minChunkSize {
			out = appendChunk(out, runes, chunkStart, curEnd, &seq)

			// 重叠起点优先对齐结构或换行边界，避免机械截断句子。
			chunkStart = applyOverlapAligned(runes, curEnd, cfg.ChunkOverlap, bounds)
		}

		curEnd = nextEnd
	}

	// 最后还有尚未 Flush 的内容。
	if curEnd > chunkStart {
		out = appendChunk(out, runes, chunkStart, curEnd, &seq)
	}

	return out
}

// findHeuristicBoundaries 查找章节、分页、全大写标题、页脚及空白分隔信号。
func findHeuristicBoundaries(text string, languages []string) []boundary {
	var bounds []boundary

	// PDF 换页符是高优先级结构边界。

	for _, index := range allRuneIndices(text, "\f") {
		bounds = append(bounds, boundary{
			runeStart: index,
			priority:  PrioFormFeed,
		})
	}

	// -------------------------------------------------------------------------
	// 逐行扫描其他结构 Pattern。
	// -------------------------------------------------------------------------

	lines := strings.Split(text, "\n")
	chapterPatterns := ChapterPatternsForLangs(languages)

	runePos := 0
	inFence := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 代码块内的编号和章节文字不作为结构信号。
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		} else if !inFence {
			added := false

			// 根据语言启用章节规则；未指定语言时尝试全部支持的规则。

			for _, pattern := range chapterPatterns {
				if pattern.MatchString(line) {
					bounds = append(bounds, boundary{
						runeStart: runePos,
						priority:  PrioChapterMarker,
					})

					added = true
					break
				}
			}

			// 识别整数或多级编号的小节标题。

			if !added && NumberedSectionPattern.MatchString(line) {
				bounds = append(bounds, boundary{
					runeStart: runePos,
					priority:  PrioNumberedHead,
				})

				added = true
			}

			// 识别全大写短标题。

			if !added && AllCapsHeadingPattern.MatchString(line) {
				bounds = append(bounds, boundary{
					runeStart: runePos,
					priority:  PrioAllCapsHeading,
				})

				added = true
			}

			// 识别连续星号或下划线等视觉分隔线。

			if !added && VisualSeparatorPattern.MatchString(line) {
				bounds = append(bounds, boundary{
					runeStart: runePos,
					priority:  PrioVisualSep,
				})

				added = true
			}

			// 匹配中英文和德语页脚。

			if !added && PageFooterPattern.MatchString(line) {
				bounds = append(bounds, boundary{
					runeStart: runePos,
					priority:  PrioPageFooter,
				})
			}
		}

		// runePos 始终使用 Unicode rune。
		runePos += RuneLen(line)

		// strings.Split 会把 "\n" 删除，
		// 所以手动把它计回原文位置。
		if i < len(lines)-1 {
			runePos++
		}
	}

	// 连续空白后的边界转换成字符位置，下一块从实际段落开始。

	for _, location := range ExcessiveBlanksPattern.FindAllStringIndex(text, -1) {
		runeStart := RuneLen(text[:location[1]])

		bounds = append(bounds, boundary{
			runeStart: runeStart,
			priority:  PrioBlankBlock,
		})
	}

	if len(bounds) == 0 {
		return nil
	}

	// 先按位置升序、再按优先级降序排序。

	sort.Slice(bounds, func(i, j int) bool {
		if bounds[i].runeStart != bounds[j].runeStart {
			return bounds[i].runeStart < bounds[j].runeStart
		}

		return bounds[i].priority > bounds[j].priority
	})

	// 同一位置只保留优先级最高的结构信号。

	deduped := bounds[:0]
	previousOffset := -1

	for _, item := range bounds {
		if item.runeStart == previousOffset {
			continue
		}

		deduped = append(deduped, item)
		previousOffset = item.runeStart
	}

	return deduped
}

// dropBoundsInsideSpans 删除严格位于保护区域内部的边界，保留区域两端。
func dropBoundsInsideSpans(bounds []boundary, spans []span) []boundary {
	if len(spans) == 0 {
		return bounds
	}

	result := bounds[:0]

boundaryLoop:
	for _, item := range bounds {
		for _, protected := range spans {
			// 当前 Protected Span 已经从 Boundary 本身或它后面开始。
			//
			// 后面的 span 只会更靠后，
			// 所以不可能再包含当前 Boundary。
			if protected.start >= item.runeStart {
				break
			}

			// protected.start < boundary < protected.end
			//
			// Boundary 严格落在保护区域内部。
			if item.runeStart < protected.end {
				continue boundaryLoop
			}
		}

		result = append(result, item)
	}

	return result
}

// allRuneIndices 返回指定文本所有出现位置的字符偏移，供结构边界计算使用。
func allRuneIndices(text, needle string) []int {
	if needle == "" {
		return nil
	}

	var result []int

	runePos := 0

	for _, r := range text {
		if string(r) == needle {
			result = append(result, runePos)
		}

		runePos++
	}

	return result
}

// appendChunk 追加原文区间对应的分块，并更新顺序编号。
func appendChunk(out []Chunk, runes []rune, start, end int, seq *int) []Chunk {
	if end <= start {
		return out
	}

	content := string(runes[start:end])

	// Boundary 聚集时可能形成一个纯空白区间。
	//
	// 这种 Chunk 对检索没有任何意义，
	// 所以跳过。
	if strings.TrimSpace(content) == "" {
		return out
	}

	out = append(out, Chunk{
		Content: content,
		Seq:     *seq,
		Start:   start,
		End:     end,
	})

	(*seq)++

	return out
}

// appendOversizeBlock 递归拆分过大结构块，将局部坐标转换成整篇原文坐标。
func appendOversizeBlock(
	out []Chunk,
	runes []rune,
	start, end int,
	cfg SplitterConfig,
	seq *int,
) []Chunk {
	if end <= start {
		return out
	}

	subText := string(runes[start:end])
	subChunks := SplitText(subText, cfg)

	for _, sub := range subChunks {
		out = append(out, Chunk{
			Content: sub.Content,
			Seq:     *seq,

			// SplitText(subText) 得到的位置从0开始。
			//
			// 必须重新加上当前 Block 在原文中的 start，
			// 才能回到整篇文档坐标系。
			Start: start + sub.Start,
			End:   start + sub.End,
		})

		(*seq)++
	}

	return out
}

// applyOverlapAligned 计算下一块的重叠起点，优先对齐结构或换行边界，并保证前进。
func applyOverlapAligned(runes []rune, curEnd, overlap int, bounds []boundary) int {
	if overlap <= 0 {
		return curEnd
	}

	target := curEnd - overlap
	if target < 0 {
		target = 0
	}

	// 最多向前扩展到 2 倍 overlap 的范围。
	windowStart := curEnd - 2*overlap
	if windowStart < 0 {
		windowStart = 0
	}

	// 只选择当前块终点之前的边界，否则重叠会始终为空。
	bestBoundary := -1

	for _, item := range bounds {
		if item.runeStart >= windowStart &&
			item.runeStart < curEnd &&
			item.runeStart > bestBoundary {

			bestBoundary = item.runeStart
		}
	}

	if bestBoundary >= 0 {
		return bestBoundary
	}

	// 没有结构 Boundary。
	//
	// 尝试从理论 target 向前找到最近一个换行，
	// 让下一 Chunk 至少从完整行开始。
	for i := target; i > windowStart && i < len(runes); i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}

	// 实在没有任何可对齐的位置，
	// 才使用机械字符 offset。
	return target
}
