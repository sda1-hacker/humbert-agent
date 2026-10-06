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
	einoindexer "github.com/cloudwego/eino/components/indexer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	core "github.com/sda1-hacker/humbert-agent/internal/rag/retriever"
	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	indexpg "github.com/sda1-hacker/humbert-agent/internal/rag/indexer/postgres"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	retrievepg "github.com/sda1-hacker/humbert-agent/internal/rag/retriever/postgres"
)

// 这些测试只创建和删除随机命名的测试 schema；连接必须指向已安装扩展的专用测试数据库。
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

func flatBatch(collection, doc, content string, attempt int64) rag.IngestionBatch {
	return rag.IngestionBatch{CollectionID: collection, DocumentID: doc, Attempt: attempt, Title: "SQL test", Markdown: content,
		Children: []rag.ChunkRecord{{ID: doc + "-child", DocumentID: doc, ChunkType: rag.ChunkTypeText, Content: content, EndRune: len([]rune(content))}}}
}

func publish(t *testing.T, ctx context.Context, p *indexpg.Indexer, collection, doc, content string) rag.IngestionBatch {
	t.Helper()
	attempt, err := p.ReserveDocument(ctx, collection, doc)
	if err != nil {
		t.Fatal(err)
	}
	batch := flatBatch(collection, doc, content, attempt)
	if err = storeBatch(p, ctx, batch); err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestIntegrationLifecycleSearchAndIsolation(t *testing.T) {
	ctx, pool := migratedPool(t)
	e := &integrationEmbedder{}
	p := versionedIndexer(t, pool, e)
	a := publish(t, ctx, p, "a", "same-id", "alphaonly installation 初始版本")
	if err := storeBatch(p, ctx, a); err != nil {
		t.Fatalf("identical attempt retry failed: %v", err)
	}
	changedTitle := a
	changedTitle.Title = "changed embedding title"
	if err := storeBatch(p, ctx, changedTitle); !errors.Is(err, indexpg.ErrStaleIngestion) {
		t.Fatalf("same revision accepted changed embedding input: %v", err)
	}
	publish(t, ctx, p, "b", "same-id", "betaonly configuration 另一个知识库")
	for _, mode := range []string{"semantic", "keyword", "hybrid"} {
		t.Run(mode, func(t *testing.T) {
			vectorCfg := retrievepg.DefaultVectorConfig()
			vectorCfg.Embedder = e
			vectorCfg.ScoreThreshold = 0
			vector, err := retrievepg.NewVectorRetriever(pool, vectorCfg)
			if err != nil {
				t.Fatal(err)
			}
			keyword, err := retrievepg.NewBM25Retriever(pool, retrievepg.DefaultBM25Config())
			if err != nil {
				t.Fatal(err)
			}
			hybrid, err := core.NewHybrid(vector, keyword, core.DefaultHybridConfig())
			if err != nil {
				t.Fatal(err)
			}
			service, err := rag.New(ctx, rag.Dependencies{Config: rag.Config{Retriever: hybrid, VectorRetriever: vector, KeywordRetriever: keyword}})
			if err != nil {
				t.Fatal(err)
			}
			r, err := service.Search(ctx, rag.SearchRequest{CollectionID: "a", Mode: mode, Limit: 10, Query: "alphaonly"})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Results) != 1 || r.Results[0].CollectionID != "a" || r.Results[0].DocumentRevision != a.Attempt || !strings.Contains(r.Results[0].Content, "alphaonly") {
				t.Fatalf("isolation/revision failure: %+v", r)
			}
		})
	}
	newer := publish(t, ctx, p, "a", "same-id", "replacementonly installation 新版本")
	if err := storeBatch(p, ctx, a); !errors.Is(err, indexpg.ErrStaleIngestion) {
		t.Fatalf("old attempt accepted: %v", err)
	}
	if err := p.InvalidateDocument(ctx, "a", "same-id"); err != nil {
		t.Fatal(err)
	}
	if err := storeBatch(p, ctx, newer); !errors.Is(err, indexpg.ErrStaleIngestion) {
		t.Fatalf("canceled attempt accepted: %v", err)
	}
	latest := publish(t, ctx, p, "a", "same-id", "latestonly installation 可用版本")
	if err := p.DeleteDocument(ctx, "a", "same-id"); err != nil {
		t.Fatal(err)
	}
	if err := storeBatch(p, ctx, latest); !errors.Is(err, indexpg.ErrStaleIngestion) {
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
	if results, err := vector.Retrieve(ctx, "betaonly"); err != nil || len(results) != 0 {
		t.Fatalf("disabled vector returned: %+v %v", results, err)
	}
	keyword, err := retrievepg.NewBM25Retriever(pool, retrievepg.BM25Config{CollectionID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if results, err := keyword.Retrieve(ctx, "betaonly"); err != nil || len(results) != 0 {
		t.Fatalf("disabled keyword returned: %+v %v", results, err)
	}
}

// 验证权威库可以独立于 PostgreSQL 的向量/BM25 索引发布并读取内容。
func TestIntegrationPublishedChunksForExternalIndexes(t *testing.T) {
	ctx, pool := migratedPool(t)
	p, err := indexpg.NewIndexer(pool, indexpg.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	revision, err := p.ReserveDocument(ctx, "external", "doc")
	if err != nil {
		t.Fatal(err)
	}
	batch := flatBatch("external", "doc", "来自权威文档的正文", revision)
	if err := p.PublishDocument(ctx, batch); err != nil {
		t.Fatal(err)
	}
	chunks, err := p.ListChunks(ctx, "external", "doc")
	if err != nil || len(chunks) != 1 || chunks[0].Content != batch.Markdown {
		t.Fatalf("%+v %v", chunks, err)
	}
	var indexCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM retrieval_index WHERE collection_id='external'`).Scan(&indexCount); err != nil || indexCount != 0 {
		t.Fatalf("权威库发布写入了检索索引：%d %v", indexCount, err)
	}
	base := retrieval.SearchResult{CollectionID: "external", DocumentID: "doc", DocumentRevision: revision, ChunkID: chunks[0].ID, Score: .8, Content: "外部索引中的旧副本"}
	unpublished, wrongCollection, wrongDocument := base, base, base
	unpublished.DocumentRevision++
	wrongCollection.CollectionID = "other"
	wrongDocument.DocumentID = "other"
	resolved, err := p.ResolveChunks(ctx, "external", []retrieval.SearchResult{unpublished, wrongCollection, wrongDocument, base})
	if err != nil || len(resolved) != 1 || resolved[0].Content != batch.Markdown || resolved[0].Score != .8 {
		t.Fatalf("版本过滤或正文回查错误：%+v %v", resolved, err)
	}
	cutoff, err := p.TombstoneDocument(ctx, "external", "doc")
	if err != nil || cutoff <= revision {
		t.Fatalf("%d %v", cutoff, err)
	}
	resolved, err = p.ResolveChunks(ctx, "external", []retrieval.SearchResult{base})
	if err != nil || len(resolved) != 0 {
		t.Fatalf("删除后返回旧分块：%+v %v", resolved, err)
	}
	if err := p.PublishDocument(ctx, batch); !errors.Is(err, indexpg.ErrStaleIngestion) {
		t.Fatalf("删除后旧任务复活：%v", err)
	}
}

func TestIntegrationParentForeignKeyAndAtomicRollback(t *testing.T) {
	ctx, pool := migratedPool(t)
	e := &integrationEmbedder{}
	p := versionedIndexer(t, pool, e)
	batch := publish(t, ctx, p, "kb", "parent-doc", "tabletoken one two three")
	batch.Parents = []rag.ChunkRecord{{ID: "parent", DocumentID: batch.DocumentID, ChunkType: rag.ChunkTypeParentText, Content: batch.Markdown, EndRune: len([]rune(batch.Markdown))}}
	batch.Children[0].ParentChunkID = "parent"
	var err error
	batch.Attempt, err = p.ReserveDocument(ctx, "kb", "parent-doc")
	if err != nil {
		t.Fatal(err)
	}
	if err := storeBatch(p, ctx, batch); err != nil {
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
	if err = storeBatch(p, ctx, bad); err == nil {
		t.Fatal("injected SQL failure ignored")
	}
	e.fail = true
	if err = storeBatch(p, ctx, bad); err == nil {
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
	// 旧版表结构没有完整原文、类型、标题、父块及独立检索 ID；迁移必须保留已有数据。
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
	// 模型身份未知的历史向量不能被静默标记为新模型。
	e := &integrationEmbedder{}
	cfg := indexpg.DefaultConfig()
	cfg.Embedder = e
	p, err := indexpg.NewIndexer(pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = storeBatch(p, ctx, flatBatch("legacy", "doc", "legacytoken", 0)); err != nil {
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
			completed <- completion{attempt, storeBatch(p, ctx, batch)}
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

func storeBatch(p *indexpg.Indexer, ctx context.Context, batch rag.IngestionBatch) error {
	_, err := p.Store(ctx, rag.IndexDocuments(batch, searchcontent.DefaultBuilder()), einoindexer.WithIndex(batch.CollectionID), rag.WithIngestionBatch(batch))
	return err
}
