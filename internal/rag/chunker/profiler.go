package chunker

import (
	"context"
	"math"
	"sort"
	"strings"
)

// DocProfile 保留导入诊断字段；Markdown 特征全部来自 AST，正则仅用于非 Markdown 的章节提示。
type DocProfile struct {
	TotalChars            int         `json:"total_chars"`
	TotalLines            int         `json:"total_lines"`
	AvgLineLen            float64     `json:"avg_line_len"`
	StdLineLen            float64     `json:"std_line_len"`
	MdHeadingCounts       map[int]int `json:"md_heading_counts"`
	MdHeadingTotal        int         `json:"md_heading_total"`
	NumberedSectionCount  int         `json:"numbered_section_count"`
	AllCapsShortLineCount int         `json:"all_caps_short_line_count"`
	BlankParagraphBreaks  int         `json:"blank_paragraph_breaks"`
	FormFeedCount         int         `json:"form_feed_count"`
	VisualSepCount        int         `json:"visual_sep_count"`
	GermanChapterCount    int         `json:"german_chapter_count"`
	EnglishChapterCount   int         `json:"english_chapter_count"`
	ChineseChapterCount   int         `json:"chinese_chapter_count"`
	RepeatedFooterCount   int         `json:"repeated_footer_count"`
	HasTables             bool        `json:"has_tables"`
	HasCode               bool        `json:"has_code"`
	CodeRatio             float64     `json:"code_ratio"`
	DetectedLangs         []string    `json:"detected_langs"`
}

func (p *DocProfile) HeadingDensity() float64 {
	return float64(p.MdHeadingTotal) / float64(max(1, p.TotalLines))
}

func (p *DocProfile) DominantHeadingLevel() int {
	for level := 1; level <= 6; level++ {
		if p.MdHeadingCounts[level] >= 3 {
			return level
		}
	}
	for level := 6; level >= 1; level-- {
		if p.MdHeadingCounts[level] > 0 {
			return level
		}
	}
	return 0
}

func (p *DocProfile) HeuristicMarkerTotal() int {
	return p.NumberedSectionCount + p.AllCapsShortLineCount + p.GermanChapterCount + p.EnglishChapterCount + p.ChineseChapterCount + p.VisualSepCount + p.FormFeedCount
}

func ProfileDocument(text string) *DocProfile {
	layout, err := NewLayout(context.Background(), text)
	if err != nil {
		return &DocProfile{MdHeadingCounts: map[int]int{}}
	}
	return layout.profile(context.Background(), 0, len(layout.source))
}

func (l *Layout) profile(ctx context.Context, start, end int) *DocProfile {
	body := string(l.source[start:end])
	p := &DocProfile{TotalChars: end - start, TotalLines: strings.Count(body, "\n") + 1, MdHeadingCounts: make(map[int]int),
		FormFeedCount: strings.Count(body, "\f"), BlankParagraphBreaks: strings.Count(body, "\n\n\n")}
	first := sort.Search(len(l.headings), func(i int) bool { return l.headings[i].start >= start })
	for _, h := range l.headings[first:] {
		if h.start >= end {
			break
		}
		if h.start >= start && h.start < end {
			p.MdHeadingCounts[h.level]++
			p.MdHeadingTotal++
		}
	}
	first = sort.Search(len(l.codes), func(i int) bool { return l.codes[i].end > start })
	for _, block := range l.codes[first:] {
		if block.start >= end {
			break
		}
		if block.start < end && block.end > start {
			p.HasCode = true
			p.CodeRatio += float64(min(end, block.end) - max(start, block.start))
		}
	}
	p.CodeRatio /= float64(max(1, p.TotalChars))
	first = sort.Search(len(l.tables), func(i int) bool { return l.tables[i].end > start })
	p.HasTables = first < len(l.tables) && l.tables[first].start < end
	first = sort.Search(len(l.dividers), func(i int) bool { return l.dividers[i].start >= start })
	for _, divider := range l.dividers[first:] {
		if divider.start >= end {
			break
		}
		if divider.start >= start && divider.start < end {
			p.VisualSepCount++
		}
	}
	var sum, sumSquares, count float64
	l.plainLines(ctx, start, end, func(line string, _ int) {
		length := float64(RuneLen(line))
		sum, sumSquares, count = sum+length, sumSquares+length*length, count+1
		for i, pattern := range plainPatterns {
			if pattern.MatchString(line) {
				switch i {
				case 0:
					p.NumberedSectionCount++
				case 1:
					p.AllCapsShortLineCount++
				case 2:
					p.GermanChapterCount++
				case 3:
					p.EnglishChapterCount++
				case 4:
					p.ChineseChapterCount++
				case 5:
					p.RepeatedFooterCount++
				}
			}
		}
	})
	if count > 0 {
		p.AvgLineLen = sum / count
		p.StdLineLen = math.Sqrt(max(0, sumSquares/count-p.AvgLineLen*p.AvgLineLen))
	}
	sample := body[:min(len(body), 4096)]
	p.DetectedLangs = []string{DetectLanguage(sample)}
	if p.DetectedLangs[0] == LangMixed {
		p.DetectedLangs = []string{LangEnglish, LangGerman, LangChinese}
	}
	return p
}

// plainLines 跳过 AST 已确认的标题、代码、表格和公式，避免把其中的内容误当成普通章节。
func (l *Layout) plainLines(ctx context.Context, start, end int, visit func(string, int)) {
	position := start
	for line := range strings.SplitAfterSeq(string(l.source[start:end]), "\n") {
		if ctx.Err() != nil {
			return
		}
		i := sort.Search(len(l.excluded), func(i int) bool { return l.excluded[i].end > position })
		if i == len(l.excluded) || position < l.excluded[i].start {
			visit(strings.TrimSuffix(line, "\n"), position)
		}
		position += RuneLen(line)
	}
}

func (l *Layout) heuristicBoundaries(ctx context.Context, start, end int, languages []string) []int {
	var points []int
	patterns := ChapterPatternsForLangs(languages)
	l.plainLines(ctx, start, end, func(line string, at int) {
		matched := NumberedSectionPattern.MatchString(line) || AllCapsHeadingPattern.MatchString(line) || PageFooterPattern.MatchString(line)
		for _, pattern := range patterns {
			matched = matched || pattern.MatchString(line)
		}
		if matched {
			points = append(points, at)
		}
		for i, r := range []rune(line) {
			if r == '\f' {
				points = append(points, at+i+1)
			}
		}
	})
	sort.Ints(points)
	return points
}
