package chunker

import (
	"strings"
	"testing"
)

func TestMergeUnitsRepeatsTableHeader(
	t *testing.T,
) {

	text :=
		"| 城市 | 标准 |\n" +
			"| --- | --- |\n" +
			"| 东京 | 12000 |\n" +
			"| 大阪 | 10000 |\n" +
			"| 京都 | 9000 |\n"

	protected :=
		protectedSpans(
			text,
		)

	units :=
		buildUnitsWithProtection(
			text,
			protected,

			DefaultSeparators(),

			// 这里不是最终 ChunkSize，
			// 只是 Recursive Unit 的目标尺寸。
			60,
		)

	// 使用比较小的 ChunkSize，
	// 强制长表格切成多个 Chunk。
	chunks :=
		mergeUnits(
			units,

			55,

			0,
		)

	if len(chunks) < 2 {
		t.Fatalf(
			"测试表格应该被切成多个 Chunk, got=%d",
			len(chunks),
		)
	}

	header :=
		"| 城市 | 标准 |"

	// 只要后面的某个 Chunk 包含大阪/京都，
	// 它就应该拥有列名上下文。
	for _, chunk := range chunks {

		if strings.Contains(
			chunk.Content,
			"大阪",
		) ||
			strings.Contains(
				chunk.Content,
				"京都",
			) {

			if !strings.Contains(
				chunk.Content,
				header,
			) {
				t.Fatalf(
					"跨 Chunk 的表格数据缺少 Header:\n%s",
					chunk.Content,
				)
			}
		}
	}
}

func TestBuildChunkNormalUnitsKeepSourceInvariant(
	t *testing.T,
) {

	text :=
		"第一段。\n第二段。"

	first :=
		"第一段。\n"

	second :=
		"第二段。"

	units :=
		[]splitUnit{
			{
				text: first,

				start: 0,
				end: RuneLen(
					first,
				),
			},

			{
				text: second,

				start: RuneLen(
					first,
				),

				end: RuneLen(
					text,
				),
			},
		}

	chunk :=
		buildChunk(
			units,
			0,
		)

	if chunk.Content != text {
		t.Fatalf(
			"Chunk Content 错误: %q",
			chunk.Content,
		)
	}

	if chunk.End-chunk.Start !=
		RuneLen(
			chunk.Content,
		) {

		t.Fatal(
			"纯 Source-backed Chunk 应保持位置不变量",
		)
	}
}

func TestSyntheticHeaderIsZeroWidth(
	t *testing.T,
) {

	header :=
		splitUnit{
			text: "| A | B |\n| --- | --- |\n",

			start: 100,
			end:   100,
		}

	rowText :=
		"| X | Y |\n"

	row :=
		splitUnit{
			text: rowText,

			start: 100,

			end: 100 +
				RuneLen(
					rowText,
				),
		}

	chunk :=
		buildChunk(
			[]splitUnit{
				header,
				row,
			},
			0,
		)

	if !strings.Contains(
		chunk.Content,
		"| A | B |",
	) {
		t.Fatal(
			"Synthetic Header 应存在于 Chunk Content",
		)
	}

	// 这里特意演示：
	//
	// Synthetic Header 是 source invariant 的特殊例外。
	if chunk.End-chunk.Start ==
		RuneLen(
			chunk.Content,
		) {

		t.Fatal(
			"包含 Synthetic Header 的 Chunk 不应伪装成完全 source-backed",
		)
	}
}
