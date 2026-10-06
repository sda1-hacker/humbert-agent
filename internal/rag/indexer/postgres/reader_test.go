package postgres

import (
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

func TestResolvePublishedChunksScopeRevisionAndAuthority(t *testing.T) {
	current := map[string]retrieval.SearchResult{"c": {CollectionID: "kb", DocumentID: "doc", DocumentRevision: 2,
		ChunkID: "c", Content: "权威正文", ContextHeader: "章节", StartRune: 10, EndRune: 20, ParentChunkID: "p"}}
	valid := retrieval.SearchResult{CollectionID: "kb", DocumentID: "doc", DocumentRevision: 2, ChunkID: "c", Score: .8,
		Content: "索引副本", ContextContent: "索引父块", ContextChunkID: "other", StartRune: 0, EndRune: 1}
	old, unpublished, otherCollection, otherDoc, deleted := valid, valid, valid, valid, valid
	old.DocumentRevision = 1
	unpublished.DocumentRevision = 3
	otherCollection.CollectionID = "other"
	otherDoc.DocumentID = "other"
	deleted.ChunkID = "missing"
	legacy := valid
	legacy.ChunkID, legacy.DocumentRevision = "legacy", 0
	current["legacy"] = legacy
	got := resolvePublishedChunks("kb", current, []retrieval.SearchResult{old, unpublished, otherCollection, otherDoc, deleted, legacy, valid})
	if len(got) != 1 {
		t.Fatalf("版本或集合过滤错误：%+v", got)
	}
	if got[0].Content != "权威正文" || got[0].StartRune != 10 || got[0].EndRune != 20 || got[0].ParentChunkID != "p" || got[0].Score != .8 {
		t.Fatalf("未使用权威正文或改变了召回分数：%+v", got[0])
	}
	if got[0].ContextContent != "" || got[0].ContextChunkID != "" {
		t.Fatal("保留了外部索引中的过期上下文")
	}
}
