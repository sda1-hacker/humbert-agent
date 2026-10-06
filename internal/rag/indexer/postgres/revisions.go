package postgres

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrStaleIngestion = errors.New("rag: ingestion attempt has been superseded, canceled or deleted")

// rollbackTransaction 使用独立短超时回滚事务，避免请求取消后无法清理。
func rollbackTransaction(ctx context.Context, tx transaction) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

// ReserveDocument 在解析前预留新版本，使旧任务无法发布；上一版已发布文档仍可检索。
func (p *Indexer) ReserveDocument(ctx context.Context, collectionID, documentID string) (int64, error) {
	if strings.TrimSpace(collectionID) == "" || strings.TrimSpace(documentID) == "" {
		return 0, errors.New("rag: stable collection/document id required")
	}
	if err := p.ensureProfile(ctx, collectionID); err != nil {
		return 0, err
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return 0, errors.New("rag: revision reservation requires a PostgreSQL pool")
	}
	var attempt int64
	err := db.pool.QueryRow(ctx, `INSERT INTO rag_document_heads(collection_id,document_id,attempt,deleted)
VALUES($1,$2,1,false) ON CONFLICT(collection_id,document_id) DO UPDATE SET attempt=rag_document_heads.attempt+1,deleted=false
RETURNING attempt`, collectionID, documentID).Scan(&attempt)
	return attempt, err
}

// InvalidateDocument 作废正在处理的版本，保留上一版已发布内容。
func (p *Indexer) InvalidateDocument(ctx context.Context, collectionID, documentID string) error {
	if strings.TrimSpace(collectionID) == "" || strings.TrimSpace(documentID) == "" {
		return errors.New("rag: stable collection/document id required")
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return errors.New("rag: invalidation requires a PostgreSQL pool")
	}
	_, err := db.pool.Exec(ctx, `INSERT INTO rag_document_heads(collection_id,document_id,attempt,deleted)
VALUES($1,$2,1,true) ON CONFLICT(collection_id,document_id) DO UPDATE SET attempt=rag_document_heads.attempt+1,deleted=true`, collectionID, documentID)
	return err
}

// DeleteDocument 删除正文并留下墓碑，阻止迟到的入库任务复活文档。
func (p *Indexer) DeleteDocument(ctx context.Context, collectionID, documentID string) error {
	_, err := p.TombstoneDocument(ctx, collectionID, documentID)
	return err
}

// TombstoneDocument 原子隐藏正文并返回删除截止版本，供分离索引清理使用。
func (p *Indexer) TombstoneDocument(ctx context.Context, collectionID, documentID string) (int64, error) {
	if strings.TrimSpace(collectionID) == "" || strings.TrimSpace(documentID) == "" {
		return 0, errors.New("rag: stable collection/document id required")
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return 0, errors.New("rag: deletion requires a PostgreSQL pool")
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer rollbackTransaction(ctx, tx)
	var revision int64
	if err = tx.QueryRow(ctx, `INSERT INTO rag_document_heads(collection_id,document_id,attempt,deleted)
VALUES($1,$2,1,true) ON CONFLICT(collection_id,document_id) DO UPDATE SET attempt=rag_document_heads.attempt+1,deleted=true
RETURNING attempt`, collectionID, documentID).Scan(&revision); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM documents WHERE collection_id=$1 AND id=$2`, collectionID, documentID); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return revision, nil
}
