// Package rag 提供解析、切分、索引与检索组成的业务 API。
// 具体数据库和模型在调用方组装，核心只接收 Eino 原生组件。
package rag

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
)

// Dependencies 声明运行所需组件，不根据 Indexer 的实际类型猜测业务能力。
// Eino 组件仍可直接使用其原生接口；文档原文、版本和发布由独立业务能力负责。
type Dependencies struct {
	// Loader 解析真实文档的 Eino 加载器。
	Loader document.Loader
	// Indexer 接收统一检索文本的 Eino 索引器。
	Indexer indexer.Indexer
	// Config 召回、精排与父块扩展配置。
	Config Config

	// ChunkReader 读取已发布分块，供展示和人工标注。
	ChunkReader ChunkReader
	// Lifecycle 解析前预留版本，并提供取消与删除能力。
	Lifecycle DocumentLifecycle
	// PublishedChunks 回查权威正文，过滤外部索引中的失效命中。
	PublishedChunks PublishedChunkReader

	// 外部索引成功后再发布权威文档。使用 PostgreSQL 原子 Indexer 时留空，
	// 因为它通过 WithIngestionBatch 在同一事务内保存原文、分块和索引。
	Publisher DocumentPublisher
	// Close 服务关闭时释放调用方指定的资源，只执行一次。
	Close func()
}

// Service 是唯一的业务入口。提供导入、检索及文档管理，不再包装另一层 Service。
type Service struct {
	loader    document.Loader
	indexer   indexer.Indexer
	config    Config
	chunks    ChunkReader
	lifecycle DocumentLifecycle
	publisher DocumentPublisher
	close     func()
	closeOnce sync.Once
}

// New 创建业务服务，允许只配置完整导入流程或只配置检索流程。
// 创建失败时资源仍由调用方释放；成功后 Close 接管指定清理函数。
func New(ctx context.Context, deps Dependencies) (*Service, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deps.Loader != nil && deps.Indexer == nil {
		return nil, ErrMissingIndexer
	}
	if deps.Indexer != nil && deps.Loader == nil {
		return nil, ErrMissingLoader
	}
	if deps.Loader == nil && deps.Config.Retriever == nil {
		return nil, ErrMissingRetriever
	}
	if err := deps.Config.Search.Validate(); err != nil {
		return nil, err
	}
	if deps.Config.RecallTopK < 0 {
		return nil, errors.New("rag: negative recall top k")
	}
	if deps.Publisher != nil && (deps.Lifecycle == nil || deps.PublishedChunks == nil) {
		return nil, errors.New("rag: external publication requires lifecycle and published chunk reader")
	}
	if deps.PublishedChunks != nil {
		// 在业务入口统一过滤，单路与混合模式都不能绕过文档版本检查。
		for _, component := range []*retriever.Retriever{&deps.Config.Retriever, &deps.Config.VectorRetriever, &deps.Config.KeywordRetriever} {
			if *component != nil {
				*component, _ = WithPublishedChunks(*component, deps.PublishedChunks)
			}
		}
	}
	return &Service{
		loader: deps.Loader, indexer: deps.Indexer, config: deps.Config,
		chunks: deps.ChunkReader, lifecycle: deps.Lifecycle, publisher: deps.Publisher,
		close: deps.Close,
	}, nil
}

// ListChunks 返回已发布的普通分块，供展示和人工标注使用。
func (s *Service) ListChunks(ctx context.Context, collectionID, documentID string) ([]ChunkRecord, error) {
	if s.chunks == nil {
		return nil, errors.New("rag: chunk reader is not configured")
	}
	collectionID, documentID, err := documentScope(ctx, collectionID, documentID)
	if err != nil {
		return nil, err
	}
	return s.chunks.ListChunks(ctx, collectionID, documentID)
}

// CancelDocument 作废正在处理的版本；上一次已发布版本仍可检索。
// 若需立即终止解析或模型请求，调用方还需取消对应 Ingest 的 context。
func (s *Service) CancelDocument(ctx context.Context, collectionID, documentID string) error {
	if s.lifecycle == nil {
		return errors.New("rag: document lifecycle is not configured")
	}
	collectionID, documentID, err := documentScope(ctx, collectionID, documentID)
	if err != nil {
		return err
	}
	return s.lifecycle.InvalidateDocument(ctx, collectionID, documentID)
}

// DeleteDocument 删除权威文档；外部索引残留通过版本检查过滤。
func (s *Service) DeleteDocument(ctx context.Context, collectionID, documentID string) error {
	if s.lifecycle == nil {
		return errors.New("rag: document lifecycle is not configured")
	}
	collectionID, documentID, err := documentScope(ctx, collectionID, documentID)
	if err != nil {
		return err
	}
	return s.lifecycle.DeleteDocument(ctx, collectionID, documentID)
}

// Close 可重复调用，底层资源只释放一次。
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.close != nil {
			s.close()
		}
	})
}

// documentScope 检查请求上下文并规范化知识库与文档 ID。
func documentScope(ctx context.Context, collectionID, documentID string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	collectionID, documentID = strings.TrimSpace(collectionID), strings.TrimSpace(documentID)
	if collectionID == "" {
		return "", "", ErrMissingCollectionID
	}
	if documentID == "" {
		return "", "", ErrMissingDocumentID
	}
	return collectionID, documentID, nil
}
