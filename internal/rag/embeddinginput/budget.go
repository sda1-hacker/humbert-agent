// Package embeddinginput validates the final provider input, including titles
// and synthetic headers. It never truncates source text.
package embeddinginput

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/embedding"
)

var ErrInputBudget = errors.New("rag embedding: input exceeds token budget")

// CountTokens may use a model tokenizer. Without one, UTF-8 bytes provide a
// conservative budget for byte-based tokenizers. Configure limits for the
// actual provider; defaults are local safety limits, not model specifications.
type Budget struct {
	MaxInputTokens int
	MaxBatchTokens int
	CountTokens    func(string) int
}

func DefaultBudget() Budget { return Budget{MaxInputTokens: 8192, MaxBatchTokens: 65536} }

func (b Budget) Validate() error {
	if b.MaxInputTokens < 0 || b.MaxBatchTokens < 0 {
		return fmt.Errorf("rag embedding: negative token budget")
	}
	return nil
}

func (b Budget) Effective() Budget {
	defaults := DefaultBudget()
	if b.MaxInputTokens == 0 {
		b.MaxInputTokens = defaults.MaxInputTokens
	}
	if b.MaxBatchTokens == 0 {
		b.MaxBatchTokens = defaults.MaxBatchTokens
	}
	if b.CountTokens == nil {
		b.CountTokens = func(s string) int { return len(s) }
	}
	return b
}

type Batch struct{ Start, End int }

// Plan validates every input before any remote request, then bounds both the
// number of strings and the total tokens in each request.
func (b Budget) Plan(texts []string, batchSize int) ([]Batch, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	b = b.Effective()
	if batchSize <= 0 {
		batchSize = len(texts)
	}
	var batches []Batch
	start, total := 0, 0
	for i, text := range texts {
		tokens := b.CountTokens(text)
		if tokens < 0 {
			return nil, fmt.Errorf("rag embedding: tokenizer returned a negative count for input %d", i)
		}
		if tokens > b.MaxInputTokens || tokens > b.MaxBatchTokens {
			return nil, fmt.Errorf("%w: input %d has %d tokens (single limit %d, batch limit %d)", ErrInputBudget, i, tokens, b.MaxInputTokens, b.MaxBatchTokens)
		}
		if i > start && (i-start >= batchSize || tokens > b.MaxBatchTokens-total) {
			batches = append(batches, Batch{start, i})
			start, total = i, 0
		}
		total += tokens
	}
	if start < len(texts) {
		batches = append(batches, Batch{start, len(texts)})
	}
	return batches, nil
}

type limitedEmbedder struct {
	delegate embedding.Embedder
	budget   Budget
}

func Limit(delegate embedding.Embedder, budget Budget) embedding.Embedder {
	return &limitedEmbedder{delegate: delegate, budget: budget}
}

func (e *limitedEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	batches, err := e.budget.Plan(texts, len(texts))
	if err != nil {
		return nil, err
	}
	var out [][]float64
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		vectors, err := e.delegate.EmbedStrings(ctx, texts[batch.Start:batch.End], opts...)
		if err != nil {
			return nil, err
		}
		if len(vectors) != batch.End-batch.Start {
			return nil, fmt.Errorf("rag embedding: provider returned %d vectors for %d inputs", len(vectors), batch.End-batch.Start)
		}
		out = append(out, vectors...)
	}
	return out, nil
}
