package chunker

import (
	"sort"
	"strings"
)

// init 在当前文件加载时，把真正的 Heuristic Splitter
// 注册到 strategy.go 中。
//
// strategy.go 里最开始只有一个安全 fallback：
//
//	var splitByHeuristics = func(...) []Chunk {
//	    return SplitText(...)
//	}
//
// 当 heuristic_splitter.go 存在以后，真正执行：
//
//	TierHeuristic
//
// 时就会走当前实现。
func init() {
	splitByHeuristics = splitByHeuristicsImpl
}

// boundary 表示 Heuristic Splitter 识别出来的一个候选结构边界。
//
// runeStart：
//
//	这个边界在原始文档中的 Unicode rune offset。
//	最终新的结构 block 会从这里开始。
//
// priority：
//
//	边界优先级。
//
//	FormFeed       = 100
//	Numbered       = 90
//	Chapter        = 85
//	ALL CAPS       = 70
//	Visual         = 60
//	Page Footer    = 50
//	Blank Block    = 40
//
// 如果两个不同规则恰好在同一个 rune offset 上产生边界，
// 最终只保留 priority 更高的那一个。
type boundary struct {
	runeStart int
	priority  int
}

// splitByHeuristicsImpl 是 Heuristic Tier 的真正实现。
//
// Heuristic Splitter 主要处理这种文档：
//
//	第一章 总则
//
//	1.1 适用范围
//
//	正文……
//
//	1.2 定义
//
//	正文……
//
// 或：
//
//	CHAPTER 1
//
//	正文……
//
//	PART II
//
//	正文……
//
// 或者 PDF/OCR 产生的：
//
//	Page 1 of 10
//
//	\f
//
//	--------------------
//
// 这些文档可能完全没有 Markdown：
//
//	#
//	##
//	###
//
// 但仍然具有明显的视觉或章节结构。
//
// -----------------------------------------------------------------------------
//
// 整体算法：
//
//	Document
//	    ↓
//	findHeuristicBoundaries()
//	    ↓
//	候选 Boundary
//	    ↓
//	Protected Span Filter
//	    ↓
//	相邻 Boundary 形成 Block
//	    ↓
//	Greedy Bin Packing
//	    │
//	    ├── Block <= ChunkSize
//	    │       ↓
//	    │    尽量聚合
//	    │
//	    └── Block > ChunkSize
//	            ↓
//	         SplitText()
//	            ↓
//	          Legacy
//
// -----------------------------------------------------------------------------
//
// profile 当前没有被直接使用。
//
// 原因是 Heuristic Tier 自己必须扫描真实 Boundary 位置。
// profile 只保存“数量统计”，没有每个结构标记的具体 offset。
//
// 保留 profile 参数只是为了与：
//
//	splitByHeadings
//	splitByHeuristics
//
// 拥有统一函数签名。
func splitByHeuristicsImpl(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
	if text == "" {
		return nil
	}

	runes := []rune(text)
	totalRunes := len(runes)

	// 文档本身已经小于 ChunkSize，
	// Heuristic 没有必要再为了章节结构把一个短文档切碎。
	//
	// 直接交给 Legacy，可以继续获得：
	//
	//	Protected Span
	//	Table Header
	//	标准 Source Offset
	//
	// 等能力。
	if totalRunes <= cfg.ChunkSize {
		return SplitText(text, cfg)
	}

	// 第一步：
	//
	// 扫描全文，找到所有可能的结构边界。
	bounds := findHeuristicBoundaries(text, cfg.Languages)

	// 第二步：
	//
	// 删除位于 Protected Span 内部的 Boundary。
	//
	// 例如：
	//
	//	$$
	//	1. equation step
	//	$$
	//
	// "1. equation step" 看起来符合 NumberedSectionPattern，
	// 但它其实在 LaTeX block 内部。
	//
	// 如果保留这个 boundary，就会把公式切开。
	//
	// 注意：
	//
	// 只有“严格位于 Protected Span 内部”的 boundary 会删除。
	//
	// 位于：
	//
	//	span.start
	//	span.end
	//
	// 的 boundary 可以保留，因为它刚好落在保护区域边缘，
	// 不会破坏 Protected Content。
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

	// 在文档末尾补一个 Sentinel Boundary。
	//
	// 例如真实 Boundary：
	//
	//	0
	//	300
	//	700
	//
	// 文档总长：
	//
	//	1000
	//
	// 我们需要：
	//
	//	[0,300)
	//	[300,700)
	//	[700,1000)
	//
	// 所以 1000 作为最后一个边界，
	// 只是为了让最后一个 Block 也能进入同一套循环逻辑。
	bounds = append(bounds, boundary{runeStart: totalRunes})

	// 文档最开头可能不是一个章节标题。
	//
	// 例如：
	//
	//	这是文档前言……
	//
	//	第一章 总则
	//
	// 此时第一个 heuristic boundary 可能是 100。
	//
	// 必须人为增加 offset=0，
	// 才能保留前言：
	//
	//	[0,100)
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

	// minChunkSize 用于避免因为结构 Boundary 太密集，
	// 产生大量极小 Chunk。
	//
	// 默认：
	//
	//	ChunkSize / 4
	//
	// 但最少：
	//
	//	50 rune
	//
	// 例如：
	//
	//	ChunkSize = 512
	//	minChunkSize = 128
	//
	// 意味着即使加入下一个 block 会超过 512，
	// 如果当前累计内容连 128 都不到，
	// 仍然倾向于继续积累，而不是立即产生极小 Chunk。
	minChunkSize := cfg.ChunkSize / 4
	if minChunkSize < 50 {
		minChunkSize = 50
	}

	// -------------------------------------------------------------------------
	// Greedy Bin Packing
	//
	// bounds：
	//
	//	0
	//	150
	//	300
	//	600
	//	900
	//
	// 相邻 Boundary 之间就是一个结构 Block：
	//
	//	[0,150)
	//	[150,300)
	//	[300,600)
	//	[600,900)
	//
	// 我们尽量往当前 Chunk 中装 Block，
	// 直到继续加入会超过 ChunkSize。
	// -------------------------------------------------------------------------

	for i := 1; i < len(bounds); i++ {
		nextEnd := bounds[i].runeStart

		// 当前最新 Boundary 区间本身的长度。
		//
		// 注意：
		//
		// 这里是：
		//
		//	nextEnd - curEnd
		//
		// 不是：
		//
		//	nextEnd - chunkStart
		//
		// 因为我们首先要判断：
		//
		// “单独这一个结构 Block 自己是不是就已经过大？”
		blockLen := nextEnd - curEnd

		// ---------------------------------------------------------------------
		// 情况一：
		//
		// 一个独立结构 Block 自己就已经 > ChunkSize。
		//
		// 例如：
		//
		//	1. Introduction
		//
		//	后面连续 5000 字正文
		//
		//	2. Methods
		//
		// Boundary 可能只有：
		//
		//	0
		//	5000
		//
		// Heuristic 只能知道：
		//
		//	“这是一个章节”
		//
		// 但并不知道这 5000 字内部应该在哪里切。
		//
		// 所以职责下沉给：
		//
		//	SplitText()
		//
		// 复用 Legacy 的 Recursive / Protected / Table 等能力。
		// ---------------------------------------------------------------------

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

		// ---------------------------------------------------------------------
		// 情况二：
		//
		// 单个 Block 并不大，
		// 但多个 Block 累加以后超过 ChunkSize。
		//
		// 同时要求：
		//
		//	当前已有内容 >= minChunkSize
		//
		// 才真正 Flush。
		//
		// 这样可以防止出现大量过小 Chunk。
		// ---------------------------------------------------------------------

		if accumulated > cfg.ChunkSize && curEnd-chunkStart >= minChunkSize {
			out = appendChunk(out, runes, chunkStart, curEnd, &seq)

			// 下一 Chunk 并不一定直接从 curEnd 开始。
			//
			// 我们尝试产生 overlap。
			//
			// 但不希望：
			//
			//	curEnd - 80
			//
			// 机械地落在一句话、一个单词或一行中间。
			//
			// 所以优先把新起点对齐到：
			//
			//	前面的 Heuristic Boundary
			//
			// 如果没有，再寻找换行。
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

// findHeuristicBoundaries 扫描整篇文档，寻找所有结构候选边界。
//
// 支持：
//
//	\f
//	第一章 / 第3节
//	Chapter / Section / Part
//	Kapitel / Abschnitt / Teil
//	1. Introduction
//	1.1 Installation
//	II. Results
//	ALL CAPS TITLE
//	----------------
//	Page 3 of 10
//	连续多个空行
//
// 返回结果保证：
//
//  1. 按 runeStart 升序排列
//  2. 同一个 offset 只保留一个 Boundary
//  3. 同 offset 时保留 priority 更高的 Boundary
func findHeuristicBoundaries(text string, languages []string) []boundary {
	var bounds []boundary

	// -------------------------------------------------------------------------
	// Form Feed
	//
	// \f 通常来自 PDF Parser，
	// 表示换页。
	//
	// 它属于非常强的结构边界，所以优先级最高。
	// -------------------------------------------------------------------------

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

		// fenced code 内部所有结构信号全部忽略。
		//
		// 例如：
		//
		//	```text
		//	1. this is not a chapter
		//	CHAPTER ONE
		//	---
		//	```
		//
		// 都只是代码内容。
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		} else if !inFence {
			added := false

			// -------------------------------------------------------------
			// Chapter Marker
			//
			// languages 会影响启用哪些规则。
			//
			// zh：
			//
			//	第一章
			//
			// en：
			//
			//	Chapter 1
			//
			// de：
			//
			//	Kapitel 1
			//
			// 如果 languages 为空，
			// 则尝试所有语言。
			// -------------------------------------------------------------

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

			// -------------------------------------------------------------
			// Numbered Section
			//
			// 例如：
			//
			//	1. Introduction
			//	1.1 Installation
			//	2.3.1 用户权限
			// -------------------------------------------------------------

			if !added && NumberedSectionPattern.MatchString(line) {
				bounds = append(bounds, boundary{
					runeStart: runePos,
					priority:  PrioNumberedHead,
				})

				added = true
			}

			// -------------------------------------------------------------
			// ALL CAPS Heading
			//
			// 例如：
			//
			//	SYSTEM ARCHITECTURE
			//	INSTALLATION:
			// -------------------------------------------------------------

			if !added && AllCapsHeadingPattern.MatchString(line) {
				bounds = append(bounds, boundary{
					runeStart: runePos,
					priority:  PrioAllCapsHeading,
				})

				added = true
			}

			// -------------------------------------------------------------
			// Visual Separator
			//
			//	---
			//	=====
			//	*****
			//	_____
			// -------------------------------------------------------------

			if !added && VisualSeparatorPattern.MatchString(line) {
				bounds = append(bounds, boundary{
					runeStart: runePos,
					priority:  PrioVisualSep,
				})

				added = true
			}

			// -------------------------------------------------------------
			// Page Footer
			//
			//	Page 3 of 10
			//	Seite 3 von 10
			//	页码 3
			// -------------------------------------------------------------

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

	// -------------------------------------------------------------------------
	// Excessive Blank Block
	//
	// 匹配：
	//
	//	\n\n\n
	//
	// 或更多连续换行。
	//
	// regex 返回 byte offset，
	// 所以需要：
	//
	//	RuneLen(text[:location[1]])
	//
	// 转成 rune offset。
	//
	// 注意这里使用 location[1]：
	//
	// Boundary 被放到“整段空白之后”，
	// 这样下一 Chunk 可以直接从下一个真实段落开始，
	// 而不是从一堆空行中间开始。
	// -------------------------------------------------------------------------

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

	// -------------------------------------------------------------------------
	// 排序：
	//
	// 第一关键字：
	//
	//	runeStart ASC
	//
	// 第二关键字：
	//
	//	priority DESC
	//
	// 因此同一个位置：
	//
	//	100 / priority 90
	//	100 / priority 70
	//
	// 排序后高优先级 90 在前。
	// -------------------------------------------------------------------------

	sort.Slice(bounds, func(i, j int) bool {
		if bounds[i].runeStart != bounds[j].runeStart {
			return bounds[i].runeStart < bounds[j].runeStart
		}

		return bounds[i].priority > bounds[j].priority
	})

	// -------------------------------------------------------------------------
	// 去重。
	//
	// 同一个 rune offset 只保留第一个。
	//
	// 因为前面已经按照 priority DESC 排序，
	// 所以保留下来的自然是最高优先级。
	// -------------------------------------------------------------------------

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

// dropBoundsInsideSpans 删除严格位于 Protected Span 内部的 Boundary。
//
// Protected Span 使用 rune offset，并且必须按 start 升序排列。
//
// 保留：
//
//	boundary == span.start
//	boundary == span.end
//
// 删除：
//
//	span.start < boundary < span.end
//
// 例如：
//
//	Protected:
//	    [100, 200)
//
// Boundary:
//
//	100  → 保留
//	150  → 删除
//	200  → 保留
//
// 因为 100 / 200 都是 Protected Content 的安全边缘。
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

// allRuneIndices 返回 needle 在 text 中所有出现位置的 rune offset。
//
// 当前主要用于：
//
//	allRuneIndices(text, "\f")
//
// 因此设计目标是单 rune needle。
//
// 为什么不用 strings.Index？
//
// strings.Index 返回的是 byte offset，
// 而我们的整个 Chunker 位置体系必须使用 rune offset。
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

// appendChunk 把原文 runes[start:end] 转成最终 Chunk 并追加到 out。
//
// 这里非常重要的一点：
//
//	Content 不 TrimSpace。
//
// 原因仍然是：
//
//	Content
//	Start / End
//
// 必须保持真实 Source Mapping。
//
// TrimSpace 只应该发生在：
//
//	EmbeddingContent()
//
// 这种检索视图层。
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

// appendOversizeBlock 处理一个本身就大于 ChunkSize 的结构 Block。
//
// Heuristic Splitter 只负责识别：
//
//	“这个章节从哪里开始，到哪里结束”。
//
// 它不应该重新实现：
//
//	Recursive Split
//	Protected Span
//	Table Header
//	Semantic Overlap
//
// 所以超大 Block 直接委托给：
//
//	SplitText()
//
// 这和 Heading Splitter 处理超大 Section 的设计完全一致：
//
//	结构层负责“结构”
//	Legacy 层负责“尺寸和文本安全”
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

// applyOverlapAligned 计算 Heuristic Chunk 的下一块起点。
//
// 假设：
//
//	curEnd  = 1000
//	Overlap = 100
//
// 理论目标位置：
//
//	target = 900
//
// 但是 900 很可能落在：
//
//	某个单词中间
//	某一句中间
//	某一行中间
//
// 所以 Heuristic Splitter 尝试“对齐”。
//
// -----------------------------------------------------------------------------
// 搜索窗口：
//
//	[curEnd - 2*overlap, curEnd)
//
// 例如：
//
//	[800, 1000)
//
// 优先寻找：
//
//	这个窗口中最靠后的 Heuristic Boundary。
//
// 例如：
//
//	Boundary = 850
//	Boundary = 920
//	Boundary = 1000
//
// 1000 必须排除，因为那等于完全没有 overlap。
//
// 最终选择：
//
//	920
//
// -----------------------------------------------------------------------------
// 如果完全没有 Heuristic Boundary：
//
// 从 target 向前找换行。
//
// 如果连换行也没有：
//
// 只能使用 raw target。
//
// -----------------------------------------------------------------------------
// 注意：
//
// 这一套 overlap 和 Legacy 的 computeOverlap() 不完全相同。
//
// Legacy：
//
//	基于 paragraph / newline / sentence semantic suffix。
//
// Heuristic：
//
//	优先利用已经识别到的结构 Boundary。
//
// 这是当前 WeKnora 的真实设计。
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

	// 优先选择搜索窗口中最靠后的 Heuristic Boundary。
	//
	// 这里必须：
	//
	//	boundary < curEnd
	//
	// 不能包含 curEnd 本身。
	//
	// 因为 curEnd 本身天然就是一个 Boundary，
	// 如果允许选它，函数每次都会返回 curEnd，
	// 最终 overlap 永远等于 0。
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
