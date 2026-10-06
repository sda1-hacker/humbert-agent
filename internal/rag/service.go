package rag

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

// 分块类型区分用于检索的正文块与用于补充上下文的父块。
const (
	ChunkTypeText       = "text"
	ChunkTypeParentText = "parent_text"
)

var (
	ErrMissingLoader       = errors.New("rag: missing document loader")
	ErrMissingIndexer      = errors.New("rag: missing Eino indexer")
	ErrMissingRetriever    = errors.New("rag: missing Eino retriever")
	ErrMissingCollectionID = errors.New("rag: missing collection id")
	ErrMissingDocumentID   = errors.New("rag: loader returned document without id")
	ErrEmptyQuery          = errors.New("rag: empty search query")
	ErrEmptyDocument       = errors.New("rag: document has no usable text")
)

// ChunkRecord 保存权威正文和原文坐标；它不承担检索库接口的职责。
type ChunkRecord struct {
	// ID 分块的唯一身份，版本化导入时包含文档版本。
	ID string
	// DocumentID 所属文档的稳定 ID。
	DocumentID string
	// ChunkType 普通正文块或父块类型。
	ChunkType string
	// ChunkIndex 同类分块在文档中的顺序编号。
	ChunkIndex int
	// Content 权威分块正文，可能包含补充表头。
	Content string
	// ContextHeader 标题路径，用于增强检索语义。
	ContextHeader string
	// StartRune 归一化解析正文中的字符区间，左闭右开；补充表头不占新原文范围。
	StartRune, EndRune int
	// ParentChunkID 对应父块 ID；普通块或无需父块时为空。
	ParentChunkID string
	// Metadata 来源与业务元数据，顶层副本可独立修改。
	Metadata map[string]any
}

// IngestionBatch 是一个完整文档版本，由原子 Indexer 或独立 Publisher 保存。
// Markdown 必须来自 Loader，不能通过拼接可能重叠的分块重建。
type IngestionBatch struct {
	// Attempt 解析前预留的文档版本；非版本化导入为零。
	Attempt int64
	// ContentHash 完整归一化正文的 SHA-256 摘要。
	ContentHash string
	// ProcessConfig 切分参数快照，供追溯与重新导入使用。
	ProcessConfig json.RawMessage
	// CollectionID 是当前版本所属的知识库。
	CollectionID string
	// DocumentID 是业务分配的稳定文档 ID。
	DocumentID string
	// Title 是用于检索和展示的文档标题。
	Title string
	// Markdown 是 Loader 输出并统一换行后的完整正文。
	Markdown string
	// Metadata 来源与业务元数据，顶层副本可独立修改。
	Metadata map[string]any
	// Parents 仅保存上下文，不进入向量或关键词索引。
	Parents []ChunkRecord
	// Children 是真正参与检索的分块。
	Children []ChunkRecord
}

// DocumentVersioner 是 Eino 未提供的业务能力：解析前预留文档版本。
type DocumentVersioner interface {
	ReserveDocument(context.Context, string, string) (int64, error)
}

// Config 直接接受 Eino 组件。Retriever 是默认检索器，可以是下面的两路混合结果，
// 也可以直接注入 eino-ext 的任意 Retriever；单路组件只在需要对应搜索模式时提供。
type Config struct {
	// Retriever 默认检索器，通常组合向量和关键词召回。
	Retriever retriever.Retriever
	// VectorRetriever 语义检索模式使用的原生 Eino 检索器。
	VectorRetriever retriever.Retriever
	// KeywordRetriever 关键词模式使用的原生 Eino 检索器。
	KeywordRetriever retriever.Retriever
	// RecallTopK 进入精排和父块扩展前的候选数量。
	RecallTopK int
	// Search 最终结果数量、超时和父块扩展策略。
	Search search.Config
	// Reranker 可选精排引擎，未提供时保留召回排序。
	Reranker *rerank.Engine
	// InputBuilder 向量和关键词共用的检索文本构造规则。
	InputBuilder searchcontent.Builder
	// ParentLoader 根据知识库创建父块读取器。
	ParentLoader func(string) (search.ParentLoader, error)
	// BeforeSearch 检索前执行的可选检查，例如验证模型档案。
	BeforeSearch func(context.Context, string) error
}

