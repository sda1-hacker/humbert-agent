package postgres

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// -----------------------------------------------------------------------------
// Fake Embedder
// -----------------------------------------------------------------------------

type fakeEmbedder struct {
	dim int
	err error
}

func (f *fakeEmbedder) EmbedStrings(
	ctx context.Context,
	texts []string,
	_ ...embedding.Option,
) ([][]float64, error) {
	if f.err != nil {
		return nil, f.err
	}

	result := make([][]float64, len(texts))

	for i := range result {
		result[i] = make([]float64, f.dim)

		for j := range result[i] {
			result[i][j] = float64(j%11) / 100
		}
	}

	return result, nil
}

// -----------------------------------------------------------------------------
// Fake Database
// -----------------------------------------------------------------------------

type queryCall struct {
	sql  string
	args []any
}

type fakeQueryer struct {
	resultRows rows
	err        error
	calls      []queryCall
}

func (f *fakeQueryer) Query(
	ctx context.Context,
	sql string,
	args ...any,
) (rows, error) {
	f.calls = append(f.calls, queryCall{
		sql:  sql,
		args: append([]any(nil), args...),
	})

	if f.err != nil {
		return nil, f.err
	}

	return f.resultRows, nil
}

type fakeRows struct {
	data  [][]any
	index int
	err   error
}

func (f *fakeRows) Next() bool {
	if f.index >= len(f.data) {
		return false
	}

	f.index++
	return true
}

func (f *fakeRows) Scan(dest ...any) error {
	if f.index <= 0 ||
		f.index > len(f.data) {

		return fmt.Errorf("Scan called without current row")
	}

	values := f.data[f.index-1]

	if len(values) != len(dest) {
		return fmt.Errorf(
			"scan destination count mismatch: values=%d dest=%d",
			len(values),
			len(dest),
		)
	}

	for i := range values {
		if err := assignScanValue(
			dest[i],
			values[i],
		); err != nil {
			return fmt.Errorf(
				"column %d: %w",
				i,
				err,
			)
		}
	}

	return nil
}

func (f *fakeRows) Err() error {
	return f.err
}

func (f *fakeRows) Close() {}

// assignScanValue 是测试用轻量 pgx Scan 模拟。
func assignScanValue(
	dest any,
	value any,
) error {
	destination := reflect.ValueOf(dest)

	if destination.Kind() != reflect.Pointer ||
		destination.IsNil() {

		return fmt.Errorf(
			"destination must be non-nil pointer",
		)
	}

	target := destination.Elem()

	if value == nil {
		target.Set(
			reflect.Zero(target.Type()),
		)

		return nil
	}

	source := reflect.ValueOf(value)

	if source.Type().AssignableTo(
		target.Type(),
	) {
		target.Set(source)
		return nil
	}

	if source.Type().ConvertibleTo(
		target.Type(),
	) {
		target.Set(
			source.Convert(target.Type()),
		)

		return nil
	}

	return fmt.Errorf(
		"cannot assign %T to %T",
		value,
		dest,
	)
}

// -----------------------------------------------------------------------------
// Vector Retriever
// -----------------------------------------------------------------------------

