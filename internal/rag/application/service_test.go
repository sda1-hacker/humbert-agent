package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
)

type fakeLoader struct {
	docs []*schema.Document
	err  error
}

func (f *fakeLoader) Load(
	ctx context.Context,
	src document.Source,
	_ ...document.LoaderOption,
) ([]*schema.Document, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.docs, nil
}

type fakeStore struct {
	batches []IngestionBatch
	err     error
}

func (f *fakeStore) ReplaceDocument(ctx context.Context, batch IngestionBatch) error {
	if f.err != nil {
		return f.err
	}

	f.batches = append(f.batches, batch)
	return nil
}

type fakeSearchEngine struct {
	response search.Response
	err      error

	query string
}

func (f *fakeSearchEngine) Search(ctx context.Context, query string) (search.Response, error) {
	f.query = query

	if f.err != nil {
		return search.Response{}, f.err
	}

	return f.response, nil
}

type fakeSearchFactory struct {
	engine SearchEngine
	err    error

	collectionID string
}

func (f *fakeSearchFactory) ForCollection(collectionID string) (SearchEngine, error) {
	f.collectionID = collectionID

	if f.err != nil {
		return nil, f.err
	}

	return f.engine, nil
}

func TestIngestFlatDocument(t *testing.T) {
	loader := &fakeLoader{
		docs: []*schema.Document{
			{
				ID:      "doc-1",
				Content: "第一段正文。\r\n\r\n第二段正文。",
				MetaData: map[string]any{
					"_title":    "测试文档",
					"file_name": "demo.md",
				},
			},
		},
	}

	store := &fakeStore{}

	service := NewService(loader, store, nil)

	cfg := chunker.DefaultConfig()
	cfg.Strategy = chunker.StrategyLegacy
	cfg.ChunkSize = 20

	response, err := service.Ingest(context.Background(), IngestRequest{
		CollectionID: "kb-1",
		Source:       document.Source{URI: "/tmp/demo.md"},
		Splitter:     cfg,
	})

	if err != nil {
		t.Fatal(err)
	}

	if len(store.batches) != 1 {
		t.Fatalf("应该保存一个 Document Batch: %d", len(store.batches))
	}

	batch := store.batches[0]

	if batch.DocumentID != "doc-1" {
		t.Fatalf("DocumentID 错误: %q", batch.DocumentID)
	}

	if strings.Contains(batch.Markdown, "\r") {
		t.Fatalf("Application 层应再次保证 LF 规范化: %q", batch.Markdown)
	}

	if batch.Title != "测试文档" {
		t.Fatalf("Title 错误: %q", batch.Title)
	}

	if len(batch.Parents) != 0 {
		t.Fatalf("Flat 模式不应该产生 Parent: %d", len(batch.Parents))
	}

	if len(batch.Children) == 0 {
		t.Fatal("Flat 模式应该产生可检索 Chunk")
	}

	for _, child := range batch.Children {
		if child.ChunkType != ChunkTypeText {
			t.Fatalf("普通 Chunk 应为 text: %+v", child)
		}

		if child.ParentChunkID != "" {
			t.Fatalf("Flat Chunk 不应该拥有 Parent: %+v", child)
		}
	}

	if response.ChildCount != len(batch.Children) {
		t.Fatalf("Response ChildCount 错误: %+v", response)
	}
}

