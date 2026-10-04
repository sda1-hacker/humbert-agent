package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
)

const (
	ChunkTypeText       = "text"
	ChunkTypeParentText = "parent_text"

	MetaSourceDocumentID = "rag_source_document_id"
	MetaChunkIndex       = "rag_chunk_index"
	MetaChunkStart       = "rag_chunk_start"
	MetaChunkEnd         = "rag_chunk_end"
	MetaContextHeader    = "rag_context_header"
	MetaChunkType        = "rag_chunk_type"
	MetaParentChunkID    = "rag_parent_chunk_id"
	// These transient adapter fields carry a complete flat source envelope.
	// They must be removed before persisting chunk metadata.
	MetaSourceMarkdown   = "rag_source_markdown"
	MetaSourceChunkCount = "rag_source_chunk_count"
)

var (
	ErrMissingLoader        = errors.New("rag application: missing document loader")
	ErrMissingStore         = errors.New("rag application: missing ingestion store")
	ErrMissingSearchFactory = errors.New("rag application: missing search factory")

	ErrMissingCollectionID = errors.New("rag application: missing collection id")
	ErrMissingDocumentID   = errors.New("rag application: loader returned document without id")
	ErrEmptyQuery          = errors.New("rag application: empty search query")
)

// ChunkRecord 是 Application → Persistence Adapter 的稳定 Chunk 模型。
//
// 它不依赖 PostgreSQL，也不依赖 pgvector。
//
// Parent / Child 的存储差异由：
//
//	IngestionBatch.Parents
//	IngestionBatch.Children
//
// 明确表达。
type ChunkRecord struct {
	ID string

	DocumentID string

	ChunkType string

	ChunkIndex int

	Content string

	ContextHeader string

	StartRune int
	EndRune   int

	ParentChunkID string

	Metadata map[string]any
}

// IngestionBatch 表示“一个完整文档的新版本”。
//
// ReplaceDocument 的语义是：
//
//	这个 Batch 中的 Parent / Child
//
// 是这个 Document 当前全部有效 Chunk。
//
// Store 应当删除这个 Document 的旧 Chunk，
// 再原子写入新的完整集合。
type IngestionBatch struct {
	Attempt       int64
	ContentHash   string
	ProcessConfig json.RawMessage
	CollectionID  string

	DocumentID string
	Title      string

	// Markdown 是 Loader 输出的完整、规范化后的原文。
	//
	// 它必须在 Chunking 之前保存，
	// 绝不能从 Chunk 反向重建。
	Markdown string

	Metadata map[string]any

	Parents []ChunkRecord

	// Children 同时包含：
	//
	//     普通 flat chunks
	//
	// 或：
	//
	//     Parent-Child 中真正参与检索的 children。
	Children []ChunkRecord
}

// IngestionStore 是 Application Service 的 persistence port。
//
// PostgreSQL 只是它的一个 Adapter。
type IngestionStore interface {
	ReplaceDocument(ctx context.Context, batch IngestionBatch) error
}

type VersionedIngestionStore interface {
	ReserveDocument(context.Context, string, string) (int64, error)
}

// SearchEngine 是一个已经绑定具体 Collection 的搜索执行器。
type SearchEngine interface {
	Search(ctx context.Context, query string) (search.Response, error)
}

// SearchFactory 根据 Collection 创建 SearchEngine。
//
// 为什么不让 Application Service 直接依赖 PostgreSQL HybridRetriever？
//
// 因为应用层不应该知道：
//
//	pgvector
//	ParadeDB
//	PostgreSQL
//
// 以后换其他后端时，只需要换 Factory Adapter。
type SearchFactory interface {
	ForCollection(collectionID string) (SearchEngine, error)
}

type RequestSearchFactory interface {
	ForRequest(SearchRequest) (SearchEngine, error)
}

// Service 是当前 RAG Application Facade。
//
// 对上层只暴露两个最重要入口：
//
//	Ingest()
//	Search()
//
// HTTP Handler / CLI / Worker 不需要再自己了解：
//
//	Loader
//	Chunker
//	Parent-Child
//	PGIndexer
//	Retriever
//	Reranker
type Service struct {
	loader        document.Loader
	store         IngestionStore
	searchFactory SearchFactory
}

