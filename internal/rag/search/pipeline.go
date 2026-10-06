package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

var (
	ErrNilRetriever = errors.New("rag search: nil retriever")
	ErrEmptyQuery   = errors.New("rag search: empty query")
)

// RetrieveFunc 是精排和引用处理的内部输入；外部 Eino 组件通过 NewEinoPipeline 接入。
type RetrieveFunc func(ctx context.Context, query string) ([]retrieval.SearchResult, error)

// ParentLoader 负责批量加载 Parent Chunk。
//
// PostgreSQL 实现放在：
//
//	retriever/postgres/context_loader.go
//
// 以后换存储后，只需要换实现。
type ParentLoader interface {
	LoadParents(ctx context.Context, parentChunkIDs []string) (map[string]retrieval.SearchResult, error)
}

// Config 控制检索超时、最终数量与父块扩展策略。
type Config struct {
	// Timeout 检索请求超时，零值由业务入口设置默认值。
	Timeout time.Duration
	// FinalTopK 是父块扩展与分组后最多返回的结果数量。
	FinalTopK int

	// ExpandParents 开启父块上下文扩展，命中子块证据仍独立保留。
	ExpandParents bool

	// CollapseSameParent 将同一父块的结果合并，保留所有命中子块证据，再限制最终数量。
	CollapseSameParent bool

	// AllowParentFallback 允许父块读取的普通错误退回子块正文；取消和超时始终返回。
	AllowParentFallback bool
}

// DefaultConfig 默认返回五条结果，并在提供父块读取器时扩展上下文。
func DefaultConfig() Config {
	return Config{
		FinalTopK:     5,
		ExpandParents: true,
	}
}

// Response 不只返回最终结果，
// 也保留 Rerank Diagnostics。
//
// 这样模型失败、阈值降级、全部拒绝等行为
// 都不会被 Pipeline 静默吞掉。
type Response struct {
	Retrieval retrieval.Diagnostics    `json:"retrieval"`
	Results   []retrieval.SearchResult `json:"results"`

	Rerank      rerank.Diagnostics `json:"rerank"`
	Diagnostics []Diagnostic       `json:"diagnostics,omitempty"`
}

// Diagnostic 流程阶段与可恢复问题的说明。
type Diagnostic struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// Pipeline 召回、可选精排与父块扩展组成的检索流程。
type Pipeline struct {
	retrieve            RetrieveFunc
	retrieveDiagnostics func(context.Context, string) ([]retrieval.SearchResult, retrieval.Diagnostics, error)
	reranker            *rerank.Engine
	parentLoader        ParentLoader
	config              Config
}

// NewPipeline 校验配置并组合检索函数、精排引擎与父块读取器。
func NewPipeline(
	retrieve RetrieveFunc,
	reranker *rerank.Engine,
	parentLoader ParentLoader,
	cfg Config,
) (*Pipeline, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if retrieve == nil {
		return nil, ErrNilRetriever
	}

	if cfg.FinalTopK <= 0 {
		if cfg.FinalTopK < 0 {
			return nil, fmt.Errorf("rag search: final top k must be non-negative")
		}
		cfg.FinalTopK = DefaultConfig().FinalTopK
	}

	return &Pipeline{
		retrieve:     retrieve,
		reranker:     reranker,
		parentLoader: parentLoader,
		config:       cfg,
	}, nil
}

// Validate 拒绝负数结果数量和超时。
func (c Config) Validate() error {
	if c.FinalTopK < 0 || c.Timeout < 0 {
		return fmt.Errorf("rag search: negative result limit or timeout")
	}
	return nil
}

// NewPipelineWithDiagnostics 接收带通道诊断的检索函数，保留失败降级信息。
func NewPipelineWithDiagnostics(retrieve func(context.Context, string) ([]retrieval.SearchResult, retrieval.Diagnostics, error), reranker *rerank.Engine, parents ParentLoader, cfg Config) (*Pipeline, error) {
	if retrieve == nil {
		return nil, ErrNilRetriever
	}
	p, err := NewPipeline(func(ctx context.Context, q string) ([]retrieval.SearchResult, error) {
		results, _, err := retrieve(ctx, q)
		return results, err
	}, reranker, parents, cfg)
	if err == nil {
		p.retrieveDiagnostics = retrieve
	}
	return p, err
}

