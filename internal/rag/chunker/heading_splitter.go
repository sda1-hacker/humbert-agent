package chunker

import "strings"

// headingBoundary 保存章节起点；文档前言使用空标题。
type headingBoundary struct {
	runeStart   int
	headingLine string
}

// splitByHeadings 按主要标题层级划分章节，过大章节交给递归切分。
func splitByHeadings(text string, cfg SplitterConfig, profile *DocProfile) []Chunk {
	if text == "" {
		return nil
	}

	if profile == nil {
		profile = ProfileDocument(text)
	}

	// 主要标题层级决定章节切分骨架。
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

		// 保留章节起始标题状态，供长章节内的子块重新计算标题路径。
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

		// 小章节的预算同时考虑标题路径，标题路径不写入原文正文。
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

		// 过大章节复用递归切分的保护区域、表头补充和重叠处理。

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

	// 合并相邻小块，减少因标题过密产生的碎片。
	return coalesceTinyHeadingChunks(chunks, cfg.ChunkSize)
}

// findHeadingBoundaries 查找主要标题层级及更高层级的章节起点，忽略代码块。
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

		// 代码块里的井号是代码内容，不作为 Markdown 标题。
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

// observeDeeperHeadings 更新章节内部更深层标题的上下文。
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

// buildSectionBreadcrumbIndex 记录长章节内标题上下文的变化位置。
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

// breadcrumbAtOffset 查找指定字符位置生效的标题路径。
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

// coalesceTinyHeadingChunks 合并相邻小块，仅合并原文连续且上下文兼容的片段。
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

// commonHeadingPrefix 返回两条标题路径共有的完整标题行。
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
