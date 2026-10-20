package chunker

import (
	"context"
	"slices"
	"sort"
	"strings"
	"unicode"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// SplitText 使用 Goldmark 的结构边界，再按字面分隔符和字符预算切分正文。
func SplitText(text string, cfg ragconfig.Splitter) []Chunk {
	cfg.Strategy = ragconfig.StrategyRecursive
	return Split(text, cfg)
}

func (l *Layout) header(start, end int) string {
	for start < end && unicode.IsSpace(l.source[start]) {
		start++
	}
	i := sort.Search(len(l.headings), func(i int) bool { return l.headings[i].start > start })
	path := ""
	if i > 0 {
		path = l.headings[i-1].path
	}
	// 跨越不同章节时仅保留共同祖先，不能把后一个章节标成前一个章节。
	for ; i < len(l.headings) && l.headings[i].start < end; i++ {
		path = commonHeadingPrefix(path, l.headings[i].path)
	}
	return path
}

func commonHeadingPrefix(a, b string) string {
	left, right := strings.Split(a, "\n"), strings.Split(b, "\n")
	i := 0
	for i < min(len(left), len(right)) && left[i] == right[i] {
		i++
	}
	return strings.Join(left[:i], "\n")
}

func (l *Layout) chunk(start, end int) Chunk {
	return Chunk{Start: start, End: end, Content: l.render(start, end), ContextHeader: l.header(start, end)}
}

// splitRange 仅操作全文坐标；子块不会重新解析局部 Markdown，也不会丢失围栏或表头上下文。
func (l *Layout) splitRange(ctx context.Context, start, end int, cfg ragconfig.Splitter, preferred []int, limits ...int) []Chunk {
	limit := chunkLimit(limits)
	var boundaries []int
	first := sort.Search(len(l.blocks), func(i int) bool { return l.blocks[i].end > start })
	for _, block := range l.blocks[first:] {
		if block.start >= end {
			break
		}
		if block.start > start && block.start < end {
			boundaries = append(boundaries, block.start)
		}
		if block.end > start && block.end < end {
			boundaries = append(boundaries, block.end)
		}
	}
	slices.Sort(boundaries)
	boundaries = slices.Compact(boundaries)
	var chunks []Chunk
	for at := start; at < end; {
		if ctx.Err() != nil {
			return nil
		}
		// 标题路径和补充表头同样占输入预算；结构不可分时允许超过软目标，最终由 Fit 严格检查。
		budget := cfg.ChunkSize
		header := l.header(at, min(at+1, end))
		overhead := RuneLen(l.render(at, at)) + RuneLen(header)
		if header != "" {
			overhead += 2
		}
		budget = max(1, budget-overhead)
		stop := min(at+budget, end)
		if stop < end {
			stop = l.chooseEnd(at, stop, cfg.Separators, preferred, boundaries)
		}
		// 软窗口不能把完整的小结构切碎；真正的模型输入硬预算仍由 Fit 检查。
		if structure, ok := l.protectedStructure(stop, cfg.ChunkSize); ok {
			if structure.start > at {
				stop = structure.start
			} else {
				stop = min(structure.end, end)
			}
		}
		if atom, inside := l.atomAt(stop); inside {
			if atom.start > at {
				stop = atom.start
			} else {
				stop = min(atom.end, end)
			}
		}
		if stop <= at {
			stop = min(at+max(1, budget), end)
		}
		part := l.chunk(at, stop)
		if strings.TrimSpace(string(l.source[at:stop])) != "" {
			part.Seq = len(chunks)
			chunks = append(chunks, part)
			if limit > 0 && len(chunks) > limit {
				return chunks
			}
		}
		if stop == end {
			break
		}
		next := max(at+1, stop-min(cfg.ChunkOverlap, (stop-at)/2))
		if structure, ok := l.protectedStructure(next, cfg.ChunkSize); ok {
			next = structure.end // 重叠不能把刚保留的完整代码块或短表格重新变成残片。
		}
		if atom, inside := l.atomAt(next); inside {
			next = atom.end // 重叠不能从链接、公式或表格行的中间开始。
		}
		at = min(next, stop)
	}
	return chunks
}

// protectedStructure 对齐 WeKnora 的代码块保护上限（7500 字符）；表格仅在软窗口能容纳时整体保留。
// 这不是模型 token 上限：标题、围栏和表头都要参与后续完整输入预算校验。
func (l *Layout) protectedStructure(position, chunkSize int) (span, bool) {
	i := sort.Search(len(l.codeUnits), func(i int) bool { return l.codeUnits[i].end > position })
	if i < len(l.codeUnits) {
		block := l.codeUnits[i]
		if block.start < position {
			return block, true
		}
	}
	i = sort.Search(len(l.tables), func(i int) bool { return l.tables[i].end > position })
	if i < len(l.tables) {
		table := l.tables[i]
		if table.start < position && table.end-table.start <= chunkSize {
			return table.span, true
		}
	}
	return span{}, false
}

// chooseEnd 优先选择自然结构，其次采用用户配置的字面分隔符；不识别 Markdown 标记。
func (l *Layout) chooseEnd(start, limit int, separators []string, preferred, boundaries []int) int {
	floor := start + (limit-start)/2
	for _, points := range [][]int{preferred, boundaries} {
		i := sort.SearchInts(points, limit+1) - 1
		if i >= 0 && points[i] > floor {
			if _, inside := l.atomAt(points[i]); !inside {
				return points[i]
			}
		}
	}
	region := string(l.source[floor:limit])
	for _, separator := range separators {
		if separator == "" {
			continue
		}
		if at := strings.LastIndex(region, separator); at >= 0 {
			stop := floor + RuneLen(region[:at+len(separator)])
			if _, inside := l.atomAt(stop); !inside {
				return stop
			}
		}
	}
	return limit
}

func (l *Layout) splitHeadings(ctx context.Context, start, end int, cfg ragconfig.Splitter, profile *DocProfile, limits ...int) []Chunk {
	limit := chunkLimit(limits)
	points := []int{start}
	level := profile.DominantHeadingLevel()
	first := sort.Search(len(l.headings), func(i int) bool { return l.headings[i].start >= start })
	for _, h := range l.headings[first:] {
		if h.start >= end {
			break
		}
		if h.start > start && h.start < end && h.level <= level {
			points = append(points, h.start)
		}
	}
	points = append(points, end)
	var chunks []Chunk
	for i := 1; i < len(points); i++ {
		remaining := 0
		if limit > 0 {
			remaining = max(1, limit-len(chunks)+1)
		} // 留一块用于同祖先小节合并。
		parts := l.splitRange(ctx, points[i-1], points[i], cfg, nil, remaining)
		for _, part := range parts {
			// 合并同一祖先下的连续小章节，正文仍从原文重建，避免重复补充表头。
			if len(chunks) > 0 {
				last := &chunks[len(chunks)-1]
				shared := commonHeadingPrefix(last.ContextHeader, part.ContextHeader)
				merged := l.chunk(last.Start, part.End)
				if shared != "" && last.End == part.Start && RuneLen(last.Content) < max(200, cfg.ChunkSize/2) && RuneLen(merged.EmbeddingContent()) <= cfg.ChunkSize {
					*last = merged
					continue
				}
			}
			chunks = append(chunks, part)
			if limit > 0 && len(chunks) > limit {
				return chunks
			}
		}
	}
	return chunks
}
