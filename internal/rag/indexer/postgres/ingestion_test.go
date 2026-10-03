package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
)

func TestReplaceDocumentEmbedsOnlyChildren(t *testing.T) {
	tx := &fakeTransaction{}
	db := &fakeDatabase{tx: tx}

	embedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	cfg := DefaultConfig()
	cfg.Embedder = embedder

	indexer := newIndexerWithDatabase(db, cfg)

	batch := testParentChildBatch()

	if err := indexer.ReplaceDocument(context.Background(), batch); err != nil {
		t.Fatal(err)
	}

	// 当前测试：
	//
	// Parent 1个
	// Child  2个
	//
	// Embedder 应只收到2个 Child。
	totalEmbedded := 0

	for _, call := range embedder.calls {
		totalEmbedded += len(call)
	}

	if totalEmbedded != 2 {
		t.Fatalf(
			"只应该 Embed Children: want=2 got=%d",
			totalEmbedded,
		)
	}
}

func TestReplaceDocumentWritesParentsButDoesNotIndexThem(t *testing.T) {
	tx := &fakeTransaction{}
	db := &fakeDatabase{tx: tx}

	cfg := DefaultConfig()
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	indexer := newIndexerWithDatabase(db, cfg)

	if err := indexer.ReplaceDocument(context.Background(), testParentChildBatch()); err != nil {
		t.Fatal(err)
	}

	chunkInsertCount := 0
	retrievalInsertCount := 0

	for _, call := range tx.calls {
		if strings.Contains(call.sql, "INSERT INTO chunks") {
			chunkInsertCount++
		}

		if strings.Contains(call.sql, "INSERT INTO retrieval_index") {
			retrievalInsertCount++
		}
	}

	// 1 Parent + 2 Children
	if chunkInsertCount != 3 {
		t.Fatalf("chunks 应写3条: got=%d", chunkInsertCount)
	}

	// 只有2个 Children 进入 retrieval_index。
	if retrievalInsertCount != 2 {
		t.Fatalf(
			"retrieval_index 只应写 Children: want=2 got=%d",
			retrievalInsertCount,
		)
	}
}

func TestReplaceDocumentPersistsFullMarkdown(t *testing.T) {
	tx := &fakeTransaction{}
	db := &fakeDatabase{tx: tx}

	cfg := DefaultConfig()
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	indexer := newIndexerWithDatabase(db, cfg)

	batch := testParentChildBatch()
	batch.Markdown = "# 完整 Markdown\n\n这里是完整文档。"

	if err := indexer.ReplaceDocument(context.Background(), batch); err != nil {
		t.Fatal(err)
	}

	found := false

	for _, call := range tx.calls {
		if !strings.Contains(call.sql, "INSERT INTO documents") {
			continue
		}

		found = true

		if len(call.args) < 4 {
			t.Fatalf("Document UPSERT 参数不完整: %+v", call.args)
		}

		if call.args[3] != batch.Markdown {
			t.Fatalf(
				"documents.markdown 应保存完整 Loader 输出\nwant=%q\ngot =%v",
				batch.Markdown,
				call.args[3],
			)
		}
	}

	if !found {
		t.Fatal("没有执行 Document UPSERT")
	}
}

func TestReplaceDocumentWritesParentBeforeChild(t *testing.T) {
	tx := &fakeTransaction{}
	db := &fakeDatabase{tx: tx}

	cfg := DefaultConfig()
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	indexer := newIndexerWithDatabase(db, cfg)

	if err := indexer.ReplaceDocument(context.Background(), testParentChildBatch()); err != nil {
		t.Fatal(err)
	}

	var insertedIDs []string

	for _, call := range tx.calls {
		if !strings.Contains(call.sql, "INSERT INTO chunks") {
			continue
		}

		insertedIDs = append(insertedIDs, call.args[1].(string))
	}

	if len(insertedIDs) != 3 {
		t.Fatalf("预期3个 Chunk Insert: %v", insertedIDs)
	}

	if insertedIDs[0] != "doc-1#parent-000000" {
		t.Fatalf(
			"Parent 必须先于 Child 插入，满足 FK: %v",
			insertedIDs,
		)
	}
}

func TestReplaceDocumentRejectsUnknownParentBeforeEmbedding(t *testing.T) {
	db := &fakeDatabase{}

	embedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	cfg := DefaultConfig()
	cfg.Embedder = embedder

	indexer := newIndexerWithDatabase(db, cfg)

	batch := testParentChildBatch()
	batch.Children[0].ParentChunkID = "missing-parent"

	err := indexer.ReplaceDocument(context.Background(), batch)

	if !errors.Is(err, ErrUnknownParentChunk) {
		t.Fatalf("未知 Parent 应失败: %v", err)
	}

	if len(embedder.calls) != 0 {
		t.Fatal("Batch 校验失败时不应该调用 Embedder")
	}

	if db.begun {
		t.Fatal("Batch 校验失败时不应该开启事务")
	}
}

func TestReplaceDocumentIsAtomicAtDatabaseLayer(t *testing.T) {
	tx := &fakeTransaction{}
	db := &fakeDatabase{tx: tx}

	cfg := DefaultConfig()
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	indexer := newIndexerWithDatabase(db, cfg)

	if err := indexer.ReplaceDocument(context.Background(), testParentChildBatch()); err != nil {
		t.Fatal(err)
	}

	if !tx.committed {
		t.Fatal("ReplaceDocument 成功后必须 Commit")
	}
}

func testParentChildBatch() application.IngestionBatch {
	return application.IngestionBatch{
		CollectionID: "kb-1",
		DocumentID:   "doc-1",
		Title:        "产品手册",
		Markdown:     "# 产品手册\n\n完整正文。",
		Metadata: map[string]any{
			"_title":    "产品手册",
			"file_name": "manual.md",
		},
		Parents: []application.ChunkRecord{
			{
				ID:            "doc-1#parent-000000",
				DocumentID:    "doc-1",
				ChunkType:     application.ChunkTypeParentText,
				ChunkIndex:    0,
				Content:       "完整 Parent Content",
				ContextHeader: "# 产品手册",
				StartRune:     0,
				EndRune:       100,
				Metadata: map[string]any{
					"rag_chunk_type": application.ChunkTypeParentText,
				},
			},
		},
		Children: []application.ChunkRecord{
			{
				ID:            "doc-1#chunk-000000",
				DocumentID:    "doc-1",
				ChunkType:     application.ChunkTypeText,
				ChunkIndex:    0,
				Content:       "Linux 安装正文。",
				ContextHeader: "# 产品手册\n## Linux",
				StartRune:     0,
				EndRune:       20,
				ParentChunkID: "doc-1#parent-000000",
				Metadata: map[string]any{
					"_title":              "产品手册",
					"rag_context_header":  "# 产品手册\n## Linux",
					"rag_parent_chunk_id": "doc-1#parent-000000",
				},
			},
			{
				ID:            "doc-1#chunk-000001",
				DocumentID:    "doc-1",
				ChunkType:     application.ChunkTypeText,
				ChunkIndex:    1,
				Content:       "Windows 安装正文。",
				ContextHeader: "# 产品手册\n## Windows",
				StartRune:     20,
				EndRune:       40,
				ParentChunkID: "doc-1#parent-000000",
				Metadata: map[string]any{
					"_title":              "产品手册",
					"rag_context_header":  "# 产品手册\n## Windows",
					"rag_parent_chunk_id": "doc-1#parent-000000",
				},
			},
		},
	}
}
