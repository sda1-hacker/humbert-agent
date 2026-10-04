package embeddinginput

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
)

type fakeEmbedder struct{ calls [][]string }

func (e *fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	e.calls = append(e.calls, append([]string(nil), texts...))
	out := make([][]float64, len(texts))
	for i := range out {
		out[i] = []float64{1}
	}
	return out, nil
}

func TestBudgetSplitsBatchByTotalTokens(t *testing.T) {
	e := &fakeEmbedder{}
	limited := Limit(e, Budget{MaxInputTokens: 4, MaxBatchTokens: 6})
	out, err := limited.EmbedStrings(context.Background(), []string{"abc", "def", "ghi", "j"})
	if err != nil || len(out) != 4 {
		t.Fatalf("%v %v", out, err)
	}
	if !reflect.DeepEqual(e.calls, [][]string{{"abc", "def"}, {"ghi", "j"}}) {
		t.Fatalf("unexpected batches: %v", e.calls)
	}
}

func TestBudgetPreflightsAllInputsBeforeRemoteCalls(t *testing.T) {
	e := &fakeEmbedder{}
	_, err := Limit(e, Budget{MaxInputTokens: 4}).EmbedStrings(context.Background(), []string{"ok", "too long"})
	if !errors.Is(err, ErrInputBudget) || len(e.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, e.calls)
	}
}

func TestBudgetUsesTokenizerAndCountLimit(t *testing.T) {
	b := Budget{MaxInputTokens: 2, MaxBatchTokens: 3, CountTokens: func(s string) int { return len([]rune(s)) }}
	plan, err := b.Plan([]string{"中文", "字", "字"}, 2)
	if err != nil || !reflect.DeepEqual(plan, []Batch{{0, 2}, {2, 3}}) {
		t.Fatalf("%v %v", plan, err)
	}
}
