package rag

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
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

// Ingest 完成解析、切分与索引。业务调用不能覆盖索引范围或 Embedding 模型。
func (s *Service) Ingest(ctx context.Context, req IngestRequest) (IngestResponse, error) {
	return s.ingest(ctx, req, false)
}

// RetryDocument 用托管原件重新执行失败/取消的导入，沿用稳定 ID 并预留新版本。
// req 只提供范围与切分参数，忽略 Source；不会自动续传中间块或反复调用模型。
func (s *Service) RetryDocument(ctx context.Context, req IngestRequest) (IngestResponse, error) {
	return s.ingest(ctx, req, true)
}

func (s *Service) ingest(ctx context.Context, req IngestRequest, retry bool) (response IngestResponse, err error) {
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
	ctx, finish, err := s.beginIngest(ctx, req)
	if err != nil {
		return IngestResponse{}, err
	}
	defer finish()
	var saved *SourceFile
	if retry {
		if s.documents == nil || s.sources == nil || req.DocumentID == "" {
			return IngestResponse{}, errors.New("rag: 重试需要文档 ID、状态存储和托管原件")
		}
		if _, err := s.documents.RecoverDocuments(ctx, req.CollectionID); err != nil {
			return IngestResponse{}, err
		}
		record, err := s.documents.GetDocument(ctx, req.CollectionID, req.DocumentID)
		if err != nil {
			return IngestResponse{}, err
		}
		if record.ParseStatus != DocumentFailed && record.ParseStatus != DocumentCancelled && !hasFailedImages(record.Metadata) {
			return IngestResponse{}, errors.New("rag: 只能重试已失败或已取消的导入")
		}
		file := record.SourceFile
		if pending, ok := record.Metadata["rag_pending_source"]; ok {
			// 缺失或损坏的新原件不能回退为旧版本原件，否则重试会导入错误内容。
			file = SourceFile{}
			data, err := json.Marshal(pending)
			if err != nil {
				return IngestResponse{}, err
			}
			if err := json.Unmarshal(data, &file); err != nil {
				return IngestResponse{}, err
			}
		}
		req.Source, err = s.sources.Resolve(ctx, file)
		if err != nil {
			return IngestResponse{}, err
		}
		saved = &file
	}
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
	source := req.Source
	file := SourceFile{URI: source.URI, Name: filepath.Base(source.URI), Type: strings.TrimPrefix(strings.ToLower(filepath.Ext(source.URI)), ".")}
	if saved != nil {
		file = *saved
	}
	stage := StageSource
	defer func() {
		if recover() != nil {
			// 在业务任务边界收尾，不向持久化或前端泄露 panic 参数、正文或调用栈。
			err = errors.New("rag: 导入组件发生内部异常")
		}
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), context.Cause(ctx), err)
		}
		var staged *IngestionError
		if errors.As(err, &staged) {
			stage = staged.Stage
		} else {
			err = &IngestionError{Stage: stage, Err: err}
		}
		if s.documents == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), ragconfig.CleanupTimeout)
		defer cancel()
		status, code := DocumentFailed, "processing_failed"
		if errors.Is(err, context.Canceled) {
			status, code = DocumentCancelled, "cancelled"
			if errors.Is(err, ErrServiceClosed) {
				code = "service_closed"
			}
		} else if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		// 状态清理独立于已取消请求；数据库再次校验版本，防止迟到错误覆盖新任务。
		failure := DocumentFailure{Status: status, Stage: stage, Code: code, Message: logging.SafeErrorText(err, 2048)}
		if stateErr := s.documents.FailDocument(cleanup, req.CollectionID, req.DocumentID, attempt, failure); stateErr != nil {
			err = errors.Join(err, fmt.Errorf("保存导入状态失败: %w", stateErr))
		}
	}()
	if s.documents != nil {
		if err := s.documents.StartDocument(ctx, req.CollectionID, req.DocumentID, attempt, file); err != nil {
			return IngestResponse{}, err
		}
	}
	advance := func(next string) error {
		stage = next
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.documents != nil {
			return s.documents.AdvanceDocument(ctx, req.CollectionID, req.DocumentID, attempt, next)
		}
		return nil
	}
	if s.sources != nil && saved == nil {
		source, file, err = s.sources.Save(ctx, source)
		if err != nil {
			return IngestResponse{}, err
		}
		if err := s.documents.StartDocument(ctx, req.CollectionID, req.DocumentID, attempt, file); err != nil {
			return IngestResponse{}, err
		}
	}

	if err := advance(StageParse); err != nil {
		return IngestResponse{}, err
	}
	sourceDocs, err := s.loader.Load(ctx, source)
	if err != nil {
		return IngestResponse{}, fmt.Errorf("load source document: %w", err)
	}
	if req.DocumentID != "" && len(sourceDocs) > 1 {
		return IngestResponse{}, errors.New("rag: one stable DocumentID cannot identify multiple source documents")
	}

	baseCfg := req.Splitter.Effective()
	if err := advance(StageChunk); err != nil {
		return IngestResponse{}, err
	}

	response = IngestResponse{Documents: make([]IngestDocumentResult, 0, len(sourceDocs))}

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
		if file.Path != "" {
			// Loader 读取的是内容摘要命名的副本，展示名和来源仍使用用户提交的原件信息。
			sourceDoc.MetaData = retrieval.CloneMetadata(sourceDoc.MetaData)
			sourceDoc.MetaData[MetaSourceURI], sourceDoc.MetaData[MetaFileName] = file.URI, file.Name
			sourceDoc.MetaData[MetaFileExt], sourceDoc.MetaData[MetaFileSize] = file.Type, file.Size
			sourceDoc.MetaData["rag_source_content_hash"] = file.Hash
			savedTitle := strings.TrimSuffix(filepath.Base(source.URI), filepath.Ext(source.URI))
			if title, _ := sourceDoc.MetaData[MetaTitle].(string); title == "" || title == savedTitle {
				sourceDoc.MetaData[MetaTitle] = strings.TrimSuffix(file.Name, filepath.Ext(file.Name))
			}
		}

		documentID := strings.TrimSpace(sourceDoc.ID)
		if documentID == "" {
			return IngestResponse{}, fmt.Errorf("%w: source document index %d", ErrMissingDocumentID, index)
		}

		markdown := chunker.NormalizeLineEndings(sourceDoc.Content)
		if strings.TrimSpace(markdown) == "" {
			continue
		}
		if len(markdown) > s.config.Ingest.MaxTextBytes {
			return IngestResponse{}, fmt.Errorf("%w: document text exceeds %d bytes", ErrIngestLimit, s.config.Ingest.MaxTextBytes)
		}

		var (
			batch IngestionBatch
			diag  *chunker.Diagnostics
		)
		layout, layoutErr := chunker.NewLayout(ctx, markdown)
		if layoutErr != nil {
			return IngestResponse{}, layoutErr
		}
		processConfig := struct {
			Version        string `json:"version"`
			MaxInputTokens int    `json:"max_input_tokens"`
			MaxBatchTokens int    `json:"max_batch_tokens"`
			// Splitter 普通切分或父子切分使用的基础参数。
			Splitter       ragconfig.Splitter  `json:"splitter"`
			ParentChild    bool                `json:"parent_child"`
			ParentSplitter *ragconfig.Splitter `json:"parent_splitter,omitempty"`
			ChildSplitter  *ragconfig.Splitter `json:"child_splitter,omitempty"`
		}{Version: "humbert-rag-v10-structures", Splitter: baseCfg, ParentChild: req.ParentChild,
			MaxInputTokens: s.config.InputBudget.Effective().MaxInputTokens, MaxBatchTokens: s.config.InputBudget.Effective().MaxBatchTokens}

		if req.ParentChild {
			parentCfg, childCfg := baseCfg.ForParentChild(req.ParentChunkSize, req.ChildChunkSize)
			processConfig.ParentSplitter, processConfig.ChildSplitter = &parentCfg, &childCfg

			parentChild, diagnostics, splitErr := layout.SplitParentChild(ctx, parentCfg, childCfg, s.config.Ingest.MaxChunks)
			if splitErr != nil {
				if errors.Is(splitErr, chunker.ErrChunkLimit) {
					return IngestResponse{}, fmt.Errorf("%w: more than %d chunks", ErrIngestLimit, s.config.Ingest.MaxChunks)
				}
				return IngestResponse{}, splitErr
			}
			diag = diagnostics

			batch = buildParentChildBatch(req.CollectionID, sourceDoc, markdown, parentChild)
		} else {
			chunks, diagnostics, splitErr := layout.Split(ctx, baseCfg, s.config.Ingest.MaxChunks)
			if splitErr != nil {
				if errors.Is(splitErr, chunker.ErrChunkLimit) {
					return IngestResponse{}, fmt.Errorf("%w: more than %d chunks", ErrIngestLimit, s.config.Ingest.MaxChunks)
				}
				return IngestResponse{}, splitErr
			}
			diag = diagnostics

			batch = buildFlatBatch(req.CollectionID, sourceDoc, markdown, chunks)
		}
		if err := ctx.Err(); err != nil {
			return IngestResponse{}, err
		}
		if err := ValidateIngestionBatch(batch); err != nil {
			return IngestResponse{}, err
		}
		if err := s.fitInputs(ctx, &batch, layout); err != nil {
			return IngestResponse{}, err
		}
		if assets, assetErr := imageAssets(batch.Metadata); assetErr != nil {
			return IngestResponse{}, assetErr
		} else if len(assets) > 0 {
			if err := advance(StageImages); err != nil {
				return IngestResponse{}, err
			}
			if err := s.enrichImages(ctx, &batch, req, attempt); err != nil {
				return IngestResponse{}, err
			}
		}

		batch.Attempt = attempt
		batch.Source = file
		if attempt > 0 {
			qualifyChunkIDs(&batch)
		}
		batch.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(batch.Markdown)))
		batch.ProcessConfig, err = json.Marshal(processConfig)
		if err != nil {
			return IngestResponse{}, fmt.Errorf("encode effective process config: %w", err)
		}
		if err := ValidateIngestionBatch(batch); err != nil {
			return IngestResponse{}, err
		}
		if err := advance(StageIndex); err != nil {
			return IngestResponse{}, err
		}
		ids, warnings, err := s.storeBatch(ctx, batch)
		if err != nil {
			return IngestResponse{}, fmt.Errorf("store document %q: %w", documentID, err)
		}

		if parseWarnings, ok := sourceDoc.MetaData[MetaParserWarnings].([]string); ok {
			warnings = append(parseWarnings, warnings...)
		}
		images, _ := imageAssets(batch.Metadata)
		warnings = append(warnings, imageWarnings(images)...)
		item := IngestDocumentResult{
			Attempt:     attempt,
			DocumentID:  documentID,
			ParentCount: len(batch.Parents),
			ChildCount:  len(batch.Children),
			ChunkIDs:    ids,
			Diagnostics: diag,
			Warnings:    warnings,
			Images:      images,
		}

		response.Documents = append(response.Documents, item)
		response.ParentCount += item.ParentCount
		response.ChildCount += item.ChildCount
	}

	if len(response.Documents) == 0 {
		return IngestResponse{}, &IngestionError{Stage: StageParse, Err: ErrEmptyDocument}
	}
	return response, nil
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