// IngestRequest 是单次文档导入请求。
type IngestRequest struct {
	// DocumentID 由业务层分配，不随文件路径改变；版本号由服务自动预留。
	DocumentID string
	// CollectionID 知识库范围，导入和检索都必须明确提供。
	CollectionID string

	// Source 本次导入的 Eino 文档来源。
	Source document.Source

	// Splitter 普通切分或父子切分使用的基础参数。
	Splitter chunker.SplitterConfig

	// ParentChild 指定是否先切父块再切子块；关闭时直接生成普通检索块。
	ParentChild bool

	// ParentChunkSize 父块目标字符数，零值使用默认值。
	ParentChunkSize int
	// ChildChunkSize 子块目标字符数，零值使用默认值。
	ChildChunkSize int
}

// IngestDocumentResult 描述一个 Loader 输出 Document 的处理结果。
type IngestDocumentResult struct {
	// Attempt 解析前预留的文档版本；非版本化导入为零。
	Attempt int64 `json:"attempt"`
	// DocumentID 所属文档的稳定 ID。
	DocumentID string `json:"document_id"`

	ParentCount int `json:"parent_count"`

	ChildCount int      `json:"child_count"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`

	Diagnostics *chunker.Diagnostics `json:"diagnostics,omitempty"`
}

// IngestResponse 各文档的导入结果及父子块总数。
type IngestResponse struct {
	Documents []IngestDocumentResult `json:"documents"`

	ParentCount int `json:"parent_count"`

	ChildCount int `json:"child_count"`
}

// SearchRequest 是最终查询入口。
type SearchRequest struct {
	// Mode hybrid、semantic 或 keyword，空值使用默认检索器。
	Mode string
	// Limit 最终返回数量，零值使用服务配置。
	Limit int
	// CollectionID 知识库范围，导入和检索都必须明确提供。
	CollectionID string
	// Query 用户的原始检索问题，当前流程不改写。
	Query string
}

// Ingest 完成解析、切分与索引。业务调用不能覆盖索引范围或 Embedding 模型。
func (s *Service) Ingest(ctx context.Context, req IngestRequest) (IngestResponse, error) {
	if s.loader == nil {
		return IngestResponse{}, ErrMissingLoader
	}

	if s.indexer == nil {
		return IngestResponse{}, ErrMissingIndexer
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
		return IngestResponse{}, errors.New("rag: negative parent/child size")
	}
	req.DocumentID = strings.TrimSpace(req.DocumentID)
	var attempt int64
	if s.lifecycle != nil {
		if req.DocumentID == "" {
			return IngestResponse{}, errors.New("rag: stable DocumentID is required for versioned ingestion")
		}
		var err error
		attempt, err = s.lifecycle.ReserveDocument(ctx, req.CollectionID, req.DocumentID)
		if err != nil {
			return IngestResponse{}, err
		}
		if attempt <= 0 {
			return IngestResponse{}, errors.New("rag: document lifecycle returned invalid version")
		}
	}

	sourceDocs, err := s.loader.Load(ctx, req.Source)
	if err != nil {
		return IngestResponse{}, fmt.Errorf("load source document: %w", err)
	}
	if req.DocumentID != "" && len(sourceDocs) > 1 {
		return IngestResponse{}, errors.New("rag: one stable DocumentID cannot identify multiple source documents")
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
			Version string `json:"version"`
			// Splitter 普通切分或父子切分使用的基础参数。
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

		batch.Attempt = attempt
		if attempt > 0 {
			qualifyChunkIDs(&batch)
		}
		batch.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(markdown)))
		batch.ProcessConfig, err = json.Marshal(processConfig)
		if err != nil {
			return IngestResponse{}, fmt.Errorf("encode effective process config: %w", err)
		}
		if err := ValidateIngestionBatch(batch); err != nil {
			return IngestResponse{}, err
		}
		docs := IndexDocuments(batch, s.config.InputBuilder)
		ids, err := s.indexer.Store(ctx, docs, indexer.WithIndex(req.CollectionID), WithIngestionBatch(batch))
		if err != nil {
			return IngestResponse{}, fmt.Errorf("store document %q: %w", documentID, err)
		}
		if err := ctx.Err(); err != nil {
			return IngestResponse{}, err
		}
		if s.publisher != nil {
			// 发布依赖明确的分块身份，不能允许后端擅自改 ID。
			if err := validateStoredIDs(batch.Children, ids); err != nil {
				return IngestResponse{}, err
			}
			if err := s.publisher.PublishDocument(ctx, batch); err != nil {
				return IngestResponse{}, fmt.Errorf("publish document %q: %w", documentID, err)
			}
		}

		item := IngestDocumentResult{
			Attempt:     attempt,
			DocumentID:  documentID,
			ParentCount: len(batch.Parents),
			ChildCount:  len(batch.Children),
			ChunkIDs:    ids,
			Diagnostics: diag,
		}

		response.Documents = append(response.Documents, item)
		response.ParentCount += item.ParentCount
		response.ChildCount += item.ChildCount
	}

	if len(response.Documents) == 0 {
		return IngestResponse{}, ErrEmptyDocument
	}
	return response, nil
}

