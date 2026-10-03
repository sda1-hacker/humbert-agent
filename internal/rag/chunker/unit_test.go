package chunker

import (
	"strings"
	"testing"
)

// TestBuildUnitsWithProtectionPreservesSource
//
// 这个测试非常重要。
//
// 我们要求：
//
//	所有 Unit 顺序拼接
//
// 必须：
//
//	100%% 恢复原始文本。
//
// 同时每一个 Unit：
//
//	text
//
// 必须和：
//
//	source[start:end]
//
// 完全一致。
func TestBuildUnitsWithProtectionPreservesSource(
	t *testing.T,
) {

	text :=
		"中文前言。\n\n" +
			"```go\n" +
			"fmt.Println(\"你好\")\n" +
			"```\n\n" +
			"最后一段。"

	protected :=
		protectedSpans(text)

	units :=
		buildUnitsWithProtection(
			text,
			protected,
			DefaultSeparators(),
			8,
		)

	if len(units) == 0 {
		t.Fatal(
			"应该产生 splitUnit",
		)
	}

	// -------------------------------------------------------------
	// 1. 拼接所有 Unit，必须恢复原文。
	// -------------------------------------------------------------

	var builder strings.Builder

	for _, unit := range units {
		builder.WriteString(
			unit.text,
		)
	}

	if builder.String() != text {
		t.Fatalf(
			"Unit 无法无损恢复原文\nwant=%q\ngot =%q",
			text,
			builder.String(),
		)
	}

	// -------------------------------------------------------------
	// 2. 每个 Unit 的 Start/End
	// 必须正确对应原文 rune。
	// -------------------------------------------------------------

	sourceRunes :=
		[]rune(text)

	for i, unit := range units {

		if unit.start < 0 ||
			unit.end < unit.start ||
			unit.end > len(sourceRunes) {

			t.Fatalf(
				"Unit[%d] 位置非法: start=%d end=%d",
				i,
				unit.start,
				unit.end,
			)
		}

		sourceText :=
			string(
				sourceRunes[unit.start:unit.end],
			)

		if sourceText != unit.text {

			t.Fatalf(
				"Unit[%d] 文本与位置不对应\nsource=%q\nunit  =%q",
				i,
				sourceText,
				unit.text,
			)
		}

		if unit.end-unit.start !=
			RuneLen(unit.text) {

			t.Fatalf(
				"Unit[%d] rune invariant 失败",
				i,
			)
		}
	}
}

// TestProtectedContentRemainsAtomic
//
// 即使代码块长度超过普通 ChunkSize，
//
// 只要没有超过：
//
//	maxProtectedUnitSize
//
// 就应该作为一个完整 Unit，
// 而不是继续按 newline 分割。
func TestProtectedContentRemainsAtomic(
	t *testing.T,
) {

	code :=
		"```go\n" +
			"func main() {\n" +
			"    fmt.Println(\"hello\")\n" +
			"}\n" +
			"```"

	text :=
		"前文\n\n" +
			code +
			"\n\n后文"

	units :=
		buildUnitsWithProtection(
			text,
			protectedSpans(text),

			[]string{
				"\n\n",
				"\n",
			},

			10,
		)

	foundCodeBlock := false

	for _, unit := range units {

		if unit.text == code {
			foundCodeBlock = true
			break
		}
	}

	if !foundCodeBlock {
		t.Fatalf(
			"Protected fenced code block 应该保持为完整 Unit",
		)
	}
}

// TestOversizedProtectedContentIsForcedSplit
//
// Protected 并不意味着无限大都不切。
//
// 超过 7500 rune 后必须强制拆分，
// 避免后续 Embedding 输入失控。
func TestOversizedProtectedContentIsForcedSplit(
	t *testing.T,
) {

	// 构造一个超过 7500 rune 的代码块。
	code :=
		"```text\n" +
			strings.Repeat(
				"a",
				8000,
			) +
			"\n```"

	text := code

	units :=
		buildUnitsWithProtection(
			text,
			protectedSpans(text),
			DefaultSeparators(),
			512,
		)

	if len(units) < 2 {
		t.Fatalf(
			"超大 Protected Span 应该被强制拆分, got units=%d",
			len(units),
		)
	}

	var builder strings.Builder

	for i, unit := range units {

		if RuneLen(unit.text) >
			maxProtectedUnitSize {

			t.Fatalf(
				"Unit[%d] 仍超过最大 Protected 大小: %d",
				i,
				RuneLen(unit.text),
			)
		}

		builder.WriteString(
			unit.text,
		)
	}

	// 强制拆分以后仍然必须能够恢复原文。
	if builder.String() != text {
		t.Fatal(
			"超大 Protected Span 拆分后无法恢复原文",
		)
	}
}

// TestUnitPositionsAreContinuous
//
// 当前阶段所有 Unit 都直接来自原文，
//
// 所以理论上：
//
//	上一个.end
//	==
//	下一个.start
//
// 不能产生位置空洞。
//
// 下一阶段 Table Header Tracker 会引入：
//
//	synthetic unit
//
// 即人为补进去的表头。
//
// 那时候才会出现特殊的：
//
//	start == end
//
// 合成内容。
func TestUnitPositionsAreContinuous(
	t *testing.T,
) {

	text :=
		"第一段。\n\n" +
			"`code`\n\n" +
			"第二段。"

	units :=
		buildUnitsWithProtection(
			text,
			protectedSpans(text),
			DefaultSeparators(),
			5,
		)

	if len(units) == 0 {
		t.Fatal(
			"没有得到 Unit",
		)
	}

	if units[0].start != 0 {
		t.Fatalf(
			"第一个 Unit 应该从 0 开始, got=%d",
			units[0].start,
		)
	}

	for i := 1; i < len(units); i++ {

		if units[i-1].end !=
			units[i].start {

			t.Fatalf(
				"Unit 位置不连续: unit[%d].end=%d unit[%d].start=%d",
				i-1,
				units[i-1].end,
				i,
				units[i].start,
			)
		}
	}

	if units[len(units)-1].end !=
		RuneLen(text) {

		t.Fatalf(
			"最后一个 Unit 应该结束于文档末尾: want=%d got=%d",
			RuneLen(text),
			units[len(units)-1].end,
		)
	}
}