// newIngestionBatch 统一两种切分策略的文档身份与元数据，避免来源处理逻辑分叉。
// 全文位置表只留在本次导入内存中；落库时每个分块仅保存与自身范围相关的出处。
func newIngestionBatch(collectionID string, source *schema.Document, markdown string, childCount int) IngestionBatch {
	metadata := retrieval.CloneMetadata(source.MetaData)
	delete(metadata, documenttext.MetaSourceBlocks)
	blocks, _ := source.MetaData[documenttext.MetaSourceBlocks].([]documenttext.SourceBlock)
	return IngestionBatch{
		CollectionID: collectionID,
		DocumentID:   source.ID,
		Title:        documentTitle(source),
		Markdown:     markdown,
		Metadata:     metadata,
		SourceBlocks: blocks,
		Children:     make([]ChunkRecord, 0, childCount),
	}
}

// buildFlatBatch 将普通切分结果转换成持久化模型。
func buildFlatBatch(collectionID string, source *schema.Document, markdown string, chunks []chunker.Chunk) IngestionBatch {
	batch := newIngestionBatch(collectionID, source, markdown, len(chunks))
	for _, chunk := range chunks {
		chunkID := fmt.Sprintf("%s#chunk-%06d", source.ID, chunk.Seq)

		batch.Children = append(batch.Children, buildChunkRecord(source, chunk, chunkID, ChunkTypeText, ""))
	}

	return batch
}

