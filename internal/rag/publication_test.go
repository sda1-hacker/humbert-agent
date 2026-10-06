package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	multiindexer "github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// 独立 Eino 后端只保存原生文档，不认识 IngestionBatch 或文档生命周期。
type externalIndex struct {
	docs       []*schema.Document
	fail       error
	changeID   bool
	afterStore func()
}

func (b *externalIndex) Store(_ context.Context, docs []*schema.Document, _ ...indexer.Option) ([]string, error) {
	ids := make([]string, len(docs))
	for i, doc := range docs {
		copy := *doc
		ids[i] = doc.ID
		if b.changeID {
			ids[i] = "backend-generated-id"
		}
		b.docs = append(b.docs, &copy)
	}
	if b.afterStore != nil {
		b.afterStore()
	}
	// 模拟已经写入部分内容后才失败的远程索引。
	return ids, b.fail
}
func (b *externalIndex) Retrieve(context.Context, string, ...retriever.Option) ([]*schema.Document, error) {
	return b.docs, nil
}

// 测试文档库保留发布版本。它与 Indexer 是两个完全独立的对象。
type documentStore struct {
	version      int64
	batch        IngestionBatch
	publications int
	deleted      bool
	publishErr   error
}

func (s *documentStore) ReserveDocument(context.Context, string, string) (int64, error) {
	s.version++
	s.deleted = false
	return s.version, nil
}
func (s *documentStore) InvalidateDocument(context.Context, string, string) error {
	s.version++
	return nil
}
func (s *documentStore) DeleteDocument(context.Context, string, string) error {
	s.version++
	s.deleted = true
	return nil
}
func (s *documentStore) PublishDocument(_ context.Context, batch IngestionBatch) error {
	if s.publishErr != nil {
		return s.publishErr
	}
	if batch.Attempt != s.version || s.deleted {
		return errors.New("版本已经失效")
	}
	s.batch = batch
	s.publications++
	return nil
}
func (s *documentStore) ListChunks(context.Context, string, string) ([]ChunkRecord, error) {
	if s.deleted {
		return nil, nil
	}
	return s.batch.Children, nil
}
func (s *documentStore) ResolveChunks(_ context.Context, collection string, hits []retrieval.SearchResult) ([]retrieval.SearchResult, error) {
	var visible []retrieval.SearchResult
	for _, hit := range hits {
		if s.deleted || collection != s.batch.CollectionID || hit.DocumentID != s.batch.DocumentID || hit.DocumentRevision != s.batch.Attempt {
			continue
		}
		for _, chunk := range s.batch.Children {
			if hit.ChunkID == chunk.ID {
				hit.Content, hit.ParentChunkID = chunk.Content, chunk.ParentChunkID
				hit.StartRune, hit.EndRune = chunk.StartRune, chunk.EndRune
				visible = append(visible, hit)
			}
		}
	}
	return visible, nil
}

