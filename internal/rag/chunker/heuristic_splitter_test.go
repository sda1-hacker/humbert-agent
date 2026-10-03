package chunker

import (
	"strings"
	"testing"
)

// TestSplitByHeuristicsEmptyText
//
// 空文本应该直接返回 nil。
func TestSplitByHeuristicsEmptyText(t *testing.T) {
	got := splitByHeuristicsImpl("", DefaultConfig(), nil)

	if got != nil {
		t.Fatalf("空文档应该返回 nil: got=%v", got)
	}
}

// TestSplitByHeuristicsShortDocumentFallsBackLegacy
//
// 即使短文档中存在 Heuristic Marker，
// 如果整篇文档本身 <= ChunkSize，
// 也不值得为了结构进一步碎片化。
//
// 应该直接走 Legacy。
func TestSplitByHeuristicsShortDocumentFallsBackLegacy(t *testing.T) {
	text := "第一章 总则\n这里是一小段内容。"

	cfg := DefaultConfig()
	cfg.ChunkSize = 1000
	cfg.ChunkOverlap = 20

	got := splitByHeuristicsImpl(text, cfg, nil)
	want := SplitText(text, cfg)

	if len(got) != len(want) {
		t.Fatalf("短文档应该直接退回 Legacy: want=%d got=%d", len(want), len(got))
	}

	if len(got) != 1 {
		t.Fatalf("短文档应该只有一个 Chunk: got=%d", len(got))
	}

	if got[0].Content != text {
		t.Fatalf("短文档 Content 被修改\nwant=%q\ngot =%q", text, got[0].Content)
	}
}

// TestSplitByHeuristicsFormFeedBoundary
//
// FormFeed 是 PDF Parser 常见的换页标记，
// 也是最高优先级的 Heuristic Boundary。
func TestSplitByHeuristicsFormFeedBoundary(t *testing.T) {
	text :=
		strings.Repeat("第一页正文内容。", 40) +
			"\f" +
			strings.Repeat("第二页正文内容。", 40)

	cfg := SplitterConfig{
		ChunkSize:    300,
		ChunkOverlap: 20,
		Separators:   []string{"。"},
		Languages:    []string{LangChinese},
	}

	chunks := splitByHeuristicsImpl(text, cfg, nil)

	if len(chunks) < 2 {
		t.Fatalf("FormFeed 应产生多个 Chunk: got=%d", len(chunks))
	}
}

// TestSplitByHeuristicsNumberedSections
//
// 验证：
//
//  1. Introduction
//  2. Methods
//  3. Results
//
// 这种纯文本章节结构。
func TestSplitByHeuristicsNumberedSections(t *testing.T) {
	body := strings.Repeat("This is body content. ", 10)

	text :=
		"1. Introduction\n" + body + "\n\n" +
			"2. Methods\n" + body + "\n\n" +
			"3. Results\n" + body

	cfg := SplitterConfig{
		ChunkSize:    220,
		ChunkOverlap: 20,
		Separators:   []string{". "},
		Languages:    []string{LangEnglish},
	}

	chunks := splitByHeuristicsImpl(text, cfg, nil)

	if len(chunks) < 2 {
		t.Fatalf("Numbered Sections 应产生多个 Chunk: got=%d", len(chunks))
	}
}

// TestSplitByHeuristicsChineseChapterMarkers
//
// 验证：
//
//	第一章
//	第二章
//
// 中文章节结构。
func TestSplitByHeuristicsChineseChapterMarkers(t *testing.T) {
	body := strings.Repeat("这里是用于测试中文章节分块的正文内容。", 30)

	text :=
		"第一章 引言\n" + body + "\n\n" +
			"第二章 方法\n" + body + "\n\n" +
			"第三章 结果\n" + body

	cfg := SplitterConfig{
		ChunkSize:    300,
		ChunkOverlap: 30,
		Separators:   []string{"。"},
		Languages:    []string{LangChinese},
	}

	chunks := splitByHeuristicsImpl(text, cfg, nil)

	if len(chunks) < 3 {
		t.Fatalf("中文章节应该被拆成多个 Chunk: got=%d", len(chunks))
	}
}

// TestSplitByHeuristicsGermanChapterMarkers
//
// 验证德语：
//
//	Kapitel
//	Abschnitt
//	Teil
func TestSplitByHeuristicsGermanChapterMarkers(t *testing.T) {
	body := strings.Repeat("Dies ist ein Beispieltext. ", 12)

	text :=
		"Kapitel 1: Einführung\n" + body + "\n\n" +
			"Kapitel 2: Hauptteil\n" + body

	cfg := SplitterConfig{
		ChunkSize:    220,
		ChunkOverlap: 20,
		Separators:   []string{". "},
		Languages:    []string{LangGerman},
	}

	chunks := splitByHeuristicsImpl(text, cfg, nil)

	if len(chunks) < 2 {
		t.Fatalf("German Chapter Marker 应产生多个 Chunk: got=%d", len(chunks))
	}
}

