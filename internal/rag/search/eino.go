package search

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// 带诊断的检索器仍然实现原生 Retriever，普通 eino-ext 组件无需实现此能力。
type diagnosticRetriever interface {
	RetrieveWithDiagnostics(context.Context, string, ...retriever.Option) ([]*schema.Document, retrieval.Diagnostics, error)
}

// NewEinoPipeline 在组件边界转换一次数据，后续引用、精排与父块扩展使用内部结果。
func NewEinoPipeline(component retriever.Retriever, scorer *rerank.Engine, parents ParentLoader, cfg Config, opts ...retriever.Option) (*Pipeline, error) {
	if component == nil {
		return nil, ErrNilRetriever
	}
	options := append([]retriever.Option(nil), opts...)
	return NewPipelineWithDiagnostics(func(ctx context.Context, query string) ([]retrieval.SearchResult, retrieval.Diagnostics, error) {
		type reply struct {
			docs []*schema.Document
			diag retrieval.Diagnostics
			err  error
		}
		out := make(chan reply, 1)
		// 超时后缓冲发送允许调用退出；真正的网络资源清理由组件遵守 context。
		go func() {
			var r reply
			if d, ok := component.(diagnosticRetriever); ok {
				r.docs, r.diag, r.err = d.RetrieveWithDiagnostics(ctx, query, options...)
			} else {
				r.docs, r.err = component.Retrieve(ctx, query, options...)
			}
			out <- r
		}()
		select {
		case <-ctx.Done():
			return nil, retrieval.Diagnostics{}, ctx.Err()
		case r := <-out:
			if err := ctx.Err(); err != nil {
				return nil, r.diag, err
			}
			if r.err != nil {
				return nil, r.diag, r.err
			}
			results, err := retrieval.Results(r.docs)
			if err != nil {
				return nil, r.diag, err
			}
			common := retriever.GetCommonOptions(nil, options...)
			if common.Index != nil {
				for i := range results {
					if results[i].CollectionID != "" && results[i].CollectionID != *common.Index {
						return nil, r.diag, fmt.Errorf("rag: backend returned another collection")
					}
					results[i].CollectionID = *common.Index
				}
			}
			return results, r.diag, nil
		}
	}, scorer, parents, cfg)
}
