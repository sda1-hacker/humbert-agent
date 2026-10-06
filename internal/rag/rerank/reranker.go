package rerank

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

// Scorer 按输入片段顺序返回精排分数；返回数量须与片段数一致。
type Scorer interface {
	Score(ctx context.Context, query string, passages []string) ([]float64, error)
}

// PassageBuilder 决定某一个 SearchResult
// 真正送给 Rerank Model 的文本。
type PassageBuilder func(result retrieval.SearchResult) string

// Outcome 精排成功、阈值降级或模型不可用等执行状态。
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

	// Outcome 表示精排成功、阈值降级或模型不可用等执行状态。
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

	// Threshold 是模型相关性分数阈值，不是融合后的排序分数阈值。
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

	// ModelWeight、BaseWeight、SourceWeight 分别控制模型相关性、召回分数和来源权重的占比。
	ModelWeight  float64
	BaseWeight   float64
	SourceWeight float64

	// MMRLambda 控制独立精排调用中相关性与内容多样性的取舍，越大越重视相关性。
	MMRLambda float64

	PassageBuilder PassageBuilder
}

// DefaultConfig 返回精排阈值、候选上限、融合权重和独立精排的 MMR 参数。
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

// Engine 模型精排引擎，负责评分、阈值处理和排序。
type Engine struct {
	scorer    Scorer
	config    Config
	configErr error
}

// NewEngine 保存配置与校验结果；配置错误在执行时返回。
func NewEngine(scorer Scorer, cfg Config) *Engine {
	err := cfg.Validate()
	cfg = normalizeConfig(cfg)

	return &Engine{
		scorer:    scorer,
		config:    cfg,
		configErr: err,
	}
}

// RerankCandidates 保留通过阈值的候选池，供父块分组后统一截断；此路径不执行 MMR 多样性选择。
func (e *Engine) RerankCandidates(ctx context.Context, query string, candidates []retrieval.SearchResult) (Result, error) {
	copy := *e
	copy.config.TopK = len(candidates)
	return copy.Rerank(ctx, query, candidates)
}

// Rerank 执行评分、过滤、排序与独立调用的 MMR 选择。普通模型错误退回召回顺序，取消和超时直接返回。
func (e *Engine) Rerank(ctx context.Context, query string, candidates []retrieval.SearchResult) (Result, error) {
	if e.configErr != nil {
		return Result{}, e.configErr
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	query = strings.TrimSpace(query)

	diag := Diagnostics{
		CandidateCount:     len(candidates),
		Threshold:          e.config.Threshold,
		EffectiveThreshold: e.config.Threshold,
	}

	// 精排不可用时保留召回顺序，统一设置降级诊断。
	fallback := func(outcome Outcome, message string) Result {
		results := fallbackResults(candidates, e.config.TopK)
		diag.Outcome, diag.Error, diag.ResultCount = outcome, message, len(results)
		return Result{Scored: results, Results: results, Diagnostics: diag}
	}

	if len(candidates) == 0 {
		diag.Outcome = OutcomeNoCandidates
		return Result{Diagnostics: diag}, nil
	}

	if e.config.Disabled {
		return fallback(OutcomeDisabled, ""), nil
	}
	if e.scorer == nil {
		return fallback(OutcomeNoModel, ""), nil
	}

	if query == "" {
		return Result{}, errors.New("rerank: empty query")
	}

	// 只取 Retrieval 排名前 MaxCandidates 的候选。
	//
	// Retriever 本身已经按 RRF / relevance 排好序，
	// 所以这里保留头部候选即可。
	candidates = slices.Clone(candidates)

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

		return fallback(OutcomeModelError, logging.SafeErrorText(err, 2048)), nil
	}

	if len(modelScores) != len(candidates) {
		return fallback(OutcomeModelError, fmt.Sprintf("rerank scorer returned %d scores for %d passages", len(modelScores), len(candidates))), nil
	}

	for i, score := range modelScores {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return fallback(OutcomeModelError, fmt.Sprintf("rerank scorer returned invalid score at index %d", i)), nil
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

	// 首次阈值没有结果时，仅降低一次阈值，并保持配置的最低值。

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

	// 阈值仍没有结果时，按最低分数要求保留最佳候选。

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

// DefaultPassageBuilder 复用统一文本构造规则，将标题、标题路径与子块正文交给精排模型。
func DefaultPassageBuilder(result retrieval.SearchResult) string {
	doc := &schema.Document{MetaData: result.Metadata}
	return searchcontent.BuildText(searchcontent.DefaultBuilder().Title(doc), result.ContextHeader, result.Content)
}

// applyMMR 使用相关性与文本集合的 Jaccard 相似度选择尽量不重复的结果，不修改相关性分数。
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
		return slices.Clone(candidates)
	}

	selected := make([]retrieval.SearchResult, 0, topK)
	remaining := slices.Clone(candidates)

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

// tokenSet 将拉丁字母和数字按词归一化，将中日韩字符按单字收集，用于轻量相似度计算。
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

// isCJK 判断字符是否属于当前支持的中日韩文字范围。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// jaccard 计算两个 token 集合的交并比，空集合返回零。
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

// sourceWeight 优先读取结果中的来源权重，再读取元数据中的可选权重。
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

// indicesAboveThreshold 返回达到相关性阈值的输入位置，保持原顺序。
func indicesAboveThreshold(scores []float64, threshold float64) []int {
	result := make([]int, 0, len(scores))

	for i, score := range scores {
		if score >= threshold {
			result = append(result, i)
		}
	}

	return result
}

// indexOfMax 返回最高分位置，同分时保留第一个。
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

// fallbackResults 保留召回排序并标记未精排，按独立调用的 TopK 截断。
func fallbackResults(candidates []retrieval.SearchResult, topK int) []retrieval.SearchResult {
	results := slices.Clone(candidates)

	for i := range results {
		results[i].BaseScore = results[i].Score
		results[i].Reranked = false
	}

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}

	return results
}

// clamp01 将有效分数限制在零到一之间。
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

// normalizeConfig 补齐候选数量、默认权重与模型输入构造函数。
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