func TestIngestParentChildDocument(t *testing.T) {
	body := strings.Repeat("这是用于 Parent Child 测试的正文内容。", 80)

	loader := &fakeLoader{
		docs: []*schema.Document{
			{
				ID:      "doc-parent-child",
				Content: body,
				MetaData: map[string]any{
					"_title": "Parent Child Document",
				},
			},
		},
	}

	store := &fakeStore{}
	service := NewService(loader, store, nil)

	cfg := chunker.DefaultConfig()
	cfg.Strategy = chunker.StrategyLegacy
	cfg.Separators = []string{"。"}

	_, err := service.Ingest(context.Background(), IngestRequest{
		CollectionID:    "kb-1",
		Source:          document.Source{URI: "/tmp/parent-child.md"},
		Splitter:        cfg,
		ParentChild:     true,
		ParentChunkSize: 500,
		ChildChunkSize:  120,
	})

	if err != nil {
		t.Fatal(err)
	}

	if len(store.batches) != 1 {
		t.Fatalf("应该写一个 Batch: %d", len(store.batches))
	}

	batch := store.batches[0]

	if len(batch.Parents) == 0 {
		t.Fatal("Parent-Child 模式应该产生 Parent")
	}

	if len(batch.Children) <= len(batch.Parents) {
		t.Fatalf(
			"测试数据应该产生比 Parent 更多的 Child: parents=%d children=%d",
			len(batch.Parents),
			len(batch.Children),
		)
	}

	parentIDs := make(map[string]struct{})

	for _, parent := range batch.Parents {
		if parent.ChunkType != ChunkTypeParentText {
			t.Fatalf("Parent ChunkType 错误: %+v", parent)
		}

		parentIDs[parent.ID] = struct{}{}
	}

	foundParentLink := false

	for _, child := range batch.Children {
		if child.ChunkType != ChunkTypeText {
			t.Fatalf("Child ChunkType 错误: %+v", child)
		}

		if child.ParentChunkID == "" {
			continue
		}

		foundParentLink = true

		if _, ok := parentIDs[child.ParentChunkID]; !ok {
			t.Fatalf(
				"Child 指向不存在的 Parent: child=%s parent=%s",
				child.ID,
				child.ParentChunkID,
			)
		}
	}

	if !foundParentLink {
		t.Fatal("应该至少存在一个 Child → Parent 关系")
	}
}

func TestIngestKeepsRedundantParentElimination(t *testing.T) {
	loader := &fakeLoader{
		docs: []*schema.Document{
			{
				ID:      "short-doc",
				Content: "这是一个很短的文档。",
			},
		},
	}

	store := &fakeStore{}
	service := NewService(loader, store, nil)

	_, err := service.Ingest(context.Background(), IngestRequest{
		CollectionID:    "kb",
		Source:          document.Source{URI: "/tmp/short.md"},
		Splitter:        chunker.DefaultConfig(),
		ParentChild:     true,
		ParentChunkSize: 4096,
		ChildChunkSize:  384,
	})

	if err != nil {
		t.Fatal(err)
	}

	batch := store.batches[0]

	if len(batch.Parents) != 0 {
		t.Fatalf("Parent 与唯一 Child 相同时不应该保存 Parent: %d", len(batch.Parents))
	}

	if len(batch.Children) != 1 {
		t.Fatalf("短文档应该只有一个 Child: %d", len(batch.Children))
	}

	if batch.Children[0].ParentChunkID != "" {
		t.Fatalf("没有 Parent 时 ParentChunkID 应为空: %+v", batch.Children[0])
	}
}

func TestIngestRequiresCollectionID(t *testing.T) {
	service := NewService(&fakeLoader{}, &fakeStore{}, nil)

	_, err := service.Ingest(context.Background(), IngestRequest{})

	if !errors.Is(err, ErrMissingCollectionID) {
		t.Fatalf("应该返回 ErrMissingCollectionID: %v", err)
	}
}

func TestIngestRejectsDocumentWithoutID(t *testing.T) {
	loader := &fakeLoader{
		docs: []*schema.Document{
			{
				Content: "正文。",
			},
		},
	}

	service := NewService(loader, &fakeStore{}, nil)

	_, err := service.Ingest(context.Background(), IngestRequest{
		CollectionID: "kb",
	})

	if !errors.Is(err, ErrMissingDocumentID) {
		t.Fatalf("缺少 Document ID 应失败: %v", err)
	}
}

func TestSearchDelegatesToCollectionEngine(t *testing.T) {
	engine := &fakeSearchEngine{
		response: search.Response{},
	}

	factory := &fakeSearchFactory{
		engine: engine,
	}

	service := NewService(nil, nil, factory)

	_, err := service.Search(context.Background(), SearchRequest{
		CollectionID: "kb-123",
		Query:        "Linux 安装",
	})

	if err != nil {
		t.Fatal(err)
	}

	if factory.collectionID != "kb-123" {
		t.Fatalf("CollectionID 没传给 SearchFactory: %q", factory.collectionID)
	}

	if engine.query != "Linux 安装" {
		t.Fatalf("Query 没传给 SearchEngine: %q", engine.query)
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	service := NewService(nil, nil, &fakeSearchFactory{})

	_, err := service.Search(context.Background(), SearchRequest{
		CollectionID: "kb",
		Query:        " ",
	})

	if !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("空 Query 应失败: %v", err)
	}
}
