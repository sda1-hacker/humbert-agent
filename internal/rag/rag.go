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
// Eino 组件仍可直接使用其原生接口；原件引用、分块版本和发布由独立业务能力负责。
type Dependencies struct {
	// Loader 解析真实文档的 Eino 加载器。
	Loader document.Loader
	// Indexer 接收统一检索文本的 Eino 索引器。
	// 原生组件应在装配时配置知识库范围；是否支持 WithIndex 以具体组件为准。
	Indexer indexer.Indexer
	// Config 召回、精排与父块扩展配置。
	Config Config

	// ChunkReader 读取已发布分块，供展示和人工标注。
	ChunkReader ChunkReader
	// Lifecycle 解析前预留版本，并提供取消与删除能力。
	Lifecycle DocumentLifecycle
	// Sources 保存原件，Documents 记录文件引用、导入状态和错误；二者由装配方显式提供。
	Sources   SourceStore
	Documents DocumentTracker
	// PublishedChunks 回查权威正文，过滤外部索引中的失效命中。
	// 这是最后的版本/范围校验，不能替代后端的分区或元数据过滤。
	PublishedChunks PublishedChunkReader

	// 外部索引成功后再发布权威文档。使用 PostgreSQL 原子 Indexer 时留空，
	// 因为它通过 WithIngestionBatch 在同一事务内保存元数据、分块和索引。
	Publisher DocumentPublisher
	// BeforePublish 等待外部索引可检索；使用强一致或同步刷新组件时可留空。
	// ES/Milvus 的刷新和一致性参数由原生组件/客户端处理，核心不复制数据库 SDK。
	BeforePublish func(context.Context, IngestionBatch) error
	// DiscardAttempt 原子作废未发布版本，返回 false 表示已发布，不能清理其索引。
	DiscardAttempt func(context.Context, string, string, int64) (bool, error)
	// RemoveIndex 使用原生客户端按分块 ID 清理所有外部索引，必须幂等且限制知识库范围。
	RemoveIndex func(context.Context, string, []string) error
	// Close 在在途请求和状态收尾全部结束后释放资源，只执行一次；不要在请求内部关闭服务。
	Close func()
}

// Service 是唯一的业务入口。提供导入、检索及文档管理，不再包装另一层 Service。
type Service struct {
	loader         document.Loader
	indexer        indexer.Indexer
	config         Config
	chunks         ChunkReader
	lifecycle      DocumentLifecycle
	sources        SourceStore
	documents      DocumentTracker
	publisher      DocumentPublisher
	beforePublish  func(context.Context, IngestionBatch) error
	discardAttempt func(context.Context, string, string, int64) (bool, error)
	removeIndex    func(context.Context, string, []string) error
	close          func()
	closeOnce      sync.Once
	closedDone     chan struct{}
	active         sync.WaitGroup
	mu             sync.Mutex
	closed         bool
	stopping       context.Context
	stop           context.CancelFunc
	ingesting      map[string]*ingestRun
	slots          chan struct{}
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
	if err := deps.Config.InputBudget.Validate(); err != nil {
		return nil, err
	}
	if err := deps.Config.Ingest.Validate(); err != nil {
		return nil, err
	}
	deps.Config.Ingest = deps.Config.Ingest.Effective()
	if deps.Documents != nil && deps.Lifecycle == nil {
		return nil, errors.New("rag: document tracking requires versioned lifecycle")
	}
	if deps.Sources != nil && deps.Documents == nil {
		return nil, errors.New("rag: managed sources require document tracking")
	}
	if deps.Publisher != nil && (deps.Lifecycle == nil || deps.PublishedChunks == nil) {
		return nil, errors.New("rag: external publication requires lifecycle and published chunk reader")
	}
	if deps.Publisher != nil && (deps.ChunkReader == nil || deps.RemoveIndex == nil || deps.DiscardAttempt == nil) {
		return nil, errors.New("rag: external publication requires chunk reader and guarded index cleanup")
	}
	if deps.PublishedChunks != nil {
		// 在业务入口统一过滤，单路与混合模式都不能绕过文档版本检查。
		for _, component := range []*retriever.Retriever{&deps.Config.Retriever, &deps.Config.VectorRetriever, &deps.Config.KeywordRetriever} {
			if *component != nil {
				*component, _ = WithPublishedChunks(*component, deps.PublishedChunks)
			}
		}
	}
	stopping, stop := context.WithCancel(context.Background())
	return &Service{
		loader: deps.Loader, indexer: deps.Indexer, config: deps.Config,
		chunks: deps.ChunkReader, lifecycle: deps.Lifecycle, publisher: deps.Publisher,
		sources: deps.Sources, documents: deps.Documents,
		beforePublish: deps.BeforePublish, discardAttempt: deps.DiscardAttempt, removeIndex: deps.RemoveIndex,
		close: deps.Close, closedDone: make(chan struct{}),
		stopping: stopping, stop: stop,
		ingesting: make(map[string]*ingestRun), slots: make(chan struct{}, deps.Config.Ingest.MaxConcurrent),
	}, nil
}

