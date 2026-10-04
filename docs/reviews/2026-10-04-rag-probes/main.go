// Deterministic review probes. These use no database or remote model.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
)

type parents struct{}

func (parents) LoadParents(_ context.Context, ids []string) (map[string]retrieval.SearchResult, error) {
	out := make(map[string]retrieval.SearchResult)
	for _, id := range ids {
		out[id] = retrieval.SearchResult{ChunkID: id, Content: id}
	}
	return out, nil
}

func main() {
	ctx := context.Background()
	text := strings.Repeat("中", 2000)
	chunks, diag := chunker.SplitWithDiagnostics(text, chunker.SplitterConfig{ChunkSize: 100, TokenLimit: 32, Languages: []string{"zh"}})
	fmt.Printf("long_paragraph: chunks=%d first_runes=%d token_estimate=%d rejected=%v\n", len(chunks), chunker.RuneLen(chunks[0].Content), chunker.ApproxTokenCount(chunks[0].Content, "zh"), diag.Rejected)
	fmt.Printf("overlap_zero: normalized=%d\n", chunker.NormalizeSplitterConfig(chunker.SplitterConfig{ChunkOverlap: 0}).ChunkOverlap)
	_, diag = chunker.SplitWithDiagnostics("# A\ntext\n## B\ntext\n## C\ntext\n## D\ntext\n", chunker.DefaultConfig())
	fmt.Printf("default_strategy: selected=%s\n", diag.SelectedTier)

	hits := make([]retrieval.SearchResult, 6)
	for i := range hits {
		parentID := "parent-A"
		if i == 5 {
			parentID = "parent-B"
		}
		hits[i] = retrieval.SearchResult{ChunkID: fmt.Sprintf("c%d", i), ParentChunkID: parentID, Content: "body", Score: 1 - float64(i)/10}
	}
	retrieve := func(context.Context, string) ([]retrieval.SearchResult, error) { return hits, nil }
	pipeline, err := search.NewPipeline(retrieve, nil, parents{}, search.Config{FinalTopK: 5, ExpandParents: true, CollapseSameParent: true})
	if err != nil {
		panic(err)
	}
	response, err := pipeline.Search(ctx, "query")
	if err != nil {
		panic(err)
	}
	fmt.Printf("collapse_after_limit: unique_contexts=%d (second parent is sixth candidate)\n", len(response.Results))
	pipeline, err = search.NewPipeline(retrieve, rerank.NewEngine(nil, rerank.DefaultConfig()), nil, search.Config{FinalTopK: 10})
	if err != nil {
		panic(err)
	}
	response, err = pipeline.Search(ctx, "query")
	if err != nil {
		panic(err)
	}
	fmt.Printf("topk_layers: requested=10 candidates=6 returned=%d\n", len(response.Results))

	var table strings.Builder
	table.WriteString("| Key | Value |\n| --- | --- |\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&table, "| row-%02d | unique-value-%02d |\n", i, i)
	}
	markdown := table.String()
	parentCfg, childCfg := chunker.DeriveParentChildConfigs(chunker.DefaultConfig(), 160, 80)
	pc := chunker.SplitParentChild(markdown, parentCfg, childCfg)
	invalid := 0
	for _, child := range pc.Children {
		if child.End > chunker.RuneLen(markdown) {
			invalid++
		}
	}
	fmt.Printf("table_parent_child: source_runes=%d parents=%d children=%d out_of_bounds=%d\n", chunker.RuneLen(markdown), len(pc.Parents), len(pc.Children), invalid)
	for _, child := range pc.Children {
		if child.End > chunker.RuneLen(markdown) {
			fmt.Printf("table_invalid_range: [%d,%d) content=%q\n", child.Start, child.End, child.Content)
		}
	}
}
