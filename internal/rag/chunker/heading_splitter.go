package chunker

import "strings"

// init 把真正的 Heading Splitter 注册到 Strategy Resolver。
//
// strategy.go 中目前有:
//
//	var splitByHeadings = func(...) {
//	    return SplitText(...)
//	}
//
// 那只是未实现 Heading Splitter 时的安全 fallback。
//
// 当前文件加载后，init 会将它替换成真正实现。
func init() {
	splitByHeadings = splitByHeadingsImpl
}

// headingBoundary 表示一个 Heading Section 的开始位置。
//
// runeStart:
//
//	当前 Section 在原始 Markdown 中的 rune offset。
//
// headingLine:
//
//	触发这个边界的原始 Markdown Heading。
//
// 对于文档开头的 preamble:
//
//	runeStart   = 0
//	headingLine = ""
type headingBoundary struct {
	runeStart   int
	headingLine string
}

// splitByHeadingsImpl 是真正的 TierHeading 实现。
//
// profile 可能为 nil。
//
// Auto Strategy:
//
//	ProfileDocument
//	    ↓
//	SelectStrategy
//	    ↓
//	runTier
//
// 这种情况下 profile 已经有了，不需要重新扫描。
//
// 显式配置:
//
//	StrategyHeading
//
// 会跳过 Profiler，所以这里需要自己补一次。
func splitByHeadingsImpl(text string, cfg SplitterConfig, profile *DocProfile) []Chunk {
	if text == "" {
		return nil
	}

	if profile == nil {
		profile = ProfileDocument(text)
	}

	// DominantHeadingLevel 决定文档的主要结构骨架。
	//
	// 例如:
	//
	//     H1 = 1
	//     H2 = 6
	//     H3 = 20
	//
	// primaryLevel = 2。
	primaryLevel := profile.DominantHeadingLevel()
	if primaryLevel == 0 {
		return SplitText(text, cfg)
	}

	boundaries := findHeadingBoundaries(text, primaryLevel)

	// 没有真正形成多个 Section。
	//
	// 此时 Heading Splitter没有意义，直接退回 Legacy。
	if len(boundaries) <= 1 {
		return SplitText(text, cfg)
	}

	sourceRunes := []rune(text)
	hierarchy := NewHeadingHierarchy()

	chunks := make([]Chunk, 0, len(boundaries))
	seq := 0

	for i, boundary := range boundaries {
		sectionEnd := len(sourceRunes)

		if i+1 < len(boundaries) {
			sectionEnd = boundaries[i+1].runeStart
		}

		// 当前 Section 是由 Heading 开始的，
		// 先让这个 Heading 进入层级栈。
		if boundary.headingLine != "" {
			hierarchy.Observe(boundary.headingLine)
		}

		// 这是当前 Section 开始时生效的 Breadcrumb。
		breadcrumb := hierarchy.BreadcrumbWithHashes()

		// 保存一份 Section 起始时的层级状态。
		//
		// 大 Section 内如果继续出现:
		//
		//     ### Linux
		//     #### Ubuntu
		//
		// 后面要从这份 seed 开始重新计算子 Chunk Breadcrumb。
		sectionSeed := *hierarchy

		sectionRunes := sourceRunes[boundary.runeStart:sectionEnd]

		// 这一轮还要提前扫描 Section 中更深层 Heading，
		// 更新全局 hierarchy。
		//
		// 这是为了下一主 Section 开始时，
		// hierarchy 状态仍然与真实文档遍历顺序一致。
		observeDeeperHeadings(sectionRunes, primaryLevel, hierarchy)

		if len(sectionRunes) == 0 {
			continue
		}

		sectionContent := string(sectionRunes)

		// WeKnora 当前对“小 Section”的预算判断会把
		// ContextHeader 长度也算进去。
		//
		// 注意:
		//
		// Breadcrumb 并不会真的写进 Content。
		//
		// 这里只是保守地保证:
		//
		//     Breadcrumb + 空行 +正文
		//
		// 不至于明显超过 ChunkSize。
		if RuneLen(breadcrumb)+2+len(sectionRunes) <= cfg.ChunkSize {
			chunks = append(chunks, Chunk{
				Content:       sectionContent,
				ContextHeader: breadcrumb,
				Seq:           seq,
				Start:         boundary.runeStart,
				End:           sectionEnd,
			})

			seq++
			continue
		}

		// -----------------------------------------------------------------
		// 当前 Section 太大。
		//
		// 不在 Heading Splitter 中重新发明切割逻辑。
		//
		// 直接交给已经成熟的 Legacy Splitter:
		//
		//     Protected Span
		//     Recursive
		//     Table Header
		//     Overlap
		//
		// 全部复用。
		// -----------------------------------------------------------------

		breadcrumbIndex := buildSectionBreadcrumbIndex(sectionRunes, primaryLevel, sectionSeed)
		subChunks := SplitText(sectionContent, cfg)

		for _, sub := range subChunks {
			chunks = append(chunks, Chunk{
				Content:       sub.Content,
				ContextHeader: breadcrumbAtOffset(breadcrumbIndex, sub.Start, breadcrumb),
				Seq:           seq,
				Start:         boundary.runeStart + sub.Start,
				End:           boundary.runeStart + sub.End,
			})

			seq++
		}
	}

	// 很多 FAQ / Install Log 文档会出现大量非常短的标题 Section。
	//
	// 最后做一次保守合并，避免 Validator 因为：
	//
	//     too many tiny chunks
	//
	// 把本来很好的 Heading Tier 整体拒绝掉。
	return coalesceTinyHeadingChunks(chunks, cfg.ChunkSize)
}

