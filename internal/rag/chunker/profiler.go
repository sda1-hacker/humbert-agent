package chunker

import (
	"math"
	"strings"
)

// DocProfile 记录文档长度、标题及章节信号，供自动选择分块策略使用。
type DocProfile struct {
	// -----------------------------------------------------------------
	// 基础规模统计
	// -----------------------------------------------------------------

	// TotalChars 是整篇文档 Unicode rune 数量。
	TotalChars int `json:"total_chars"`

	// TotalLines 是按 "\n" 分割后的总行数。
	TotalLines int `json:"total_lines"`

	// AvgLineLen 是非 fenced-code 内容的平均行长度。
	AvgLineLen float64 `json:"avg_line_len"`

	// StdLineLen 是非代码内容行长度的总体标准差，用于粗略描述排版形态。
	StdLineLen float64 `json:"std_line_len"`

	// -----------------------------------------------------------------
	// Markdown Heading 结构
	// -----------------------------------------------------------------

	// MdHeadingCounts 保存各标题层级的数量。
	MdHeadingCounts map[int]int `json:"md_heading_counts"`

	// MdHeadingTotal 是所有 Markdown Heading 总数。
	MdHeadingTotal int `json:"md_heading_total"`

	// -----------------------------------------------------------------
	// Heuristic 结构信号
	// -----------------------------------------------------------------

	NumberedSectionCount  int `json:"numbered_section_count"`
	AllCapsShortLineCount int `json:"all_caps_short_line_count"`
	BlankParagraphBreaks  int `json:"blank_paragraph_breaks"`
	FormFeedCount         int `json:"form_feed_count"`
	VisualSepCount        int `json:"visual_sep_count"`

	GermanChapterCount  int `json:"german_chapter_count"`
	EnglishChapterCount int `json:"english_chapter_count"`
	ChineseChapterCount int `json:"chinese_chapter_count"`

	// 名字叫 RepeatedFooterCount，
	// 但当前实现实际上统计的是匹配 PageFooterPattern 的行数。
	//
	// 是否“重复”并没有进一步做文本去重判断。
	RepeatedFooterCount int `json:"repeated_footer_count"`

	// -----------------------------------------------------------------
	// 内容特征
	// -----------------------------------------------------------------

	HasTables bool `json:"has_tables"`
	HasCode   bool `json:"has_code"`

	// CodeRatio 是代码字符数占文档总字符数的比例。
	CodeRatio float64 `json:"code_ratio"`

	// DetectedLangs 是章节识别的语言提示，混合文本启用全部已支持语言。
	DetectedLangs []string `json:"detected_langs"`
}

// HeadingDensity 返回标题行数与总行数的比例。
func (p *DocProfile) HeadingDensity() float64 {
	if p.TotalLines == 0 {
		return 0
	}

	return float64(p.MdHeadingTotal) / float64(p.TotalLines)
}

// DominantHeadingLevel 选择用于划分主章节的标题层级。
func (p *DocProfile) DominantHeadingLevel() int {
	if p.MdHeadingTotal == 0 {
		return 0
	}

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

// HeuristicMarkerTotal 返回启发式结构信号的总数。
func (p *DocProfile) HeuristicMarkerTotal() int {
	return p.NumberedSectionCount +
		p.GermanChapterCount +
		p.EnglishChapterCount +
		p.ChineseChapterCount +
		p.AllCapsShortLineCount +
		p.VisualSepCount +
		p.FormFeedCount
}

// ProfileDocument 扫描文档结构，统计标题、章节、表格、代码及分页信号。
func ProfileDocument(text string) *DocProfile {
	profile := &DocProfile{
		MdHeadingCounts: make(map[int]int),
	}

	if text == "" {
		return profile
	}

	// 整篇文档字符数必须继续遵守整个 Chunker 的约定：
	//
	//     Unicode rune
	//
	// 而不是 byte。
	profile.TotalChars = RuneLen(text)

	// \f 一般来自 PDF 页面边界。
	profile.FormFeedCount = strings.Count(text, "\f")

	lines := strings.Split(text, "\n")
	profile.TotalLines = len(lines)

	// lengths 只统计非 fenced-code 行。
	//
	// 原因是大量代码行会严重改变普通文档的行长分布，
	// 而 Profiler 的主要目标是判断文档结构。
	lengths := make([]float64, 0, len(lines))

	inFence := false
	codeChars := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 快速扫描围栏代码，代码内部不统计标题或章节信号。

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			profile.HasCode = true

			// 代码区域不参与标题识别与普通行长统计。
			continue
		}

		if inFence {
			codeChars += RuneLen(line)
			continue
		}

		lineLen := RuneLen(line)
		lengths = append(lengths, float64(lineLen))

		// 已识别为 Markdown 标题的行不再重复计入其他结构信号。
		if matchHeading(line, profile.MdHeadingCounts) {
			profile.MdHeadingTotal++
			continue
		}

		if NumberedSectionPattern.MatchString(line) {
			profile.NumberedSectionCount++
		}

		if GermanChapterPattern.MatchString(line) {
			profile.GermanChapterCount++
		}

		if EnglishChapterPattern.MatchString(line) {
			profile.EnglishChapterCount++
		}

		if ChineseChapterPattern.MatchString(line) {
			profile.ChineseChapterCount++
		}

		if AllCapsHeadingPattern.MatchString(line) {
			profile.AllCapsShortLineCount++
		}

		if VisualSeparatorPattern.MatchString(line) {
			profile.VisualSepCount++
		}

		if PageFooterPattern.MatchString(line) {
			profile.RepeatedFooterCount++
		}

		// 使用两端竖线快速判断是否包含 Markdown 表格。
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			profile.HasTables = true
		}
	}

	calculateLineStats(profile, lengths)

	if profile.TotalChars > 0 {
		profile.CodeRatio = float64(codeChars) / float64(profile.TotalChars)
	}

	// 画像中的空白计数使用非重叠统计，与完整边界检测的正则计数不同。
	profile.BlankParagraphBreaks = strings.Count(text, "\n\n\n")

	detectProfileLanguages(profile, text)

	return profile
}

// matchHeading 判断 Markdown ATX 标题并累计对应层级的数量。
func matchHeading(line string, counts map[int]int) bool {
	match := MarkdownHeadingPattern.FindStringSubmatch(line)

	if match == nil {
		return false
	}

	level := len(match[1])

	if level < 1 || level > 6 {
		return false
	}

	counts[level]++

	return true
}

// calculateLineStats 计算平均行长与总体标准差。
func calculateLineStats(profile *DocProfile, lengths []float64) {
	if len(lengths) == 0 {
		return
	}

	var sum float64

	for _, length := range lengths {
		sum += length
	}

	profile.AvgLineLen = sum / float64(len(lengths))

	var variance float64

	for _, length := range lengths {
		diff := length - profile.AvgLineLen
		variance += diff * diff
	}

	variance /= float64(len(lengths))
	profile.StdLineLen = math.Sqrt(variance)
}

// detectProfileLanguages 仅采样文档前 4096 字节，以限制语言判断的扫描成本。
func detectProfileLanguages(profile *DocProfile, text string) {
	const sampleSize = 4096

	sample := text

	if len(sample) > sampleSize {
		sample = sample[:sampleSize]
	}

	lang := DetectLanguage(sample)

	profile.DetectedLangs = []string{lang}

	// 混合语言启用全部已支持的章节识别规则。
	if lang == LangMixed {
		profile.DetectedLangs = []string{
			LangEnglish,
			LangGerman,
			LangChinese,
		}
	}
}
