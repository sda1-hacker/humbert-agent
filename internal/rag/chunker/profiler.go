package chunker

import (
	"math"
	"strings"
)

// DocProfile 表示一整篇文档的结构画像。
//
// Profiler 不负责切文档。
//
// 它负责回答：
//
//	“这篇文档长什么样？”
//
// 后面的 Strategy Resolver 再根据这些特征决定：
//
//	Heading
//	Heuristic
//	Legacy
//
// -----------------------------------------------------------------------------
// 这里保留 json tag。
//
// 当前 Demo 还没有 HTTP Preview API，
// 但是 WeKnora 的 Chunk Preview 会直接把 DocProfile 返回给前端。
//
// 我们提前保持相同 JSON 结构，
// 后面增加 Preview API 时就不需要修改数据模型。
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

	// StdLineLen 是非 fenced-code 内容行长度的标准差。
	//
	// 这个指标可以粗略反映文档排版形态：
	//
	//     行长度非常稳定
	//         → 可能是 OCR / 固定宽度文本
	//
	//     行长度差异很大
	//         → 可能有标题、段落、表格等明显结构
	StdLineLen float64 `json:"std_line_len"`

	// -----------------------------------------------------------------
	// Markdown Heading 结构
	// -----------------------------------------------------------------

	// MdHeadingCounts：
	//
	//     heading level -> 数量
	//
	// 例如：
	//
	//     H1 = 1
	//     H2 = 5
	//     H3 = 12
	//
	// 保存成：
	//
	//     map[int]int{
	//         1: 1,
	//         2: 5,
	//         3: 12,
	//     }
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

	// CodeRatio = fenced code 中字符数量 / 整篇文档字符数量。
	//
	// 它可以帮助以后识别：
	//
	//     代码型技术文档
	//
	// 和：
	//
	//     普通自然语言文档
	CodeRatio float64 `json:"code_ratio"`

	// DetectedLangs 是语言提示。
	//
	// 普通文档通常只有一个：
	//
	//     ["zh"]
	//
	// Mixed 文档会转换成：
	//
	//     ["en", "de", "zh"]
	//
	// 这样后面的 Heuristic Splitter 会同时尝试三套章节规则。
	DetectedLangs []string `json:"detected_langs"`
}

// HeadingDensity 返回 Markdown Heading 占总行数的比例。
//
// 例如：
//
//	TotalLines      = 100
//	MdHeadingTotal = 5
//
// 则：
//
//	HeadingDensity = 0.05
func (p *DocProfile) HeadingDensity() float64 {
	if p.TotalLines == 0 {
		return 0
	}

	return float64(p.MdHeadingTotal) / float64(p.TotalLines)
}

// DominantHeadingLevel 返回最适合用作主要分割边界的 Heading Level。
//
// 这是后面 Heading Splitter 非常关键的规则。
//
// 规则分两层。
//
// 第一优先：
//
//	从 H1 → H6
//
// 找第一个：
//
//	出现次数 >= 3
//
// 的层级。
//
// 例如：
//
//	H1 = 1
//	H2 = 5
//	H3 = 20
//
// 返回：
//
//	H2
//
// 因为 H2 已经形成稳定的文档主骨架。
//
// -----------------------------------------------------------------------------
// 如果没有任何层级出现至少 3 次：
//
//	选择最深的、至少出现一次的 Heading Level。
//
// 例如：
//
//	H1 = 1
//	H2 = 2
//
// 返回：
//
//	H2
//
// 对小型文档来说，用 H2 比只用 H1 更有实际分块价值。
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

// HeuristicMarkerTotal 返回所有“非 Markdown Heading”结构信号总数。
//
// 注意这里没有加入：
//
//	BlankParagraphBreaks
//	RepeatedFooterCount
//
// 这是当前 WeKnora 的真实策略。
//
// 真正参与这个总分的有：
//
//	编号章节
//	德文章节
//	英文章节
//	中文章节
//	全大写标题
//	视觉分隔线
//	换页符
func (p *DocProfile) HeuristicMarkerTotal() int {
	return p.NumberedSectionCount +
		p.GermanChapterCount +
		p.EnglishChapterCount +
		p.ChineseChapterCount +
		p.AllCapsShortLineCount +
		p.VisualSepCount +
		p.FormFeedCount
}

