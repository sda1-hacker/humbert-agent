package rerank

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

type fakeScorer struct {
	scores []float64
	err    error

	query    string
	passages []string
}

func (f *fakeScorer) Score(ctx context.Context, query string, passages []string) ([]float64, error) {
	f.query = query
	f.passages = append([]string(nil), passages...)

	if f.err != nil {
		return nil, f.err
	}

	return append([]float64(nil), f.scores...), nil
}

func TestRerankCompositeScore(t *testing.T) {
	scorer := &fakeScorer{
		scores: []float64{0.9},
	}

	cfg := DefaultConfig()
	cfg.TopK = 1

	engine := NewEngine(scorer, cfg)

	result, err := engine.Rerank(
		context.Background(),
		"安装方法",
		[]retrieval.SearchResult{
			{
				ChunkID:      "A",
				Content:      "安装正文",
				Score:        0.8,
				SourceWeight: 0.5,
			},
		},
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("应该返回一个结果: %d", len(result.Results))
	}

	// 0.6 * 0.9
	// +
	// 0.3 * 0.8
	// +
	// 0.1 * 0.5
	//
	// = 0.83
	if math.Abs(result.Results[0].Score-0.83) > 1e-9 {
		t.Fatalf(
			"Composite Score 错误: %.6f",
			result.Results[0].Score,
		)
	}

	if result.Results[0].BaseScore != 0.8 {
		t.Fatalf("BaseScore 错误: %+v", result.Results[0])
	}

	if result.Results[0].ModelScore != 0.9 {
		t.Fatalf("ModelScore 错误: %+v", result.Results[0])
	}

	if !result.Results[0].Reranked {
		t.Fatal("结果应该标记 Reranked=true")
	}
}

func TestRerankThresholdDegradation(t *testing.T) {
	scorer := &fakeScorer{
		scores: []float64{0.60, 0.55},
	}

	cfg := DefaultConfig()
	cfg.Threshold = 0.8

	engine := NewEngine(scorer, cfg)

	result, err := engine.Rerank(
		context.Background(),
		"query",
		testCandidates(2),
	)

	if err != nil {
		t.Fatal(err)
	}

	if result.Diagnostics.Outcome != OutcomeThresholdDegraded {
		t.Fatalf(
			"应该触发 Threshold Degradation: %+v",
			result.Diagnostics,
		)
	}

	// max(0.8 * 0.7, 0.3) = 0.56
	if math.Abs(result.Diagnostics.EffectiveThreshold-0.56) > 1e-9 {
		t.Fatalf(
			"Effective Threshold 错误: %.6f",
			result.Diagnostics.EffectiveThreshold,
		)
	}

	if len(result.Results) != 1 {
		t.Fatalf(
			"0.60 应通过0.56，而0.55不通过: got=%d",
			len(result.Results),
		)
	}
}

func TestRerankFallbackTop1(t *testing.T) {
	scorer := &fakeScorer{
		scores: []float64{0.20, 0.10},
	}

	cfg := DefaultConfig()
	cfg.Threshold = 0.3

	engine := NewEngine(scorer, cfg)

	result, err := engine.Rerank(
		context.Background(),
		"query",
		testCandidates(2),
	)

	if err != nil {
		t.Fatal(err)
	}

	if result.Diagnostics.Outcome != OutcomeFallbackTop1 {
		t.Fatalf(
			"应该进入 fallback_top1: %+v",
			result.Diagnostics,
		)
	}

	if len(result.Results) != 1 {
		t.Fatalf("应该只保留 Top1: %d", len(result.Results))
	}

	if result.Results[0].ModelScore != 0.20 {
		t.Fatalf("应该保留模型最高分: %+v", result.Results[0])
	}
}

func TestRerankAllBelowThreshold(t *testing.T) {
	scorer := &fakeScorer{
		scores: []float64{0.14, 0.10},
	}

	engine := NewEngine(scorer, DefaultConfig())

	result, err := engine.Rerank(
		context.Background(),
		"query",
		testCandidates(2),
	)

	if err != nil {
		t.Fatal(err)
	}

	if result.Diagnostics.Outcome != OutcomeAllBelowThreshold {
		t.Fatalf(
			"应该全部拒绝: %+v",
			result.Diagnostics,
		)
	}

	if len(result.Results) != 0 {
		t.Fatalf("结果应该为空: %d", len(result.Results))
	}
}

