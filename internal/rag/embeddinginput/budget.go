// Package embeddinginput 校验包含标题与补充表头的完整模型输入，不截断原文。
package embeddinginput

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/embedding"
)

var ErrInputBudget = errors.New("rag embedding: input exceeds token budget")

// Budget 限制完整模型输入与单批请求的 token 数。可注入真实分词器；默认以 UTF-8 字节数作保守估算，限制值需按实际模型配置。
type Budget struct {
	// MaxInputTokens 单条完整模型输入的 token 上限。
	MaxInputTokens int
	// MaxBatchTokens 一次请求的总 token 上限。
	MaxBatchTokens int
	// CountTokens 可选模型分词计数器，缺失时按 UTF-8 字节数估算。
	CountTokens func(string) int
}

// DefaultBudget 返回本地默认保护值，不代表任何模型的官方上下文长度。
func DefaultBudget() Budget { return Budget{MaxInputTokens: 8192, MaxBatchTokens: 65536} }

// Validate 拒绝负数预算，零值在 Effective 中补齐。
func (b Budget) Validate() error {
	if b.MaxInputTokens < 0 || b.MaxBatchTokens < 0 {
		return fmt.Errorf("rag embedding: negative token budget")
	}
	return nil
}

// Effective 补齐预算和计数函数，不修改调用方配置。
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

// Batch 一次模型请求的左闭右开输入区间。
type Batch struct{ Start, End int }

// Plan 在请求模型前校验所有输入，同时限制每批条数与总 token 数。
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

// Limit 包装 Eino Embedder，拒绝超长输入并按批请求，不截断原文。
func Limit(delegate embedding.Embedder, budget Budget) embedding.Embedder {
	return &limitedEmbedder{delegate: delegate, budget: budget}
}

// EmbedStrings 按预算分批生成向量，保持输入顺序并校验向量数量。
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
