package retrieval

import (
	"encoding/json"
	"github.com/cloudwego/eino/schema"
	"math"
	"testing"
)

func TestDocumentMetadataSurvivesJSONRoundTrip(t *testing.T) {
	input := SearchResult{CollectionID: "kb", DocumentID: "doc", DocumentRevision: 9007199254740993, ChunkID: "child", ParentChunkID: "parent", ChunkIndex: 2, StartRune: 11, EndRune: 18, Content: "原始正文", ContextHeader: "# 标题", Score: .7, MatchType: MatchHybrid, VectorScore: .9, KeywordScore: 12, VectorRank: 2, KeywordRank: 1, Metadata: map[string]any{"title": "业务标题"}}
	docs := Documents([]SearchResult{input})
	// 模拟外部索引保存元数据后再读取，位置通常会变成 float64。
	encoded, err := json.Marshal(docs)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []*schema.Document
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded[0].Content = "带标题的检索表示"
	got, err := Results(decoded)
	if err != nil {
		t.Fatal(err)
	}
	r := got[0]
	if r.Content != input.Content || r.CollectionID != input.CollectionID || r.DocumentID != input.DocumentID || r.DocumentRevision != input.DocumentRevision || r.ParentChunkID != input.ParentChunkID || r.StartRune != 11 || r.EndRune != 18 || r.Score != .7 || r.VectorScore != .9 || r.KeywordScore != 12 || r.VectorRank != 2 || r.KeywordRank != 1 {
		t.Fatalf("元数据或原文损坏: %+v", r)
	}
	if input.Metadata[MetaRevision] != nil {
		t.Fatal("转换污染了原始元数据")
	}
}

func TestDocumentsWithoutRAGMetadataRemainUsable(t *testing.T) {
	got, err := Results([]*schema.Document{(&schema.Document{ID: "native", Content: "正文"}).WithScore(.8)})
	if err != nil || len(got) != 1 || got[0].Content != "正文" || got[0].Score != .8 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestInvalidMetadataDoesNotBecomeCitation(t *testing.T) {
	for _, metadata := range []map[string]any{{MetaRevision: float64(9007199254740993)}, {MetaChunkStart: 1.5}, {MetaChunkIndex: -1}, {MetaChunkEnd: math.Inf(1)}} {
		if _, err := Results([]*schema.Document{{ID: "doc", Content: "正文", MetaData: metadata}}); err == nil {
			t.Fatalf("错误元数据未拒绝: %+v", metadata)
		}
	}
}
