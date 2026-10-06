package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
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

func (f *fakeStore) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	batch := indexer.GetImplSpecificOptions(&IngestionOptions{}, opts...).Batch
	if batch == nil {
		return nil, errors.New("missing batch")
	}
	f.batches = append(f.batches, *batch)
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	return ids, nil
}

type fakeSearchEngine struct {
	query, collectionID string
	topK                int
}

func (f *fakeSearchEngine) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	f.query = query
	common := retriever.GetCommonOptions(nil, opts...)
	f.collectionID = *common.Index
	f.topK = *common.TopK
	return []*schema.Document{(&schema.Document{ID: "hit", Content: "正文"}).WithScore(.9)}, nil
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

	service := newTestService(t, loader, store, Config{})

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
	service := newTestService(t, loader, store, Config{})

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
	service := newTestService(t, loader, store, Config{})

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
	service := newTestService(t, &fakeLoader{}, &fakeStore{}, Config{})

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

	service := newTestService(t, loader, &fakeStore{}, Config{})

	_, err := service.Ingest(context.Background(), IngestRequest{
		CollectionID: "kb",
	})

	if !errors.Is(err, ErrMissingDocumentID) {
		t.Fatalf("缺少 Document ID 应失败: %v", err)
	}
}

func TestSearchCallsNativeRetriever(t *testing.T) {
	engine := &fakeSearchEngine{}
	service := newTestService(t, nil, nil, Config{Retriever: engine})
	r, err := service.Search(context.Background(), SearchRequest{CollectionID: "kb-123", Query: "Linux 安装", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if engine.collectionID != "kb-123" || engine.query != "Linux 安装" || engine.topK != 8 || len(r.Results) != 1 || r.Results[0].Score != .9 {
		t.Fatalf("原生参数或结果没有正确传递: %+v %+v", engine, r)
	}
}
func TestSearchRejectsEmptyQuery(t *testing.T) {
	service := newTestService(t, nil, nil, Config{})
	_, err := service.Search(context.Background(), SearchRequest{CollectionID: "kb", Query: " "})
	if !errors.Is(err, ErrEmptyQuery) {
		t.Fatal(err)
	}
}

// 测试也通过唯一构造入口创建服务，避免测试与实际运行使用不同初始化规则。
func newTestService(t *testing.T, loader document.Loader, idx indexer.Indexer, cfg Config) *Service {
	t.Helper()
	if loader == nil && cfg.Retriever == nil {
		cfg.Retriever = &fakeSearchEngine{}
	}
	service, err := New(context.Background(), Dependencies{Loader: loader, Indexer: idx, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
