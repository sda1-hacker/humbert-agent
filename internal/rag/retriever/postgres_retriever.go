package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

var (
	ErrMissingCollectionID = errors.New("postgres retriever: missing collection id")
	ErrMissingEmbedder     = errors.New("postgres retriever: missing embedder")
	ErrEmptyQuery          = errors.New("postgres retriever: empty query")
	ErrInvalidEmbedding    = errors.New("postgres retriever: invalid query embedding")
	ErrSubIndexUnsupported = errors.New("postgres retriever: sub index is not supported")
	ErrDSLUnsupported      = errors.New("postgres retriever: DSLInfo is not supported yet")
)

// queryer 直接采用 pgx 的查询签名，连接池和事务均可使用，无需再包装行集。
type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// queryDocuments 共用结果转换与阈值过滤；向量/BM25 的 SQL、参数和评分仍各自负责。
func queryDocuments(ctx context.Context, db queryer, statement string, options *eino.Options,
	kind retrieval.MatchType, args ...any) ([]*schema.Document, error) {
	rows, err := db.Query(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres %s search: %w", kind, err)
	}
	results, err := scanSearchResults(rows, strings.TrimSpace(*options.Index), kind)
	if err != nil {
		return nil, err
	}
	// 不接受取消后才返回的结果；pgx 已通过同一个 context 取消实际查询。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return retrieval.Documents(filterByScore(results, *options.ScoreThreshold, *options.TopK)), nil
}

// scanSearchResults 统一解码分块、文档版本与原始分数。
// pgx.CollectRows 负责迭代错误和关闭行集，扫描失败也会释放数据库连接。
func scanSearchResults(rows pgx.Rows, collectionID string, kind retrieval.MatchType) ([]retrieval.SearchResult, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (retrieval.SearchResult, error) {
		result := retrieval.SearchResult{CollectionID: collectionID, MatchType: kind}
		var parentID *string
		var metadata []byte
		if err := row.Scan(&result.ChunkID, &result.DocumentID, &result.Content, &result.ContextHeader,
			&result.ChunkIndex, &result.StartRune, &result.EndRune, &parentID, &metadata, &result.Score); err != nil {
			return result, fmt.Errorf("scan postgres retrieval row: %w", err)
		}
		if parentID != nil {
			result.ParentChunkID = *parentID
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &result.Metadata); err != nil {
				return result, fmt.Errorf("decode retrieval metadata for chunk %q: %w", result.ChunkID, err)
			}
		}
		var err error
		result.SourceLocators = retrieval.Locators(result.Metadata)
		result.DocumentRevision, err = retrieval.Revision(result.Metadata)
		if err != nil {
			return result, err
		}
		switch kind {
		case retrieval.MatchVector:
			result.VectorScore = result.Score
		case retrieval.MatchKeyword:
			result.KeywordScore = result.Score
		}
		return result, nil
	})
}

// validateSearchOptions 在调用模型或数据库前校验公共选项；不同通道的分数上界各自校验。
func validateSearchOptions(ctx context.Context, query string, options *eino.Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if query == "" {
		return ErrEmptyQuery
	}
	if options.Index == nil || strings.TrimSpace(*options.Index) == "" {
		return ErrMissingCollectionID
	}
	if options.TopK == nil || *options.TopK <= 0 || *options.TopK > ragconfig.PostgresMaxTopK {
		return ragconfig.ErrInvalidTopK
	}
	if options.SubIndex != nil {
		return ErrSubIndexUnsupported
	}
	if len(options.DSLInfo) > 0 {
		return ErrDSLUnsupported
	}
	if options.ScoreThreshold == nil || !finite(*options.ScoreThreshold) || *options.ScoreThreshold < 0 {
		return ragconfig.ErrInvalidThreshold
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// filterByScore 保持数据库排序；BM25 和余弦分数分别使用各自的原始阈值。
func filterByScore(results []retrieval.SearchResult, threshold float64, topK int) []retrieval.SearchResult {
	filtered := results[:0]
	for _, result := range results {
		if result.Score >= threshold {
			filtered = append(filtered, result)
			if len(filtered) == topK {
				break
			}
		}
	}
	return filtered
}
