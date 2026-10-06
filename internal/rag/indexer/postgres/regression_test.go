package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	"github.com/sda1-hacker/humbert-agent/internal/rag/embeddinginput"
	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

func TestOutOfBoundsSourceRangeFailsBeforeEmbedding(t *testing.T) {
	embedder := &fakeEmbedder{dim: EmbeddingDimensions}
	db := &fakeDatabase{}
	cfg := DefaultConfig()
	cfg.Embedder = embedder
	p := newIndexerWithDatabase(db, cfg)
	batch := testParentChildBatch()
	batch.Children[0].EndRune = len([]rune(batch.Markdown)) + 1
	if err := storeBatch(p, context.Background(), batch); !errors.Is(err, rag.ErrInvalidIngestionBatch) {
		t.Fatalf("%v", err)
	}
	if len(embedder.calls) > 0 || db.begun {
		t.Fatal("invalid coordinates reached embedding/storage")
	}
}

func TestCompleteSearchInputIsBudgetedBeforeEmbedding(t *testing.T) {
	embedder := &fakeEmbedder{dim: EmbeddingDimensions}
	db := &fakeDatabase{}
	cfg := DefaultConfig()
	cfg.Embedder = embedder
	cfg.InputBudget = embeddinginput.Budget{MaxInputTokens: 32}
	p := newIndexerWithDatabase(db, cfg)
	batch := testParentChildBatch()
	batch.Title = strings.Repeat("title", 100)
	for i := range batch.Children {
		batch.Children[i].Metadata = nil
		batch.Children[i].ContextHeader = ""
		batch.Children[i].Content = "body"
	}
	if err := storeBatch(p, context.Background(), batch); !errors.Is(err, embeddinginput.ErrInputBudget) {
		t.Fatalf("%v", err)
	}
	if len(embedder.calls) > 0 || db.begun {
		t.Fatal("oversized title reached provider/storage")
	}
}

// 完整文档的子块不能只传一部分，错误应在模型和事务调用前发现。
func TestStoreRejectsPartialBatch(t *testing.T) {
	db := &fakeDatabase{}
	embedder := &fakeEmbedder{dim: EmbeddingDimensions}
	cfg := DefaultConfig()
	cfg.Embedder = embedder
	p := newIndexerWithDatabase(db, cfg)
	batch := testParentChildBatch()
	docs := rag.IndexDocuments(batch, searchcontent.DefaultBuilder())
	_, err := p.Store(context.Background(), docs[:1], indexer.WithIndex(batch.CollectionID), rag.WithIngestionBatch(batch))
	if !errors.Is(err, ErrIncompleteSource) || len(embedder.calls) > 0 || db.begun {
		t.Fatalf("部分替换未提前拒绝: %v", err)
	}
}

func TestZeroVectorCannotBeIndexed(t *testing.T) {
	for _, value := range []float64{0, 1e-40} {
		v := make([]float64, EmbeddingDimensions)
		for i := range v {
			v[i] = value
		}
		if _, err := makeHalfVector(v); !errors.Is(err, ErrInvalidEmbedding) {
			t.Fatalf("zero half vector accepted: %v", err)
		}
	}
}

func TestStaleAttemptDoesNotDeletePublishedChunks(t *testing.T) {
	tx := &fakeTransaction{staleAttempt: true}
	db := &fakeDatabase{tx: tx}
	cfg := DefaultConfig()
	cfg.Embedder = &fakeEmbedder{dim: EmbeddingDimensions}
	p := newIndexerWithDatabase(db, cfg)
	batch := testParentChildBatch()
	batch.Attempt = 2
	if err := storeBatch(p, context.Background(), batch); !errors.Is(err, ErrStaleIngestion) {
		t.Fatalf("%v", err)
	}
	for _, call := range tx.calls {
		if strings.Contains(call.sql, "DELETE FROM chunks") {
			t.Fatal("stale worker deleted published chunks")
		}
	}
	if tx.committed || !tx.rolledBack {
		t.Fatal("stale transaction was not rolled back")
	}
}

func TestBackendVersionRequirements(t *testing.T) {
	for _, s := range []BackendStatus{{150000, "0.7.0", "0.25.0"}, {170000, "0.8.1", "0.100.0"}} {
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []BackendStatus{{140000, "0.8.1", "0.25.0"}, {170000, "0.6.2", "0.25.0"}, {170000, "0.8.1", "0.21.0"}, {170000, "0.8.1", ""}} {
		if s.Validate() == nil {
			t.Fatalf("incompatible backend accepted: %+v", s)
		}
	}
}

func TestSameAttemptSnapshotIncludesChangedEmbeddingInputs(t *testing.T) {
	batch := testParentChildBatch()
	first, err := processSnapshot(batch, []string{"initial title and body"}, "index_input_hash")
	if err != nil {
		t.Fatal(err)
	}
	again, err := processSnapshot(batch, []string{"initial title and body"}, "index_input_hash")
	if err != nil || string(first) != string(again) {
		t.Fatal("non-deterministic snapshot")
	}
	changed, err := processSnapshot(batch, []string{"changed title and body"}, "index_input_hash")
	if err != nil || string(first) == string(changed) {
		t.Fatal("embedding input was not included in snapshot")
	}
	batch.Children[0].ContextHeader = "changed breadcrumb"
	changed, err = processSnapshot(batch, []string{"initial title and body"}, "index_input_hash")
	if err != nil || string(first) == string(changed) {
		t.Fatal("citation content was not included in snapshot")
	}
}

func TestPinnedProfileRejectsEmbeddingOverridesBeforeModelOrDatabase(t *testing.T) {
	for _, option := range []indexer.Option{indexer.WithEmbedding(&fakeEmbedder{dim: EmbeddingDimensions}), WithEmbeddingOptions(embedding.WithModel("other-model"))} {
		db := &fakeDatabase{}
		embedder := &fakeEmbedder{dim: EmbeddingDimensions}
		cfg := DefaultConfig()
		cfg.CollectionID, cfg.Embedder, cfg.ProfileID = "kb", embedder, "pinned-profile"
		p := newIndexerWithDatabase(db, cfg)
		_, err := p.Store(context.Background(), []*schema.Document{{ID: "doc", Content: "正文"}}, option)
		if !errors.Is(err, ErrPinnedEmbedding) || db.begun || len(embedder.calls) != 0 {
			t.Fatalf("固定模型被覆盖: %v", err)
		}
	}
}
