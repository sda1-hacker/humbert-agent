package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// ParentLoader 批量读取 Parent Chunk。
//
// 它在创建时绑定一个 CollectionID。
//
// 因此 Search Pipeline 不需要知道 PostgreSQL 的：
//
//	collection_id
//
// 也不需要把 CollectionID 再塞进 Retrieval Core Model。
type ParentLoader struct {
	db           queryer
	collectionID string
}

func NewParentLoader(
	pool *pgxpool.Pool,
	collectionID string,
) (*ParentLoader, error) {
	if pool == nil {
		return nil, fmt.Errorf(
			"postgres parent loader: nil pool",
		)
	}

	collectionID = strings.TrimSpace(collectionID)

	if collectionID == "" {
		return nil, ErrMissingCollectionID
	}

	return newParentLoader(
		poolQueryer{pool: pool},
		collectionID,
	), nil
}

func newParentLoader(
	db queryer,
	collectionID string,
) *ParentLoader {
	return &ParentLoader{
		db:           db,
		collectionID: strings.TrimSpace(collectionID),
	}
}

// LoadParents 一次性批量读取 Parent。
//
// 为什么不能：
//
//	for each child:
//	    SELECT parent
//
// 因为那会制造典型：
//
//	N+1 Query
//
// 一个 Search Top5 就可能额外打5次数据库。
//
// 这里始终只执行一次 SQL。
func (l *ParentLoader) LoadParents(
	ctx context.Context,
	parentChunkIDs []string,
) (map[string]retrieval.SearchResult, error) {
	ids := uniqueStrings(parentChunkIDs)

	if len(ids) == 0 {
		return map[string]retrieval.SearchResult{}, nil
	}

	resultRows, err := l.db.Query(
		ctx,
		parentChunkSQL,
		l.collectionID,
		ids,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"load parent chunks: %w",
			err,
		)
	}

	defer resultRows.Close()

	result := make(
		map[string]retrieval.SearchResult,
		len(ids),
	)

	for resultRows.Next() {
		var (
			parent retrieval.SearchResult

			parentParentID *string
			rawMetadata    []byte
		)

		if err := resultRows.Scan(
			&parent.ChunkID,
			&parent.DocumentID,
			&parent.Content,
			&parent.ContextHeader,
			&parent.ChunkIndex,
			&parent.StartRune,
			&parent.EndRune,
			&parentParentID,
			&rawMetadata,
		); err != nil {
			return nil, fmt.Errorf(
				"scan parent chunk: %w",
				err,
			)
		}

		if parentParentID != nil {
			parent.ParentChunkID = *parentParentID
		}

		if len(rawMetadata) > 0 {
			if err := json.Unmarshal(
				rawMetadata,
				&parent.Metadata,
			); err != nil {
				return nil, fmt.Errorf(
					"decode parent metadata %q: %w",
					parent.ChunkID,
					err,
				)
			}
		}

		parent.CollectionID = l.collectionID
		parent.DocumentRevision, err = documentRevision(parent.Metadata)
		if err != nil {
			return nil, err
		}
		result[parent.ChunkID] = parent
	}

	if err := resultRows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate parent chunks: %w",
			err,
		)
	}

	return result, nil
}

const parentChunkSQL = `
SELECT
    c.id, c.document_id, c.content, c.context_header, c.chunk_index,
    c.start_rune, c.end_rune, c.parent_chunk_id,
    c.metadata || jsonb_build_object('rag_document_revision', d.revision::text)
FROM chunks c
JOIN documents d ON d.collection_id=c.collection_id AND d.id=c.document_id
WHERE c.collection_id = $1 AND c.id = ANY($2::text[]) AND c.chunk_type='parent_text'
`

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
