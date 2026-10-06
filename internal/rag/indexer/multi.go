// Package indexer 组合 Eino 原生索引器，不重新定义索引接口。
package indexer

import (
	"context"
	"errors"
	"fmt"

	eino "github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

type multi struct{ components []eino.Indexer }

// NewMulti 将同一批子块依次写入独立向量索引和关键词索引。
// 所有组件成功后才能由 rag.Service 发布文档；任一路失败立即返回错误。
// 组件必须只负责索引并保留输入 ID，不能提前发布权威文档。
// 多个数据库没有共同事务，失败后的残留由版本过滤隐藏，清理由具体后端负责。
func NewMulti(components ...eino.Indexer) (eino.Indexer, error) {
	if len(components) == 0 {
		return nil, errors.New("rag: at least one indexer is required")
	}
	for _, component := range components {
		if component == nil {
			return nil, errors.New("rag: nil indexer")
		}
	}
	return &multi{components: append([]eino.Indexer(nil), components...)}, nil
}

// Store 将同一批分块写入所有索引，保护输入元数据并校验后端保留全部分块 ID。
func (m *multi) Store(ctx context.Context, docs []*schema.Document, opts ...eino.Option) ([]string, error) {
	ids := make([]string, len(docs))
	seen := make(map[string]bool, len(docs))
	for i, doc := range docs {
		if doc == nil || doc.ID == "" {
			return nil, errors.New("rag: index document requires an id")
		}
		if seen[doc.ID] {
			return nil, errors.New("rag: duplicate index document id")
		}
		seen[doc.ID] = true
		ids[i] = doc.ID
	}
	for i, component := range m.components {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// 不让某个组件修改 Document 后影响另一路的正文和顶层元数据。
		input := make([]*schema.Document, len(docs))
		for j, doc := range docs {
			copy := *doc
			copy.MetaData = retrieval.CloneMetadata(doc.MetaData)
			input[j] = &copy
		}
		stored, err := component.Store(ctx, input, opts...)
		if err != nil {
			return nil, fmt.Errorf("store index %d: %w", i+1, err)
		}
		expected := make(map[string]bool, len(ids))
		for _, id := range ids {
			expected[id] = true
		}
		if len(stored) != len(ids) {
			return nil, errors.New("rag: indexer must preserve all unique input ids")
		}
		for _, id := range stored {
			if !expected[id] {
				return nil, errors.New("rag: indexer changed or duplicated an input id")
			}
			delete(expected, id)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
