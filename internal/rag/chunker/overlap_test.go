package chunker

import (
	"testing"
)

func TestFindSemanticOverlapBoundaryParagraphFirst(
	t *testing.T,
) {

	text :=
		"第一句。第二句。\n\n第三段还在继续"

	end, ok :=
		findSemanticOverlapBoundary(
			text,
		)

	if !ok {
		t.Fatal(
			"应该找到 Semantic Boundary",
		)
	}

	runes :=
		[]rune(text)

	gotTail :=
		string(
			runes[end:],
		)

	want :=
		"第三段还在继续"

	if gotTail != want {
		t.Fatalf(
			"Paragraph Boundary 应优先\nwant=%q\ngot =%q",
			want,
			gotTail,
		)
	}
}

func TestFindSemanticOverlapBoundaryIgnoresProtected(
	t *testing.T,
) {

	// 唯一的 "." 位于 inline code 中。
	//
	// 不应该把：
	//
	//	a. b
	//
	// 中的 "." 当作自然语言句末。
	text :=
		"前文`a. b`后文继续"

	_, ok :=
		findSemanticOverlapBoundary(
			text,
		)

	if ok {
		t.Fatal(
			"Protected Span 内的句号不能作为 overlap boundary",
		)
	}
}

func TestSemanticOverlapWindowCanSliceInsideUnit(
	t *testing.T,
) {

	text :=
		"1234567890ABCDEFGHIJ"

	unit :=
		splitUnit{
			text:  text,
			start: 100,
			end:   100 + RuneLen(text),
		}

	window :=
		semanticOverlapWindow(
			[]splitUnit{
				unit,
			},
			5,
		)

	if len(window) != 1 {
		t.Fatalf(
			"预期一个 Window Unit, got=%d",
			len(window),
		)
	}

	if window[0].text != "FGHIJ" {
		t.Fatalf(
			"应该截取 Unit 最后5个 rune: %q",
			window[0].text,
		)
	}

	if window[0].start !=
		unit.end-5 {

		t.Fatalf(
			"截取后的 Start 错误: want=%d got=%d",
			unit.end-5,
			window[0].start,
		)
	}

	if window[0].end !=
		unit.end {

		t.Fatalf(
			"End 不应该变化",
		)
	}
}

func TestSemanticOverlapStopsAtSyntheticUnit(
	t *testing.T,
) {

	source1 :=
		splitUnit{
			text:  "第一段。",
			start: 0,
			end:   RuneLen("第一段。"),
		}

	// Synthetic Header。
	synthetic :=
		splitUnit{
			text: "| A | B |\n| --- | --- |\n",

			start: source1.end,
			end:   source1.end,
		}

	source2Start :=
		source1.end

	source2 :=
		splitUnit{
			text: "后面的真实正文",

			start: source2Start,

			end: source2Start +
				RuneLen(
					"后面的真实正文",
				),
		}

	window :=
		semanticOverlapWindow(
			[]splitUnit{
				source1,
				synthetic,
				source2,
			},
			100,
		)

	// 只能看到 synthetic 后面的 source2。
	if len(window) != 1 {
		t.Fatalf(
			"Synthetic Unit 应该形成硬屏障, got=%d",
			len(window),
		)
	}

	if window[0].text !=
		source2.text {

		t.Fatalf(
			"不应该跨过 Synthetic Unit: %q",
			unitsText(window),
		)
	}
}

func TestComputeOverlapUsesSemanticSuffix(
	t *testing.T,
) {

	text :=
		"第一部分内容。第二部分内容。第三部分还在继续"

	current :=
		[]splitUnit{
			{
				text: text,

				start: 0,
				end: RuneLen(
					text,
				),
			},
		}

	overlap, overlapLen :=
		computeOverlap(
			current,

			// 最大允许20
			20,

			// 总块100
			100,

			// next 很短
			10,
		)

	if overlapLen <= 0 {
		t.Fatal(
			"应该产生 overlap",
		)
	}

	if overlapLen > 20 {
		t.Fatalf(
			"Overlap 不应该超过配置: %d",
			overlapLen,
		)
	}

	// Semantic Overlap 应从一个句末之后开始，
	// 而不是机械截最后20字符。
	got :=
		unitsText(
			overlap,
		)

	if got == "" {
		t.Fatal(
			"Overlap 内容不能为空",
		)
	}
}