// Search 执行召回、可选精排、父块扩展与最终选择；当前统一流程不执行 MMR 或问题改写。
func (p *Pipeline) Search(ctx context.Context, query string) (Response, error) {
	timeout := p.config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	query = strings.TrimSpace(query)

	if query == "" {
		return Response{}, ErrEmptyQuery
	}

	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	var candidates []retrieval.SearchResult
	var diagnostics retrieval.Diagnostics
	var err error
	if p.retrieveDiagnostics != nil {
		candidates, diagnostics, err = p.retrieveDiagnostics(ctx, query)
	} else {
		candidates, err = p.retrieve(ctx, query)
	}
	if err != nil {
		return Response{}, fmt.Errorf("retrieve candidates: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	if len(candidates) == 0 {
		return Response{
			Retrieval: diagnostics,
			Rerank: rerank.Diagnostics{
				Outcome: rerank.OutcomeNoCandidates,
			},
		}, nil
	}

	var response Response
	response.Retrieval = diagnostics

	if p.reranker != nil {
		reranked, err := p.reranker.RerankCandidates(ctx, query, candidates)
		if err != nil {
			return Response{}, fmt.Errorf("rerank candidates: %w", err)
		}

		response.Results = reranked.Results
		response.Rerank = reranked.Diagnostics
	} else {
		response.Results = slices.Clone(candidates)

		response.Rerank = rerank.Diagnostics{
			Applied:        false,
			Outcome:        rerank.OutcomeNoModel,
			CandidateCount: len(candidates),
		}
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	// 即使没有 Parent Expansion，
	// 也初始化 Effective Context。
	//
	// 这样下游永远可以简单调用：
	//
	//     result.EffectiveContent()
	for i := range response.Results {
		response.Results[i].ContextChunkID = response.Results[i].ChunkID
		response.Results[i].ContextContent = response.Results[i].Content
		response.Results[i].ContextStartRune = response.Results[i].StartRune
		response.Results[i].ContextEndRune = response.Results[i].EndRune
		response.Results[i].ContextSourceHeader = response.Results[i].ContextHeader
	}

	if !p.config.ExpandParents ||
		p.parentLoader == nil ||
		len(response.Results) == 0 {

		return p.finalize(response), nil
	}

	parentIDs := uniqueParentIDs(response.Results)

	if len(parentIDs) == 0 {
		return p.finalize(response), nil
	}

	parents, err := p.parentLoader.LoadParents(ctx, parentIDs)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if p.config.AllowParentFallback && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			response.Diagnostics = append(response.Diagnostics, Diagnostic{Stage: "parent_expansion", Message: logging.SafeErrorText(err, 2048)})
			return p.finalize(response), nil
		}
		return Response{}, fmt.Errorf("load parent context: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	for i := range response.Results {
		parentID := response.Results[i].ParentChunkID

		if parentID == "" {
			continue
		}

		parent, ok := parents[parentID]
		if !ok {
			// 父块缺失时保留合法子块证据，不丢弃整条召回结果。
			continue
		}

		child := response.Results[i]
		if (parent.DocumentID != "" && parent.DocumentID != child.DocumentID) ||
			(parent.CollectionID != "" && parent.CollectionID != child.CollectionID) ||
			parent.DocumentRevision != child.DocumentRevision {
			response.Diagnostics = append(response.Diagnostics, Diagnostic{Stage: "parent_revision", Message: "parent changed after retrieval; retained child evidence"})
			continue
		}
		response.Results[i].ContextChunkID = parent.ChunkID
		response.Results[i].ContextContent = parent.Content
		response.Results[i].ContextStartRune = parent.StartRune
		response.Results[i].ContextEndRune = parent.EndRune
		response.Results[i].ContextSourceHeader = parent.ContextHeader
	}
	return p.finalize(response), nil
}

// finalize 按需合并父块并统一截断最终结果，保留子块命中证据。
func (p *Pipeline) finalize(response Response) Response {
	if p.config.CollapseSameParent {
		response.Results = collapseSameContext(response.Results)
	}

	if p.config.FinalTopK > 0 &&
		len(response.Results) > p.config.FinalTopK {

		response.Results = response.Results[:p.config.FinalTopK]
	}

	response.Rerank.ResultCount = len(response.Results)

	return response
}

// uniqueParentIDs 收集不重复的父块 ID，供批量读取。
func uniqueParentIDs(results []retrieval.SearchResult) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0)

	for _, result := range results {
		if result.ParentChunkID == "" {
			continue
		}

		if _, exists := seen[result.ParentChunkID]; exists {
			continue
		}

		seen[result.ParentChunkID] = struct{}{}
		ids = append(ids, result.ParentChunkID)
	}

	return ids
}

// collapseSameContext 按最终上下文身份分组，聚合全部命中子块的引用证据。
func collapseSameContext(results []retrieval.SearchResult) []retrieval.SearchResult {
	if len(results) <= 1 {
		return results
	}

	seen := make(map[string]int, len(results))
	out := make([]retrieval.SearchResult, 0, len(results))

	for _, result := range results {
		contextIdentity := result
		contextIdentity.ChunkID = result.EffectiveChunkID()
		key := contextIdentity.IdentityKey()
		evidence := result.Evidence
		if len(evidence) == 0 {
			evidence = []retrieval.HitEvidence{{ChunkID: result.ChunkID, StartRune: result.StartRune, EndRune: result.EndRune, Content: result.Content, Score: result.Score}}
		}

		if index, exists := seen[key]; exists {
			out[index].Evidence = append(out[index].Evidence, evidence...)
			continue
		}

		seen[key] = len(out)
		result.Evidence = append([]retrieval.HitEvidence(nil), evidence...)
		out = append(out, result)
	}

	return out
}
