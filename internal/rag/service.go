package rag

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/retriever"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
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
	// ID 是跨检索后端的分块业务键，对应 PG 的 chunk_key；版本化导入时包含文档版本。
	ID string
	// DocumentID 是所属文档的稳定业务键，对应 PG 的 source_key，数据库外键另用 BIGINT。
	DocumentID string
	// ChunkType 区分正文、父块与图片的 OCR/描述块。
	ChunkType string
	// ChunkIndex 父块、检索子块各自编号；正文和图片共用子块序列，避免业务 ID 重复。
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

// IngestionBatch 是一个待发布版本；原子 Indexer 或 Publisher 保存元数据和分块。
// Markdown 来自 Loader，末尾可附加独立的图片识别结果，仅用于内存中的范围校验和摘要计算，不写入主表。
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
	// Markdown 是归一化正文及图片识别文本；普通正文块在识别前已固定，不把 OCR 混入其内容。
	Markdown string
	// SourceBlocks 只参与导入中的范围裁剪；文档主表与分块中均不保存全文位置列表。
	SourceBlocks []documenttext.SourceBlock `json:"-"`
	// Source 是已托管原件的信息；纯文本 Eino Store 可留空。
	Source SourceFile
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

// Config 是运行时组件装配，数值参数复用 rag/config 的定义，不在这里读取部署配置。
// Retriever 是默认检索器，可以是下面的两路混合结果，
// 也可以直接注入 eino-ext 的任意 Retriever；单路组件只在需要对应搜索模式时提供。
type Config struct {
	// Ingest 限制并发、文档规模与总时间；入库前检查，避免无界模型请求。
	Ingest ragconfig.Ingest
	// InputBudget 与实际 Embedder 共用计数规则，检查标题、路径和正文组成的完整输入。
	InputBudget ragconfig.Budget
	// Retriever 默认检索器，通常组合向量和关键词召回。
	Retriever retriever.Retriever
	// VectorRetriever 语义检索模式使用的原生 Eino 检索器。
	VectorRetriever retriever.Retriever
	// KeywordRetriever 关键词模式使用的原生 Eino 检索器。
	KeywordRetriever retriever.Retriever
	// RecallTopK 进入精排和父块扩展前的候选数量。
	RecallTopK int
	// Search 最终结果数量、超时和父块扩展策略。
	Search ragconfig.Search
	// Reranker 可选精排引擎，未提供时保留召回排序。
	Reranker *rerank.Engine
	// InputBuilder 向量和关键词共用的检索文本构造规则。
	InputBuilder ragconfig.SearchText
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
	// StoredSource 是上传队列已经保存的原件；再次校验摘要后使用，避免排队期间原文件变化。
	// 仅由业务装配层传入，不接受模型生成的文件路径。
	StoredSource *SourceFile

	// Splitter 普通切分或父子切分使用的基础参数。
	Splitter ragconfig.Splitter

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
	// Warnings 保留解析降级、未开启 OCR 和发布后清理警告；文档已发布，不能据此重跑整个向量化。
	Warnings []string `json:"warnings,omitempty"`
	// Images 返回实际图片/动作状态，混合文档部分失败时也不能显示为全部识别成功。
	Images []ImageAsset `json:"images,omitempty"`
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

// Search 选择 Eino 检索器，再复用精排与父块扩展流水线。
func (s *Service) Search(ctx context.Context, req SearchRequest) (search.Response, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return search.Response{}, ErrServiceClosed
	}
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
	cfg = cfg.Effective()
	if err := cfg.Validate(); err != nil {
		return search.Response{}, err
	}
	ctx, cancel, err := s.beginRequest(ctx, cfg.Timeout)
	if err != nil {
		return search.Response{}, err
	}
	defer cancel()

	if s.config.BeforeSearch != nil {
		if err := s.config.BeforeSearch(ctx, req.CollectionID); err != nil {
			return search.Response{}, err
		}
	}
	var parents search.ParentLoader
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
