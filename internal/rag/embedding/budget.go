// Package embedding 管理向量服务、输入预算与模型档案，不截断原文。
package embedding

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/embedding"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

type limitedEmbedder struct {
	delegate   embedding.Embedder
	budget     ragconfig.Budget
	fixedModel string
	timeout    time.Duration
	dimensions int
}

// Limit 包装 Eino Embedder，拒绝超长输入并按批请求，不截断原文。
func Limit(delegate embedding.Embedder, budget ragconfig.Budget) embedding.Embedder {
	return &limitedEmbedder{delegate: delegate, budget: budget}
}

// EmbedStrings 按预算分批生成向量，保持输入顺序并校验向量数量。
func (e *limitedEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.fixedModel != "" {
		options := embedding.GetCommonOptions(nil, opts...)
		if options.Model != nil && *options.Model != e.fixedModel {
			return nil, fmt.Errorf("rag embedding: recreate the provider to change the pinned model")
		}
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
		vectors, err := e.embedBatch(ctx, texts[batch.Start:batch.End], opts...)
		if err != nil {
			return nil, err
		}
		if len(vectors) != batch.End-batch.Start {
			return nil, fmt.Errorf("rag embedding: provider returned %d vectors for %d inputs", len(vectors), batch.End-batch.Start)
		}
		// 服务端可能不支持或忽略 dimensions；在返回给索引器前拒绝不匹配，禁止静默裁剪。
		for _, vector := range vectors {
			if e.dimensions > 0 && len(vector) != e.dimensions {
				return nil, fmt.Errorf("%w: requested %d, provider returned %d", ragconfig.ErrInvalidDimensions, e.dimensions, len(vector))
			}
		}
		out = append(out, vectors...)
	}
	return out, nil
}

// embedBatch 按请求限制执行时间，并恢复被部分上游适配器转成文本的取消/超时原因。
func (e *limitedEmbedder) embedBatch(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	vectors, err := e.delegate.EmbedStrings(ctx, texts, opts...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return vectors, err
}
