package documenttext

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tsawler/tabula/text"
)

// 下列几何阅读顺序和质量回退规则移植自 WeKnora pdf_parser.py（2c0d838）。
// Tabula 返回文本片段而非 PDFium 的逐字包围盒，不能据此伪造精确字形坐标；正文不经过 RAG 分块器。
type pdfLine struct {
	text   string
	height float64
}

func pdfMedian(values []float64) float64 {
	if len(values) == 0 {
		return 1
	}
	slices.Sort(values)
	n := len(values)
	if n%2 == 1 {
		return values[n/2]
	}
	return (values[n/2-1] + values[n/2]) / 2
}

func pdfLayoutText(fragments []text.TextFragment, width float64, cfg PDFOptions) string {
	heights := make([]float64, 0, len(fragments))
	for _, f := range fragments {
		if f.Height > 0 {
			heights = append(heights, f.Height)
		}
	}
	scale := pdfMedian(heights)
	columns := pdfColumns(fragments, math.Max(scale*2.5, width*.04), 0)
	var kept [][]text.TextFragment
	for _, col := range columns {
		lines := pdfLines(col, cfg.WordGapWidthRatio)
		span, yMin, yMax, single := pdfColumnSpan(col), math.Inf(1), math.Inf(-1), 0
		for _, f := range col {
			yMin = math.Min(yMin, f.Y+f.Height/2)
			yMax = math.Max(yMax, f.Y+f.Height/2)
		}
		for _, line := range lines {
			if utf8.RuneCountInString(line.text) <= 2 {
				single++
			}
		}
		fraction := float64(single) / float64(max(1, len(lines)))
		artifact := span <= 0 || (span/width < cfg.MarginColWidthRatio && fraction >= .45) || (yMax-yMin > span*3.5 && len(col) >= 8 && fraction >= .35)
		if !artifact {
			kept = append(kept, col)
		}
	}
	if len(kept) == 0 && len(columns) > 0 {
		widest := columns[0]
		for _, col := range columns[1:] {
			if pdfColumnSpan(col) > pdfColumnSpan(widest) {
				widest = col
			}
		}
		kept = append(kept, widest)
	}
	var blocks []string
	for _, col := range kept {
		lines := pdfLines(col, cfg.WordGapWidthRatio)
		// 仅标点的偏移行并回上一行，避免 Figure 1 与冒号被拆成两段。
		merged := make([]pdfLine, 0, len(lines))
		for _, line := range lines {
			if len(merged) > 0 && pdfOrphanPunctuationLine(line.text) {
				last := &merged[len(merged)-1]
				if strings.HasSuffix(last.text, " ") || strings.HasSuffix(last.text, "-") {
					last.text = strings.TrimSpace(last.text + " " + line.text)
				} else {
					last.text += strings.Join(strings.Fields(line.text), "")
				}
			} else {
				merged = append(merged, line)
			}
		}
		heights = heights[:0]
		for _, line := range merged {
			heights = append(heights, line.height)
		}
		body := pdfMedian(heights)
		levels := make([]int, len(merged))
		count := 0
		for i, line := range merged {
			n := utf8.RuneCountInString(line.text)
			if !cfg.DetectHeadings || body <= 0 || n < cfg.MinHeadingLineChars || n > 80 || strings.ContainsRune(".。!！?？,，;；:：", []rune(line.text)[n-1]) {
				continue
			}
			ratio := line.height / body
			switch {
			case ratio >= 2:
				levels[i] = 1
			case ratio >= 1.6:
				levels[i] = 2
			case ratio >= 1.35:
				levels[i] = 3
			}
			if levels[i] > 0 {
				count++
			}
		}
		if count > max(1, int(.4*float64(len(merged)))) {
			clear(levels)
		}
		var out []string
		for i, line := range merged {
			prefix := ""
			if levels[i] > 0 {
				prefix = strings.Repeat("#", levels[i]) + " "
			}
			out = append(out, prefix+line.text)
		}
		if len(out) > 0 {
			blocks = append(blocks, strings.Join(out, "\n"))
		}
	}
	return strings.Join(blocks, "\n")
}

func pdfColumnSpan(fragments []text.TextFragment) float64 {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, f := range fragments {
		lo = math.Min(lo, f.X)
		hi = math.Max(hi, f.X+f.Width)
	}
	return hi - lo
}

// pdfColumns 在没有任何文字跨越的完整栏间距递归分栏；最多 10 层，与参考实现一致。
func pdfColumns(fragments []text.TextFragment, minGap float64, depth int) [][]text.TextFragment {
	if len(fragments) <= 1 || depth > 10 {
		return [][]text.TextFragment{fragments}
	}
	ordered := slices.Clone(fragments)
	slices.SortStableFunc(ordered, func(a, b text.TextFragment) int { return comparePDF(a.X, b.X) })
	end, best, cut := ordered[0].X+ordered[0].Width, 0., 0.
	for _, f := range ordered[1:] {
		gap := f.X - end
		if gap >= minGap && gap > best {
			best, cut = gap, end+gap/2
		}
		end = math.Max(end, f.X+f.Width)
	}
	if best == 0 {
		return [][]text.TextFragment{fragments}
	}
	var left, right []text.TextFragment
	for _, f := range fragments {
		if f.X+f.Width/2 < cut {
			left = append(left, f)
		} else {
			right = append(right, f)
		}
	}
	if len(left) == 0 || len(right) == 0 {
		return [][]text.TextFragment{fragments}
	}
	return append(pdfColumns(left, minGap, depth+1), pdfColumns(right, minGap, depth+1)...)
}

