package retriever

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// ParentLoader 绑定知识库范围，批量读取已发布父块用于上下文扩展。
type ParentLoader struct {
	db           queryer
	collectionID string
}

// NewParentLoader 校验连接池和知识库 ID，创建父块读取器。
func NewParentLoader(
	pool *pgxpool.Pool,
	collectionID string,
) (*ParentLoader, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres parent loader: nil pool")
	}

	collectionID = strings.TrimSpace(collectionID)

	if collectionID == "" {
		return nil, ErrMissingCollectionID
	}

	return &ParentLoader{db: pool, collectionID: collectionID}, nil
}

// LoadParents 使用一次查询读取所有父块，避免逐条子块查询数据库。
func (l *ParentLoader) LoadParents(
	ctx context.Context,
	parentChunkIDs []string,
) (map[string]retrieval.SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := uniqueStrings(parentChunkIDs)

	if len(ids) == 0 {
		return map[string]retrieval.SearchResult{}, nil
	}

	resultRows, err := l.db.Query(ctx, parentChunkSQL, l.collectionID, ids)

	if err != nil {
		return nil, fmt.Errorf("load parent chunks: %w", err)
	}

	// 父块不参与排名，复用统一解码以保留文档版本、范围和元数据。
	parents, err := scanSearchResults(resultRows, l.collectionID, "")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]retrieval.SearchResult, len(parents))
	for _, parent := range parents {
		result[parent.ChunkID] = parent
	}
	return result, nil
}

const parentChunkSQL = `
SELECT
    c.chunk_key, d.source_key, c.content, c.context_header, c.chunk_index,
    c.start_rune, c.end_rune, p.chunk_key,
    c.metadata || jsonb_build_object('rag_document_revision', d.revision::text),
    0::float8 AS score
FROM chunks c
JOIN documents d ON d.collection_id=c.collection_id AND d.id=c.document_id
LEFT JOIN chunks p ON p.collection_id=c.collection_id AND p.id=c.parent_chunk_id
WHERE c.collection_id = $1 AND c.chunk_key = ANY($2::text[]) AND c.chunk_type='parent_text'
`

// uniqueStrings 去除空值与重复 ID，保留首次出现顺序。
func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)

		if value == "" {
			continue
		}

		if _, exists := seen[value]; exists {
			continue
		}

		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}
