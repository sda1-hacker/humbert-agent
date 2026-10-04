package rerank

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// Scorer 是我们整个 RAG Core 对 Rerank Model 的唯一依赖。
//
// 任何 Provider：
//
//	BGE Reranker
//	Cohere
//	Jina
//	DashScope
//	Volcengine
//	Tencent LKEAP
//	vLLM
//
// 最终都只需要实现：
//
//	Query + Passages
//	    ↓
//	[]score
//
// -----------------------------------------------------------------------------
// 接口约定:
//
// 返回 scores:
//
//	len(scores) == len(passages)
//
// 并且每个 score 应该已经转换到：
//
//	[0, 1]
//
// Provider 如果返回 logit，
// 应该在自己的 Adapter 中先进行 normalization。
//
// 这样 Rerank Core 永远不需要知道不同厂商的协议。
type Scorer interface {
	Score(ctx context.Context, query string, passages []string) ([]float64, error)
}

// PassageBuilder 决定某一个 SearchResult
// 真正送给 Rerank Model 的文本。
type PassageBuilder func(result retrieval.SearchResult) string

type Outcome string

const (
	OutcomeOK                Outcome = "ok"
	OutcomeThresholdDegraded Outcome = "threshold_degraded"
	OutcomeFallbackTop1      Outcome = "fallback_top1"
	OutcomeAllBelowThreshold Outcome = "all_below_threshold"
	OutcomeModelError        Outcome = "model_error"
	OutcomeNoModel           Outcome = "no_model"
	OutcomeDisabled          Outcome = "disabled"
	OutcomeNoCandidates      Outcome = "no_candidates"
)

// Diagnostics 用于解释一次 Rerank 到底发生了什么。
//
// 未来 API / Debug UI 可以直接返回这个结构。
type Diagnostics struct {
	Applied bool `json:"applied"`

	Outcome Outcome `json:"outcome"`

	Threshold float64 `json:"threshold"`

	EffectiveThreshold float64 `json:"effective_threshold"`

	TopScore float64 `json:"top_score"`

	CandidateCount int `json:"candidate_count"`

	ResultCount int `json:"result_count"`

	Error string `json:"error,omitempty"`
}

// Config 控制整个 Rerank Pipeline。
type Config struct {
	// Disabled 显式关闭 Rerank。
	Disabled bool

	// TopK 是最终经过 MMR 后最多保留多少结果。
	TopK int

	// Threshold 针对 ModelScore。
	//
	// 注意：
	//
	//     0
	//     负数
	//
	// 都是合法值，因此这里不能把 <=0 当“未配置”。
	Threshold float64

	// MaxCandidates 限制单次送给模型的候选数量。
	MaxCandidates int

	// 高阈值完全没有结果时：
	//
	//     newThreshold =
	//         max(threshold * factor, floor)
	DegradeFactor float64
	DegradeFloor  float64

	// 所有 Threshold 都失败以后，
	// 最高模型分达到这个值时仍保留 Top1。
	FallbackMinScore float64

	// Composite Score:
	//
	//     ModelWeight  * modelScore
	//   + BaseWeight   * retrievalScore
	//   + SourceWeight * sourceWeight
	ModelWeight  float64
	BaseWeight   float64
	SourceWeight float64

	// MMR:
	//
	//     λ * relevance
	//     -
	//     (1-λ) * redundancy
	MMRLambda float64

	PassageBuilder PassageBuilder
}

// DefaultConfig 对齐我们当前采用的 WeKnora 风格。
//
// TopK 保持我们最开始设计的：
//
//	Recall Top30
//	    ↓
//	Rerank
//	    ↓
//	Top5
func DefaultConfig() Config {
	return Config{
		TopK:             5,
		Threshold:        0.3,
		MaxCandidates:    200,
		DegradeFactor:    0.7,
		DegradeFloor:     0.3,
		FallbackMinScore: 0.15,

		ModelWeight:  0.6,
		BaseWeight:   0.3,
		SourceWeight: 0.1,

		MMRLambda: 0.7,

		PassageBuilder: DefaultPassageBuilder,
	}
}

// Result 保存一次完整 Rerank 的输出。
type Result struct {
	// Scored 是通过模型阈值以后，
	// 按 Composite Score 排序的结果。
	//
	// 还没有经过 MMR。
	Scored []retrieval.SearchResult `json:"scored"`

	// Results 是最终经过 MMR TopK 的结果。
	Results []retrieval.SearchResult `json:"results"`

	Diagnostics Diagnostics `json:"diagnostics"`
}

type Engine struct {
	scorer    Scorer
	config    Config
	configErr error
}

