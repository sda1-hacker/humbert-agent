package rag

import (
	"context"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// DocumentPublisher 保存并发布完整原文与父子分块。
// 只有预留版本仍有效时才能成功，失败时上一版文档应保持可见。
// 外部 Indexer 写入时必须保留分块 ID 和集合、文档、版本元数据。
type DocumentPublisher interface {
	PublishDocument(context.Context, IngestionBatch) error
}

// ChunkReader 返回当前已发布文档的普通可检索分块，按 ChunkIndex 升序排列。
type ChunkReader interface {
	ListChunks(context.Context, string, string) ([]ChunkRecord, error)
}

// PublishedChunkReader 按文档版本校验外部索引命中，并补全权威正文。
// 已删除、未发布及过期版本的命中必须丢弃；保留输入顺序和召回分数。
type PublishedChunkReader interface {
	ResolveChunks(context.Context, string, []retrieval.SearchResult) ([]retrieval.SearchResult, error)
}

// DocumentLifecycle 与存储实现无关，保留版本预留、取消和删除能力。
type DocumentLifecycle interface {
	DocumentVersioner
	InvalidateDocument(context.Context, string, string) error
	DeleteDocument(context.Context, string, string) error
}
