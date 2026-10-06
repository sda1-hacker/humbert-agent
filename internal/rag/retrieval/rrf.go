package retrieval

import (
	"fmt"
	"math"
	"sort"
)

// RRFConfig 控制两路召回权重与排名平滑常数；融合分数按理论最大值归一化。
type RRFConfig struct {
	K             int
	VectorWeight  float64
	KeywordWeight float64
}

// DefaultRRFConfig 返回当前推荐默认值。
func DefaultRRFConfig() RRFConfig {
	return RRFConfig{
		K:             60,
		VectorWeight:  0.7,
		KeywordWeight: 0.3,
	}
}

// FuseRRF 按 Eino 返回的相关性顺序计算加权排名融合。
// 两路分数不需要统一量纲；缺省分数也不影响双路排名。
// 单路召回保留向量分数或归一化 BM25 分数，同时保留原始分数。
func FuseRRF(vectorResults, keywordResults []SearchResult, cfg RRFConfig) []SearchResult {
	if cfg.Validate() != nil {
		return nil
	}
	cfg = normalizeRRFConfig(cfg)
	if cfg.VectorWeight == 0 {
		vectorResults = nil
	}
	if cfg.KeywordWeight == 0 {
		keywordResults = nil
	}

	vectorResults = prepareRankedChannel(vectorResults, MatchVector)
	keywordResults = prepareRankedChannel(keywordResults, MatchKeyword)

	if len(vectorResults) == 0 {
		return keywordOnlyResults(keywordResults)
	}

	if len(keywordResults) == 0 {
		return vectorOnlyResults(vectorResults)
	}

	vectorRanks := make(map[string]int, len(vectorResults))
	keywordRanks := make(map[string]int, len(keywordResults))

	for i := range vectorResults {
		vectorRanks[vectorResults[i].IdentityKey()] = i + 1
	}

	for i := range keywordResults {
		keywordRanks[keywordResults[i].IdentityKey()] = i + 1
	}

	// all 保存所有唯一 Chunk。
	//
	// 如果 Dense 与 Keyword 都有同一 Chunk，
	// 优先以 Vector Result 的结构信息作为 base，
	// 再补 KeywordScore。
	all := make(map[string]SearchResult, len(vectorResults)+len(keywordResults))

	for _, item := range vectorResults {
		item.VectorScore = item.Score
		item.VectorRank = vectorRanks[item.IdentityKey()]
		item.MatchType = MatchVector

		all[item.IdentityKey()] = item
	}

	for _, item := range keywordResults {
		if existing, ok := all[item.IdentityKey()]; ok {
			existing.KeywordScore = item.Score
			existing.KeywordRank = keywordRanks[item.IdentityKey()]
			existing.MatchType = MatchHybrid

			all[item.IdentityKey()] = existing
			continue
		}

		item.KeywordScore = item.Score
		item.KeywordRank = keywordRanks[item.IdentityKey()]
		item.MatchType = MatchKeyword

		all[item.IdentityKey()] = item
	}

	// 一个 Chunk 同时排名两个 Retriever 第一时的理论最大 RRF。
	maxRRF := (cfg.VectorWeight + cfg.KeywordWeight) / float64(cfg.K+1)

	result := make([]SearchResult, 0, len(all))

	for chunkID, item := range all {
		rawRRF := 0.0

		if rank := vectorRanks[chunkID]; rank > 0 {
			rawRRF += cfg.VectorWeight / float64(cfg.K+rank)
			item.VectorRank = rank
		}

		if rank := keywordRanks[chunkID]; rank > 0 {
			rawRRF += cfg.KeywordWeight / float64(cfg.K+rank)
			item.KeywordRank = rank
		}

		if item.VectorRank > 0 && item.KeywordRank > 0 {
			item.MatchType = MatchHybrid
		} else if item.VectorRank > 0 {
			item.MatchType = MatchVector
		} else {
			item.MatchType = MatchKeyword
		}

		item.Score = rawRRF / maxRRF

		// 理论上不会超过1。
		//
		// 这里只处理极小的 floating point 误差。
		if item.Score > 1 && item.Score < 1.0000000001 {
			item.Score = 1
		}

		result = append(result, item)
	}

	sortResults(result)

	return result
}

// vectorOnlyResults 保留 cosine similarity 本身。
//
// Dense similarity 本来就是一个具有实际意义的分数，
// 没必要因为只开启了一路 Retriever 就人为改变它。
func vectorOnlyResults(results []SearchResult) []SearchResult {
	out := make([]SearchResult, len(results))
	copy(out, results)

	for i := range out {
		out[i].VectorScore = out[i].Score
		out[i].VectorRank = i + 1
		out[i].MatchType = MatchVector
	}

	return out
}

// keywordOnlyResults 保留关键词召回顺序，并按当前结果最大分数归一化，不改写原始关键词分数。
func keywordOnlyResults(results []SearchResult) []SearchResult {
	if len(results) == 0 {
		return nil
	}

	out := make([]SearchResult, len(results))
	copy(out, results)

	maxScore := out[0].Score

	for i := range out {
		raw := out[i].Score

		out[i].KeywordScore = raw
		out[i].KeywordRank = i + 1
		out[i].MatchType = MatchKeyword

		switch {
		case raw <= 0:
			out[i].Score = 0

		case maxScore > 1:
			out[i].Score = raw / maxScore
		}
	}

	return out
}

// Limit 截取最终 TopK。
//
// 返回新的 slice header，底层 SearchResult 不会被修改。
func Limit(results []SearchResult, topK int) []SearchResult {
	if topK <= 0 || len(results) <= topK {
		return results
	}

	return results[:topK]
}

func normalizeRRFConfig(cfg RRFConfig) RRFConfig {
	defaults := DefaultRRFConfig()
	if cfg == (RRFConfig{}) {
		return defaults
	}

	return cfg
}

// Effective 补齐排名平滑常数和默认权重。
func (c RRFConfig) Effective() RRFConfig { return normalizeRRFConfig(c) }

// Validate 检查排名常数与权重，拒绝负数和非有限值。
func (c RRFConfig) Validate() error {
	if c == (RRFConfig{}) {
		return nil
	}
	if c.K < 0 || c.K > 1_000_000 || math.IsNaN(c.VectorWeight) || math.IsNaN(c.KeywordWeight) || math.IsInf(c.VectorWeight, 0) || math.IsInf(c.KeywordWeight, 0) ||
		c.VectorWeight < 0 || c.KeywordWeight < 0 || c.VectorWeight+c.KeywordWeight <= 0 || math.IsInf(c.VectorWeight+c.KeywordWeight, 0) {
		return fmt.Errorf("rag RRF: invalid k or weights")
	}
	return nil
}

// 按分数降序排列，同分按分块 ID 升序，保证结果稳定。
func sortResults(results []SearchResult) {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}

		return results[i].IdentityKey() < results[j].IdentityKey()
	})
}

// 同一路重复命中保留第一个，即排名最高的结果。
func prepareRankedChannel(results []SearchResult, kind MatchType) []SearchResult {
	seen := make(map[string]bool, len(results))
	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		if r.ChunkID == "" || seen[r.IdentityKey()] || math.IsNaN(r.Score) || math.IsInf(r.Score, 0) {
			continue
		}
		seen[r.IdentityKey()] = true
		r.MatchType = kind
		out = append(out, r)
	}
	return out
}