// TestSplitByHeuristicsUnstructuredDocument
//
// 没有任何 Heuristic Boundary 的普通文本
// 应退回 Legacy。
func TestSplitByHeuristicsUnstructuredDocument(t *testing.T) {
	text := strings.Repeat(
		"plain prose without any structural marker. ",
		5,
	)

	cfg := SplitterConfig{
		ChunkSize:    1000,
		ChunkOverlap: 20,
		Separators:   []string{". "},
	}

	chunks := splitByHeuristicsImpl(text, cfg, nil)

	if len(chunks) != 1 {
		t.Fatalf("普通短文档应该只有一个 Chunk: got=%d", len(chunks))
	}
}

// TestFindHeuristicBoundariesOrdered
//
// findHeuristicBoundaries 最终必须保证 Boundary
// 按 rune offset 升序排列。
func TestFindHeuristicBoundariesOrdered(t *testing.T) {
	text := `Kapitel 1: A
正文。

---

2. Section B
正文。

Page 3 of 10

第三章 C
正文。
`

	bounds := findHeuristicBoundaries(text, nil)

	if len(bounds) < 2 {
		t.Fatalf("应该识别出多个 Boundary: got=%d", len(bounds))
	}

	for i := 1; i < len(bounds); i++ {
		if bounds[i].runeStart < bounds[i-1].runeStart {
			t.Fatalf(
				"Boundary 顺序错误: bounds[%d]=%d < bounds[%d]=%d",
				i,
				bounds[i].runeStart,
				i-1,
				bounds[i-1].runeStart,
			)
		}
	}
}

