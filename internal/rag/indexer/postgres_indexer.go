package indexer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

var _ indexer.Indexer = (*Indexer)(nil)

var (
	ErrMissingCollectionID   = errors.New("postgres indexer: missing collection id")
	ErrMissingEmbedder       = errors.New("postgres indexer: missing embedder")
	ErrMissingChunkID        = errors.New("postgres indexer: missing document id")
	ErrEmptySearchContent    = errors.New("postgres indexer: empty search content")
	ErrSubIndexesUnsupported = errors.New("postgres indexer: sub indexes are not supported")
	ErrInvalidEmbedding      = errors.New("postgres indexer: invalid embedding")
	ErrIncompleteSource      = errors.New("postgres indexer: chunked documents require WithIngestionBatch with the complete source")
	ErrPinnedEmbedding       = errors.New("postgres indexer: embedding options cannot override a pinned collection profile")
)

type storeOptions struct{ embeddingOptions []embedding.Option }

// WithEmbeddingOptions 使用 Eino 实现专属 Option 向模型转发调用参数。
func WithEmbeddingOptions(opts ...embedding.Option) indexer.Option {
	copied := append([]embedding.Option(nil), opts...)
	return indexer.WrapImplSpecificOptFn(func(o *storeOptions) { o.embeddingOptions = copied })
}

// Indexer 实现 Eino Store：校验输入、批量向量化，再交给文档存储原子发布。
// 文档版本、原文保存和删除由 DocumentStore 负责，不在这里另建一套持久化逻辑。
type Indexer struct {
	*DocumentStore
	config ragconfig.Indexer
}

// NewIndexer 校验配置并绑定连接池，连接池的关闭由组装层负责。
func NewIndexer(pool *pgxpool.Pool, cfg ragconfig.Indexer) (*Indexer, error) {
	if cfg.Dimensions <= 0 || cfg.Dimensions > ragconfig.PostgresMaxDimensions {
		return nil, fmt.Errorf("%w: postgres indexer requires configured dimensions in 1..%d", ragconfig.ErrInvalidDimensions, ragconfig.PostgresMaxDimensions)
	}
	if err := cfg.InputBudget.Validate(); err != nil {
		return nil, err
	}
	if cfg.EmbeddingBatchSize < 0 {
		return nil, errors.New("postgres indexer: negative embedding batch size")
	}
	if pool == nil {
		return nil, errors.New("postgres indexer: nil pool")
	}
	return newIndexerWithDatabase(pool, cfg), nil
}
func newIndexerWithDatabase(db database, cfg ragconfig.Indexer) *Indexer {
	if cfg.EmbeddingBatchSize == 0 {
		cfg.EmbeddingBatchSize = ragconfig.DefaultEmbeddingBatchSize
	}
	return &Indexer{DocumentStore: &DocumentStore{db: db, config: DocumentConfig{cfg.ProfileID, cfg.ProfileJSON}}, config: cfg}
}