func NewService(loader document.Loader, store IngestionStore, searchFactory SearchFactory) *Service {
	return &Service{
		loader:        loader,
		store:         store,
		searchFactory: searchFactory,
	}
}

// IngestRequest 是单次文档导入请求。
type IngestRequest struct {
	// DocumentID is allocated by the knowledge business layer and survives
	// source path changes. Attempt can reuse a previously reserved worker job.
	DocumentID   string
	Attempt      int64
	CollectionID string

	Source document.Source

	Splitter chunker.SplitterConfig

	// ParentChild=false:
	//
	//     Document → Split()
	//
	// ParentChild=true:
	//
	//     Document → SplitParentChild()
	ParentChild bool

	ParentChunkSize int
	ChildChunkSize  int
}

// IngestDocumentResult 描述一个 Loader 输出 Document 的处理结果。
type IngestDocumentResult struct {
	Attempt    int64  `json:"attempt"`
	DocumentID string `json:"document_id"`

	ParentCount int `json:"parent_count"`

	ChildCount int `json:"child_count"`

	Diagnostics *chunker.Diagnostics `json:"diagnostics,omitempty"`
}

type IngestResponse struct {
	Documents []IngestDocumentResult `json:"documents"`

	ParentCount int `json:"parent_count"`

	ChildCount int `json:"child_count"`
}

// SearchRequest 是最终查询入口。
type SearchRequest struct {
	Mode         string
	Limit        int
	CollectionID string
	Query        string
}