func TestRerankModelErrorFallsBackToRetrievalOrder(t *testing.T) {
	scorer := &fakeScorer{
		err: errors.New("rerank server unavailable"),
	}

	cfg := DefaultConfig()
	cfg.TopK = 2

	engine := NewEngine(scorer, cfg)

	input := []retrieval.SearchResult{
		{ChunkID: "A", Score: 0.9},
		{ChunkID: "B", Score: 0.8},
		{ChunkID: "C", Score: 0.7},
	}

	result, err := engine.Rerank(
		context.Background(),
		"query",
		input,
	)

	if err != nil {
		t.Fatal(err)
	}

	if result.Diagnostics.Outcome != OutcomeModelError {
		t.Fatalf(
			"应该 model_error fallback: %+v",
			result.Diagnostics,
		)
	}

	if len(result.Results) != 2 {
		t.Fatalf("应该按 TopK fallback: %d", len(result.Results))
	}

	if result.Results[0].ChunkID != "A" ||
		result.Results[1].ChunkID != "B" {

		t.Fatalf(
			"Fallback 不应该改变 Retrieval 顺序: %+v",
			result.Results,
		)
	}
}

func TestRerankContextCancellationPropagates(t *testing.T) {
	scorer := &fakeScorer{
		err: context.Canceled,
	}

	engine := NewEngine(scorer, DefaultConfig())

	_, err := engine.Rerank(
		context.Background(),
		"query",
		testCandidates(1),
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"context cancellation 应向上传播: %v",
			err,
		)
	}
}

func TestRerankMMRPrefersDiversity(t *testing.T) {
	scorer := &fakeScorer{
		scores: []float64{
			0.95,
			0.94,
			0.90,
		},
	}

	cfg := DefaultConfig()
	cfg.TopK = 2
	cfg.Threshold = 0

	engine := NewEngine(scorer, cfg)

	candidates := []retrieval.SearchResult{
		{
			ChunkID: "A",
			Content: "Redis deployment uses Sentinel for high availability.",
			Score:   0.9,
		},
		{
			ChunkID: "B",
			Content: "Redis deployment uses Sentinel for high availability.",
			Score:   0.89,
		},
		{
			ChunkID: "C",
			Content: "PostgreSQL backup uses WAL archive and base backup.",
			Score:   0.80,
		},
	}

	result, err := engine.Rerank(
		context.Background(),
		"deployment",
		candidates,
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(result.Results) != 2 {
		t.Fatalf("应该返回2个结果: %d", len(result.Results))
	}

	if result.Results[0].ChunkID != "A" {
		t.Fatalf("第一名应该仍是A: %+v", result.Results)
	}

	if result.Results[1].ChunkID != "C" {
		t.Fatalf(
			"MMR 应避免选择和A完全重复的B: %+v",
			result.Results,
		)
	}
}

func TestDefaultPassageBuilder(t *testing.T) {
	result := retrieval.SearchResult{
		Content:       "执行 go test ./...",
		ContextHeader: "# Go 手册\n## 测试",
		Metadata: map[string]any{
			"title": "Go 工程规范",
		},
	}

	got := DefaultPassageBuilder(result)

	want := "Go 工程规范\n\n" +
		"# Go 手册\n## 测试\n\n" +
		"执行 go test ./..."

	if got != want {
		t.Fatalf(
			"Passage 错误\nwant=%q\ngot =%q",
			want,
			got,
		)
	}
}

func TestRerankCapsCandidates(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxCandidates = 2

	scorer := &fakeScorer{
		scores: []float64{0.9, 0.8},
	}

	engine := NewEngine(scorer, cfg)

	_, err := engine.Rerank(
		context.Background(),
		"query",
		testCandidates(5),
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(scorer.passages) != 2 {
		t.Fatalf(
			"应该最多送2个候选给模型: %d",
			len(scorer.passages),
		)
	}
}

func testCandidates(count int) []retrieval.SearchResult {
	results := make([]retrieval.SearchResult, count)

	for i := 0; i < count; i++ {
		results[i] = retrieval.SearchResult{
			ChunkID: string(rune('A' + i)),
			Content: "测试正文",
			Score:   0.9 - float64(i)*0.1,
		}
	}

	return results
}