func TestVectorRetrieverSearch(t *testing.T) {
	db := &fakeQueryer{
		resultRows: &fakeRows{
			data: [][]any{
				searchRow(
					"chunk-a",
					"doc-a",
					"正文 A",
					0.91,
				),
				searchRow(
					"chunk-b",
					"doc-a",
					"正文 B",
					0.82,
				),
			},
		},
	}

	cfg := DefaultVectorConfig()
	cfg.CollectionID = "kb-1"
	cfg.Embedder = &fakeEmbedder{
		dim: DefaultDimensions,
	}

	retriever := newVectorRetriever(db, cfg)

	results, err := retriever.Search(
		context.Background(),
		"安装方法",
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 {
		t.Fatalf(
			"应该返回2个结果: got=%d",
			len(results),
		)
	}

	if results[0].ChunkID != "chunk-a" {
		t.Fatalf(
			"第一名错误: %+v",
			results[0],
		)
	}

	if results[0].VectorScore != 0.91 {
		t.Fatalf(
			"VectorScore 错误: %+v",
			results[0],
		)
	}

	if results[0].MatchType !=
		retrieval.MatchVector {

		t.Fatalf(
			"MatchType 错误: %+v",
			results[0],
		)
	}

	if len(db.calls) != 1 {
		t.Fatalf(
			"应该执行一次 SQL: %d",
			len(db.calls),
		)
	}

	if !strings.Contains(
		db.calls[0].sql,
		"<=>",
	) {
		t.Fatalf(
			"Vector SQL 应使用 cosine operator: %s",
			db.calls[0].sql,
		)
	}
}

func TestVectorRetrieverThreshold(t *testing.T) {
	db := &fakeQueryer{
		resultRows: &fakeRows{
			data: [][]any{
				searchRow(
					"A",
					"doc",
					"A",
					0.8,
				),
				searchRow(
					"B",
					"doc",
					"B",
					0.2,
				),
			},
		},
	}

	cfg := DefaultVectorConfig()
	cfg.CollectionID = "kb"
	cfg.ScoreThreshold = 0.5
	cfg.Embedder = &fakeEmbedder{
		dim: DefaultDimensions,
	}

	retriever := newVectorRetriever(db, cfg)

	results, err := retriever.Search(
		context.Background(),
		"query",
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 ||
		results[0].ChunkID != "A" {

		t.Fatalf(
			"Vector Threshold 过滤错误: %+v",
			results,
		)
	}
}

func TestVectorRetrieverRejectsWrongDimension(
	t *testing.T,
) {
	db := &fakeQueryer{}

	cfg := DefaultVectorConfig()
	cfg.CollectionID = "kb"
	cfg.Embedder = &fakeEmbedder{
		dim: 768,
	}

	retriever := newVectorRetriever(db, cfg)

	_, err := retriever.Search(
		context.Background(),
		"query",
	)

	if err == nil ||
		!strings.Contains(
			err.Error(),
			"expected 1024 dimensions",
		) {

		t.Fatalf(
			"应该拒绝768维 Query Embedding: %v",
			err,
		)
	}

	if len(db.calls) != 0 {
		t.Fatal(
			"Embedding 维度错误时不应该查询数据库",
		)
	}
}

// -----------------------------------------------------------------------------
// BM25 Retriever
// -----------------------------------------------------------------------------

func TestBM25RetrieverSearch(t *testing.T) {
	db := &fakeQueryer{
		resultRows: &fakeRows{
			data: [][]any{
				searchRow(
					"chunk-a",
					"doc-a",
					"BM25 正文",
					12.5,
				),
			},
		},
	}

	cfg := DefaultBM25Config()
	cfg.CollectionID = "kb"

	retriever := newBM25Retriever(db, cfg)

	results, err := retriever.Search(
		context.Background(),
		"BM25",
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf(
			"应该返回一个 BM25 结果: %d",
			len(results),
		)
	}

	if results[0].KeywordScore != 12.5 {
		t.Fatalf(
			"Raw BM25 Score 丢失: %+v",
			results[0],
		)
	}

	sql := db.calls[0].sql

	if !strings.Contains(
		sql,
		"pdb.score",
	) {
		t.Fatalf(
			"BM25 SQL 缺少 pdb.score: %s",
			sql,
		)
	}

	if !strings.Contains(
		sql,
		"|||",
	) {
		t.Fatalf(
			"BM25 SQL 缺少 Match Any operator: %s",
			sql,
		)
	}
}

func TestBM25RetrieverThreshold(t *testing.T) {
	db := &fakeQueryer{
		resultRows: &fakeRows{
			data: [][]any{
				searchRow("A", "doc", "A", 2.0),
				searchRow("B", "doc", "B", 0.2),
			},
		},
	}

	cfg := DefaultBM25Config()
	cfg.CollectionID = "kb"
	cfg.ScoreThreshold = 0.3

	retriever := newBM25Retriever(db, cfg)

	results, err := retriever.Search(
		context.Background(),
		"query",
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 ||
		results[0].ChunkID != "A" {

		t.Fatalf(
			"BM25 Threshold 过滤错误: %+v",
			results,
		)
	}
}

// -----------------------------------------------------------------------------
// Eino mapping
// -----------------------------------------------------------------------------

func TestRetrieverMapsSearchResultToEinoDocument(
	t *testing.T,
) {
	results := []retrieval.SearchResult{
		{
			ChunkID:       "chunk-1",
			DocumentID:    "doc-1",
			ParentChunkID: "parent-1",
			ChunkIndex:    3,
			Content:       "正文",
			ContextHeader: "# 文档\n## 安装",
			StartRune:     100,
			EndRune:       200,
			Score:         0.9,
			VectorScore:   0.88,
			KeywordScore:  10,
			VectorRank:    1,
			KeywordRank:   2,
			MatchType:     retrieval.MatchHybrid,
			Metadata: map[string]any{
				"title": "产品手册",
			},
		},
	}

	docs := searchResultsToDocuments(results)

	if len(docs) != 1 {
		t.Fatalf(
			"应该得到一个 Document: %d",
			len(docs),
		)
	}

	doc := docs[0]

	if doc.ID != "chunk-1" {
		t.Fatalf(
			"Document ID 错误: %q",
			doc.ID,
		)
	}

	if doc.Content != "正文" {
		t.Fatalf(
			"Document Content 错误: %q",
			doc.Content,
		)
	}

	if doc.Score() != 0.9 {
		t.Fatalf(
			"Eino Score 错误: %f",
			doc.Score(),
		)
	}

	if doc.MetaData[MetaMatchType] !=
		"hybrid" {

		t.Fatalf(
			"MatchType metadata 错误: %v",
			doc.MetaData,
		)
	}

	if doc.MetaData["title"] !=
		"产品手册" {

		t.Fatal(
			"原 Chunk metadata 不应该丢失",
		)
	}
}

// -----------------------------------------------------------------------------
// Utility
// -----------------------------------------------------------------------------

func TestFilterByScore(t *testing.T) {
	input := []retrieval.SearchResult{
		{ChunkID: "A", Score: 0.9},
		{ChunkID: "B", Score: 0.7},
		{ChunkID: "C", Score: 0.2},
	}

	got := filterByScore(
		input,
		0.5,
		1,
	)

	if len(got) != 1 ||
		got[0].ChunkID != "A" {

		t.Fatalf(
			"FilterByScore 错误: %+v",
			got,
		)
	}
}

func TestValidateTopK(t *testing.T) {
	if err := validateTopK(1); err != nil {
		t.Fatal(err)
	}

	if err := validateTopK(MaxTopK); err != nil {
		t.Fatal(err)
	}

	if err := validateTopK(0); err == nil {
		t.Fatal(
			"TopK=0 应该失败",
		)
	}

	if err := validateTopK(MaxTopK + 1); err == nil {
		t.Fatal(
			"超过最大 TopK 应该失败",
		)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func searchRow(
	chunkID string,
	documentID string,
	content string,
	score float64,
) []any {
	return []any{
		chunkID,
		documentID,
		content,
		"# 文档",
		0,
		0,
		len([]rune(content)),
		nil,
		[]byte(`{"title":"测试文档"}`),
		score,
	}
}
