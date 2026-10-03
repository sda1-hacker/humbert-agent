package chunker

import (
	"reflect"
	"testing"
)

func TestSelectStrategyNilProfile(t *testing.T) {
	got := SelectStrategy(nil)
	want := []StrategyTier{TierLegacy}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nil Profile 应退回 Legacy: want=%v got=%v", want, got)
	}
}

func TestSelectStrategyHeading(t *testing.T) {
	profile := &DocProfile{
		TotalLines:     400,
		MdHeadingTotal: 3,
		MdHeadingCounts: map[int]int{
			2: 3,
		},
	}

	// Density:
	//
	//     3 / 400
	//     = 0.0075
	//
	// > 0.005
	//
	// 并且 H2 出现3次，
	// 所以 Heading 成为候选。
	got := SelectStrategy(profile)

	want := []StrategyTier{
		TierHeading,
		TierLegacy,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Heading Strategy Chain 错误: want=%v got=%v", want, got)
	}
}

func TestSelectStrategyHeadingDensityTooLow(t *testing.T) {
	profile := &DocProfile{
		TotalLines:     1000,
		MdHeadingTotal: 3,
		MdHeadingCounts: map[int]int{
			2: 3,
		},
	}

	// 3 / 1000 = 0.003
	//
	// 不满足：
	//
	//     > 0.005
	//
	// 所以不能选择 Heading。
	got := SelectStrategy(profile)

	want := []StrategyTier{
		TierLegacy,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Heading Density 太低时不应选择 Heading: want=%v got=%v", want, got)
	}
}

func TestSelectStrategyHeuristicByMarkerCount(t *testing.T) {
	profile := &DocProfile{
		AllCapsShortLineCount: 5,
	}

	got := SelectStrategy(profile)

	want := []StrategyTier{
		TierHeuristic,
		TierLegacy,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Heuristic Strategy Chain 错误: want=%v got=%v", want, got)
	}
}

func TestSelectStrategyHeuristicByChapter(t *testing.T) {
	profile := &DocProfile{
		ChineseChapterCount: 1,
	}

	// 即使 MarkerTotal 不到5，
	// 只要出现明确 Chapter Marker，
	// 也应该启用 Heuristic。
	got := SelectStrategy(profile)

	want := []StrategyTier{
		TierHeuristic,
		TierLegacy,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chapter Marker 应触发 Heuristic: want=%v got=%v", want, got)
	}
}

func TestSelectStrategyHeuristicByFormFeed(t *testing.T) {
	profile := &DocProfile{
		FormFeedCount: 1,
	}

	got := SelectStrategy(profile)

	want := []StrategyTier{
		TierHeuristic,
		TierLegacy,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FormFeed 应触发 Heuristic: want=%v got=%v", want, got)
	}
}

func TestSelectStrategyHeadingAndHeuristic(t *testing.T) {
	profile := &DocProfile{
		TotalLines:     300,
		MdHeadingTotal: 3,
		MdHeadingCounts: map[int]int{
			2: 3,
		},

		ChineseChapterCount: 1,
	}

	got := SelectStrategy(profile)

	want := []StrategyTier{
		TierHeading,
		TierHeuristic,
		TierLegacy,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("完整 Strategy Chain 错误: want=%v got=%v", want, got)
	}
}

func TestSelectStrategyNoSignals(t *testing.T) {
	profile := &DocProfile{
		TotalLines: 100,
		TotalChars: 1000,
	}

	got := SelectStrategy(profile)

	want := []StrategyTier{
		TierLegacy,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("没有结构信号时应该只使用 Legacy: want=%v got=%v", want, got)
	}
}