func NewEngine(scorer Scorer, cfg Config) *Engine {
	err := cfg.Validate()
	cfg = normalizeConfig(cfg)

	return &Engine{
		scorer:    scorer,
		config:    cfg,
		configErr: err,
	}
}

// RerankCandidates preserves the accepted candidate pool for context grouping.
// The search pipeline owns the final context count; standalone Rerank retains
// its configured TopK behavior. MaxCandidates still limits model requests.
func (e *Engine) RerankCandidates(ctx context.Context, query string, candidates []retrieval.SearchResult) (Result, error) {
	copy := *e
	copy.config.TopK = len(candidates)
	return copy.Rerank(ctx, query, candidates)
}

// Rerank 执行完整重排。
//
// 模型普通错误：
//
//	不向上传播
//	↓
//	退回原 Retrieval 顺序
//	↓
//	Diagnostics = model_error
//
// Context Cancel / Deadline：
//
//	继续向上传播
//
// 因为取消请求不能被误认为“模型暂时失败”。
func (e *Engine) Rerank(ctx context.Context, query string, candidates []retrieval.SearchResult) (Result, error) {
	if e.configErr != nil {
		return Result{}, e.configErr
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	query = strings.TrimSpace(query)

	diag := Diagnostics{
		Threshold:          e.config.Threshold,
		EffectiveThreshold: e.config.Threshold,
	}

	if len(candidates) == 0 {
		diag.Outcome = OutcomeNoCandidates
		return Result{Diagnostics: diag}, nil
	}

	if e.config.Disabled {
		results := fallbackResults(candidates, e.config.TopK)

		diag.Outcome = OutcomeDisabled
		diag.CandidateCount = len(candidates)
		diag.ResultCount = len(results)

		return Result{
			Scored:      results,
			Results:     results,
			Diagnostics: diag,
		}, nil
	}

	if e.scorer == nil {
		results := fallbackResults(candidates, e.config.TopK)

		diag.Outcome = OutcomeNoModel
		diag.CandidateCount = len(candidates)
		diag.ResultCount = len(results)

		return Result{
			Scored:      results,
			Results:     results,
			Diagnostics: diag,
		}, nil
	}

	if query == "" {
		return Result{}, errors.New("rerank: empty query")
	}

	// 只取 Retrieval 排名前 MaxCandidates 的候选。
	//
	// Retriever 本身已经按 RRF / relevance 排好序，
	// 所以这里保留头部候选即可。
	candidates = cloneResults(candidates)

	if len(candidates) > e.config.MaxCandidates {
		candidates = candidates[:e.config.MaxCandidates]
	}

	diag.CandidateCount = len(candidates)

	passages := make([]string, len(candidates))

	for i := range candidates {
		passages[i] = e.config.PassageBuilder(candidates[i])
	}

	modelScores, err := e.scorer.Score(ctx, query, passages)
	if contextErr := ctx.Err(); contextErr != nil {
		return Result{}, contextErr
	}

	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {

			return Result{}, err
		}

		results := fallbackResults(candidates, e.config.TopK)

		diag.Outcome = OutcomeModelError
		diag.Error = logging.SafeErrorText(err, 2048)
		diag.ResultCount = len(results)

		return Result{
			Scored:      results,
			Results:     results,
			Diagnostics: diag,
		}, nil
	}

	if len(modelScores) != len(candidates) {
		results := fallbackResults(candidates, e.config.TopK)

		diag.Outcome = OutcomeModelError
		diag.Error = fmt.Sprintf(
			"rerank scorer returned %d scores for %d passages",
			len(modelScores),
			len(candidates),
		)
		diag.ResultCount = len(results)

		return Result{
			Scored:      results,
			Results:     results,
			Diagnostics: diag,
		}, nil
	}

	for i, score := range modelScores {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			results := fallbackResults(candidates, e.config.TopK)

			diag.Outcome = OutcomeModelError
			diag.Error = fmt.Sprintf("rerank scorer returned invalid score at index %d", i)
			diag.ResultCount = len(results)

			return Result{
				Scored:      results,
				Results:     results,
				Diagnostics: diag,
			}, nil
		}

		modelScores[i] = clamp01(score)
	}

	topScore := modelScores[0]

	for _, score := range modelScores[1:] {
		if score > topScore {
			topScore = score
		}
	}

	diag.TopScore = topScore

	selected := indicesAboveThreshold(modelScores, e.config.Threshold)
	outcome := OutcomeOK

	// -------------------------------------------------------------------------
	// Threshold Degradation
	//
	// 例如：
	//
	//     threshold = 0.8
	//
	// 没有结果：
	//
	//     max(0.8 * 0.7, 0.3)
	//     = 0.56
	//
	// 再试一次。
	// -------------------------------------------------------------------------

	if len(selected) == 0 && e.config.Threshold > e.config.DegradeFloor {
		degraded := math.Max(
			e.config.Threshold*e.config.DegradeFactor,
			e.config.DegradeFloor,
		)

		selected = indicesAboveThreshold(modelScores, degraded)
		diag.EffectiveThreshold = degraded

		if len(selected) > 0 {
			outcome = OutcomeThresholdDegraded
		}
	}

	// -------------------------------------------------------------------------
	// Fallback Top1
	// -------------------------------------------------------------------------

	if len(selected) == 0 {
		bestIndex := indexOfMax(modelScores)

		if bestIndex >= 0 &&
			modelScores[bestIndex] >= e.config.FallbackMinScore {

			selected = []int{bestIndex}
			outcome = OutcomeFallbackTop1
		} else {
			diag.Applied = true
			diag.Outcome = OutcomeAllBelowThreshold
			diag.ResultCount = 0

			return Result{
				Diagnostics: diag,
			}, nil
		}
	}

	scored := make([]retrieval.SearchResult, 0, len(selected))
	passageByChunkID := make(map[string]string, len(selected))

	for _, index := range selected {
		item := candidates[index]

		baseScore := clamp01(item.Score)
		sourceWeight := sourceWeight(item)
		modelScore := modelScores[index]

		item.BaseScore = baseScore
		item.ModelScore = modelScore
		item.SourceWeight = sourceWeight
		item.Reranked = true

		item.Score = clamp01(
			e.config.ModelWeight*modelScore +
				e.config.BaseWeight*baseScore +
				e.config.SourceWeight*sourceWeight,
		)

		scored = append(scored, item)
		passageByChunkID[item.ChunkID] = passages[index]
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}

		return scored[i].ChunkID < scored[j].ChunkID
	})

	final := applyMMR(
		scored,
		passageByChunkID,
		e.config.TopK,
		e.config.MMRLambda,
	)

	diag.Applied = true
	diag.Outcome = outcome
	diag.ResultCount = len(final)

	return Result{
		Scored:      scored,
		Results:     final,
		Diagnostics: diag,
	}, nil
}

