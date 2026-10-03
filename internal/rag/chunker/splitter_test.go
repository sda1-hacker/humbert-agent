package chunker

import (
	"strings"
	"testing"
)

func TestSplitTextEmpty(
	t *testing.T,
) {

	chunks :=
		SplitText(
			"",
			DefaultConfig(),
		)

	if chunks != nil {
		t.Fatalf(
			"空文本应该返回 nil",
		)
	}
}

func TestSplitTextSimpleDocument(
	t *testing.T,
) {

	text :=
		"第一段内容比较长，用来测试基础分块逻辑。" +
			"这里继续补充第一段内容。\n\n" +
			"第二段内容也比较长，" +
			"这里是另外一个主题。" +
			"继续补充第二段内容。\n\n" +
			"第三段内容作为最后一段。"

	cfg :=
		DefaultConfig()

	cfg.ChunkSize = 40
	cfg.ChunkOverlap = 10

	chunks :=
		SplitText(
			text,
			cfg,
		)

	if len(chunks) < 2 {
		t.Fatalf(
			"文本应该产生多个 Chunk, got=%d",
			len(chunks),
		)
	}

	for i, chunk := range chunks {

		if chunk.Seq != i {
			t.Fatalf(
				"Chunk Seq 错误: want=%d got=%d",
				i,
				chunk.Seq,
			)
		}

		if chunk.Content == "" {
			t.Fatalf(
				"Chunk[%d] Content 不应该为空",
				i,
			)
		}
	}
}

func TestSplitTextProtectedCodeRemainsWhole(
	t *testing.T,
) {

	code :=
		"```go\n" +
			"func main() {\n" +
			"    fmt.Println(\"hello\")\n" +
			"}\n" +
			"```"

	text :=
		"开始内容。\n\n" +
			code +
			"\n\n结束内容。"

	cfg :=
		DefaultConfig()

	// 明显小于代码块长度，
	// 但 protected code 未超过7500，
	// 不应该从代码块内部切。
	cfg.ChunkSize = 20
	cfg.ChunkOverlap = 0

	chunks :=
		SplitText(
			text,
			cfg,
		)

	foundWholeCode := false

	for _, chunk := range chunks {

		if strings.Contains(
			chunk.Content,
			code,
		) {
			foundWholeCode = true
			break
		}
	}

	if !foundWholeCode {
		t.Fatal(
			"Fenced Code 应完整出现在某个 Chunk 中",
		)
	}
}

func TestSplitTextExplicitZeroOverlap(
	t *testing.T,
) {

	text :=
		"第一段文字内容。\n\n" +
			"第二段文字内容。\n\n" +
			"第三段文字内容。"

	cfg :=
		DefaultConfig()

	cfg.ChunkSize = 10

	// 注意：
	//
	// 这里直接调用底层 SplitText，
	// 所以 0 表示真的不 overlap。
	cfg.ChunkOverlap = 0

	chunks :=
		SplitText(
			text,
			cfg,
		)

	if len(chunks) == 0 {
		t.Fatal(
			"应该产生 Chunk",
		)
	}

	// 这个测试主要是把当前底层 API 行为固定下来。
	//
	// 真正 Strategy Split() 后面会通过 ensureDefaults
	// 将 <=0 的 overlap 恢复为默认值。
}

func TestSplitTextChunkPositionsValid(
	t *testing.T,
) {

	text :=
		"第一段。\n\n" +
			"第二段。\n\n" +
			"第三段。"

	cfg :=
		DefaultConfig()

	cfg.ChunkSize = 8
	cfg.ChunkOverlap = 0

	chunks :=
		SplitText(
			text,
			cfg,
		)

	sourceRunes :=
		[]rune(text)

	for i, chunk := range chunks {

		if chunk.Start < 0 {
			t.Fatalf(
				"Chunk[%d] Start 非法",
				i,
			)
		}

		if chunk.End <
			chunk.Start {

			t.Fatalf(
				"Chunk[%d] End < Start",
				i,
			)
		}

		if chunk.End >
			len(sourceRunes) {

			t.Fatalf(
				"Chunk[%d] 超出原文范围",
				i,
			)
		}

		// 没有 Synthetic Header 的普通文本，
		// Content 应与 source slice 一致。
		source :=
			string(
				sourceRunes[chunk.Start:chunk.End],
			)

		if source !=
			chunk.Content {

			t.Fatalf(
				"Chunk[%d] Content 与 Source Offset 不一致\nsource=%q\nchunk =%q",
				i,
				source,
				chunk.Content,
			)
		}
	}
}
