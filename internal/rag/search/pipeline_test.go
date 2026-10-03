package search

import (
	"context"
	"errors"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

type fakeScorer struct {
	scores []float64
	err    error
}

func (f *fakeScorer) Score(
	ctx context.Context,
	query string,
	passages []string,
) ([]float64, error) {
	if f.err != nil {
		return nil, f.err
	}

	return append([]float64(nil), f.scores...), nil
}

type fakeParentLoader struct {
	parents map[string]retrieval.SearchResult
	err     error

	requested []string
}

func (f *fakeParentLoader) LoadParents(
	ctx context.Context,
	parentChunkIDs []string,
) (map[string]retrieval.SearchResult, error) {
	f.requested = append(
		[]string(nil),
		parentChunkIDs...,
	)

	if f.err != nil {
		return nil, f.err
	}

	return f.parents, nil
}

func TestPipelineRetrieveRerankParentExpand(t *testing.T) {
	retrieve := func(
		ctx context.Context,
		query string,
	) ([]retrieval.SearchResult, error) {
		return []retrieval.SearchResult{
			{
				ChunkID:       "child-a",
				DocumentID:    "doc-1",
				ParentChunkID: "parent-1",
				Content:       "Linux child",
				Score:         0.80,
			},
			{
				ChunkID:    "child-b",
				DocumentID: "doc-2",
				Content:    "Windows child",
				Score:      0.90,
			},
		}, nil
	}

	// Reranker 认为 Linux Child 更相关。
	scorer := &fakeScorer{
		scores: []float64{
			0.95,
			0.40,
		},
	}

	rerankCfg := rerank.DefaultConfig()
	rerankCfg.TopK = 2

	reranker := rerank.NewEngine(
		scorer,
		rerankCfg,
	)

	parentLoader := &fakeParentLoader{
		parents: map[string]retrieval.SearchResult{
			"parent-1": {
				ChunkID: "parent-1",
				Content: "这是完整的 Linux 安装 Parent Context。",
			},
		},
	}

	pipeline, err := NewPipeline(
		retrieve,
		reranker,
		parentLoader,
		DefaultConfig(),
	)

	if err != nil {
		t.Fatal(err)
	}

	response, err := pipeline.Search(
		context.Background(),
		"Linux 安装",
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(response.Results) != 2 {
		t.Fatalf(
			"应该返回2个结果: %d",
			len(response.Results),
		)
	}

	first := response.Results[0]

	if first.ChunkID != "child-a" {
		t.Fatalf(
			"Reranker 应把 child-a 排第一: %+v",
			first,
		)
	}

	// 真正命中的 Child 不应该被 Parent 覆盖。
	if first.Content != "Linux child" {
		t.Fatalf(
			"Child Content 不应该被修改: %q",
			first.Content,
		)
	}

	if first.ContextChunkID != "parent-1" {
		t.Fatalf(
			"应该扩展到 Parent: %+v",
			first,
		)
	}

	if first.EffectiveContent() !=
		"这是完整的 Linux 安装 Parent Context。" {

		t.Fatalf(
			"EffectiveContent 应该使用 Parent: %q",
			first.EffectiveContent(),
		)
	}
}

func TestPipelineWithoutParentUsesChildAsContext(t *testing.T) {
	retrieve := func(
		ctx context.Context,
		query string,
	) ([]retrieval.SearchResult, error) {
		return []retrieval.SearchResult{
			{
				ChunkID: "child",
				Content: "Child Content",
				Score:   0.9,
			},
		}, nil
	}

	cfg := DefaultConfig()

	pipeline, err := NewPipeline(
		retrieve,
		nil,
		nil,
		cfg,
	)

	if err != nil {
		t.Fatal(err)
	}

	response, err := pipeline.Search(
		context.Background(),
		"query",
	)

	if err != nil {
		t.Fatal(err)
	}

	got := response.Results[0]

	if got.ContextChunkID != "child" {
		t.Fatalf(
			"无 Parent 时 ContextChunkID 应等于 Child: %+v",
			got,
		)
	}

	if got.EffectiveContent() != "Child Content" {
		t.Fatalf(
			"无 Parent 时应该读取 Child: %q",
			got.EffectiveContent(),
		)
	}
}

func TestPipelineMissingParentFallsBackToChild(t *testing.T) {
	retrieve := func(
		ctx context.Context,
		query string,
	) ([]retrieval.SearchResult, error) {
		return []retrieval.SearchResult{
			{
				ChunkID:       "child",
				ParentChunkID: "missing-parent",
				Content:       "Child Content",
				Score:         0.9,
			},
		}, nil
	}

	loader := &fakeParentLoader{
		parents: map[string]retrieval.SearchResult{},
	}

	pipeline, err := NewPipeline(
		retrieve,
		nil,
		loader,
		DefaultConfig(),
	)

	if err != nil {
		t.Fatal(err)
	}

	response, err := pipeline.Search(
		context.Background(),
		"query",
	)

	if err != nil {
		t.Fatal(err)
	}

	if response.Results[0].EffectiveContent() !=
		"Child Content" {

		t.Fatalf(
			"Parent 丢失时必须保留 Child Context: %+v",
			response.Results[0],
		)
	}
}

func TestPipelineCollapseSameParent(t *testing.T) {
	retrieve := func(
		ctx context.Context,
		query string,
	) ([]retrieval.SearchResult, error) {
		return []retrieval.SearchResult{
			{
				ChunkID:       "child-a",
				ParentChunkID: "parent",
				Content:       "A",
				Score:         0.9,
			},
			{
				ChunkID:       "child-b",
				ParentChunkID: "parent",
				Content:       "B",
				Score:         0.8,
			},
		}, nil
	}

	loader := &fakeParentLoader{
		parents: map[string]retrieval.SearchResult{
			"parent": {
				ChunkID: "parent",
				Content: "Parent Content",
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CollapseSameParent = true

	pipeline, err := NewPipeline(
		retrieve,
		nil,
		loader,
		cfg,
	)

	if err != nil {
		t.Fatal(err)
	}

	response, err := pipeline.Search(
		context.Background(),
		"query",
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(response.Results) != 1 {
		t.Fatalf(
			"同一 Parent 应折叠为一个 Context: %d",
			len(response.Results),
		)
	}

	if response.Results[0].ChunkID != "child-a" {
		t.Fatalf(
			"应该保留得分更高的 Child Hit: %+v",
			response.Results[0],
		)
	}
}

func TestPipelineRerankModelErrorStillReturnsResults(t *testing.T) {
	retrieve := func(
		ctx context.Context,
		query string,
	) ([]retrieval.SearchResult, error) {
		return []retrieval.SearchResult{
			{
				ChunkID: "A",
				Content: "A",
				Score:   0.9,
			},
			{
				ChunkID: "B",
				Content: "B",
				Score:   0.8,
			},
		}, nil
	}

	scorer := &fakeScorer{
		err: errors.New("model unavailable"),
	}

	reranker := rerank.NewEngine(
		scorer,
		rerank.DefaultConfig(),
	)

	pipeline, err := NewPipeline(
		retrieve,
		reranker,
		nil,
		DefaultConfig(),
	)

	if err != nil {
		t.Fatal(err)
	}

	response, err := pipeline.Search(
		context.Background(),
		"query",
	)

	if err != nil {
		t.Fatal(err)
	}

	if response.Rerank.Outcome !=
		rerank.OutcomeModelError {

		t.Fatalf(
			"应该记录 model_error: %+v",
			response.Rerank,
		)
	}

	if len(response.Results) != 2 {
		t.Fatalf(
			"模型失败不应该让检索结果消失: %d",
			len(response.Results),
		)
	}

	if response.Results[0].ChunkID != "A" {
		t.Fatalf(
			"模型失败应该保持 Retrieval 顺序: %+v",
			response.Results,
		)
	}
}

func TestPipelineNoCandidates(t *testing.T) {
	retrieve := func(
		ctx context.Context,
		query string,
	) ([]retrieval.SearchResult, error) {
		return nil, nil
	}

	pipeline, err := NewPipeline(
		retrieve,
		nil,
		nil,
		DefaultConfig(),
	)

	if err != nil {
		t.Fatal(err)
	}

	response, err := pipeline.Search(
		context.Background(),
		"query",
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(response.Results) != 0 {
		t.Fatalf(
			"没有候选时结果应该为空: %d",
			len(response.Results),
		)
	}

	if response.Rerank.Outcome !=
		rerank.OutcomeNoCandidates {

		t.Fatalf(
			"Diagnostics 错误: %+v",
			response.Rerank,
		)
	}
}

func TestPipelineRejectsEmptyQuery(t *testing.T) {
	retrieve := func(
		ctx context.Context,
		query string,
	) ([]retrieval.SearchResult, error) {
		return nil, nil
	}

	pipeline, err := NewPipeline(
		retrieve,
		nil,
		nil,
		DefaultConfig(),
	)

	if err != nil {
		t.Fatal(err)
	}

	_, err = pipeline.Search(
		context.Background(),
		"   ",
	)

	if !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf(
			"空 Query 应返回 ErrEmptyQuery: %v",
			err,
		)
	}
}
