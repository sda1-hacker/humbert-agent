package chunker

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
)

var ErrStructureBudget = errors.New("rag chunker: indivisible table row, formula or link exceeds input budget")

type fenceRange struct {
	span
	headerEnd, closeStart int
	header, marker        string
}
type tableRange struct {
	span
	headerEnd, promoteAfter int
	header                  string
}

// Layout 一次建立原文结构索引，二次预算切分复用它；合成表头和围栏不占原文坐标。
type Layout struct {
	source []rune
	atoms  []span
	fences []fenceRange
	tables []tableRange
}

// Quote 直接截取原文字符范围的短引文，避免给每个引用重复编码整篇正文。
func (l *Layout) Quote(start, end int) string {
	return strings.TrimSpace(string(l.source[start:min(end, start+128)]))
}

func (l *Layout) clippedFence(start, end int) bool {
	index := sort.Search(len(l.fences), func(i int) bool { return l.fences[i].end > start })
	for _, block := range l.fences[index:] {
		if block.start >= end {
			break
		}
		if start > block.start || end < block.end {
			return true
		}
	}
	return false
}

func NewLayout(ctx context.Context, text string) (*Layout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	layout := &Layout{source: []rune(text)}
	var fence documenttext.Fence
	offset := 0
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := offset + RuneLen(line)
		active := fence.Active()
		if fence.Observe(line) {
			if !active {
				layout.fences = append(layout.fences, fenceRange{span: span{offset, len(layout.source)}, headerEnd: end, closeStart: len(layout.source), header: line, marker: fence.Marker()})
			} else {
				last := &layout.fences[len(layout.fences)-1]
				last.closeStart, last.end = offset, end
			}
		}
		if !active && !fence.Active() && i+1 < len(lines) && defaultHeaderHooks[0].startPattern.MatchString(line+lines[i+1]) {
			header := line + lines[i+1]
			table := tableRange{span: span{offset, offset + RuneLen(header)}, headerEnd: offset + RuneLen(header), header: header}
			// 沿用已有空表头修复规则，后续切片重复真实列名；第一条原文行自身仍按原样保留。
			if isEmptyTableHeaderRow(header) && i+2 < len(lines) && tableRowPattern.MatchString(strings.TrimSpace(lines[i+2])) {
				table.header = strings.TrimRight(lines[i+2], "\n") + "\n" + extractSeparatorLine(header)
				table.promoteAfter = table.headerEnd + RuneLen(lines[i+2])
			}
			layout.tables = append(layout.tables, table)
		}
		if len(layout.tables) > 0 {
			last := &layout.tables[len(layout.tables)-1]
			if offset >= last.headerEnd && offset == last.end && tableRowPattern.MatchString(strings.TrimSpace(line)) && !headerColumnMismatch(last.header, line) {
				last.end = end
			}
		}
		offset = end
	}
	for _, atom := range protectedSpansRune(text, protectedSpansContext(ctx, text)) {
		index := sort.Search(len(layout.fences), func(i int) bool { return layout.fences[i].end > atom.start })
		if index < len(layout.fences) && layout.fences[index].start <= atom.start {
			continue // 代码块可以切分，但会自动补齐前后围栏；代码内的表格和链接按代码处理。
		}
		layout.atoms = append(layout.atoms, atom)
	}
	for _, block := range layout.fences {
		layout.atoms = append(layout.atoms, span{block.start, block.headerEnd})
		if block.closeStart < block.end {
			layout.atoms = append(layout.atoms, span{block.closeStart, block.end})
		}
	}
	sort.Slice(layout.atoms, func(i, j int) bool { return layout.atoms[i].start < layout.atoms[j].start })
	merged := layout.atoms[:0]
	for _, atom := range layout.atoms {
		if len(merged) > 0 && atom.start < merged[len(merged)-1].end {
			merged[len(merged)-1].end = max(merged[len(merged)-1].end, atom.end)
		} else {
			merged = append(merged, atom)
		}
	}
	layout.atoms = merged
	return layout, ctx.Err()
}

func (l *Layout) atomAt(position int) (span, bool) {
	index := sort.Search(len(l.atoms), func(i int) bool { return l.atoms[i].end > position })
	if index < len(l.atoms) && l.atoms[index].start < position {
		return l.atoms[index], true
	}
	return span{}, false
}

// Normalize 修复上游软切分落在结构内部的边界，并按完整原文范围重新渲染。
func (l *Layout) Normalize(chunk Chunk) Chunk {
	if atom, ok := l.atomAt(chunk.Start); ok {
		chunk.Start = atom.start
	}
	if atom, ok := l.atomAt(chunk.End); ok {
		chunk.End = atom.end
	}
	chunk.Content = l.render(chunk.Start, chunk.End)
	return chunk
}

func (l *Layout) render(start, end int) string {
	var prefix, suffix string
	index := sort.Search(len(l.fences), func(i int) bool { return l.fences[i].end > start })
	for _, block := range l.fences[index:] {
		if block.start >= end {
			break
		}
		if start > block.start && start < block.end {
			prefix = strings.TrimRight(block.header, "\n") + "\n"
		}
		if end >= block.headerEnd && (end <= block.closeStart || block.closeStart == block.end) {
			suffix = "\n" + block.marker + "\n"
		}
	}
	index = sort.Search(len(l.tables), func(i int) bool { return l.tables[i].end > start })
	if prefix == "" && index < len(l.tables) {
		table := l.tables[index]
		if start >= table.headerEnd && start < table.end {
			header := table.header
			if table.promoteAfter > start {
				header = string(l.source[table.start:table.headerEnd])
			}
			prefix = strings.TrimRight(header, "\n") + "\n"
		}
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
