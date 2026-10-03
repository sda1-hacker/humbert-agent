package chunker

import (
	"reflect"
	"strings"
	"testing"
)

// TestSplitBySeparatorsUnderChunkSize
//
// 文本已经小于 ChunkSize 时，
// 即使内部存在 separator，
// 也不应该继续拆碎。
func TestSplitBySeparatorsUnderChunkSize(
	t *testing.T,
) {

	text :=
		"第一段。\n\n第二段。"

	got :=
		splitBySeparators(
			text,
			DefaultSeparators(),
			100,
		)

	want :=
		[]string{text}

	if !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf(
			"文本未超过 ChunkSize 时不应该拆\nwant=%v\ngot=%v",
			want,
			got,
		)
	}
}

// TestSplitBySeparatorsRecursivePriority
//
// 这是整个递归策略最核心的测试。
//
// 输入：
//
//	AAAA
//
//	BBBB
//	CCCC
//	DDDD
//
// 第一层：
//
//	\n\n
//
// 第二部分仍然过大，
//
// 所以只对第二部分继续使用：
//
//	\n
func TestSplitBySeparatorsRecursivePriority(
	t *testing.T,
) {

	text :=
		"AAAA\n\nBBBB\nCCCC\nDDDD"

	got :=
		splitBySeparators(
			text,

			[]string{
				"\n\n",
				"\n",
				"。",
			},

			6,
		)

	want :=
		[]string{
			"AAAA",
			"\n\n",
			"BBBB",
			"\n",
			"CCCC",
			"\n",
			"DDDD",
		}

	if !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf(
			"递归 separator 结果错误\nwant=%q\ngot =%q",
			want,
			got,
		)
	}

	// -------------------------------------------------------------
	// 一个极其重要的不变量：
	//
	// 所有 piece 拼起来必须和原文完全一样。
	// -------------------------------------------------------------

	if strings.Join(got, "") != text {
		t.Fatalf(
			"递归切割后无法恢复原文",
		)
	}
}

// TestSplitBySeparatorsSeparatorNotFound
//
// 如果所有 separator 都不存在，
// 则只能保持原文不动。
func TestSplitBySeparatorsSeparatorNotFound(
	t *testing.T,
) {

	text :=
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ"

	got :=
		splitBySeparators(
			text,
			DefaultSeparators(),
			5,
		)

	if len(got) != 1 ||
		got[0] != text {

		t.Fatalf(
			"不存在 separator 时应该保留原文: %q",
			got,
		)
	}
}

// TestSplitBySeparatorsPreservesSeparator
//
// 验证 separator 自己不能丢失。
func TestSplitBySeparatorsPreservesSeparator(
	t *testing.T,
) {

	text :=
		"第一段。\n\n第二段。"

	got :=
		splitBySeparators(
			text,

			[]string{
				"\n\n",
			},

			5,
		)

	reconstructed :=
		strings.Join(
			got,
			"",
		)

	if reconstructed != text {
		t.Fatalf(
			"separator 被丢失\nwant=%q\ngot =%q",
			text,
			reconstructed,
		)
	}
}
