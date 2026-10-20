package chunker

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/yuin/goldmark/ast"
	tableast "github.com/yuin/goldmark/extension/ast"
)

var ErrStructureBudget = errors.New("rag chunker: indivisible table row/group, formula or link exceeds input budget")

// span 在 Layout 中统一使用 Unicode 字符坐标，构建期间先接收 AST 的字节坐标。
type span struct{ start, end int }
type heading struct {
	span
	level int
	title string
	path  string
}
type codeRange struct {
	span
	body           span
	header, suffix string
}
type tableRange struct {
	span
	headerRange, promoted span
	header                string
	closing               string // HTML 表格续块的结束标签；Markdown 表格留空。
}

// Layout 是整篇文档的结构索引。父块、子块和预算切分共用它，不重新解析被截断的 Markdown。
type Layout struct {
	source    []rune
	atoms     []span
	blocks    []span
	excluded  []span
	dividers  []span
	headings  []heading
	codes     []codeRange
	codeUnits []span // 按 WeKnora 的 7500 字符上限保护代码片段，硬 token 预算仍可继续拆分。
	tables    []tableRange
}

func NewLayout(ctx context.Context, source string) (*Layout, error) {
	md, err := documenttext.ParseMarkdown(ctx, source)
	if err != nil {
		return nil, err
	}
	l := &Layout{source: []rune(source)}
	lineEnd := func(start int) int {
		if next := strings.IndexByte(source[start:], '\n'); next >= 0 {
			return start + next + 1
		}
		return len(source)
	}
	err = ast.Walk(md.Root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		r := md.Range(n)
		region := span{r.Start, r.End}
		if region.end <= region.start {
			return ast.WalkContinue, nil
		}
		if n.Type() == ast.TypeBlock && (n.FirstChild() == nil || n.FirstChild().Type() == ast.TypeInline) {
			l.blocks = append(l.blocks, region)
		}
		switch node := n.(type) {
		case *ast.ThematicBreak:
			l.dividers = append(l.dividers, region)
			l.atoms = append(l.atoms, region)
		case *ast.Heading:
			l.headings = append(l.headings, heading{span: region, level: node.Level, title: strings.TrimSpace(string(node.Lines().Value(md.Source)))})
			l.atoms = append(l.atoms, region)
			l.excluded = append(l.excluded, region)
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			// 缩进代码跨块时输出为围栏代码；选择不会与正文冲突的标记，仅用于序列化。
			longest, run := 2, 0
			for _, r := range source[region.start:region.end] {
				if r == '~' {
					run++
					longest = max(longest, run)
				} else {
					run = 0
				}
			}
			fenceText := strings.Repeat("~", longest+1)
			block := codeRange{span: region, body: region, header: fenceText + "\n", suffix: "\n" + fenceText + "\n"}
			if fence, ok := node.(*ast.FencedCodeBlock); ok {
				block.body.start, block.body.end = lineEnd(region.start), lineEnd(region.start)
				if node.Lines().Len() > 0 {
					block.body.end = node.Lines().At(node.Lines().Len() - 1).Stop
				}
				block.header = source[region.start:block.body.start]
				// 围栏和语言范围由 Goldmark 确认；这里只复制标记，用于跨块输出。
				markerEnd := block.body.start
				if fence.Info != nil {
					markerEnd = fence.Info.Segment.Start
				}
				marker := strings.TrimSpace(source[node.Pos():markerEnd])
				block.suffix = "\n" + source[region.start:node.Pos()] + marker + "\n"
				l.atoms = append(l.atoms, span{region.start, block.body.start})
				if block.body.end < region.end {
					l.atoms = append(l.atoms, span{block.body.end, region.end})
				}
			}
			l.codes = append(l.codes, block)
			l.excluded = append(l.excluded, region)
			return ast.WalkSkipChildren, nil
		case *tableast.Table:
			header := md.Range(node.FirstChild())
			region.start = header.Start
			table := tableRange{span: region, headerRange: span{header.Start, lineEnd(header.End)}}
			table.header = source[header.Start:table.headerRange.end]
			l.atoms = append(l.atoms, table.headerRange)
			// 空表头仅在后续块补充首行列名；原文和引用坐标保持不变。
			empty := true
			for cell := node.FirstChild().FirstChild(); cell != nil; cell = cell.NextSibling() {
				empty = empty && strings.TrimSpace(string(cell.Lines().Value(md.Source))) == ""
			}
			for row := node.FirstChild().NextSibling(); row != nil; row = row.NextSibling() {
				location := md.Range(row)
				l.atoms = append(l.atoms, span{location.Start, location.End})
				if empty {
					table.header = source[location.Start:location.End] + source[header.End:table.headerRange.end]
					table.promoted = span{location.Start, location.End}
					empty = false
				}
			}
			l.tables = append(l.tables, table)
			l.excluded = append(l.excluded, region)
			l.blocks = append(l.blocks, region)
			return ast.WalkSkipChildren, nil
		case *ast.HTMLBlock:
			value := source[region.start:region.end]
			locations := documenttext.HTMLTableRanges(value)
			if len(locations) == 1 && strings.TrimSpace(value[:locations[0][0]]) == "" && strings.TrimSpace(value[locations[0][1]:]) == "" {
				location := locations[0]
				if table, ok := documenttext.ParseHTMLTable(value[location[0]:location[1]]); ok {
					base := region.start + location[0]
					t := tableRange{span: region, headerRange: span{region.start, base + table.HeaderEnd}, header: value[:location[0]] + table.Header, closing: table.Closing}
					l.tables = append(l.tables, t)
					l.atoms = append(l.atoms, t.headerRange)
					for _, row := range table.Rows {
						l.atoms = append(l.atoms, span{base + row.Start, base + row.End})
					}
					l.excluded = append(l.excluded, region)
					return ast.WalkSkipChildren, nil
				}
			}
			// 嵌套表、无界 rowspan 或不完整 HTML 无法安全逐行拆分，保留整体并由硬预算明确拒绝。
			l.atoms = append(l.atoms, region)
			l.excluded = append(l.excluded, region)
			return ast.WalkSkipChildren, nil
		case *passthrough.PassthroughBlock, *passthrough.PassthroughInline, *ast.Link, *ast.Image, *ast.CodeSpan, *ast.AutoLink:
			l.atoms = append(l.atoms, region)
			l.excluded = append(l.excluded, region)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	// 只转换实际结构端点，按原文扫描一次，避免逐节点从全文开头统计中文字符。
	var regions []*span
	for _, list := range [][]span{l.atoms, l.blocks, l.excluded, l.dividers} {
		for i := range list {
			regions = append(regions, &list[i])
		}
	}
	for i := range l.headings {
		regions = append(regions, &l.headings[i].span)
	}
	for i := range l.codes {
		regions = append(regions, &l.codes[i].span, &l.codes[i].body)
	}
	for i := range l.tables {
		regions = append(regions, &l.tables[i].span, &l.tables[i].headerRange, &l.tables[i].promoted)
	}
	if err := convertRanges(ctx, source, regions); err != nil {
		return nil, err
	}
	for _, code := range l.codes {
		for at := code.start; at < code.end; {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			stop := min(at+7500, code.end)
			if stop < code.end {
				// 与参考实现相同，在上限前 200 字符内寻找自然行尾，避免为大代码块生成大量微小子块。
				for i := stop - 1; i > max(at, stop-200); i-- {
					if l.source[i] == '\n' {
						stop = i + 1
						break
					}
				}
			}
			l.codeUnits = append(l.codeUnits, span{at, stop})
			at = stop
		}
	}
	l.atoms = mergeSpans(l.atoms)
	l.excluded = mergeSpans(l.excluded)
	l.blocks = mergeSpans(l.blocks)
	sort.Slice(l.headings, func(i, j int) bool { return l.headings[i].start < l.headings[j].start })
	var path [6]string
	for i := range l.headings {
		h := &l.headings[i]
		path[h.level-1] = strings.Repeat("#", h.level) + " " + h.title
		clear(path[h.level:])
		var lines []string
		for _, title := range path {
			if title != "" {
				lines = append(lines, title)
			}
		}
		h.path = strings.Join(lines, "\n")
	}
	return l, ctx.Err()
}

func convertRanges(ctx context.Context, source string, ranges []*span) error {
	points := make([]int, 0, 2*len(ranges))
	for _, r := range ranges {
		points = append(points, r.start, r.end)
	}
	slices.Sort(points)
	offsets := make(map[int]int, len(points))
	bytePos, runePos := 0, 0
	for _, point := range slices.Compact(points) {
		if err := ctx.Err(); err != nil {
			return err
		}
		runePos += utf8.RuneCountInString(source[bytePos:point])
		offsets[point], bytePos = runePos, point
	}
	for _, r := range ranges {
		r.start, r.end = offsets[r.start], offsets[r.end]
	}
	return nil
}

func mergeSpans(spans []span) []span {
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := spans[:0]
	for _, r := range spans {
		if r.end <= r.start {
			continue
		}
		if len(merged) > 0 && r.start < merged[len(merged)-1].end {
			merged[len(merged)-1].end = max(merged[len(merged)-1].end, r.end)
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

func (l *Layout) atomAt(position int) (span, bool) {
	i := sort.Search(len(l.atoms), func(i int) bool { return l.atoms[i].end > position })
	if i < len(l.atoms) && l.atoms[i].start < position {
		return l.atoms[i], true
	}
	return span{}, false
}

// Quote 使用原文短引文，合成表头和围栏不占用新的原文坐标。
func (l *Layout) Quote(start, end int) string {
	return strings.TrimSpace(string(l.source[start:min(end, start+128)]))
}

func (l *Layout) Normalize(chunk Chunk) Chunk {
	if atom, ok := l.atomAt(chunk.Start); ok {
		chunk.Start = atom.start
	}
	if atom, ok := l.atomAt(chunk.End); ok {
		chunk.End = atom.end
	}
	chunk.Content = l.render(chunk.Start, chunk.End)
	chunk.ContextHeader = l.header(chunk.Start, chunk.End)
	return chunk
}

func (l *Layout) render(start, end int) string {
	var prefix, suffix string
	i := sort.Search(len(l.codes), func(i int) bool { return l.codes[i].end > start })
	for _, block := range l.codes[i:] {
		if block.start >= end {
			break
		}
		if start > block.start && start < block.end {
			prefix = block.header
		}
		if end >= block.body.start && end <= block.body.end {
			suffix = block.suffix
		}
	}
	i = sort.Search(len(l.tables), func(i int) bool { return l.tables[i].end > start })
	if i < len(l.tables) {
		t := l.tables[i]
		if prefix == "" && start >= t.headerRange.end && start < t.end {
			prefix = t.header
			if t.promoted.end > start {
				prefix = string(l.source[t.headerRange.start:t.headerRange.end])
			}
		}
	}
	// 开头和结尾可能落在不同表格里，结束标签必须按 end 单独查找。
	i = sort.Search(len(l.tables), func(i int) bool { return l.tables[i].end > end })
	if i < len(l.tables) && end > l.tables[i].start && l.tables[i].closing != "" {
		suffix = l.tables[i].closing
	}
	return prefix + string(l.source[start:end]) + suffix
}

// Fit 按完整模型输入检查预算。指数探测限制重复编码量；结构不可再分且仍超预算时明确失败。
func (l *Layout) Fit(ctx context.Context, original Chunk, fits func(Chunk) (bool, error), emit func(Chunk) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	original = l.Normalize(original)
	if ok, err := fits(original); err != nil {
		return err
	} else if ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return emit(original)
	}
	for start := original.Start; start < original.End; {
		if err := ctx.Err(); err != nil {
			return err
		}
		part := original
		check := func(end int) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			part.Start, part.End = start, end
			part.Content = l.render(start, end)
			part.ContextHeader = l.header(start, end) // 硬预算再次细分后，标题路径也必须按新范围重算。
			return fits(part)
		}
		best, high := start, min(start+256, original.End)
		for {
			ok, err := check(high)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			best = high
			if high == original.End {
				break
			}
			high = best + min(best-start, original.End-best)
		}
		for low := best + 1; low <= high; {
			middle := low + (high-low)/2
			ok, err := check(middle)
			if err != nil {
				return err
			}
			if ok {
				best, low = middle, middle+1
			} else {
				high = middle - 1
			}
		}
		if atom, ok := l.atomAt(best); ok {
			best = max(start, atom.start)
		}
		if best <= start {
			return ErrStructureBudget
		}
		// 在预算允许的后半段优先选换行或句末；不能把边界退回结构内部。
		if best < original.End {
			for i := best - 1; i > start+(best-start)/2; i-- {
				if l.source[i] == '\n' || l.source[i] == '。' {
					if _, inside := l.atomAt(i + 1); !inside {
						best = i + 1
						break
					}
				}
			}
		}
		if ok, err := check(best); err != nil {
			return err
		} else if !ok {
			return ErrStructureBudget
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(part); err != nil {
			return err
		}
		start = best
	}
	return nil
}
