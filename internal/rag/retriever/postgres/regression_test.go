package postgres

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

type vectorFunc func(context.Context) ([]retrieval.SearchResult, error)

func (f vectorFunc) search(ctx context.Context, _ string, _ string, _ int, _ float64, _ embedding.Embedder) ([]retrieval.SearchResult, error) {
	return f(ctx)
}

type keywordFunc func(context.Context) ([]retrieval.SearchResult, error)

func (f keywordFunc) search(ctx context.Context, _ string, _ string, _ int, _ float64) ([]retrieval.SearchResult, error) {
	return f(ctx)
}

func TestHybridPartialFailureIsExplicitAndCancellationPropagates(t *testing.T) {
	errUnavailable := errors.New("temporary keyword failure")
	v := vectorFunc(func(context.Context) ([]retrieval.SearchResult, error) {
		return []retrieval.SearchResult{{ChunkID: "v", Score: 1}}, nil
	})
	k := keywordFunc(func(context.Context) ([]retrieval.SearchResult, error) {
		return []retrieval.SearchResult{{ChunkID: "partial", Score: 100}}, errUnavailable
	})
	cfg := DefaultHybridConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{dim: DefaultDimensions}
	r := newHybridRetriever(v, k, cfg)
	if _, _, err := r.SearchWithDiagnostics(context.Background(), "q"); !errors.Is(err, errUnavailable) {
		t.Fatal(err)
	}
	cfg.FailurePolicy = FailureAllowPartial
	r = newHybridRetriever(v, k, cfg)
	got, diag, err := r.SearchWithDiagnostics(context.Background(), "q")
	if err != nil || len(got) != 1 || got[0].ChunkID != "v" || !diag.Degraded || diag.ModeUsed != retrieval.MatchVector || len(diag.Channels) != 1 {
		t.Fatalf("%+v %+v %v", got, diag, err)
	}
	r = newHybridRetriever(v, keywordFunc(func(context.Context) ([]retrieval.SearchResult, error) { return nil, context.Canceled }), cfg)
	if _, _, err := r.SearchWithDiagnostics(context.Background(), "q"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation converted into partial success: %v", err)
	}
}

func TestHybridDeadlineReturnsEvenWhenChannelIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	v := vectorFunc(func(context.Context) ([]retrieval.SearchResult, error) { <-release; return nil, nil })
	k := keywordFunc(func(context.Context) ([]retrieval.SearchResult, error) { return nil, nil })
	cfg := DefaultHybridConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{dim: DefaultDimensions}
	cfg.Timeout = 20 * time.Millisecond
	r := newHybridRetriever(v, k, cfg)
	start := time.Now()
	if _, _, err := r.SearchWithDiagnostics(context.Background(), "q"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline waited for uncooperative channel")
	}
}

func TestHybridZeroThresholdsAndChannelWeightsSurvive(t *testing.T) {
	v := vectorFunc(func(context.Context) ([]retrieval.SearchResult, error) {
		t.Error("disabled vector channel invoked")
		return nil, nil
	})
	k := keywordFunc(func(context.Context) ([]retrieval.SearchResult, error) {
		return []retrieval.SearchResult{{ChunkID: "k", Score: 1}}, nil
	})
	cfg := DefaultHybridConfig()
	cfg.CollectionID = "kb"
	cfg.VectorThreshold = 0
	cfg.RRF = retrieval.RRFConfig{KeywordWeight: 1}
	r := newHybridRetriever(v, k, cfg)
	if r.config.VectorThreshold != 0 {
		t.Fatal("zero threshold reset")
	}
	if _, diag, err := r.SearchWithDiagnostics(context.Background(), "q"); err != nil || diag.ModeUsed != retrieval.MatchKeyword {
		t.Fatalf("%+v %v", diag, err)
	}
	cfg.VectorThreshold = math.NaN()
	if cfg.Validate() == nil {
		t.Fatal("NaN threshold accepted")
	}
}

func TestRevisionMappingPreservesBigintPrecision(t *testing.T) {
	rev, err := documentRevision(map[string]any{"rag_document_revision": "9007199254740993"})
	if err != nil || rev != 9007199254740993 {
		t.Fatalf("%d %v", rev, err)
	}
	if _, err := documentRevision(map[string]any{"rag_document_revision": float64(2)}); err == nil {
		t.Fatal("lossy numeric revision accepted")
	}
}
