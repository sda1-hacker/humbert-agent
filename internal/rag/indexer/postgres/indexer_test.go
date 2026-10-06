package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
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
	staleAttempt bool
	calls        []execCall
	execErr      error
	committed    bool
	rolledBack   bool
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

	if f.staleAttempt && strings.Contains(sql, "UPDATE rag_document_heads") {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
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

func TestStoreNativeOptionsAndDocument(t *testing.T) {
	defaultEmbedder := &fakeEmbedder{dim: EmbeddingDimensions}
	override := &fakeEmbedder{dim: EmbeddingDimensions}
	db := &fakeDatabase{}
	cfg := DefaultConfig()
	cfg.CollectionID = "default"
	cfg.Embedder = defaultEmbedder
	idx := newIndexerWithDatabase(db, cfg)
	doc := &schema.Document{ID: "document", Content: "真实原文", MetaData: map[string]any{"title": "文档标题"}}
	ids, err := idx.Store(context.Background(), []*schema.Document{doc}, indexer.WithIndex("kb"), indexer.WithEmbedding(override))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != doc.ID || len(defaultEmbedder.calls) != 0 || len(override.calls) != 1 {
		t.Fatal("Eino 调用级配置没有正确覆盖默认值")
	}
	if !db.tx.committed || len(db.tx.calls) != 4 {
		t.Fatalf("单文档写入应包含原文、删除旧块、分块、索引: %+v", db.tx)
	}
	for _, c := range db.tx.calls {
		if c.args[0] != "kb" {
			t.Fatalf("知识库范围未传递: %+v", c)
		}
		if strings.Contains(c.sql, "INSERT INTO documents") && c.args[3] != doc.Content {
			t.Fatal("普通 Eino 文档未保存完整原文")
		}
	}
}

func TestStoreRejectsInvalidInputBeforeEmbedding(t *testing.T) {
	for _, kind := range []string{"collection", "embedding", "subindex", "nil", "duplicate", "empty", "chunk-without-source", "dimension", "provider"} {
		t.Run(kind, func(t *testing.T) {
			db := &fakeDatabase{}
			embedder := &fakeEmbedder{dim: EmbeddingDimensions}
			cfg := DefaultConfig()
			cfg.CollectionID = "kb"
			cfg.Embedder = embedder
			docs := []*schema.Document{{ID: "document", Content: "正文"}}
			var opts []indexer.Option
			var expected error
			switch kind {
			case "collection":
				cfg.CollectionID = ""
				expected = ErrMissingCollectionID
			case "embedding":
				cfg.Embedder = nil
				expected = ErrMissingEmbedder
			case "subindex":
				opts = append(opts, indexer.WithSubIndexes([]string{"sub"}))
				expected = ErrSubIndexesUnsupported
			case "nil":
				docs[0] = nil
				expected = ErrMissingChunkID
			case "duplicate":
				docs = append(docs, docs[0])
			case "empty":
				docs[0].Content = " "
				expected = ErrEmptySearchContent
			case "chunk-without-source":
				docs[0].MetaData = map[string]any{retrieval.MetaSourceDocumentID: "source"}
				expected = ErrIncompleteSource
			case "dimension":
				embedder.dim = 768
				expected = ErrInvalidEmbedding
			case "provider":
				embedder.err = errors.New("model unavailable")
				expected = embedder.err
			}
			_, err := newIndexerWithDatabase(db, cfg).Store(context.Background(), docs, opts...)
			if err == nil || expected != nil && !errors.Is(err, expected) || db.begun {
				t.Fatalf("错误输入未提前拒绝: %v", err)
			}
		})
	}
}

func TestStoreBatchesActualIndexInputs(t *testing.T) {
	db := &fakeDatabase{}
	embedder := &fakeEmbedder{dim: EmbeddingDimensions}
	cfg := DefaultConfig()
	cfg.Embedder = embedder
	cfg.EmbeddingBatchSize = 1
	idx := newIndexerWithDatabase(db, cfg)
	batch := testParentChildBatch()
	if err := storeBatch(idx, context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if len(embedder.calls) != 2 {
		t.Fatalf("两条子块应分两次向量化: %+v", embedder.calls)
	}
	for i, c := range embedder.calls {
		if !strings.Contains(c[0], batch.Children[i].Content) || !strings.Contains(c[0], batch.Title) {
			t.Fatal("完整检索文本没有送给模型")
		}
	}
	deletes := 0
	for _, c := range db.tx.calls {
		if strings.Contains(c.sql, "DELETE FROM chunks") {
			deletes++
		}
	}
	if deletes != 1 {
		t.Fatalf("同一文档只应删除一次旧块: %d", deletes)
	}
}

func TestStoreRollbackOnWriteFailure(t *testing.T) {
	db := &fakeDatabase{tx: &fakeTransaction{execErr: errors.New("write failed")}}
	cfg := DefaultConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{dim: EmbeddingDimensions}
	_, err := newIndexerWithDatabase(db, cfg).Store(context.Background(), []*schema.Document{{ID: "doc", Content: "正文"}})
	if err == nil || db.tx.committed || !db.tx.rolledBack {
		t.Fatal("失败的入库没有回滚")
	}
}
