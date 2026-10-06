package rag_test

import (
	"context"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	"testing"
)

type customLoader struct{}

func (customLoader) Load(context.Context, document.Source, ...document.LoaderOption) ([]*schema.Document, error) {
	return []*schema.Document{{ID: "source", Content: "文档正文", MetaData: map[string]any{"title": "标题"}}}, nil
}

// 只实现 Eino 两个原生接口，不读取任何 RAG 专属 Option，也不依赖 PostgreSQL。
type einoBackend struct {
	docs  []*schema.Document
	index string
}

var _ indexer.Indexer = (*einoBackend)(nil)
var _ retriever.Retriever = (*einoBackend)(nil)

func (b *einoBackend) Store(_ context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error) {
	common := indexer.GetCommonOptions(nil, opts...)
	b.index = *common.Index
	b.docs = docs
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	return ids, nil
}
func (b *einoBackend) Retrieve(_ context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	common := retriever.GetCommonOptions(nil, opts...)
	if *common.Index != b.index {
		return nil, nil
	}
	var docs []*schema.Document
	for _, d := range b.docs {
		copy := *d
		copy.MetaData = map[string]any{}
		for k, v := range d.MetaData {
			copy.MetaData[k] = v
		}
		docs = append(docs, copy.WithScore(.9))
	}
	return docs, nil
}

func TestRuntimeUsesUnmodifiedEinoInterfaces(t *testing.T) {
	ctx := context.Background()
	backend := &einoBackend{}
	closed := 0
	runtime, err := rag.New(ctx, rag.Dependencies{Loader: customLoader{}, Indexer: backend, Config: rag.Config{Retriever: backend}, Close: func() { closed++ }})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ingested, err := runtime.Ingest(ctx, rag.IngestRequest{CollectionID: "kb", DocumentID: "doc"})
	if err != nil || ingested.ChildCount != 1 || len(ingested.Documents[0].ChunkIDs) != 1 {
		t.Fatalf("%+v %v", ingested, err)
	}
	// 官方组件直接接收统一的检索表示，原文不能被标题污染。
	if backend.docs[0].Content != "标题\n\n文档正文" {
		t.Fatalf("错误索引文本: %q", backend.docs[0].Content)
	}
	result, err := runtime.Search(ctx, rag.SearchRequest{CollectionID: "kb", Query: "正文"})
	if err != nil || len(result.Results) != 1 || result.Results[0].Content != "文档正文" || result.Results[0].DocumentID != "doc" || result.Results[0].Score != .9 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := runtime.Search(ctx, rag.SearchRequest{CollectionID: "other", Query: "正文"}); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	runtime.Close()
	if closed != 1 {
		t.Fatal("资源重复清理")
	}
}