// GetDocument 读取文件信息与最近导入状态，不加载整篇正文或分块。
func (s *Service) GetDocument(ctx context.Context, collectionID, documentID string) (DocumentRecord, error) {
	ctx, finish, err := s.beginRequest(ctx, 0)
	if err != nil {
		return DocumentRecord{}, err
	}
	defer finish()
	if s.documents == nil {
		return DocumentRecord{}, errors.New("rag: document tracking is not configured")
	}
	collectionID, documentID, err = documentScope(ctx, collectionID, documentID)
	if err != nil {
		return DocumentRecord{}, err
	}
	return s.documents.GetDocument(ctx, collectionID, documentID)
}

// RecoverDocuments 由启动入口或人工恢复操作调用，只收尾过期任务，不重新解析或请求模型。
// 仍在总时限内的任务可能属于另一进程，必须保留；旧版本的可检索内容也会保留。
func (s *Service) RecoverDocuments(ctx context.Context, collectionID string) (int64, error) {
	ctx, finish, err := s.beginRequest(ctx, s.config.Search.Effective().Timeout)
	if err != nil {
		return 0, err
	}
	defer finish()
	if s.documents == nil {
		return 0, errors.New("rag: document tracking is not configured")
	}
	return s.documents.RecoverDocuments(ctx, collectionID)
}

// ListChunks 返回已发布的普通分块，供展示和人工标注使用。
func (s *Service) ListChunks(ctx context.Context, collectionID, documentID string) ([]ChunkRecord, error) {
	ctx, finish, err := s.beginRequest(ctx, 0)
	if err != nil {
		return nil, err
	}
	defer finish()
	if s.chunks == nil {
		return nil, errors.New("rag: chunk reader is not configured")
	}
	collectionID, documentID, err = documentScope(ctx, collectionID, documentID)
	if err != nil {
		return nil, err
	}
	return s.chunks.ListChunks(ctx, collectionID, documentID)
}

// CancelDocument 作废正在处理的版本；上一次已发布版本仍可检索。
// 同时取消本进程中该文档的解析或模型请求；跨进程任务仍由版本检查阻止发布。
func (s *Service) CancelDocument(ctx context.Context, collectionID, documentID string) error {
	ctx, finish, err := s.beginRequest(ctx, 0)
	if err != nil {
		return err
	}
	defer finish()
	if s.lifecycle == nil {
		return errors.New("rag: document lifecycle is not configured")
	}
	collectionID, documentID, err = documentScope(ctx, collectionID, documentID)
	if err != nil {
		return err
	}
	// 先停止本进程任务，再作废版本；等待模型结束不再仅依赖数据库版本检查。
	release := s.stopIngest(collectionID, documentID)
	defer release()
	return s.lifecycle.InvalidateDocument(ctx, collectionID, documentID)
}

// DeleteDocument 删除权威文档；外部索引残留通过版本检查过滤。
func (s *Service) DeleteDocument(ctx context.Context, collectionID, documentID string) error {
	ctx, finish, err := s.beginRequest(ctx, 0)
	if err != nil {
		return err
	}
	defer finish()
	if s.lifecycle == nil {
		return errors.New("rag: document lifecycle is not configured")
	}
	collectionID, documentID, err = documentScope(ctx, collectionID, documentID)
	if err != nil {
		return err
	}
	release := s.stopIngest(collectionID, documentID)
	defer release()
	var ids []string
	if s.publisher != nil {
		chunks, err := s.chunks.ListChunks(ctx, collectionID, documentID)
		if err != nil {
			return err
		}
		ids = chunkIDs(chunks)
	}
	if err := s.lifecycle.DeleteDocument(ctx, collectionID, documentID); err != nil {
		return err
	}
	if len(ids) > 0 {
		return s.removeIndex(ctx, collectionID, ids)
	}
	return nil
}

// Shutdown 停止接收请求并取消在途操作，等失败回写、索引补偿结束后才释放资源。
// 等待超时只结束本次等待；清理仍会继续，不能提前关闭在途任务依赖的连接池。
func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.stop()
		s.mu.Unlock()
		go func() {
			s.active.Wait()
			if s.close != nil {
				s.close()
			}
			close(s.closedDone)
		}()
	})
	select {
	case <-s.closedDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close 可重复调用，等待清理完成；有退出截止时间的调用方使用 Shutdown。
// 注入的 Eino 组件与存储必须响应 context，不能在组件内部调用本服务的 Close。
func (s *Service) Close() {
	_ = s.Shutdown(context.Background())
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