// Search 选择 Eino 检索器，再复用精排与父块扩展流水线。
func (s *Service) Search(ctx context.Context, req SearchRequest) (search.Response, error) {
	if err := ctx.Err(); err != nil {
		return search.Response{}, err
	}
	if req.Limit < 0 {
		return search.Response{}, errors.New("rag: negative result limit")
	}
	req.CollectionID, req.Query = strings.TrimSpace(req.CollectionID), strings.TrimSpace(req.Query)
	if req.CollectionID == "" {
		return search.Response{}, ErrMissingCollectionID
	}
	if req.Query == "" {
		return search.Response{}, ErrEmptyQuery
	}
	component := s.config.Retriever
	switch req.Mode {
	case "", "hybrid":
	case "semantic":
		component = s.config.VectorRetriever
	case "keyword":
		component = s.config.KeywordRetriever
	default:
		return search.Response{}, errors.New("rag: unknown search mode")
	}
	if component == nil {
		return search.Response{}, ErrMissingRetriever
	}
	cfg := s.config.Search
	if req.Limit > 0 {
		cfg.FinalTopK = req.Limit
	}
	if cfg.FinalTopK == 0 {
		cfg.FinalTopK = search.DefaultConfig().FinalTopK
	}
	if err := cfg.Validate(); err != nil {
		return search.Response{}, err
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if s.config.BeforeSearch != nil {
		if err := s.config.BeforeSearch(ctx, req.CollectionID); err != nil {
			return search.Response{}, err
		}
	}
	var parents search.ParentLoader
	var err error
	if cfg.ExpandParents && s.config.ParentLoader != nil {
		parents, err = s.config.ParentLoader(req.CollectionID)
		if err != nil {
			return search.Response{}, err
		}
	}
	// 业务入口只设置候选数量和知识库范围，底层模型与过滤器在组装时确定。
	recall := max(s.config.RecallTopK, cfg.FinalTopK)
	options := []retriever.Option{retriever.WithTopK(recall), retriever.WithIndex(req.CollectionID)}
	pipeline, err := search.NewEinoPipeline(component, s.config.Reranker, parents, cfg, options...)
	if err != nil {
		return search.Response{}, err
	}
	result, err := pipeline.Search(ctx, req.Query)
	if err == nil && result.Retrieval.ModeUsed == "" {
		switch req.Mode {
		case "semantic":
			result.Retrieval.ModeUsed = "vector"
		case "keyword":
			result.Retrieval.ModeUsed = "keyword"
		}
	}
	return result, err
}

// buildFlatBatch 将普通 Split() 结果转换成持久化模型。
func buildFlatBatch(
	collectionID string,
	source *schema.Document,
	markdown string,
	chunks []chunker.Chunk,
) IngestionBatch {
	metadata := retrieval.CloneMetadata(source.MetaData)

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

// buildParentChildBatch 转换父子切分结果，保存父块身份与子块的关联关系。
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
		Metadata:     retrieval.CloneMetadata(source.MetaData),
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

// buildChunkRecord 保存正文、原文坐标和来源元数据，不生成模型向量。
func buildChunkRecord(
	source *schema.Document,
	chunk chunker.Chunk,
	id string,
	chunkType string,
	parentChunkID string,
) ChunkRecord {
	metadata := retrieval.CloneMetadata(source.MetaData)

	metadata[retrieval.MetaSourceDocumentID] = source.ID
	metadata[retrieval.MetaChunkIndex] = chunk.Seq
	metadata[retrieval.MetaChunkStart] = chunk.Start
	metadata[retrieval.MetaChunkEnd] = chunk.End
	metadata[retrieval.MetaContextHeader] = chunk.ContextHeader
	metadata[retrieval.MetaChunkType] = chunkType

	if parentChunkID != "" {
		metadata[retrieval.MetaParentChunkID] = parentChunkID
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

// documentTitle 按统一标题优先级取值，缺失时使用文档 ID。
func documentTitle(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	if title := searchcontent.DefaultBuilder().Title(doc); title != "" {
		return title
	}

	return doc.ID
}
