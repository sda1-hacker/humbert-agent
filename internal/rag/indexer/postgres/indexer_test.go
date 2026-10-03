package postgres

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

type fakeEmbedder struct {
	dim   int
	err   error
	calls [][]string
}

func (f *fakeEmbedder) EmbedStrings(
	ctx context.Context,
	texts []string,
	_ ...embedding.Option,
) ([][]float64, error) {
	if f.err != nil {
		return nil, f.err
	}

	copied := append([]string(nil), texts...)
	f.calls = append(f.calls, copied)

	vectors := make([][]float64, len(texts))

	for i := range texts {
		vectors[i] = make([]float64, f.dim)

		for j := range vectors[i] {
			vectors[i][j] = float64(j%17) / 100
		}
	}

	return vectors, nil
}

type execCall struct {
	sql  string
	args []any
}

type fakeTransaction struct {
	calls      []execCall
	execErr    error
	committed  bool
	rolledBack bool
}

func (f *fakeTransaction) Exec(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}

	f.calls = append(f.calls, execCall{
		sql:  sql,
		args: append([]any(nil), arguments...),
	})

	return pgconn.NewCommandTag("OK"), nil
}

func (f *fakeTransaction) Commit(context.Context) error {
	f.committed = true
	return nil
}

func (f *fakeTransaction) Rollback(context.Context) error {
	f.rolledBack = true
	return nil
}

type fakeDatabase struct {
	tx       *fakeTransaction
	beginErr error
	begun    bool
}

func (f *fakeDatabase) Begin(context.Context) (transaction, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}

	f.begun = true

	if f.tx == nil {
		f.tx = &fakeTransaction{}
	}

	return f.tx, nil
}

func TestIndexerImplementsEinoInterface(t *testing.T) {
	var _ indexer.Indexer = (*Indexer)(nil)
}

func TestStoreRequiresCollectionID(t *testing.T) {
	db := &fakeDatabase{}
	embedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	cfg := DefaultConfig()
	cfg.Embedder = embedder

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
	)

	if !errors.Is(err, ErrMissingCollectionID) {
		t.Fatalf(
			"缺少 CollectionID 应失败: got=%v",
			err,
		)
	}
}

func TestStoreAllowsCallLevelIndex(t *testing.T) {
	db := &fakeDatabase{}
	embedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	cfg := DefaultConfig()
	cfg.Embedder = embedder

	idx := newIndexerWithDatabase(db, cfg)

	ids, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
		indexer.WithIndex("knowledge-base-1"),
	)

	if err != nil {
		t.Fatalf("WithIndex 应提供 CollectionID: %v", err)
	}

	if len(ids) != 1 ||
		ids[0] != "doc-1#chunk-000000" {

		t.Fatalf("Store IDs 错误: %v", ids)
	}
}

func TestStoreRequiresEmbedder(t *testing.T) {
	db := &fakeDatabase{}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
	)

	if !errors.Is(err, ErrMissingEmbedder) {
		t.Fatalf(
			"缺少 Embedder 应失败: got=%v",
			err,
		)
	}
}

func TestStoreCallLevelEmbeddingOverridesDefault(t *testing.T) {
	db := &fakeDatabase{}

	defaultEmbedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	overrideEmbedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = defaultEmbedder

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
		indexer.WithEmbedding(overrideEmbedder),
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(defaultEmbedder.calls) != 0 {
		t.Fatal("默认 Embedder 不应该被调用")
	}

	if len(overrideEmbedder.calls) != 1 {
		t.Fatalf(
			"调用级 Embedder 应被调用一次: got=%d",
			len(overrideEmbedder.calls),
		)
	}
}

func TestStoreRejectsSubIndexes(t *testing.T) {
	db := &fakeDatabase{}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
		indexer.WithSubIndexes([]string{"a"}),
	)

	if !errors.Is(err, ErrSubIndexesUnsupported) {
		t.Fatalf(
			"当前 PGIndexer 不支持 SubIndexes: got=%v",
			err,
		)
	}
}