// ProfileDocument 扫描整篇文档并生成 DocProfile。
//
// 这个过程不会修改原文，也不会产生 Chunk。
//
// 整体流程：
//
//	Document
//	    ↓
//	rune / line 基础统计
//	    ↓
//	逐行结构扫描
//	    ↓
//	Heading / Chapter / Table / Code...
//	    ↓
//	行长度统计
//	    ↓
//	Language Detection
//	    ↓
//	DocProfile
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

		// -------------------------------------------------------------
		// Fenced Code 检测。
		//
		// 当前使用非常简单的：
		//
		//     strings.HasPrefix(trimmed, "```")
		//
		// 每看到一次就 toggle。
		//
		// 这里没有使用 Protected Span 正则，
		// 因为 Profiler 只需要便宜的结构扫描。
		// -------------------------------------------------------------

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			profile.HasCode = true

			// fence 本身不进入：
			//
			//     codeChars
			//     line length stats
			//     heading detection
			continue
		}

		if inFence {
			codeChars += RuneLen(line)
			continue
		}

		lineLen := RuneLen(line)
		lengths = append(lengths, float64(lineLen))

		// Markdown Heading 的优先级最高。
		//
		// 如果这一行已经确定是：
		//
		//     ## Installation
		//
		// 就不继续把它识别为：
		//
		//     ALL CAPS
		//     Numbered section
		//
		// 等其他 heuristic signal。
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

		// 当前用一个非常便宜的规则判断 Markdown Table。
		//
		// 只要某个非代码行：
		//
		//     去掉首尾空格后
		//     以 | 开始
		//     以 | 结束
		//
		// 就认为文档包含表格。
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			profile.HasTables = true
		}
	}

	calculateLineStats(profile, lengths)

	if profile.TotalChars > 0 {
		profile.CodeRatio = float64(codeChars) / float64(profile.TotalChars)
	}

	// 当前 WeKnora 使用 "\n\n\n" 的出现次数
	// 作为 BlankParagraphBreaks。
	//
	// 注意：
	//
	// strings.Count 是非重叠计数，
	// 它和 ExcessiveBlanksPattern 的“结构边界检测”
	// 不是完全相同的概念。
	profile.BlankParagraphBreaks = strings.Count(text, "\n\n\n")

	detectProfileLanguages(profile, text)

	return profile
}

// matchHeading 判断当前行是否为 Markdown ATX Heading。
//
// 如果匹配：
//
//	## Installation
//
// 会得到：
//
//	level = 2
//
// 并执行：
//
//	counts[2]++
//
// 返回 true。
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

// calculateLineStats 计算平均行长和标准差。
//
// 标准差使用总体方差：
//
//	variance /= N
//
// 而不是样本方差：
//
//	variance /= N - 1
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

// detectProfileLanguages 为 DocProfile 填充语言提示。
//
// 为了避免超大文档语言检测扫描全部内容，
// 当前只取前 4096 byte 作为 sample。
//
// 注意这里是：
//
//	byte
//
// 不是 rune。
//
// 这是为了对标当前 WeKnora 的真实实现。
//
// Language Detection 本身只是辅助信号，
// 即使 UTF-8 恰好在边界被截断，一个尾部 RuneError
// 对整体 CJK / Latin 比例影响也可以忽略。
func detectProfileLanguages(profile *DocProfile, text string) {
	const sampleSize = 4096

	sample := text

	if len(sample) > sampleSize {
		sample = sample[:sampleSize]
	}

	lang := DetectLanguage(sample)

	profile.DetectedLangs = []string{lang}

	// mixed 的语义比较特殊。
	//
	// 后面的 Heuristic Splitter 不应该使用一个：
	//
	//     "mixed chapter regex"
	//
	// 而应该把所有支持的语言章节规则都打开。
	//
	// 因此转换成：
	//
	//     en
	//     de
	//     zh
	if lang == LangMixed {
		profile.DetectedLangs = []string{
			LangEnglish,
			LangGerman,
			LangChinese,
		}
	}
}