// DefaultPassageBuilder 构造 Rerank Model 输入。
//
// 当前使用：
//
//	title
//
//	context header
//
//	body
//
// 为什么加 ContextHeader？
//
// 因为 Parent 内部经过 Recursive Split 的 Child
// 可能已经不再包含真正的 Heading 行，
// 但 ContextHeader 仍然保存了：
//
//	# Manual
//	## Installation
//	### Linux
//
// 这对 Reranker 判断局部语义很有帮助。
//
// 我们不做激进 Markdown 删除。
// 当前 WeKnora 的 passage cleaner 也已经专门改进为
// 避免破坏 code / math 内容。:chatgpt-content-reference{index="3"}
func DefaultPassageBuilder(result retrieval.SearchResult) string {
	parts := make([]string, 0, 3)

	appendUnique := func(value string) {
		value = strings.TrimSpace(value)

		if value == "" {
			return
		}

		for _, existing := range parts {
			if existing == value {
				return
			}
		}

		parts = append(parts, value)
	}

	appendUnique(resultTitle(result))
	appendUnique(result.ContextHeader)
	appendUnique(result.Content)

	return strings.Join(parts, "\n\n")
}

// applyMMR 使用 Maximal Marginal Relevance 降低 TopK 内部的内容重复。
//
// 公式：
//
//	MMR
//	  = λ * relevance
//	  - (1-λ) * max_similarity_to_selected
//
// relevance：
//
//	Composite Score
//
// redundancy：
//
//	当前采用 token-set Jaccard。
//
// SearchResult.Score 不会被替换成 MMR 临时分数。
// MMR 只负责“选择顺序”。
func applyMMR(
	candidates []retrieval.SearchResult,
	passages map[string]string,
	topK int,
	lambda float64,
) []retrieval.SearchResult {
	if len(candidates) == 0 || topK <= 0 {
		return nil
	}

	if len(candidates) <= topK {
		return cloneResults(candidates)
	}

	selected := make([]retrieval.SearchResult, 0, topK)
	remaining := cloneResults(candidates)

	tokenCache := make(map[string]map[string]struct{}, len(candidates))

	getTokens := func(result retrieval.SearchResult) map[string]struct{} {
		if tokens, ok := tokenCache[result.ChunkID]; ok {
			return tokens
		}

		tokens := tokenSet(passages[result.ChunkID])
		tokenCache[result.ChunkID] = tokens

		return tokens
	}

	// 第一条一定使用 Composite Score 最高的结果。
	selected = append(selected, remaining[0])
	remaining = remaining[1:]

	for len(selected) < topK && len(remaining) > 0 {
		bestIndex := -1
		bestMMR := math.Inf(-1)

		for i, candidate := range remaining {
			maxRedundancy := 0.0
			candidateTokens := getTokens(candidate)

			for _, chosen := range selected {
				redundancy := jaccard(
					candidateTokens,
					getTokens(chosen),
				)

				if redundancy > maxRedundancy {
					maxRedundancy = redundancy
				}
			}

			mmrScore := lambda*candidate.Score -
				(1-lambda)*maxRedundancy

			if bestIndex == -1 ||
				mmrScore > bestMMR ||
				(mmrScore == bestMMR &&
					candidate.ChunkID < remaining[bestIndex].ChunkID) {

				bestIndex = i
				bestMMR = mmrScore
			}
		}

		selected = append(selected, remaining[bestIndex])

		remaining = append(
			remaining[:bestIndex],
			remaining[bestIndex+1:]...,
		)
	}

	return selected
}