// Store 是唯一索引入口。普通 Eino 文档以“一份原文、一个分块”保存；应用层切分的
// WithIngestionBatch 附带处理期正文、原件引用和父块；事务只持久化元数据、分块和检索行。
// 模型调用放在事务前，避免等待网络时占用数据库事务。
func (p *Indexer) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	collection := strings.TrimSpace(p.config.CollectionID)
	common := indexer.GetCommonOptions(&indexer.Options{Index: &collection, Embedding: p.config.Embedder}, opts...)
	if common.Index == nil || strings.TrimSpace(*common.Index) == "" {
		return nil, ErrMissingCollectionID
	}
	collection = strings.TrimSpace(*common.Index)
	if len(common.SubIndexes) > 0 {
		return nil, ErrSubIndexesUnsupported
	}
	specific := indexer.GetImplSpecificOptions(&storeOptions{}, opts...)
	if p.config.ProfileID != "" && (indexer.GetCommonOptions(nil, opts...).Embedding != nil || len(specific.embeddingOptions) > 0) {
		// 配置签名不能继续声明旧模型，却实际使用调用方临时替换的模型。
		return nil, ErrPinnedEmbedding
	}
	source := indexer.GetImplSpecificOptions(&rag.IngestionOptions{}, opts...).Batch
	if len(docs) == 0 && source == nil {
		return nil, nil
	}
	if common.Embedding == nil && len(docs) > 0 {
		return nil, ErrMissingEmbedder
	}
	ids := make([]string, len(docs))
	seen := make(map[string]bool, len(docs))
	for i, d := range docs {
		if d == nil || strings.TrimSpace(d.ID) == "" {
			return nil, ErrMissingChunkID
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("postgres indexer: duplicate id %q", d.ID)
		}
		seen[d.ID] = true
		if strings.TrimSpace(d.Content) == "" {
			return nil, ErrEmptySearchContent
		}
		ids[i] = d.ID
	}
	if source != nil {
		batch := *source
		if batch.CollectionID != collection || len(docs) != len(batch.Children) {
			return nil, ErrIncompleteSource
		}
		texts := make([]string, len(docs))
		for i, d := range docs {
			raw, ok := d.MetaData[retrieval.MetaRawContent].(string)
			if d.ID != batch.Children[i].ID || !ok || raw != batch.Children[i].Content {
				return nil, ErrIncompleteSource
			}
			texts[i] = d.Content
		}
		if err := p.storeDocument(ctx, batch, texts, common.Embedding, specific.embeddingOptions); err != nil {
			return nil, err
		}
		return ids, nil
	}
	// 有分块身份的输入不能被误当作完整原文，否则一次部分更新会删掉其他分块。
	for _, d := range docs {
		if d.MetaData[retrieval.MetaSourceDocumentID] != nil || d.MetaData[retrieval.MetaDocumentID] != nil {
			return nil, ErrIncompleteSource
		}
	}
	for _, d := range docs {
		batch := rag.IngestionBatch{CollectionID: collection, DocumentID: d.ID, Title: retrieval.Title(ragconfig.SearchText{}, d), Markdown: d.Content, Metadata: d.MetaData,
			Children: []rag.ChunkRecord{{ID: d.ID, DocumentID: d.ID, ChunkType: rag.ChunkTypeText, Content: d.Content, EndRune: utf8.RuneCountInString(d.Content), Metadata: d.MetaData}}}
		if p.config.VersionedDocuments {
			revision, err := p.ReserveDocument(ctx, collection, d.ID)
			if err != nil {
				return nil, err
			}
			batch.Attempt = revision
		}
		if err := p.storeDocument(ctx, batch, []string{d.Content}, common.Embedding, specific.embeddingOptions); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// embedTexts 按输入预算分批生成向量，校验数量、维度和半精度有效范围。
func embedTexts(
	ctx context.Context,
	embedder embedding.Embedder,
	texts []string,
	batchSize int,
	opts []embedding.Option,
	budget ragconfig.Budget,
	dimensions int,
) ([]any, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	if batchSize <= 0 {
		batchSize = ragconfig.DefaultEmbeddingBatchSize
	}

	result := make([]any, 0, len(texts))
	batches, err := budget.Plan(texts, batchSize)
	if err != nil {
		return nil, err
	}
	for _, batch := range batches {
		start, end := batch.Start, batch.End
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		vectors, err := embedder.EmbedStrings(ctx, texts[start:end], opts...)

		if err != nil {
			return nil, fmt.Errorf("embed documents [%d:%d]: %w", start, end, err)
		}

		if len(vectors) != end-start {
			return nil, fmt.Errorf("%w: embedder returned %d vectors for %d texts", ErrInvalidEmbedding, len(vectors), end-start)
		}

		// 每批立即校验并转换，错误模型不再继续调用；仅保留入库需要的半精度输入。
		for _, vector := range vectors {
			half, err := makeHalfVector(vector, dimensions)
			if err != nil {
				return nil, err
			}
			result = append(result, half)
		}
	}

	return result, nil
}

// makeHalfVector 校验配置维度与数值后交给 pgvector 编码，不在这里改变模型向量。
func makeHalfVector(vector []float64, dimensions int) (pgvector.HalfVector, error) {
	if dimensions <= 0 || len(vector) != dimensions {
		return pgvector.HalfVector{}, fmt.Errorf("%w: expected %d dimensions, got %d", ErrInvalidEmbedding, dimensions, len(vector))
	}

	values := make([]float32, len(vector))
	nonzero := false

	for i, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return pgvector.HalfVector{}, fmt.Errorf("%w: vector[%d] is not finite", ErrInvalidEmbedding, i)
		}

		// 超过 binary16 最大有限值会在数据库半精度转换时溢出。
		if math.Abs(value) > 65504 {
			return pgvector.HalfVector{}, fmt.Errorf("%w: vector[%d]=%f exceeds half precision range", ErrInvalidEmbedding, i, value)
		}

		values[i] = float32(value)
		// 半精度转换后归零的向量不能用于余弦检索，需要提前拒绝。
		if math.Abs(float64(values[i])) > math.Ldexp(1, -25) {
			nonzero = true
		}
	}
	if !nonzero {
		return pgvector.HalfVector{}, fmt.Errorf("%w: zero vector after half precision conversion", ErrInvalidEmbedding)
	}

	return pgvector.NewHalfVector(values), nil
}
