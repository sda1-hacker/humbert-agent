package config

import "fmt"

// Effective 补齐预算和计数函数，不修改调用方配置。
func (b Budget) Effective() Budget {
	if b.MaxInputTokens == 0 {
		b.MaxInputTokens = defaultMaxInputTokens
	}
	if b.MaxBatchTokens == 0 {
		b.MaxBatchTokens = defaultMaxBatchTokens
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
