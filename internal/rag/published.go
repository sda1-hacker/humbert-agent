package rag

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

type published struct {
	component retriever.Retriever
	reader    PublishedChunkReader
}

// WithPublishedChunks 包装原生 Eino Retriever，按权威文档库过滤未发布和过期版本。
// 外部索引必须保留集合、文档、版本、分块 ID；只有基础检索时可以不使用该包装。
func WithPublishedChunks(component retriever.Retriever, reader PublishedChunkReader) (retriever.Retriever, error) {
	if component == nil || reader == nil {
		return nil, errors.New("rag: retriever and published chunk reader are required")
	}
	return &published{component: component, reader: reader}, nil
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
	var docs []*schema.Document
	var err error
	if component, ok := p.component.(interface {
		RetrieveWithDiagnostics(context.Context, string, ...retriever.Option) ([]*schema.Document, retrieval.Diagnostics, error)
	}); ok {
		docs, diagnostics, err = component.RetrieveWithDiagnostics(ctx, query, opts...)
	} else {
		docs, err = p.component.Retrieve(ctx, query, opts...)
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
	return retrieval.Documents(hits), diagnostics, nil
}
