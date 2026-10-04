package search

import (
	"context"
	"errors"
	"fmt"
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

// RetrieveFunc 是 Search Pipeline 对 Retrieval 层的唯一依赖。
//
// 为什么不直接依赖：
//
//	postgres.HybridRetriever
//
// 因为 Final Search Pipeline 不应该知道：
//
//	PostgreSQL
//	pgvector
//	ParadeDB
//	Eino
//
// 使用时只需要：
//
//	func(ctx, query) ([]SearchResult, error)
//
// 即可。
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

type Config struct {
	Timeout time.Duration
	// FinalTopK 是 Search Pipeline 最终最多返回多少条。
	FinalTopK int

	// ExpandParents 开启 Parent-Child Context Expansion。
	ExpandParents bool

	// CollapseSameParent 表示：
	//
	// 如果多个 Child 最终都指向同一个 Parent，
	// 是否只保留得分最高的那个 Child Hit。
	//
	// 默认关闭。
	//
	// 原因是多个 Child 命中本身也是很有价值的 Retrieval Evidence。
	// Prompt Assembly 阶段可以再根据 ContextChunkID 去重。
	CollapseSameParent bool

	// AllowParentFallback keeps child evidence on a recoverable parent read
	// failure. Cancellation and request deadlines always propagate.
	AllowParentFallback bool
}

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

type Diagnostic struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

type Pipeline struct {
	retrieve            RetrieveFunc
	retrieveDiagnostics func(context.Context, string) ([]retrieval.SearchResult, retrieval.Diagnostics, error)
	reranker            *rerank.Engine
	parentLoader        ParentLoader
	config              Config
}

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

func (c Config) Validate() error {
	if c.FinalTopK < 0 || c.Timeout < 0 {
		return fmt.Errorf("rag search: negative result limit or timeout")
	}
	return nil
}

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

// Search 是整个 RAG Core 最终的检索入口。
//
// 流程:
//
//	Query
//	  ↓
//	Retrieve
//	  ↓
//	RRF candidates
//	  ↓
//	Rerank
//	  ↓
//	MMR
//	  ↓
//	Parent Expansion
//	  ↓
//	Final SearchResult[]
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
		response.Results = cloneResults(candidates)

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
			// Parent 不存在时不要把整个 SearchResult 丢掉。
			//
			// 这可能来自：
			//
			//     数据迁移
			//     旧数据
			//     Parent 被清理
			//
			// Child 自身仍然是一个合法 Retrieval Result。
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

// collapseSameContext 用于可选的 Parent Context 去重。
//
// 如果：
//
//	Child A → Parent P
//	Child B → Parent P
//
// 最终只保留排名更靠前的 A。
//
// 由于输入本身已经按最终 Score 排序，
// 第一次看到某个 ContextChunkID 就是最优结果。
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

func cloneResults(src []retrieval.SearchResult) []retrieval.SearchResult {
	if len(src) == 0 {
		return nil
	}

	dst := make([]retrieval.SearchResult, len(src))
	copy(dst, src)

	return dst
}
