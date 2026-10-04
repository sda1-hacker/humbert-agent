package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
	indexpg "github.com/sda1-hacker/humbert-agent/internal/rag/indexer/postgres"
	retrievepg "github.com/sda1-hacker/humbert-agent/internal/rag/retriever/postgres"
)

// These tests create and drop only their own randomly named schema. The DSN
// must point at a dedicated test database with extensions already installed.
func integrationPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("HUMBERT_RAG_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set HUMBERT_RAG_TEST_DATABASE_URL to run real PostgreSQL RAG tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(logging.SafeErrorText(err, 2048))
	}
	if err = indexpg.CheckExtensions(ctx, conn); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	var suffix [12]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	schemaName := "humbert_rag_test_" + hex.EncodeToString(suffix[:])
	quoted := pgx.Identifier{schemaName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if _, err := conn.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("cleanup test schema: %s", logging.SafeErrorText(err, 2048))
		}
		conn.Close(cleanup)
	})
	var testDSN string
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, parseErr := url.Parse(dsn)
		if parseErr != nil {
			t.Fatal("invalid test database URL")
		}
		q := u.Query()
		q.Set("search_path", schemaName+",public")
		u.RawQuery = q.Encode()
		testDSN = u.String()
	} else {
		testDSN = dsn + " search_path='" + schemaName + ",public'"
	}
	pool, err := indexpg.NewPool(ctx, testDSN)
	if err != nil {
		t.Fatal(logging.SafeErrorText(err, 2048))
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

type integrationEmbedder struct{ fail bool }

func (e *integrationEmbedder) EmbedStrings(ctx context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.fail {
		return nil, errors.New("injected embedding failure")
	}
	result := make([][]float64, len(texts))
	for i := range result {
		result[i] = make([]float64, indexpg.EmbeddingDimensions)
		result[i][0] = 1
	}
	return result, nil
}

func migratedPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx, pool := integrationPool(t)
	if err := indexpg.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := indexpg.Migrate(ctx, pool); err != nil {
		t.Fatalf("migration is not repeatable: %v", err)
	}
	if err := indexpg.CheckSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}