func externalService(t *testing.T, loader *fakeLoader, idx indexer.Indexer, recall *externalIndex, store *documentStore) *Service {
	t.Helper()
	s, err := New(context.Background(), Dependencies{
		Loader: loader, Indexer: idx,
		Config:    Config{Retriever: recall, VectorRetriever: recall, KeywordRetriever: recall},
		Lifecycle: store, Publisher: store, PublishedChunks: store, ChunkReader: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExternalIndexesPublishOnlyAfterAllWritesSucceed(t *testing.T) {
	ctx := context.Background()
	loader := &fakeLoader{docs: []*schema.Document{{ID: "source-path-id", Content: "第一版原文"}}}
	vector, keyword, store := &externalIndex{}, &externalIndex{}, &documentStore{}
	idx, err := multiindexer.NewMulti(vector, keyword)
	if err != nil {
		t.Fatal(err)
	}
	s := externalService(t, loader, idx, vector, store)
	req := IngestRequest{CollectionID: "kb", DocumentID: "stable-doc"}
	first, err := s.Ingest(ctx, req)
	if err != nil || first.Documents[0].Attempt != 1 || store.batch.Markdown != "第一版原文" || len(keyword.docs) != 1 {
		t.Fatalf("原生组件与发布流程没有接通: %+v %v", first, err)
	}
	oldID := first.Documents[0].ChunkIDs[0]

	// 两个索引均可能留下未发布新版本，但查询必须继续返回旧版正文。
	loader.docs[0].Content = "第二版原文"
	keyword.fail = errors.New("关键词索引失败")
	if _, err := s.Ingest(ctx, req); !errors.Is(err, keyword.fail) {
		t.Fatal(err)
	}
	if store.publications != 1 || vector.docs[1].ID == oldID || keyword.docs[1].ID == oldID {
		t.Fatal("失败版本被发布，或覆盖了旧版本 ID")
	}
	for _, mode := range []string{"hybrid", "semantic", "keyword"} {
		result, err := s.Search(ctx, SearchRequest{CollectionID: "kb", Query: "原文", Mode: mode})
		if err != nil || len(result.Results) != 1 || result.Results[0].ChunkID != oldID || result.Results[0].Content != "第一版原文" {
			t.Fatalf("%s 泄漏未发布版本: %+v %v", mode, result, err)
		}
	}

	keyword.fail = nil
	if _, err := s.Ingest(ctx, req); err != nil {
		t.Fatal(err)
	}
	result, err := s.Search(ctx, SearchRequest{CollectionID: "kb", Query: "原文"})
	if err != nil || store.publications != 2 || len(result.Results) != 1 || result.Results[0].DocumentRevision != 3 || result.Results[0].Content != "第二版原文" {
		t.Fatalf("新发布版本未正确替换旧版: %+v %v", result, err)
	}
	if err := s.DeleteDocument(ctx, "kb", "stable-doc"); err != nil {
		t.Fatal(err)
	}
	result, err = s.Search(ctx, SearchRequest{CollectionID: "kb", Query: "原文"})
	if err != nil || len(result.Results) != 0 {
		t.Fatalf("删除文档的残留索引仍可见: %+v %v", result, err)
	}
}

func TestExternalPublicationRejectsChangedIDsAndPublicationFailure(t *testing.T) {
	for _, name := range []string{"changed-id", "publication-failed", "cancelled", "invalidated-during-index", "deleted-during-index"} {
		t.Run(name, func(t *testing.T) {
			loader := &fakeLoader{docs: []*schema.Document{{ID: "source", Content: "正文"}}}
			idx, store := &externalIndex{}, &documentStore{}
			if name == "changed-id" {
				idx.changeID = true
			}
			if name == "publication-failed" {
				store.publishErr = errors.New("权威库失败")
			}
			if name == "invalidated-during-index" {
				idx.afterStore = func() { _ = store.InvalidateDocument(context.Background(), "kb", "doc") }
			}
			if name == "deleted-during-index" {
				idx.afterStore = func() { _ = store.DeleteDocument(context.Background(), "kb", "doc") }
			}
			s := externalService(t, loader, idx, idx, store)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			if _, err := s.Ingest(ctx, IngestRequest{CollectionID: "kb", DocumentID: "doc"}); err == nil || store.publications != 0 {
				t.Fatalf("错误写入被发布: %v", err)
			}
		})
	}
}

func TestVersionedParentChildIDsKeepCorrectLinks(t *testing.T) {
	loader := &fakeLoader{docs: []*schema.Document{{ID: "source", Content: strings.Repeat("这是文档中的正文。", 200)}}}
	idx, store := &externalIndex{}, &documentStore{}
	s := externalService(t, loader, idx, idx, store)
	_, err := s.Ingest(context.Background(), IngestRequest{CollectionID: "kb", DocumentID: "doc", ParentChild: true, ParentChunkSize: 500, ChildChunkSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	parents := make(map[string]bool)
	for _, parent := range store.batch.Parents {
		parents[parent.ID] = true
	}
	if len(parents) == 0 {
		t.Fatal("测试文档未生成父块")
	}
	for i, child := range store.batch.Children {
		if !parents[child.ParentChunkID] || idx.docs[i].MetaData[retrieval.MetaParentChunkID] != child.ParentChunkID {
			t.Fatalf("版本化后父子引用断开: %+v", child)
		}
	}
}

func TestNewRejectsUnsafePublicationConfiguration(t *testing.T) {
	_, err := New(context.Background(), Dependencies{Loader: &fakeLoader{}, Indexer: &externalIndex{}, Publisher: &documentStore{}})
	if err == nil {
		t.Fatal("缺少版本与发布回查时不能接受外部发布")
	}
}

// 普通检索接口之外的诊断能力也必须穿过版本包装，供调用方识别降级。
type diagnosticIndex struct{ *externalIndex }

func (d diagnosticIndex) RetrieveWithDiagnostics(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, retrieval.Diagnostics, error) {
	docs, err := d.Retrieve(ctx, query, opts...)
	return docs, retrieval.Diagnostics{ModeUsed: retrieval.MatchVector, Degraded: true, Channels: []retrieval.ChannelDiagnostic{{Channel: retrieval.MatchKeyword, Error: "关键词不可用"}}}, err
}

func TestPublishedWrapperKeepsRetrievalDiagnostics(t *testing.T) {
	loader := &fakeLoader{docs: []*schema.Document{{ID: "source", Content: "正文"}}}
	idx, store := &externalIndex{}, &documentStore{}
	s := externalService(t, loader, idx, idx, store)
	if _, err := s.Ingest(context.Background(), IngestRequest{CollectionID: "kb", DocumentID: "doc"}); err != nil {
		t.Fatal(err)
	}
	reader, err := New(context.Background(), Dependencies{Config: Config{Retriever: diagnosticIndex{idx}}, PublishedChunks: store})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Search(context.Background(), SearchRequest{CollectionID: "kb", Query: "正文"})
	if err != nil || !result.Retrieval.Degraded || result.Retrieval.ModeUsed != retrieval.MatchVector || len(result.Retrieval.Channels) != 1 || len(result.Results) != 1 {
		t.Fatalf("版本包装吞掉了降级诊断: %+v %v", result, err)
	}
}