// Ingest 完成：
//
//	Source
//	    ↓
//	Loader
//	    ↓
//	Markdown
//	    ↓
//	Chunk / Parent-Child
//	    ↓
//	IngestionBatch
//	    ↓
//	Store.ReplaceDocument
func (s *Service) Ingest(ctx context.Context, req IngestRequest) (IngestResponse, error) {
	if s.loader == nil {
		return IngestResponse{}, ErrMissingLoader
	}

	if s.store == nil {
		return IngestResponse{}, ErrMissingStore
	}

	req.CollectionID = strings.TrimSpace(req.CollectionID)
	if req.CollectionID == "" {
		return IngestResponse{}, ErrMissingCollectionID
	}

	if err := ctx.Err(); err != nil {
		return IngestResponse{}, err
	}
	if err := req.Splitter.Validate(); err != nil {
		return IngestResponse{}, err
	}
	if req.ParentChunkSize < 0 || req.ChildChunkSize < 0 {
		return IngestResponse{}, errors.New("rag application: negative parent/child size")
	}
	req.DocumentID = strings.TrimSpace(req.DocumentID)
	if req.Attempt < 0 {
		return IngestResponse{}, errors.New("rag application: negative ingestion attempt")
	}
	if versioned, ok := s.store.(VersionedIngestionStore); ok {
		if req.DocumentID == "" {
			return IngestResponse{}, errors.New("rag application: stable DocumentID is required for versioned ingestion")
		}
		if req.Attempt == 0 {
			attempt, err := versioned.ReserveDocument(ctx, req.CollectionID, req.DocumentID)
			if err != nil {
				return IngestResponse{}, err
			}
			req.Attempt = attempt
		}
	}

	sourceDocs, err := s.loader.Load(ctx, req.Source)
	if err != nil {
		return IngestResponse{}, fmt.Errorf("load source document: %w", err)
	}
	if req.DocumentID != "" && len(sourceDocs) > 1 {
		return IngestResponse{}, errors.New("rag application: one stable DocumentID cannot identify multiple source documents")
	}

	baseCfg := chunker.NormalizeSplitterConfig(req.Splitter)

	response := IngestResponse{
		Documents: make([]IngestDocumentResult, 0, len(sourceDocs)),
	}

	for index, sourceDoc := range sourceDocs {
		if err := ctx.Err(); err != nil {
			return IngestResponse{}, err
		}

		if sourceDoc == nil {
			continue
		}
		copy := *sourceDoc
		copy.ID = strings.TrimSpace(copy.ID)
		if req.DocumentID != "" {
			copy.ID = req.DocumentID
		}
		sourceDoc = &copy

		documentID := strings.TrimSpace(sourceDoc.ID)
		if documentID == "" {
			return IngestResponse{}, fmt.Errorf("%w: source document index %d", ErrMissingDocumentID, index)
		}

		markdown := chunker.NormalizeLineEndings(sourceDoc.Content)
		if strings.TrimSpace(markdown) == "" {
			continue
		}

		var (
			batch IngestionBatch
			diag  *chunker.Diagnostics
		)
		processConfig := struct {
			Version        string                  `json:"version"`
			Splitter       chunker.SplitterConfig  `json:"splitter"`
			ParentChild    bool                    `json:"parent_child"`
			ParentSplitter *chunker.SplitterConfig `json:"parent_splitter,omitempty"`
			ChildSplitter  *chunker.SplitterConfig `json:"child_splitter,omitempty"`
		}{Version: "humbert-rag-v2", Splitter: baseCfg, ParentChild: req.ParentChild}

		if req.ParentChild {
			parentCfg, childCfg := chunker.DeriveParentChildConfigs(
				baseCfg,
				req.ParentChunkSize,
				req.ChildChunkSize,
			)
			processConfig.ParentSplitter, processConfig.ChildSplitter = &parentCfg, &childCfg

			parentChild, diagnostics := chunker.SplitParentChildWithDiagnostics(markdown, parentCfg, childCfg)
			diag = diagnostics

			batch = buildParentChildBatch(
				req.CollectionID,
				sourceDoc,
				markdown,
				parentChild,
			)
		} else {
			chunks, diagnostics := chunker.SplitWithDiagnostics(markdown, baseCfg)
			diag = diagnostics

			batch = buildFlatBatch(
				req.CollectionID,
				sourceDoc,
				markdown,
				chunks,
			)
		}

		batch.Attempt = req.Attempt
		batch.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(markdown)))
		batch.ProcessConfig, err = json.Marshal(processConfig)
		if err != nil {
			return IngestResponse{}, fmt.Errorf("encode effective process config: %w", err)
		}
		if err := s.store.ReplaceDocument(ctx, batch); err != nil {
			return IngestResponse{}, fmt.Errorf("store document %q: %w", documentID, err)
		}

		item := IngestDocumentResult{
			Attempt:     req.Attempt,
			DocumentID:  documentID,
			ParentCount: len(batch.Parents),
			ChildCount:  len(batch.Children),
			Diagnostics: diag,
		}

		response.Documents = append(response.Documents, item)
		response.ParentCount += item.ParentCount
		response.ChildCount += item.ChildCount
	}

	return response, nil
}

// Search 是最终应用级搜索入口。
func (s *Service) Search(ctx context.Context, req SearchRequest) (search.Response, error) {
	if err := ctx.Err(); err != nil {
		return search.Response{}, err
	}
	if req.Limit < 0 || req.Limit > 200 {
		return search.Response{}, errors.New("rag application: result limit must be 0..200")
	}
	switch req.Mode {
	case "", "hybrid", "semantic", "keyword":
	default:
		return search.Response{}, errors.New("rag application: unknown search mode")
	}
	if s.searchFactory == nil {
		return search.Response{}, ErrMissingSearchFactory
	}

	collectionID := strings.TrimSpace(req.CollectionID)
	if collectionID == "" {
		return search.Response{}, ErrMissingCollectionID
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		return search.Response{}, ErrEmptyQuery
	}

	var engine SearchEngine
	var err error
	if factory, ok := s.searchFactory.(RequestSearchFactory); ok {
		req.CollectionID = collectionID
		engine, err = factory.ForRequest(req)
	} else {
		if req.Mode != "" && req.Mode != "hybrid" || req.Limit != 0 {
			return search.Response{}, errors.New("rag application: search factory does not support request options")
		}
		engine, err = s.searchFactory.ForCollection(collectionID)
	}
	if err != nil {
		return search.Response{}, fmt.Errorf("create search engine for collection %q: %w", collectionID, err)
	}

	result, err := engine.Search(ctx, query)
	if err != nil {
		return search.Response{}, fmt.Errorf("search collection %q: %w", collectionID, err)
	}

	return result, nil
}