func versionedIndexer(t *testing.T, pool *pgxpool.Pool, e *integrationEmbedder) *indexpg.Indexer {
	t.Helper()
	cfg := indexpg.DefaultConfig()
	cfg.Embedder = e
	cfg.VersionedDocuments = true
	cfg.ProfileID = "integration-model-v1"
	cfg.ProfileJSON = `{"model":"deterministic-sql-test"}`
	p, err := indexpg.NewIndexer(pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func flatBatch(collection, doc, content string, attempt int64) application.IngestionBatch {
	return application.IngestionBatch{CollectionID: collection, DocumentID: doc, Attempt: attempt, Title: "SQL test", Markdown: content,
		Children: []application.ChunkRecord{{ID: doc + "-child", DocumentID: doc, ChunkType: application.ChunkTypeText, Content: content, EndRune: len([]rune(content))}}}
}

func publish(t *testing.T, ctx context.Context, p *indexpg.Indexer, collection, doc, content string) application.IngestionBatch {
	t.Helper()
	attempt, err := p.ReserveDocument(ctx, collection, doc)
	if err != nil {
		t.Fatal(err)
	}
	batch := flatBatch(collection, doc, content, attempt)
	if err = p.ReplaceDocument(ctx, batch); err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestIntegrationLifecycleSearchAndIsolation(t *testing.T) {
	ctx, pool := migratedPool(t)
	e := &integrationEmbedder{}
	p := versionedIndexer(t, pool, e)
	a := publish(t, ctx, p, "a", "same-id", "alphaonly installation 初始版本")
	if err := p.ReplaceDocument(ctx, a); err != nil {
		t.Fatalf("identical attempt retry failed: %v", err)
	}
	changedTitle := a
	changedTitle.Title = "changed embedding title"
	if err := p.ReplaceDocument(ctx, changedTitle); !errors.Is(err, indexpg.ErrStaleIngestion) {
		t.Fatalf("same revision accepted changed embedding input: %v", err)
	}
	publish(t, ctx, p, "b", "same-id", "betaonly configuration 另一个知识库")
	for _, mode := range []string{"semantic", "keyword", "hybrid"} {
		t.Run(mode, func(t *testing.T) {
			cfg := retrievepg.DefaultHybridConfig()
			cfg.CollectionID = "a"
			cfg.Embedder = e
			cfg.VectorThreshold = 0
			factory, err := retrievepg.NewPipelineFactory(pool, retrievepg.PipelineFactoryConfig{Hybrid: cfg})
			if err != nil {
				t.Fatal(err)
			}
			engine, err := factory.ForRequest(application.SearchRequest{CollectionID: "a", Mode: mode, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			r, err := engine.Search(ctx, "alphaonly")
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Results) != 1 || r.Results[0].CollectionID != "a" || r.Results[0].DocumentRevision != a.Attempt || !strings.Contains(r.Results[0].Content, "alphaonly") {
				t.Fatalf("isolation/revision failure: %+v", r)
			}
		})
	}
	newer := publish(t, ctx, p, "a", "same-id", "replacementonly installation 新版本")
	if err := p.ReplaceDocument(ctx, a); !errors.Is(err, indexpg.ErrStaleIngestion) {
		t.Fatalf("old attempt accepted: %v", err)
	}
	if err := p.InvalidateDocument(ctx, "a", "same-id"); err != nil {
		t.Fatal(err)
	}
	if err := p.ReplaceDocument(ctx, newer); !errors.Is(err, indexpg.ErrStaleIngestion) {
		t.Fatalf("canceled attempt accepted: %v", err)
	}
	latest := publish(t, ctx, p, "a", "same-id", "latestonly installation 可用版本")
	if err := p.DeleteDocument(ctx, "a", "same-id"); err != nil {
		t.Fatal(err)
	}
	if err := p.ReplaceDocument(ctx, latest); !errors.Is(err, indexpg.ErrStaleIngestion) {
		t.Fatalf("deleted document resurrected: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM retrieval_index WHERE collection_id='a'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale index rows: %d %v", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM retrieval_index WHERE collection_id='b'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cross-collection deletion: %d %v", count, err)
	}
	if err := indexpg.EnsureCollectionProfile(ctx, pool, "b", "changed-model", `{}`); !errors.Is(err, indexpg.ErrEmbeddingProfileMismatch) {
		t.Fatalf("model mismatch accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE retrieval_index SET enabled=false WHERE collection_id='b'`); err != nil {
		t.Fatal(err)
	}
	vector, err := retrievepg.NewVectorRetriever(pool, retrievepg.VectorConfig{CollectionID: "b", Embedder: e, ScoreThreshold: 0})
	if err != nil {
		t.Fatal(err)
	}
	if results, err := vector.Search(ctx, "betaonly"); err != nil || len(results) != 0 {
		t.Fatalf("disabled vector returned: %+v %v", results, err)
	}
	keyword, err := retrievepg.NewBM25Retriever(pool, retrievepg.BM25Config{CollectionID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if results, err := keyword.Search(ctx, "betaonly"); err != nil || len(results) != 0 {
		t.Fatalf("disabled keyword returned: %+v %v", results, err)
	}
}

func TestIntegrationParentForeignKeyAndAtomicRollback(t *testing.T) {
	ctx, pool := migratedPool(t)
	e := &integrationEmbedder{}
	p := versionedIndexer(t, pool, e)
	batch := publish(t, ctx, p, "kb", "parent-doc", "tabletoken one two three")
	batch.Parents = []application.ChunkRecord{{ID: "parent", DocumentID: batch.DocumentID, ChunkType: application.ChunkTypeParentText, Content: batch.Markdown, EndRune: len([]rune(batch.Markdown))}}
	batch.Children[0].ParentChunkID = "parent"
	var err error
	batch.Attempt, err = p.ReserveDocument(ctx, "kb", "parent-doc")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ReplaceDocument(ctx, batch); err != nil {
		t.Fatal(err)
	}
	var chunks, indexes int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM chunks),(SELECT count(*) FROM retrieval_index)`).Scan(&chunks, &indexes); err != nil || chunks != 2 || indexes != 1 {
		t.Fatalf("parent incorrectly indexed: %d %d %v", chunks, indexes, err)
	}
	loader, err := retrievepg.NewParentLoader(pool, "kb")
	if err != nil {
		t.Fatal(err)
	}
	parents, err := loader.LoadParents(ctx, []string{"parent"})
	if err != nil || parents["parent"].DocumentRevision != batch.Attempt {
		t.Fatalf("parent load: %+v %v", parents, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM chunks WHERE collection_id='kb' AND id='parent'`); err != nil {
		t.Fatalf("parent FK delete: %v", err)
	}
	var collection string
	var parentID *string
	if err := pool.QueryRow(ctx, `SELECT collection_id,parent_chunk_id FROM chunks`).Scan(&collection, &parentID); err != nil || collection != "kb" || parentID != nil {
		t.Fatalf("FK nulled collection: %q %v %v", collection, parentID, err)
	}
	old := publish(t, ctx, p, "kb", "rollback-doc", "oldtoken published")
	for _, sql := range []string{
		`CREATE FUNCTION reject_test_row() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.search_content LIKE '%force_fail%' THEN RAISE EXCEPTION 'injected SQL failure'; END IF; RETURN NEW; END $$`,
		`CREATE TRIGGER reject_test_row BEFORE INSERT ON retrieval_index FOR EACH ROW EXECUTE FUNCTION reject_test_row()`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	attempt, err := p.ReserveDocument(ctx, "kb", "rollback-doc")
	if err != nil {
		t.Fatal(err)
	}
	bad := flatBatch("kb", "rollback-doc", "force_fail new generation", attempt)
	if err = p.ReplaceDocument(ctx, bad); err == nil {
		t.Fatal("injected SQL failure ignored")
	}
	e.fail = true
	if err = p.ReplaceDocument(ctx, bad); err == nil {
		t.Fatal("injected embedding failure ignored")
	}
	e.fail = false
	var markdown string
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT markdown,revision FROM documents WHERE collection_id='kb' AND id='rollback-doc'`).Scan(&markdown, &revision); err != nil || markdown != old.Markdown || revision != old.Attempt {
		t.Fatalf("failed replacement lost published document: %q %d %v", markdown, revision, err)
	}
	if err := pool.QueryRow(ctx, `SELECT content FROM chunks WHERE collection_id='kb' AND document_id='rollback-doc'`).Scan(&markdown); err != nil || markdown != old.Markdown {
		t.Fatalf("rollback lost chunks: %q %v", markdown, err)
	}
}

func TestIntegrationLegacySchemaUpgrade(t *testing.T) {
	ctx, pool := integrationPool(t)
	// Known pre-parent-child schema: no markdown/type/header/parent columns,
	// and no independent ParadeDB ID. Adoption must retain the existing row.
	for _, sql := range []string{
		`CREATE TABLE documents(collection_id TEXT NOT NULL,id TEXT NOT NULL,title TEXT NOT NULL DEFAULT '',metadata JSONB NOT NULL DEFAULT '{}',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),PRIMARY KEY(collection_id,id))`,
		`CREATE TABLE chunks(collection_id TEXT NOT NULL,id TEXT NOT NULL,document_id TEXT NOT NULL,chunk_index INTEGER NOT NULL,content TEXT NOT NULL,start_rune INTEGER NOT NULL,end_rune INTEGER NOT NULL,metadata JSONB NOT NULL DEFAULT '{}',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),PRIMARY KEY(collection_id,id),UNIQUE(collection_id,document_id,chunk_index),FOREIGN KEY(collection_id,document_id) REFERENCES documents(collection_id,id) ON DELETE CASCADE)`,
		`CREATE TABLE retrieval_index(collection_id TEXT NOT NULL,chunk_id TEXT NOT NULL,document_id TEXT NOT NULL,search_content TEXT NOT NULL,embedding HALFVEC(1024) NOT NULL,enabled BOOLEAN NOT NULL DEFAULT TRUE,metadata JSONB NOT NULL DEFAULT '{}',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),PRIMARY KEY(collection_id,chunk_id),FOREIGN KEY(collection_id,chunk_id) REFERENCES chunks(collection_id,id) ON DELETE CASCADE)`,
		`INSERT INTO documents(collection_id,id,title)VALUES('legacy','doc','legacy title')`,
		`INSERT INTO chunks(collection_id,id,document_id,chunk_index,content,start_rune,end_rune)VALUES('legacy','chunk','doc',0,'legacytoken',0,11)`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := indexpg.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := indexpg.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var content string
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT c.content,d.revision FROM chunks c JOIN documents d ON d.collection_id=c.collection_id AND d.id=c.document_id`).Scan(&content, &revision); err != nil || content != "legacytoken" || revision != 0 {
		t.Fatalf("legacy data lost: %q %d %v", content, revision, err)
	}
	// Unknown historical vectors must not silently acquire a new model label.
	e := &integrationEmbedder{}
	cfg := indexpg.DefaultConfig()
	cfg.Embedder = e
	p, err := indexpg.NewIndexer(pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.ReplaceDocument(ctx, flatBatch("legacy", "doc", "legacytoken", 0)); err != nil {
		t.Fatal(err)
	}
	if err = indexpg.EnsureCollectionProfile(ctx, pool, "legacy", "unverified-model", `{}`); !errors.Is(err, indexpg.ErrUnboundLegacyIndex) {
		t.Fatalf("guessed legacy model: %v", err)
	}
}

func TestIntegrationConcurrentAttempts(t *testing.T) {
	ctx, pool := migratedPool(t)
	p := versionedIndexer(t, pool, &integrationEmbedder{})
	type reservation struct {
		attempt int64
		err     error
	}
	reserved := make(chan reservation, 4)
	for range 4 {
		go func() {
			attempt, err := p.ReserveDocument(ctx, "kb", "concurrent")
			reserved <- reservation{attempt, err}
		}()
	}
	var attempts []int64
	var latest int64
	for range 4 {
		r := <-reserved
		if r.err != nil {
			t.Fatal(r.err)
		}
		attempts = append(attempts, r.attempt)
		latest = max(latest, r.attempt)
	}
	if latest != 4 {
		t.Fatalf("reservation lost an increment: %v", attempts)
	}
	type completion struct {
		attempt int64
		err     error
	}
	completed := make(chan completion, 4)
	for _, attempt := range attempts {
		go func(attempt int64) {
			batch := flatBatch("kb", "concurrent", fmt.Sprintf("attempt %d source", attempt), attempt)
			completed <- completion{attempt, p.ReplaceDocument(ctx, batch)}
		}(attempt)
	}
	for range 4 {
		r := <-completed
		if r.attempt == latest {
			if r.err != nil {
				t.Fatal(r.err)
			}
		} else if !errors.Is(r.err, indexpg.ErrStaleIngestion) {
			t.Fatalf("obsolete concurrent completion: %+v", r)
		}
	}
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT revision FROM documents WHERE collection_id='kb' AND id='concurrent'`).Scan(&revision); err != nil || revision != latest {
		t.Fatalf("published wrong revision: %d %v", revision, err)
	}
}
