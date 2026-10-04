package retrieval

import (
	"math"
	"testing"
)

func TestRRFIdentityIncludesCollectionAndRevision(t *testing.T) {
	hits := []SearchResult{
		{CollectionID: "a", DocumentID: "doc", DocumentRevision: 1, ChunkID: "chunk", Score: 1},
		{CollectionID: "b", DocumentID: "doc", DocumentRevision: 1, ChunkID: "chunk", Score: 1},
		{CollectionID: "a", DocumentID: "doc", DocumentRevision: 2, ChunkID: "chunk", Score: 1},
	}
	if got := FuseRRF(hits, hits, DefaultRRFConfig()); len(got) != 3 {
		t.Fatalf("lost independent identities: %+v", got)
	}
}

func TestRRFZeroWeightDisablesChannel(t *testing.T) {
	vector := []SearchResult{{ChunkID: "v", Score: 1}}
	keyword := []SearchResult{{ChunkID: "k", Score: 2}}
	got := FuseRRF(vector, keyword, RRFConfig{VectorWeight: 0, KeywordWeight: 1})
	if len(got) != 1 || got[0].ChunkID != "k" || got[0].MatchType != MatchKeyword {
		t.Fatalf("%+v", got)
	}
	for _, cfg := range []RRFConfig{{VectorWeight: math.NaN(), KeywordWeight: 1}, {VectorWeight: 1, KeywordWeight: math.Inf(1)}, {VectorWeight: -1, KeywordWeight: 1}, {K: math.MaxInt, VectorWeight: 1}} {
		if cfg.Validate() == nil {
			t.Fatalf("invalid weights accepted: %+v", cfg)
		}
	}
}