// findHeadingBoundaries 找出主 Section 边界。
//
// 有一个特别容易误解的地方：
//
// 如果:
//
//	primaryLevel = 2
//
// 边界并不是只有 H2。
//
// 而是:
//
//	H1
//	H2
//
// 即:
//
//	level <= primaryLevel
//
// 都可以作为边界。
//
// 例如:
//
//	# Chapter 1
//	## A
//	## B
//	# Chapter 2
//	## C
//
// primary = H2 时，以上每个 H1/H2 都是结构边界。
func findHeadingBoundaries(text string, primaryLevel int) []headingBoundary {
	boundaries := []headingBoundary{{runeStart: 0}}

	if text == "" {
		return boundaries
	}

	lines := strings.Split(text, "\n")

	runePos := 0
	inFence := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// fenced code 内的 Heading 必须完全忽略。
		//
		// 例如:
		//
		//     ```bash
		//     # this is a shell comment
		//     ```
		//
		// 这里的 "# ..." 不是 Markdown Heading。
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			runePos += RuneLen(line)

			if i < len(lines)-1 {
				runePos++ // strings.Split 去掉的 '\n'
			}

			continue
		}

		if !inFence {
			match := MarkdownHeadingPattern.FindStringSubmatch(line)

			if match != nil {
				level := len(match[1])

				if level >= 1 && level <= primaryLevel {
					if runePos == 0 {
						// 第一行本来就从 offset=0 开始，
						// 不新增第二个重复 boundary，
						// 只给已有的 leading boundary 填上 Heading。
						boundaries[0].headingLine = line
					} else {
						boundaries = append(boundaries, headingBoundary{
							runeStart:   runePos,
							headingLine: line,
						})
					}
				}
			}
		}

		runePos += RuneLen(line)

		if i < len(lines)-1 {
			runePos++
		}
	}

	return boundaries
}

// observeDeeperHeadings 扫描一个主 Section 内部比 primaryLevel 更深的 Heading。
//
// 例如:
//
//	primaryLevel = 2
//
// Section:
//
//	## Installation
//
//	### Linux
//
//	#### Ubuntu
//
//	### Windows
//
// 当前主边界仍然只有:
//
//	## Installation
//
// 但内部 hierarchy 必须依次知道:
//
//	H3 Linux
//	H4 Ubuntu
//	H3 Windows
//
// 否则后续 Section 的标题状态会和真实文档脱节。
func observeDeeperHeadings(section []rune, primaryLevel int, hierarchy *HeadingHierarchy) {
	if len(section) == 0 {
		return
	}

	inFence := false

	for _, line := range strings.Split(string(section), "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}

		if inFence {
			continue
		}

		match := MarkdownHeadingPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		if len(match[1]) > primaryLevel {
			hierarchy.Observe(line)
		}
	}
}

// sectionBreadcrumb 表示:
//
// 从 runeStart 开始，后面的内容进入 breadcrumb 所描述的标题上下文。
type sectionBreadcrumb struct {
	runeStart  int
	breadcrumb string
}

