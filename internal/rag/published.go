package rag

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

type published struct {
	component     retriever.Retriever
	reader        PublishedChunkReader
	maxCandidates int
}

// WithPublishedChunks 包装原生 Eino Retriever，按权威文档库过滤未发布和过期版本。
// 外部索引必须保留集合、文档、版本、分块 ID；只有基础检索时可以不使用该包装。
func WithPublishedChunks(component retriever.Retriever, reader PublishedChunkReader, maxCandidates ...int) (retriever.Retriever, error) {
	if component == nil || reader == nil {
		return nil, errors.New("rag: retriever and published chunk reader are required")
	}
	limit := 200
	if len(maxCandidates) > 0 {
		limit = maxCandidates[0]
	}
	if limit <= 0 {
		return nil, errors.New("rag: published candidate limit must be positive")
	}
	return &published{component: component, reader: reader, maxCandidates: limit}, nil
}

// Retrieve 执行原生检索，再过滤未发布、已删除和过期版本的分块。
func (p *published) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	docs, _, err := p.RetrieveWithDiagnostics(ctx, query, opts...)
	return docs, err
}

// 版本过滤保留混合检索的降级诊断，不能让包装隐藏某一路失败的信息。
func (p *published) RetrieveWithDiagnostics(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, retrieval.Diagnostics, error) {
	var diagnostics retrieval.Diagnostics
	common := retriever.GetCommonOptions(nil, opts...)
	if common.Index == nil || *common.Index == "" {
		return nil, diagnostics, ErrMissingCollectionID
	}
	target := 50
	if common.TopK != nil {
		target = *common.TopK
	}
	if target <= 0 || target > p.maxCandidates {
		return nil, diagnostics, errors.New("rag: top k exceeds published candidate limit")
	}
	// 使用更大的有界候选池补足旧版本过滤，不能反复请求同样的 TopK 或无限重试。
	for candidateLimit := target; ; candidateLimit += min(candidateLimit, p.maxCandidates-candidateLimit) {
		if err := ctx.Err(); err != nil {
			return nil, diagnostics, err
		}
		options := append(append([]retriever.Option(nil), opts...), retriever.WithTopK(candidateLimit))
		var docs []*schema.Document
		var err error
		if component, ok := p.component.(interface {
			RetrieveWithDiagnostics(context.Context, string, ...retriever.Option) ([]*schema.Document, retrieval.Diagnostics, error)
		}); ok {
			docs, diagnostics, err = component.RetrieveWithDiagnostics(ctx, query, options...)
		} else {
			docs, err = p.component.Retrieve(ctx, query, options...)
		}
		if err != nil {
			return nil, diagnostics, err
		}
		hits, err := retrieval.Results(docs)
		if err != nil {
			return nil, diagnostics, err
		}
		if err := ctx.Err(); err != nil {
			return nil, diagnostics, err
		}
		for _, hit := range hits {
			if hit.CollectionID != *common.Index {
				return nil, diagnostics, errors.New("rag: published retrieval requires matching collection metadata")
			}
		}
		hits, err = p.reader.ResolveChunks(ctx, *common.Index, hits)
		if err != nil {
			return nil, diagnostics, err
		}
		seen := make(map[string]bool, len(hits))
		unique := hits[:0]
		for _, hit := range hits {
			if !seen[hit.IdentityKey()] {
				seen[hit.IdentityKey()] = true
				unique = append(unique, hit)
			}
		}
		hits = unique
		if len(hits) >= target || len(docs) < candidateLimit || candidateLimit == p.maxCandidates {
			return retrieval.Documents(retrieval.Limit(hits, target)), diagnostics, nil
		}
	}
}