// buildParentChildBatch 转换父子切分结果，保存父块身份与子块的关联关系。
func buildParentChildBatch(collectionID string, source *schema.Document, markdown string, result chunker.ParentChildResult) IngestionBatch {
	batch := newIngestionBatch(collectionID, source, markdown, len(result.Children))
	batch.Parents = make([]ChunkRecord, 0, len(result.Parents))

	parentIDs := make([]string, len(result.Parents))

	for i, parent := range result.Parents {
		parentID := fmt.Sprintf("%s#parent-%06d", source.ID, parent.Seq)
		parentIDs[i] = parentID

		batch.Parents = append(batch.Parents, buildChunkRecord(source, parent, parentID, ChunkTypeParentText, ""))
	}

	for _, child := range result.Children {
		parentID := ""

		if child.ParentIndex >= 0 && child.ParentIndex < len(parentIDs) {
			parentID = parentIDs[child.ParentIndex]
		}

		childID := fmt.Sprintf("%s#chunk-%06d", source.ID, child.Seq)

		batch.Children = append(batch.Children, buildChunkRecord(source, child.Chunk, childID, ChunkTypeText, parentID))
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
	delete(metadata, "rag_image_assets")
	delete(metadata, "rag_image_recognition")
	delete(metadata, "rag_parse_diagnostics")
	delete(metadata, "rag_parse_config")
	delete(metadata, MetaParserWarnings)
	documenttext.AttachLocators(metadata, chunk.Start, chunk.End)

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

	if title := retrieval.Title(ragconfig.SearchText{}, doc); title != "" {
		return title
	}

	return doc.ID
}