func comparePDF(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func pdfLines(fragments []text.TextFragment, gapRatio float64) []pdfLine {
	if len(fragments) == 0 {
		return nil
	}
	ordered := slices.Clone(fragments)
	slices.SortStableFunc(ordered, func(a, b text.TextFragment) int { return comparePDF(b.Y+b.Height/2, a.Y+a.Height/2) })
	var heights []float64
	for _, f := range ordered {
		if f.Height > 0 {
			heights = append(heights, f.Height)
		}
	}
	tolerance := pdfMedian(heights) * .5
	var groups [][]text.TextFragment
	for _, f := range ordered {
		if strings.TrimSpace(f.Text) == "" {
			continue
		}
		if len(groups) == 0 || math.Abs(f.Y+f.Height/2-(groups[len(groups)-1][0].Y+groups[len(groups)-1][0].Height/2)) > tolerance {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], f)
	}
	var lines []pdfLine
	for _, group := range groups {
		slices.SortStableFunc(group, func(a, b text.TextFragment) int { return comparePDF(a.X, b.X) })
		var widths []float64
		heights = heights[:0]
		for _, f := range group {
			if f.Width > 0 {
				widths = append(widths, f.Width/float64(max(1, utf8.RuneCountInString(f.Text))))
			}
			if f.Height > 0 {
				heights = append(heights, f.Height)
			}
		}
		threshold := pdfMedian(widths) * gapRatio
		var line strings.Builder
		for i, f := range group {
			if i > 0 && f.X-(group[i-1].X+group[i-1].Width) > threshold && !strings.HasSuffix(group[i-1].Text, " ") && !strings.HasPrefix(f.Text, " ") {
				line.WriteByte(' ')
			}
			line.WriteString(f.Text)
		}
		if s := strings.TrimSpace(line.String()); s != "" {
			lines = append(lines, pdfLine{s, pdfMedian(heights)})
		}
	}
	return lines
}

var pdfNumbers = regexp.MustCompile(`[+−-]?\p{Nd}+(?:[.,:/]\p{Nd}+)*(?:[%‰])?`)
var pdfCitation = regexp.MustCompile(`\[[\p{L}\p{N}_]+,\s`)
var pdfBrokenCitation = regexp.MustCompile(`\[[\p{L}\p{N}_]+\s+[\p{L}\p{N}_]+\s+\p{Nd}`)

func pdfPlainWellFormed(plain string) bool {
	if pdfCitation.MatchString(plain) || strings.Count(plain, " . . ") >= 2 {
		return true
	}
	words := strings.Fields(plain)
	n := 0
	for _, w := range words {
		n += utf8.RuneCountInString(w)
	}
	return len(words) >= 30 && float64(n)/float64(len(words)) >= 5
}

func pdfPreferPlain(plain, layout string) bool {
	if strings.TrimSpace(layout) == "" {
		return true
	}
	if strings.TrimSpace(plain) == "" {
		return false
	}
	// 几何重排不得改变数值证据；按多重集合比较，允许合法的分栏重排。
	counts := map[string]int{}
	for _, n := range pdfNumbers.FindAllString(plain, -1) {
		counts[n]++
	}
	for _, n := range pdfNumbers.FindAllString(layout, -1) {
		counts[n]--
	}
	for _, n := range counts {
		if n != 0 {
			return true
		}
	}
	n, single, punct := 0, 0, 0
	for line := range strings.SplitSeq(layout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n++
		if utf8.RuneCountInString(line) <= 2 {
			single++
		}
		if pdfPunctuationLine(line) {
			punct++
		}
	}
	if n == 0 || float64(single)/float64(n) >= .18 || float64(punct)/float64(n) >= .12 {
		return true
	}
	if pdfGarbledFraction(layout) >= .20 && pdfGarbledFraction(plain) < .08 {
		return true
	}
	if pdfCitation.MatchString(plain) && pdfBrokenCitation.MatchString(layout) {
		return true
	}
	alnum := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				return r
			}
			return -1
		}, s)
	}
	for line := range strings.SplitSeq(plain, "\n") {
		if utf8.RuneCountInString(strings.TrimSpace(line)) < 24 {
			continue
		}
		probe := []rune(alnum(line))
		if len(probe) > 16 {
			probe = probe[:16]
		}
		if len(probe) >= 12 {
			return !strings.Contains(alnum(layout), string(probe))
		}
	}
	return false
}

func pdfPunctuationLine(s string) bool {
	return s != "" && utf8.RuneCountInString(s) <= 4 && strings.Trim(s, " \t.,;:!?…·-–—") == ""
}

func pdfGarbledFraction(s string) float64 {
	n, bad := 0, 0
	for line := range strings.SplitSeq(s, "\n") {
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		n++
		short := 0
		for _, w := range words {
			if utf8.RuneCountInString(w) <= 2 {
				short++
			}
		}
		if len(words) >= 6 && float64(short)/float64(len(words)) > .45 {
			bad++
		}
	}
	return float64(bad) / float64(max(1, n))
}

// 孤立标点合并不包含横线；质量检查中的纯标点行仍包含横线，二者对应参考实现不同规则。
func pdfOrphanPunctuationLine(s string) bool {
	return s != "" && utf8.RuneCountInString(s) <= 4 && strings.Trim(s, " \t.,;:!?…·") == ""
}