// tokenSet 做一个轻量多语言 tokenizer。
//
// Latin / Number：
//
//	连续字母数字组成 token。
//
// CJK：
//
//	每个 rune 作为一个 token。
//
// 这比单纯 strings.Fields 更适合中文，
// 因为中文正文通常没有空格。
func tokenSet(text string) map[string]struct{} {
	result := make(map[string]struct{})

	var word []rune

	flushWord := func() {
		if len(word) == 0 {
			return
		}

		result[string(word)] = struct{}{}
		word = word[:0]
	}

	for _, r := range strings.ToLower(text) {
		switch {
		case isCJK(r):
			flushWord()
			result[string(r)] = struct{}{}

		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word = append(word, r)

		default:
			flushWord()
		}
	}

	flushWord()

	return result
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	intersection := 0

	for token := range a {
		if _, ok := b[token]; ok {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection

	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

func sourceWeight(result retrieval.SearchResult) float64 {
	if result.SourceWeight != 0 {
		return clamp01(result.SourceWeight)
	}

	if len(result.Metadata) == 0 {
		return 0
	}

	value, ok := result.Metadata["rag_source_weight"]
	if !ok {
		return 0
	}

	switch typed := value.(type) {
	case float64:
		return clamp01(typed)

	case float32:
		return clamp01(float64(typed))

	case int:
		return clamp01(float64(typed))

	default:
		return 0
	}
}

func resultTitle(result retrieval.SearchResult) string {
	for _, key := range []string{
		"title",
		"_title",
		"file_name",
	} {
		if value, ok := result.Metadata[key].(string); ok {
			value = strings.TrimSpace(value)

			if value != "" {
				return value
			}
		}
	}

	return ""
}

func indicesAboveThreshold(scores []float64, threshold float64) []int {
	result := make([]int, 0, len(scores))

	for i, score := range scores {
		if score >= threshold {
			result = append(result, i)
		}
	}

	return result
}

func indexOfMax(values []float64) int {
	if len(values) == 0 {
		return -1
	}

	best := 0

	for i := 1; i < len(values); i++ {
		if values[i] > values[best] {
			best = i
		}
	}

	return best
}

func fallbackResults(candidates []retrieval.SearchResult, topK int) []retrieval.SearchResult {
	results := cloneResults(candidates)

	for i := range results {
		results[i].BaseScore = results[i].Score
		results[i].Reranked = false
	}

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}

	return results
}

func cloneResults(src []retrieval.SearchResult) []retrieval.SearchResult {
	if len(src) == 0 {
		return nil
	}

	dst := make([]retrieval.SearchResult, len(src))
	copy(dst, src)

	return dst
}

func clamp01(value float64) float64 {
	switch {
	case value < 0:
		return 0

	case value > 1:
		return 1

	default:
		return value
	}
}

func normalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()

	if cfg.TopK <= 0 {
		cfg.TopK = defaults.TopK
	}

	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = defaults.MaxCandidates
	}

	// 三个 Weight 允许调用方显式把其中某一个设成0。
	//
	// 只有三个都没有设置时，才恢复默认权重。
	if cfg.ModelWeight == 0 &&
		cfg.BaseWeight == 0 &&
		cfg.SourceWeight == 0 {

		cfg.ModelWeight = defaults.ModelWeight
		cfg.BaseWeight = defaults.BaseWeight
		cfg.SourceWeight = defaults.SourceWeight
	}

	if cfg.MMRLambda < 0 || cfg.MMRLambda > 1 {
		cfg.MMRLambda = defaults.MMRLambda
	}

	if cfg.PassageBuilder == nil {
		cfg.PassageBuilder = defaults.PassageBuilder
	}

	return cfg
}
