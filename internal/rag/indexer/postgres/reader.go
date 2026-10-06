package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/rag"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

var _ rag.ChunkReader = (*Indexer)(nil)
var _ rag.PublishedChunkReader = (*Indexer)(nil)

// ListChunks 按原文顺序返回已发布文档的可检索分块，父分块不参与评估。
func (p *Indexer) ListChunks(ctx context.Context, collectionID, documentID string) ([]rag.ChunkRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	collectionID, documentID = strings.TrimSpace(collectionID), strings.TrimSpace(documentID)
	if collectionID == "" || documentID == "" {
		return nil, errors.New("rag: collection/document id required")
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return nil, errors.New("rag: chunk reading requires a PostgreSQL pool")
	}
	rows, err := db.pool.Query(ctx, `
		SELECT c.id, c.document_id, c.chunk_type, c.chunk_index,
		       c.content, c.context_header, c.start_rune, c.end_rune,
		       COALESCE(c.parent_chunk_id, ''), c.metadata
		FROM chunks c JOIN documents d ON d.collection_id=c.collection_id AND d.id=c.document_id
		WHERE c.collection_id=$1 AND c.document_id=$2 AND c.chunk_type='text'
		ORDER BY c.chunk_index
	`, collectionID, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []rag.ChunkRecord
	for rows.Next() {
		var record rag.ChunkRecord
		var metadata []byte
		if err := rows.Scan(&record.ID, &record.DocumentID, &record.ChunkType, &record.ChunkIndex,
			&record.Content, &record.ContextHeader, &record.StartRune, &record.EndRune,
			&record.ParentChunkID, &metadata); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &record.Metadata); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// ResolveChunks 一次读取外部命中的权威分块，只保留当前已发布版本。
// 即使外部库尚未删除旧向量、或索引写入后正文发布失败，也不会返回这些内容。
func (p *Indexer) ResolveChunks(ctx context.Context, collectionID string, hits []retrieval.SearchResult) ([]retrieval.SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return nil, rag.ErrMissingCollectionID
	}
	if len(hits) == 0 {
		return nil, nil
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return nil, errors.New("rag: chunk resolution requires a PostgreSQL pool")
	}
	ids := make([]string, 0, len(hits))
	seen := make(map[string]bool)
	for _, hit := range hits {
		if hit.CollectionID == collectionID && hit.DocumentRevision > 0 && !seen[hit.ChunkID] {
			ids = append(ids, hit.ChunkID)
			seen[hit.ChunkID] = true
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := db.pool.Query(ctx, `
		SELECT c.id, c.document_id, d.revision, c.content, c.context_header,
		       c.chunk_index, c.start_rune, c.end_rune, COALESCE(c.parent_chunk_id, ''), c.metadata
		FROM chunks c JOIN documents d ON d.collection_id=c.collection_id AND d.id=c.document_id
		WHERE c.collection_id=$1 AND c.id=ANY($2::text[]) AND c.chunk_type='text'
	`, collectionID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	current := make(map[string]retrieval.SearchResult)
	for rows.Next() {
		var record retrieval.SearchResult
		var metadata []byte
		if err := rows.Scan(&record.ChunkID, &record.DocumentID, &record.DocumentRevision, &record.Content,
			&record.ContextHeader, &record.ChunkIndex, &record.StartRune, &record.EndRune,
			&record.ParentChunkID, &metadata); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &record.Metadata); err != nil {
			return nil, err
		}
		record.CollectionID = collectionID
		current[record.ChunkID] = record
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return resolvePublishedChunks(collectionID, current, hits), nil
}

// 版本匹配与正文替换共用此函数，便于独立验证删除、旧版本和跨集合命中。
func resolvePublishedChunks(collectionID string, current map[string]retrieval.SearchResult, hits []retrieval.SearchResult) []retrieval.SearchResult {
	var results []retrieval.SearchResult
	for _, hit := range hits {
		record, ok := current[hit.ChunkID]
		if !ok || hit.DocumentRevision <= 0 || hit.CollectionID != collectionID || hit.DocumentID != record.DocumentID || hit.DocumentRevision != record.DocumentRevision {
			continue
		}
		// 保留召回分数和顺序，正文、父块和出处全部以权威库为准。
		hit.Content, hit.ContextHeader = record.Content, record.ContextHeader
		hit.ParentChunkID, hit.ChunkIndex = record.ParentChunkID, record.ChunkIndex
		hit.StartRune, hit.EndRune, hit.Metadata = record.StartRune, record.EndRune, record.Metadata
		hit.ContextContent, hit.ContextChunkID = "", ""
		hit.Evidence = nil
		results = append(results, hit)
	}
	return results
}