func TestStoreBuildsSearchContentBeforeEmbedding(t *testing.T) {
	db := &fakeDatabase{}
	embedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = embedder

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(embedder.calls) != 1 ||
		len(embedder.calls[0]) != 1 {

		t.Fatalf(
			"Embedder 调用异常: %#v",
			embedder.calls,
		)
	}

	got := embedder.calls[0][0]

	if !strings.Contains(got, "产品手册") {
		t.Fatalf(
			"SearchContent 缺少 title: %q",
			got,
		)
	}

	if !strings.Contains(got, "## 安装") {
		t.Fatalf(
			"SearchContent 缺少 ContextHeader: %q",
			got,
		)
	}

	if !strings.Contains(got, "安装正文") {
		t.Fatalf(
			"SearchContent 缺少 body: %q",
			got,
		)
	}
}

func TestStoreBatchesEmbedding(t *testing.T) {
	db := &fakeDatabase{}
	embedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = embedder
	cfg.EmbeddingBatchSize = 2

	idx := newIndexerWithDatabase(db, cfg)

	docs := []*schema.Document{
		validChunkDocumentWithIndex(0),
		validChunkDocumentWithIndex(1),
		validChunkDocumentWithIndex(2),
		validChunkDocumentWithIndex(3),
		validChunkDocumentWithIndex(4),
	}

	_, err := idx.Store(
		context.Background(),
		docs,
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(embedder.calls) != 3 {
		t.Fatalf(
			"5 个文档、batch=2 应调用3次 Embedder: got=%d",
			len(embedder.calls),
		)
	}

	if len(embedder.calls[0]) != 2 ||
		len(embedder.calls[1]) != 2 ||
		len(embedder.calls[2]) != 1 {

		t.Fatalf(
			"Embedding batch 切分错误: %#v",
			embedder.calls,
		)
	}
}

func TestStoreRejectsWrongEmbeddingDimensionBeforeTransaction(t *testing.T) {
	db := &fakeDatabase{}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{
		dim: 768,
	}

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
	)

	if !errors.Is(err, ErrInvalidEmbedding) {
		t.Fatalf(
			"维度错误应返回 ErrInvalidEmbedding: got=%v",
			err,
		)
	}

	if db.begun {
		t.Fatal(
			"Embedding 校验失败时不应该开启数据库事务",
		)
	}
}

func TestStoreRejectsMissingSourceDocumentID(t *testing.T) {
	db := &fakeDatabase{}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	idx := newIndexerWithDatabase(db, cfg)

	doc := validChunkDocument()
	delete(
		doc.MetaData,
		metaSourceDocumentID,
	)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{doc},
	)

	if !errors.Is(err, ErrMissingSourceDocumentID) {
		t.Fatalf(
			"缺少 Source Document ID 应失败: got=%v",
			err,
		)
	}
}

func TestStoreWritesDocumentChunkAndRetrievalRows(t *testing.T) {
	tx := &fakeTransaction{}
	db := &fakeDatabase{tx: tx}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
	)

	if err != nil {
		t.Fatal(err)
	}

	if !tx.committed {
		t.Fatal("事务应该 Commit")
	}

	var (
		sawDocumentInsert bool
		sawDelete         bool
		sawChunkInsert    bool
		sawRetrieval      bool
	)

	for _, call := range tx.calls {
		switch {
		case strings.Contains(
			call.sql,
			"INSERT INTO documents",
		):
			sawDocumentInsert = true

		case strings.Contains(
			call.sql,
			"DELETE FROM chunks",
		):
			sawDelete = true

		case strings.Contains(
			call.sql,
			"INSERT INTO chunks",
		):
			sawChunkInsert = true

		case strings.Contains(
			call.sql,
			"INSERT INTO retrieval_index",
		):
			sawRetrieval = true
		}
	}

	if !sawDocumentInsert {
		t.Fatal("没有写 documents")
	}

	if !sawDelete {
		t.Fatal(
			"重新索引前应该删除旧 document chunks",
		)
	}

	if !sawChunkInsert {
		t.Fatal("没有写 chunks")
	}

	if !sawRetrieval {
		t.Fatal("没有写 retrieval_index")
	}
}

func TestStoreDeletesOldChunksOnlyOncePerDocument(t *testing.T) {
	tx := &fakeTransaction{}
	db := &fakeDatabase{tx: tx}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocumentWithIndex(0),
			validChunkDocumentWithIndex(1),
			validChunkDocumentWithIndex(2),
		},
	)

	if err != nil {
		t.Fatal(err)
	}

	deleteCount := 0
	documentUpsertCount := 0

	for _, call := range tx.calls {
		if strings.Contains(
			call.sql,
			"DELETE FROM chunks",
		) {
			deleteCount++
		}

		if strings.Contains(
			call.sql,
			"INSERT INTO documents",
		) {
			documentUpsertCount++
		}
	}

	if deleteCount != 1 {
		t.Fatalf(
			"同一 Document 应只执行一次 replace delete: got=%d",
			deleteCount,
		)
	}

	if documentUpsertCount != 1 {
		t.Fatalf(
			"同一 Document 应只 upsert 一次: got=%d",
			documentUpsertCount,
		)
	}
}

