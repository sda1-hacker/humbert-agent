package retrieval

import (
	"fmt"
	"math"
	"sort"
)

// RRFConfig 控制 Reciprocal Rank Fusion。
//
// 公式：
//
//	score(d)
//	  = vectorWeight  / (k + vectorRank)
//	  + keywordWeight / (k + keywordRank)
//
// 当前默认值对齐 WeKnora main：
//
//	k              = 60
//	vector weight  = 0.7
//	keyword weight = 0.3
//
// 最后再除以理论最大值：
//
//	(vectorWeight + keywordWeight) / (k + 1)
//
// 将分数归一化到大约 [0,1]。
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

// FuseRRF 将 Dense 和 Keyword 两路结果融合。
//
// 它同时正确处理三种情况：
//
//	vector + keyword
//	    → Weighted RRF
//
//	vector only
//	    → 保留 cosine similarity
//
//	keyword only
//	    → BM25 按当前结果集最大值归一化
//
// -----------------------------------------------------------------------------
// 一个非常重要的规则：
//
// RRF 使用的是“rank”，不是 Retriever 返回 slice 的偶然位置。
//
// 所以在计算 rank 前一定会：
//
//  1. 按 ChunkID 去重
//  2. 保留最高 score
//  3. 按 score DESC 排序
//
// 这样即使未来两个 Retriever 并发执行，
// 或多个结果列表合并顺序发生变化，rank 仍然正确。
func FuseRRF(
	vectorResults []SearchResult,
	keywordResults []SearchResult,
	cfg RRFConfig,
) []SearchResult {
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

	vectorResults = prepareChannel(vectorResults, MatchVector)
	keywordResults = prepareChannel(keywordResults, MatchKeyword)

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

// prepareChannel 对某一路 Retriever 的结果：
//
//	去掉非法 score
//	ChunkID 去重
//	保留最高 score
//	score DESC 排序
//
// 注意：RRF 的 rank 必须来源于这个排序后的列表。
func prepareChannel(results []SearchResult, matchType MatchType) []SearchResult {
	best := make(map[string]SearchResult, len(results))

	for _, item := range results {
		if item.ChunkID == "" {
			continue
		}

		if math.IsNaN(item.Score) || math.IsInf(item.Score, 0) {
			continue
		}

		existing, exists := best[item.IdentityKey()]

		if !exists || item.Score > existing.Score {
			item.MatchType = matchType
			best[item.IdentityKey()] = item
		}
	}

	result := make([]SearchResult, 0, len(best))

	for _, item := range best {
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

// keywordOnlyResults 将 BM25 score 映射到 [0,1] 附近。
//
// BM25 分数本身没有统一上界：
//
//	2.3
//	7.8
//	14.6
//
// 都可能出现。
//
// 对外统一 SearchResult.Score 时，如果最高分 > 1，
// 就让当前列表的最好结果等于1，其余按比例缩放。
//
// 原始 BM25 仍然完整保留在：
//
//	KeywordScore
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

func (c RRFConfig) Effective() RRFConfig { return normalizeRRFConfig(c) }

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

// sortResults 统一保证：
//
//	Score DESC
//	ChunkID ASC
//
// 第二排序键保证同分情况下结果稳定。
func sortResults(results []SearchResult) {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}

		return results[i].IdentityKey() < results[j].IdentityKey()
	})
}
