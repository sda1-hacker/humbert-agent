package search

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

func TestPipelineGroupsParentsBeforeFinalLimit(t *testing.T) {
	hits := make([]retrieval.SearchResult, 6)
	for i := range hits {
		p := "a"
		if i == 5 {
			p = "b"
		}
		hits[i] = retrieval.SearchResult{ChunkID: fmt.Sprint(i), ParentChunkID: p, Content: fmt.Sprint(i), Score: 1 - float64(i)/10}
	}
	loader := &fakeParentLoader{parents: map[string]retrieval.SearchResult{"a": {ChunkID: "a", Content: "parent a", StartRune: 10, EndRune: 100}, "b": {ChunkID: "b", Content: "parent b"}}}
	for _, engine := range []*rerank.Engine{nil, rerank.NewEngine(nil, rerank.DefaultConfig())} {
		p, err := NewPipeline(func(context.Context, string) ([]retrieval.SearchResult, error) { return hits, nil }, engine, loader, Config{FinalTopK: 5, ExpandParents: true, CollapseSameParent: true})
		if err != nil {
			t.Fatal(err)
		}
		r, err := p.Search(context.Background(), "q")
		if err != nil || len(r.Results) != 2 {
			t.Fatalf("%v %+v", err, r)
		}
		if len(r.Results[0].Evidence) != 5 || r.Results[0].ContextStartRune != 10 {
			t.Fatalf("lost evidence/context range: %+v", r.Results[0])
		}
	}
}

func TestPipelineOwnsFinalTopKWithoutParentExpansion(t *testing.T) {
	hits := make([]retrieval.SearchResult, 6)
	for i := range hits {
		hits[i] = retrieval.SearchResult{ChunkID: fmt.Sprint(i), Score: 1}
	}
	p, _ := NewPipeline(func(context.Context, string) ([]retrieval.SearchResult, error) { return hits, nil }, rerank.NewEngine(nil, rerank.DefaultConfig()), nil, Config{FinalTopK: 10})
	r, err := p.Search(context.Background(), "q")
	if err != nil || len(r.Results) != 6 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestParentFallbackDistinguishesFailureFromCancellation(t *testing.T) {
	hits := []retrieval.SearchResult{{ChunkID: "child", ParentChunkID: "parent", Content: "evidence"}}
	loader := &fakeParentLoader{err: errors.New("database unavailable")}
	p, _ := NewPipeline(func(context.Context, string) ([]retrieval.SearchResult, error) { return hits, nil }, nil, loader, Config{ExpandParents: true, AllowParentFallback: true})
	r, err := p.Search(context.Background(), "q")
	if err != nil || len(r.Results) != 1 || len(r.Diagnostics) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	loader.err = context.Canceled
	if _, err = p.Search(context.Background(), "q"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation swallowed: %v", err)
	}
}

func TestParentExpansionNeverCombinesDifferentRevisions(t *testing.T) {
	hits := []retrieval.SearchResult{{CollectionID: "kb", DocumentID: "doc", DocumentRevision: 1, ChunkID: "child", ParentChunkID: "parent", Content: "old evidence"}}
	loader := &fakeParentLoader{parents: map[string]retrieval.SearchResult{"parent": {CollectionID: "kb", DocumentID: "doc", DocumentRevision: 2, ChunkID: "parent", Content: "new parent"}}}
	p, _ := NewPipeline(func(context.Context, string) ([]retrieval.SearchResult, error) { return hits, nil }, nil, loader, Config{ExpandParents: true, CollapseSameParent: true})
	r, err := p.Search(context.Background(), "query")
	if err != nil || r.Results[0].EffectiveContent() != "old evidence" || r.Results[0].EffectiveChunkID() != "child" || len(r.Diagnostics) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCanceledRetrieverCannotReturnSuccessfulEmptySearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, _ := NewPipeline(func(context.Context, string) ([]retrieval.SearchResult, error) { cancel(); return nil, nil }, nil, nil, Config{})
	if _, err := p.Search(ctx, "query"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation swallowed: %v", err)
	}
}