func TestMetadataIntAcceptsJSONFloat64(t *testing.T) {
	metadata := map[string]any{
		"number": float64(42),
	}

	got, err := metadataInt(
		metadata,
		"number",
	)

	if err != nil {
		t.Fatal(err)
	}

	if got != 42 {
		t.Fatalf(
			"float64 integer 转换错误: got=%d",
			got,
		)
	}
}

func TestStripChunkMetadata(t *testing.T) {
	input := map[string]any{
		"title":               "文档",
		metaSourceDocumentID:  "doc-1",
		metaChunkIndex:        3,
		metaChunkStart:        100,
		metaChunkEnd:          200,
		metaContextHeader:     "## A",
		"business_custom_key": "keep",
	}

	got := stripChunkMetadata(input)

	if _, ok := got[metaChunkIndex]; ok {
		t.Fatal(
			"document metadata 不应包含 chunk index",
		)
	}

	if _, ok := got[metaChunkStart]; ok {
		t.Fatal(
			"document metadata 不应包含 chunk start",
		)
	}

	if _, ok := got[metaContextHeader]; ok {
		t.Fatal(
			"document metadata 不应包含 ContextHeader",
		)
	}

	if got["business_custom_key"] != "keep" {
		t.Fatal(
			"业务 metadata 不应该被删除",
		)
	}

	if got[metaSourceDocumentID] != "doc-1" {
		t.Fatal(
			"Source Document ID 应该保留",
		)
	}
}

func TestStoreEmbeddingFailureDoesNotBeginTransaction(t *testing.T) {
	db := &fakeDatabase{}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{
		dim: EmbeddingDimensions,
		err: errors.New("embedding service unavailable"),
	}

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{
			validChunkDocument(),
		},
	)

	if err == nil {
		t.Fatal(
			"Embedding Failure 应返回错误",
		)
	}

	if db.begun {
		t.Fatal(
			"Embedding 失败时不应该启动 PostgreSQL 事务",
		)
	}
}

func TestSearchBuilderConfigCanBeCustomized(t *testing.T) {
	db := &fakeDatabase{}
	embedder := &fakeEmbedder{
		dim: EmbeddingDimensions,
	}

	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = embedder
	cfg.SearchBuilder = searchcontent.Builder{
		TitleKeys: []string{
			"custom_title",
		},
		ContextHeaderKey: "custom_header",
	}

	doc := validChunkDocument()

	doc.MetaData["custom_title"] = "CUSTOM TITLE"
	doc.MetaData["custom_header"] = "CUSTOM HEADER"

	idx := newIndexerWithDatabase(db, cfg)

	_, err := idx.Store(
		context.Background(),
		[]*schema.Document{doc},
	)

	if err != nil {
		t.Fatal(err)
	}

	got := embedder.calls[0][0]

	if !strings.Contains(
		got,
		"CUSTOM TITLE",
	) {
		t.Fatalf(
			"Custom Search Builder title 没生效: %q",
			got,
		)
	}

	if !strings.Contains(
		got,
		"CUSTOM HEADER",
	) {
		t.Fatalf(
			"Custom Search Builder header 没生效: %q",
			got,
		)
	}
}

func validChunkDocument() *schema.Document {
	return validChunkDocumentWithIndex(0)
}

func validChunkDocumentWithIndex(
	index int,
) *schema.Document {
	return &schema.Document{
		ID: "doc-1#chunk-" + sixDigits(index),

		Content: "## 安装\n\n这里是安装正文。",

		MetaData: map[string]any{
			"title":                "产品手册",
			metaSourceDocumentID:   "doc-1",
			metaChunkIndex:         index,
			metaChunkStart:         index * 100,
			metaChunkEnd:           index*100 + 15,
			metaContextHeader:      "# 产品手册\n## 安装",
			"file_name":            "manual.pdf",
			"business_custom_data": "keep",
		},
	}
}

func sixDigits(value int) string {
	text := "000000" +
		strconv.Itoa(value)

	return text[len(text)-6:]
}