// buildSectionBreadcrumbIndex 为一个长 Section 建立 Breadcrumb 索引。
//
// seed 是 Section 开头的 HeadingHierarchy 状态。
//
// 例如:
//
//	# 产品手册
//	## 安装
//
// 初始:
//
//	offset 0
//	-> # 产品手册
//	   ## 安装
//
// Section 后面出现:
//
//	### Linux
//
// 就增加:
//
//	offset X
//	-> # 产品手册
//	   ## 安装
//	   ### Linux
func buildSectionBreadcrumbIndex(
	section []rune,
	primaryLevel int,
	seed HeadingHierarchy,
) []sectionBreadcrumb {

	hierarchy := seed

	result := []sectionBreadcrumb{
		{
			runeStart:  0,
			breadcrumb: hierarchy.BreadcrumbWithHashes(),
		},
	}

	if len(section) == 0 {
		return result
	}

	lines := strings.Split(string(section), "\n")

	runePos := 0
	inFence := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			runePos += RuneLen(line)

			if i < len(lines)-1 {
				runePos++
			}

			continue
		}

		if !inFence {
			match := MarkdownHeadingPattern.FindStringSubmatch(line)

			if match != nil && len(match[1]) > primaryLevel {
				hierarchy.Observe(line)

				result = append(result, sectionBreadcrumb{
					runeStart:  runePos,
					breadcrumb: hierarchy.BreadcrumbWithHashes(),
				})
			}
		}

		runePos += RuneLen(line)

		if i < len(lines)-1 {
			runePos++
		}
	}

	return result
}

// breadcrumbAtOffset 找出某个 rune offset 当时真正生效的 Breadcrumb。
//
// bcs 已按 runeStart 升序排列。
//
// 例如:
//
//	0   -> # Doc / ## A
//	200 -> # Doc / ## A / ### Linux
//	500 -> # Doc / ## A / ### Linux / #### Ubuntu
//
// offset = 650
//
// 最终返回 offset=500 那条 Breadcrumb。
func breadcrumbAtOffset(bcs []sectionBreadcrumb, offset int, fallback string) string {
	breadcrumb := fallback

	for _, item := range bcs {
		if item.runeStart > offset {
			break
		}

		breadcrumb = item.breadcrumb
	}

	return breadcrumb
}

// coalesceTinyHeadingChunks 合并相邻且较小的 Heading Chunk。
//
// 注意它非常保守，并不是普通“尽量装满 ChunkSize”的 Merge。
//
// 只有同时满足以下条件才合并:
//
//  1. 两块拥有共同 Heading 前缀
//  2. cur.End == next.Start
//  3. 当前 cur 还小于 merge target
//  4. 合并后 <= ChunkSize
//
// 第 2 条尤其重要。
//
// Legacy SplitText 产生的子 Chunk 可能存在 overlap:
//
//	cur.End > next.Start
//
// 这种 Chunk 绝对不能直接 Content 拼接，
// 否则 overlap 会被重复写入。
func coalesceTinyHeadingChunks(chunks []Chunk, chunkSize int) []Chunk {
	if len(chunks) <= 1 || chunkSize <= 0 {
		return chunks
	}

	// 当前 WeKnora 的目标大约是 ChunkSize / 2，
	// 但最低不小于 200。
	target := chunkSize / 2

	if target < 200 {
		target = 200
	}

	result := make([]Chunk, 0, len(chunks))

	current := chunks[0]
	currentLen := RuneLen(current.Content)

	for i := 1; i < len(chunks); i++ {
		next := chunks[i]
		nextLen := RuneLen(next.Content)

		sharedHeader := commonHeadingPrefix(current.ContextHeader, next.ContextHeader)

		canMerge :=
			sharedHeader != "" &&
				current.End == next.Start &&
				currentLen < target &&
				currentLen+nextLen <= chunkSize

		if canMerge {
			current.Content += next.Content
			current.ContextHeader = sharedHeader
			current.End = next.End
			currentLen += nextLen
			continue
		}

		result = append(result, current)

		current = next
		currentLen = nextLen
	}

	result = append(result, current)

	// Merge 后原来的 Seq 可能出现跳号。
	//
	// 下游希望:
	//
	//     0,1,2,3...
	//
	// 连续，所以重新编号。
	for i := range result {
		result[i].Seq = i
	}

	return result
}

// commonHeadingPrefix 返回两个 Breadcrumb 的最长“完整行”公共前缀。
//
// 例如:
//
// A:
//
//	# Doc
//	## Install
//	### Linux
//
// B:
//
//	# Doc
//	## Install
//	### Windows
//
// 返回:
//
//	# Doc
//	## Install
//
// 为什么必须逐行比较？
//
// 因为不能做字符串字符级 prefix。
//
// 否则:
//
//	### User
//	### Users
//
// 有可能错误地产生:
//
//	### User
//
// 这种已经不是一个真实 Heading 的结果。
func commonHeadingPrefix(a, b string) string {
	if a == b {
		return a
	}

	aLines := strings.Split(a, "\n")
	bLines := strings.Split(b, "\n")

	limit := len(aLines)

	if len(bLines) < limit {
		limit = len(bLines)
	}

	common := 0

	for i := 0; i < limit; i++ {
		if aLines[i] != bLines[i] {
			break
		}

		common = i + 1
	}

	if common == 0 {
		return ""
	}

	return strings.Join(aLines[:common], "\n")
}