// buildFlatBatch 将普通 Split() 结果转换成持久化模型。
func buildFlatBatch(
	collectionID string,
	source *schema.Document,
	markdown string,
	chunks []chunker.Chunk,
) IngestionBatch {
	metadata := cloneMetadata(source.MetaData)

	batch := IngestionBatch{
		CollectionID: collectionID,
		DocumentID:   source.ID,
		Title:        documentTitle(source),
		Markdown:     markdown,
		Metadata:     metadata,
		Children:     make([]ChunkRecord, 0, len(chunks)),
	}

	for _, chunk := range chunks {
		chunkID := fmt.Sprintf("%s#chunk-%06d", source.ID, chunk.Seq)

		batch.Children = append(batch.Children, buildChunkRecord(
			source,
			chunk,
			chunkID,
			ChunkTypeText,
			"",
		))
	}

	return batch
}

// buildParentChildBatch 把 ParentChildResult 转换成：
//
//	Parents
//	    → 只保存
//
//	Children
//	    → 保存 + retrieval index
//
// ParentIndex 在这里正式从内存下标转换成稳定 ParentChunkID。
func buildParentChildBatch(
	collectionID string,
	source *schema.Document,
	markdown string,
	result chunker.ParentChildResult,
) IngestionBatch {
	batch := IngestionBatch{
		CollectionID: collectionID,
		DocumentID:   source.ID,
		Title:        documentTitle(source),
		Markdown:     markdown,
		Metadata:     cloneMetadata(source.MetaData),
		Parents:      make([]ChunkRecord, 0, len(result.Parents)),
		Children:     make([]ChunkRecord, 0, len(result.Children)),
	}

	parentIDs := make([]string, len(result.Parents))

	for i, parent := range result.Parents {
		parentID := fmt.Sprintf("%s#parent-%06d", source.ID, parent.Seq)
		parentIDs[i] = parentID

		batch.Parents = append(batch.Parents, buildChunkRecord(
			source,
			parent,
			parentID,
			ChunkTypeParentText,
			"",
		))
	}

	for _, child := range result.Children {
		parentID := ""

		if child.ParentIndex >= 0 && child.ParentIndex < len(parentIDs) {
			parentID = parentIDs[child.ParentIndex]
		}

		childID := fmt.Sprintf("%s#chunk-%06d", source.ID, child.Seq)

		batch.Children = append(batch.Children, buildChunkRecord(
			source,
			child.Chunk,
			childID,
			ChunkTypeText,
			parentID,
		))
	}

	return batch
}

func buildChunkRecord(
	source *schema.Document,
	chunk chunker.Chunk,
	id string,
	chunkType string,
	parentChunkID string,
) ChunkRecord {
	metadata := cloneMetadata(source.MetaData)

	metadata[MetaSourceDocumentID] = source.ID
	metadata[MetaChunkIndex] = chunk.Seq
	metadata[MetaChunkStart] = chunk.Start
	metadata[MetaChunkEnd] = chunk.End
	metadata[MetaContextHeader] = chunk.ContextHeader
	metadata[MetaChunkType] = chunkType

	if parentChunkID != "" {
		metadata[MetaParentChunkID] = parentChunkID
	}

	return ChunkRecord{
		ID:            id,
		DocumentID:    source.ID,
		ChunkType:     chunkType,
		ChunkIndex:    chunk.Seq,
		Content:       chunk.Content,
		ContextHeader: chunk.ContextHeader,
		StartRune:     chunk.Start,
		EndRune:       chunk.End,
		ParentChunkID: parentChunkID,
		Metadata:      metadata,
	}
}

func documentTitle(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	for _, key := range []string{"title", "_title", "file_name"} {
		value, ok := doc.MetaData[key].(string)

		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return doc.ID
}

func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return make(map[string]any)
	}

	dst := make(map[string]any, len(src)+8)

	for key, value := range src {
		dst[key] = value
	}

	return dst
}
