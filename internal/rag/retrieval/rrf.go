package retrieval

import (
	"math"
	"sort"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// FuseRRF 按 Eino 返回的相关性顺序计算加权排名融合。
// 两路分数不需要统一量纲；缺省分数也不影响双路排名。
// 单路召回保留向量分数或归一化 BM25 分数，同时保留原始分数。
func FuseRRF(vectorResults, keywordResults []SearchResult, cfg ragconfig.RRF) []SearchResult {
	if cfg.Validate() != nil {
		return nil
	}
	cfg = cfg.Effective()
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

	// 以知识库、文档版本和分块身份去重；双路命中时沿用向量侧的正文与引用。
	// 排名直接保存在结果中，Score 暂存本轮 RRF 贡献，避免另外维护两套排名映射。
	all := make(map[string]SearchResult, len(vectorResults)+len(keywordResults))

	for i, item := range vectorResults {
		item.VectorScore = item.Score
		item.VectorRank = i + 1
		item.MatchType = MatchVector
		item.Score = cfg.VectorWeight / float64(cfg.K+item.VectorRank)
		all[item.IdentityKey()] = item
	}

	for i, item := range keywordResults {
		key, rank := item.IdentityKey(), i+1
		contribution := cfg.KeywordWeight / float64(cfg.K+rank)
		if existing, ok := all[key]; ok {
			existing.KeywordScore = item.Score
			existing.KeywordRank = rank
			existing.MatchType = MatchHybrid
			existing.Score += contribution
			all[key] = existing
			continue
		}

		item.KeywordScore = item.Score
		item.KeywordRank = rank
		item.MatchType = MatchKeyword
		item.Score = contribution
		all[key] = item
	}

	// 一个 Chunk 同时排名两个 Retriever 第一时的理论最大 RRF。
	maxRRF := (cfg.VectorWeight + cfg.KeywordWeight) / float64(cfg.K+1)

	result := make([]SearchResult, 0, len(all))

	for _, item := range all {
		if item.VectorRank > 0 && item.KeywordRank > 0 {
			item.MatchType = MatchHybrid
		} else if item.VectorRank > 0 {
			item.MatchType = MatchVector
		} else {
			item.MatchType = MatchKeyword
		}

		item.Score /= maxRRF

		// 按双路同时排名第一的理论最大值归一化；只修正极小的浮点误差。
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
