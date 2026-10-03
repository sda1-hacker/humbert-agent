package retrieval

import (
	"math"
	"testing"
)

func TestFuseRRFHybrid(t *testing.T) {
	vector := []SearchResult{
		{ChunkID: "A", Score: 0.95},
		{ChunkID: "B", Score: 0.90},
		{ChunkID: "C", Score: 0.80},
	}

	keyword := []SearchResult{
		{ChunkID: "B", Score: 10},
		{ChunkID: "A", Score: 8},
		{ChunkID: "D", Score: 6},
	}

	got := FuseRRF(vector, keyword, DefaultRRFConfig())

	if len(got) != 4 {
		t.Fatalf("应该有4个唯一 Chunk: got=%d", len(got))
	}

	// A / B 同时命中两路，应该排在只有单路命中的 C / D 前面。
	if got[0].MatchType != MatchHybrid {
		t.Fatalf("第一名应该是 Hybrid 命中: %+v", got[0])
	}

	if got[1].MatchType != MatchHybrid {
		t.Fatalf("第二名应该是 Hybrid 命中: %+v", got[1])
	}

	for _, item := range got {
		if item.Score < 0 || item.Score > 1 {
			t.Fatalf("归一化 RRF 应位于[0,1]: %+v", item)
		}
	}
}

func TestFuseRRFSortsBeforeAssigningRanks(t *testing.T) {
	// 故意传入错误顺序：
	//
	// A 分数最低，却排在 slice 第一位。
	vector := []SearchResult{
		{ChunkID: "A", Score: 0.1},
		{ChunkID: "B", Score: 0.9},
		{ChunkID: "C", Score: 0.5},
	}

	got := FuseRRF(vector, nil, DefaultRRFConfig())

	if got[0].ChunkID != "B" {
		t.Fatalf("必须先按 score 排序，最高分应该是B: %+v", got)
	}

	if got[0].VectorRank != 1 {
		t.Fatalf("B 的 VectorRank 应为1: %+v", got[0])
	}

	if got[2].ChunkID != "A" || got[2].VectorRank != 3 {
		t.Fatalf("A 应为第三名: %+v", got[2])
	}
}

func TestFuseRRFDeduplicatesByBestScore(t *testing.T) {
	vector := []SearchResult{
		{ChunkID: "A", Score: 0.4},
		{ChunkID: "A", Score: 0.9},
		{ChunkID: "B", Score: 0.8},
	}

	got := FuseRRF(vector, nil, DefaultRRFConfig())

	if len(got) != 2 {
		t.Fatalf("重复 Chunk 应去重: got=%d", len(got))
	}

	if got[0].ChunkID != "A" || got[0].Score != 0.9 {
		t.Fatalf("应该保留 A 的最高分版本: %+v", got)
	}
}

func TestFuseRRFKeywordOnlyNormalizesUnboundedBM25(t *testing.T) {
	keyword := []SearchResult{
		{ChunkID: "A", Score: 12},
		{ChunkID: "B", Score: 6},
		{ChunkID: "C", Score: 3},
	}

	got := FuseRRF(nil, keyword, DefaultRRFConfig())

	if math.Abs(got[0].Score-1.0) > 1e-9 {
		t.Fatalf("最高 BM25 应归一化到1: %+v", got[0])
	}

	if math.Abs(got[1].Score-0.5) > 1e-9 {
		t.Fatalf("第二名应该为0.5: %+v", got[1])
	}

	if got[0].KeywordScore != 12 {
		t.Fatalf("Raw BM25 必须保留: %+v", got[0])
	}
}

func TestFuseRRFVectorOnlyKeepsCosineScore(t *testing.T) {
	vector := []SearchResult{
		{ChunkID: "A", Score: 0.92},
		{ChunkID: "B", Score: 0.81},
	}

	got := FuseRRF(vector, nil, DefaultRRFConfig())

	if got[0].Score != 0.92 {
		t.Fatalf("Vector-only 不应重新归一化: %+v", got[0])
	}

	if got[0].VectorScore != 0.92 {
		t.Fatalf("VectorScore 应保留 raw cosine: %+v", got[0])
	}
}

func TestFuseRRFStoresBothRawScores(t *testing.T) {
	vector := []SearchResult{
		{ChunkID: "A", Score: 0.88},
	}

	keyword := []SearchResult{
		{ChunkID: "A", Score: 9.5},
	}

	got := FuseRRF(vector, keyword, DefaultRRFConfig())

	if len(got) != 1 {
		t.Fatalf("应该只有一个结果: %d", len(got))
	}

	if got[0].VectorScore != 0.88 {
		t.Fatalf("VectorScore 丢失: %+v", got[0])
	}

	if got[0].KeywordScore != 9.5 {
		t.Fatalf("KeywordScore 丢失: %+v", got[0])
	}

	if got[0].MatchType != MatchHybrid {
		t.Fatalf("同一 Chunk 两路命中应为 hybrid: %+v", got[0])
	}
}

func TestDefaultRRFConfig(t *testing.T) {
	cfg := DefaultRRFConfig()

	if cfg.K != 60 {
		t.Fatalf("默认 RRF K 应为60: %d", cfg.K)
	}

	if cfg.VectorWeight != 0.7 {
		t.Fatalf("默认 VectorWeight 应为0.7: %f", cfg.VectorWeight)
	}

	if cfg.KeywordWeight != 0.3 {
		t.Fatalf("默认 KeywordWeight 应为0.3: %f", cfg.KeywordWeight)
	}
}

func TestLimit(t *testing.T) {
	input := []SearchResult{
		{ChunkID: "A"},
		{ChunkID: "B"},
		{ChunkID: "C"},
	}

	got := Limit(input, 2)

	if len(got) != 2 {
		t.Fatalf("TopK 错误: got=%d", len(got))
	}
}
