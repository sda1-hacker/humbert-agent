package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

var (
	ErrNilRetriever = errors.New("rag search: nil retriever")
	ErrEmptyQuery   = errors.New("rag search: empty query")
)

// RetrieveFunc 是精排和引用处理的内部输入；外部 Eino 组件通过 NewEinoPipeline 接入。
type RetrieveFunc func(ctx context.Context, query string) ([]retrieval.SearchResult, error)

// ParentLoader 批量读取父块；更换检索库不影响权威上下文的读取。
type ParentLoader interface {
	LoadParents(ctx context.Context, parentChunkIDs []string) (map[string]retrieval.SearchResult, error)
}

// Response 不只返回最终结果，
// 也保留 Rerank Diagnostics。
//
// 这样模型失败、阈值降级、全部拒绝等行为
// 都不会被 Pipeline 静默吞掉。
type Response struct {
	Retrieval retrieval.Diagnostics    `json:"retrieval"`
	Results   []retrieval.SearchResult `json:"results"`

	Rerank        rerank.Diagnostics `json:"rerank"`
	Diagnostics   []Diagnostic       `json:"diagnostics,omitempty"`
	ContextTokens int                `json:"context_tokens"`
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
	config              ragconfig.Search
}

// NewPipeline 校验配置并组合检索函数、精排引擎与父块读取器。
func NewPipeline(
	retrieve RetrieveFunc,
	reranker *rerank.Engine,
	parentLoader ParentLoader,
	cfg ragconfig.Search,
) (*Pipeline, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if retrieve == nil {
		return nil, ErrNilRetriever
	}

	cfg = cfg.Effective()

	return &Pipeline{retrieve: retrieve, reranker: reranker, parentLoader: parentLoader, config: cfg}, nil
}

// NewPipelineWithDiagnostics 接收带通道诊断的检索函数，保留失败降级信息。
func NewPipelineWithDiagnostics(retrieve func(context.Context, string) ([]retrieval.SearchResult, retrieval.Diagnostics, error), reranker *rerank.Engine, parents ParentLoader, cfg ragconfig.Search) (*Pipeline, error) {
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
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
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
		return Response{Retrieval: diagnostics, Rerank: rerank.Diagnostics{Outcome: rerank.OutcomeNoCandidates}}, nil
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

		response.Rerank = rerank.Diagnostics{Applied: false, Outcome: rerank.OutcomeNoModel, CandidateCount: len(candidates)}
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	// 先初始化命中子块上下文，父块缺失或读取降级时仍保留正确的引用范围。
	for i := range response.Results {
		response.Results[i].ContextChunkID = response.Results[i].ChunkID
		response.Results[i].ContextContent = response.Results[i].Content
		response.Results[i].ContextStartRune = response.Results[i].StartRune
		response.Results[i].ContextEndRune = response.Results[i].EndRune
		response.Results[i].ContextSourceHeader = response.Results[i].ContextHeader
		response.Results[i].ContextSourceLocators = response.Results[i].SourceLocators
	}

	if !p.config.ExpandParents || p.parentLoader == nil || len(response.Results) == 0 {

		return p.finalize(ctx, response)
	}

	parentIDs := uniqueParentIDs(response.Results)

	if len(parentIDs) == 0 {
		return p.finalize(ctx, response)
	}

	parents, err := p.parentLoader.LoadParents(ctx, parentIDs)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if p.config.AllowParentFallback && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			response.Diagnostics = append(response.Diagnostics, Diagnostic{Stage: "parent_expansion", Message: logging.SafeErrorText(err, 2048)})
			return p.finalize(ctx, response)
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
		response.Results[i].ContextSourceLocators = parent.SourceLocators
	}
	return p.finalize(ctx, response)
}

// finalize 先合并相同上下文，再按排名与预算选择完整块，避免截断后引用坐标失真。
func (p *Pipeline) finalize(ctx context.Context, response Response) (Response, error) {
	if p.config.CollapseSameParent {
		response.Results = collapseSameContext(response.Results)
	}
	selected := make([]retrieval.SearchResult, 0, min(len(response.Results), p.config.FinalTopK))
	count := func(r retrieval.SearchResult) int {
		text := r.EffectiveContent()
		if r.ContextSourceHeader != "" {
			text = r.ContextSourceHeader + "\n\n" + text
		}
		return p.config.CountTokens(text)
	}
	for _, result := range response.Results {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		tokens := count(result)
		if tokens < 0 {
			return Response{}, errors.New("rag search: tokenizer returned a negative count")
		}
		remaining := p.config.MaxContextTokens - response.ContextTokens
		if tokens > remaining && result.EffectiveChunkID() != result.ChunkID {
			// 父块超出余量时退回实际命中子块，合并时的其他证据不再计为已提供上下文。
			result.ContextChunkID, result.ContextContent = result.ChunkID, result.Content
			result.ContextStartRune, result.ContextEndRune = result.StartRune, result.EndRune
			result.ContextSourceHeader = result.ContextHeader
			result.ContextSourceLocators = result.SourceLocators
			result.Evidence = nil
			tokens = count(result)
			response.Diagnostics = append(response.Diagnostics, Diagnostic{Stage: "context_budget", Message: "parent exceeded context budget; retained child evidence"})
		}
		if tokens < 0 {
			return Response{}, errors.New("rag search: tokenizer returned a negative count")
		}
		if tokens > remaining {
			response.Diagnostics = append(response.Diagnostics, Diagnostic{Stage: "context_budget", Message: "skipped chunk exceeding remaining context budget"})
			continue
		}
		selected = append(selected, result)
		response.ContextTokens += tokens
		if len(selected) == p.config.FinalTopK {
			break
		}
	}
	response.Results = selected
	response.Rerank.ResultCount = len(response.Results)
	return response, nil
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
			evidence = []retrieval.HitEvidence{{ChunkID: result.ChunkID, StartRune: result.StartRune, EndRune: result.EndRune, Content: result.Content, Score: result.Score, SourceLocators: result.SourceLocators}}
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