// TestFindHeuristicBoundariesUsesRuneOffset
//
// 中文必须验证 rune offset，
// 防止不小心把 regexp/string byte offset 直接写入 Chunk。
func TestFindHeuristicBoundariesUsesRuneOffset(t *testing.T) {
	prefix := "这是中文前言。\n\n"
	text := prefix + "第一章 总则\n正文。"

	bounds := findHeuristicBoundaries(text, []string{LangChinese})

	found := false

	for _, item := range bounds {
		if item.runeStart == RuneLen(prefix) {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf(
			"中文 Chapter Boundary 应位于 rune offset=%d, got=%v",
			RuneLen(prefix),
			bounds,
		)
	}
}

// TestFindHeuristicBoundariesIgnoresFence
//
// fenced code 内：
//
//  1. Section
//     CHAPTER
//     ---
//
// 都不能被当作真实文档结构。
func TestFindHeuristicBoundariesIgnoresFence(t *testing.T) {
	text := "第一章 正文\n" +
		"内容。\n\n" +
		"```text\n" +
		"1. Fake Section\n" +
		"FAKE TITLE\n" +
		"---\n" +
		"```\n\n" +
		"第二章 正文\n" +
		"内容。"

	bounds := findHeuristicBoundaries(text, []string{LangChinese})

	fakeOffset := RuneLen("第一章 正文\n内容。\n\n```text\n")

	for _, item := range bounds {
		if item.runeStart == fakeOffset {
			t.Fatalf("代码块内部的 Numbered Section 不应该成为 Boundary")
		}
	}
}

// TestSplitByHeuristicsOversizeBlockUsesLegacy
//
// 一个 Heuristic Section 自己就非常大时，
// 必须交给 Legacy SplitText 做内部切分。
func TestSplitByHeuristicsOversizeBlockUsesLegacy(t *testing.T) {
	huge := strings.Repeat("This is a long sentence. ", 200)

	text := "1. Introduction\n" + huge

	cfg := SplitterConfig{
		ChunkSize:    500,
		ChunkOverlap: 50,
		Separators:   []string{". "},
		Languages:    []string{LangEnglish},
	}

	chunks := splitByHeuristicsImpl(text, cfg, nil)

	if len(chunks) < 5 {
		t.Fatalf("超大 Section 应通过 Legacy 产生多个子 Chunk: got=%d", len(chunks))
	}

	for i, chunk := range chunks {
		// Validator 后面的真实容忍线也是 2 * ChunkSize。
		if RuneLen(chunk.Content) > 2*cfg.ChunkSize {
			t.Fatalf(
				"Chunk[%d] 明显超过尺寸预算: len=%d",
				i,
				RuneLen(chunk.Content),
			)
		}
	}
}

// TestDropBoundsInsideProtectedSpans
//
// 验证 Heuristic Boundary 不会切进 LaTeX Protected Span。
func TestDropBoundsInsideProtectedSpans(t *testing.T) {
	body := strings.Repeat("filler text. ", 30)

	text :=
		body +
			"\n\n$$\n" +
			"x = 1\n" +
			"1. equation step one\n" +
			"y = 2\n" +
			"$$\n\n" +
			body

	bounds := findHeuristicBoundaries(text, nil)
	protected := protectedSpansRune(text, protectedSpans(text))

	if len(protected) == 0 {
		t.Fatal("测试文本应该产生 Protected Span")
	}

	filtered := dropBoundsInsideSpans(bounds, protected)

	for _, item := range filtered {
		for _, protectedSpan := range protected {
			if item.runeStart > protectedSpan.start &&
				item.runeStart < protectedSpan.end {

				t.Fatalf(
					"Boundary %d 仍然位于 Protected Span [%d,%d) 内",
					item.runeStart,
					protectedSpan.start,
					protectedSpan.end,
				)
			}
		}
	}

	// 测试数据故意在 $$ 中放了一行：
	//
	//     1. equation step one
	//
	// 它会先被 NumberedSectionPattern 识别，
	// 然后应该被 Protected Filter 删除。
	if len(filtered) >= len(bounds) {
		t.Fatalf(
			"Protected Filter 没有删除任何 Boundary: before=%d after=%d",
			len(bounds),
			len(filtered),
		)
	}
}

// TestDropBoundsKeepsProtectedEdges
//
// Protected Span 的 start/end 本身是安全边界，
// 不应该删除。
func TestDropBoundsKeepsProtectedEdges(t *testing.T) {
	bounds := []boundary{
		{runeStart: 10, priority: PrioNumberedHead},
		{runeStart: 15, priority: PrioNumberedHead},
		{runeStart: 20, priority: PrioNumberedHead},
	}

	spans := []span{
		{start: 10, end: 20},
	}

	got := dropBoundsInsideSpans(bounds, spans)

	if len(got) != 2 {
		t.Fatalf("应该保留 span start/end，只删除内部 Boundary: got=%v", got)
	}

	if got[0].runeStart != 10 || got[1].runeStart != 20 {
		t.Fatalf("Protected Span 边缘保留错误: got=%v", got)
	}
}

// TestAllRuneIndices
//
// 再次验证 rune offset。
func TestAllRuneIndices(t *testing.T) {
	text := "中文\f第二页\f第三页"

	got := allRuneIndices(text, "\f")

	want := []int{
		RuneLen("中文"),
		RuneLen("中文\f第二页"),
	}

	if len(got) != len(want) {
		t.Fatalf("FormFeed 数量错误: want=%v got=%v", want, got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rune Offset 错误: want=%v got=%v", want, got)
		}
	}
}

// TestApplyOverlapAlignedUsesPreviousBoundary
//
// Heuristic overlap 最优先对齐到已有结构 Boundary。
func TestApplyOverlapAlignedUsesPreviousBoundary(t *testing.T) {
	runes := []rune(strings.Repeat("a", 1000))

	bounds := []boundary{
		{runeStart: 700},
		{runeStart: 850},
		{runeStart: 920},

		// curEnd 自己必须被忽略。
		{runeStart: 1000},
	}

	// curEnd    = 1000
	// overlap   = 100
	//
	// 搜索窗口：
	//
	//     [800, 1000)
	//
	// 里面 Boundary：
	//
	//     850
	//     920
	//
	// 应该选择最靠后的：
	//
	//     920
	got := applyOverlapAligned(runes, 1000, 100, bounds)

	if got != 920 {
		t.Fatalf("Overlap 应对齐到920: got=%d", got)
	}
}

// TestApplyOverlapAlignedFallsBackToNewline
//
// 搜索窗口中没有 Heuristic Boundary 时，
// 应该向前寻找换行。
func TestApplyOverlapAlignedFallsBackToNewline(t *testing.T) {
	text := strings.Repeat("a", 80) + "\n" +
		strings.Repeat("b", 100)

	runes := []rune(text)

	curEnd := len(runes)
	overlap := 80

	got := applyOverlapAligned(runes, curEnd, overlap, nil)

	// 唯一换行在第80个 rune。
	//
	// 下一 Chunk 应从换行后的字符开始：
	//
	//     81
	if got != 81 {
		t.Fatalf("Overlap 应对齐到换行后: want=81 got=%d", got)
	}
}

// TestSplitByHeuristicsOverlapActuallyOverlaps
//
// 这是一个非常重要的回归测试。
//
// 以前如果 applyOverlapAligned 把 curEnd 本身也当成候选 Boundary，
//
// 那么：
//
//	bestBoundary = curEnd
//
// 最终每次都：
//
//	chunkStart = curEnd
//
// 等于完全没有 overlap。
//
// 当前必须保证至少有一对普通 Heuristic Chunk
// 真正共享一段内容。
func TestSplitByHeuristicsOverlapActuallyOverlaps(t *testing.T) {
	var builder strings.Builder

	for i := 1; i <= 12; i++ {
		builder.WriteString("\n\n")

		// 构造：
		//
		//     1. ...
		//     2. ...
		//     ...
		builder.WriteByte(byte('0' + i%10))
		builder.WriteString(". ")
		builder.WriteString(strings.Repeat("alpha beta gamma. ", 4))
	}

	text := builder.String()

	cfg := SplitterConfig{
		ChunkSize:    200,
		ChunkOverlap: 80,
		Separators:   []string{". "},
		Languages:    []string{LangEnglish},
	}

	chunks := splitByHeuristicsImpl(text, cfg, nil)

	if len(chunks) < 2 {
		t.Fatalf("测试 overlap 至少需要两个 Chunk: got=%d", len(chunks))
	}

	sawOverlap := false

	for i := 1; i < len(chunks); i++ {
		previous := []rune(chunks[i-1].Content)
		current := []rune(chunks[i].Content)

		maxScan := len(previous)
		if len(current) < maxScan {
			maxScan = len(current)
		}

		longest := 0

		for n := 1; n <= maxScan; n++ {
			previousSuffix := string(previous[len(previous)-n:])
			currentPrefix := string(current[:n])

			if previousSuffix == currentPrefix {
				longest = n
			}
		}

		if longest >= 20 {
			sawOverlap = true
			break
		}
	}

	if !sawOverlap {
		t.Fatalf(
			"应该至少存在一对拥有 >=20 rune overlap 的 Chunk，sizes=%v",
			heuristicChunkLengths(chunks),
		)
	}
}

// TestSplitByHeuristicsPreservesSourceOffsetsWithoutOverlap
//
// 当 ChunkOverlap=0 且不存在 synthetic content 时，
// 每个 Chunk.Content 必须严格对应：
//
//	source[Start:End]
func TestSplitByHeuristicsPreservesSourceOffsetsWithoutOverlap(t *testing.T) {
	body := strings.Repeat("这里是正文内容。", 20)

	text :=
		"第一章 引言\n" + body + "\n\n" +
			"第二章 方法\n" + body + "\n\n" +
			"第三章 结果\n" + body

	cfg := SplitterConfig{
		ChunkSize:    250,
		ChunkOverlap: 0,
		Separators:   []string{"。"},
		Languages:    []string{LangChinese},
	}

	chunks := splitByHeuristicsImpl(text, cfg, nil)
	source := []rune(text)

	if len(chunks) == 0 {
		t.Fatal("应该产生 Chunk")
	}

	for i, chunk := range chunks {
		if chunk.Start < 0 || chunk.End < chunk.Start || chunk.End > len(source) {
			t.Fatalf(
				"Chunk[%d] Source Range 非法: [%d,%d)",
				i,
				chunk.Start,
				chunk.End,
			)
		}

		got := string(source[chunk.Start:chunk.End])

		if got != chunk.Content {
			t.Fatalf(
				"Chunk[%d] Source Mapping 错误\nsource=%q\nchunk =%q",
				i,
				got,
				chunk.Content,
			)
		}
	}
}

// TestSplitExplicitHeuristicUsesRealImplementation
//
// 最后确认 init() 注册确实生效。
//
// 我们不只测试：
//
//	splitByHeuristicsImpl()
//
// 还必须测试真正生产入口：
//
//	SplitWithDiagnostics()
func TestSplitExplicitHeuristicUsesRealImplementation(t *testing.T) {
	body := strings.Repeat("这里是一段足够长的正文内容。", 25)

	text :=
		"第一章 引言\n" + body + "\n\n" +
			"第二章 方法\n" + body + "\n\n" +
			"第三章 结果\n" + body

	cfg := DefaultConfig()
	cfg.Strategy = StrategyHeuristic
	cfg.ChunkSize = 300
	cfg.ChunkOverlap = 30
	cfg.Languages = []string{LangChinese}

	chunks, diag := SplitWithDiagnostics(text, cfg)

	if len(chunks) == 0 {
		t.Fatal("Heuristic Strategy 不应该返回空结果")
	}

	if diag.SelectedTier != TierHeuristic {
		t.Fatalf(
			"Heuristic 实现注册后应真正选择 TierHeuristic: got=%s rejected=%v",
			diag.SelectedTier,
			diag.Rejected,
		)
	}
}

// heuristicChunkLengths 是测试辅助函数，
// 用来打印所有 Chunk 的 rune 长度。
func heuristicChunkLengths(chunks []Chunk) []int {
	lengths := make([]int, len(chunks))

	for i, chunk := range chunks {
		lengths[i] = RuneLen(chunk.Content)
	}

	return lengths
}
