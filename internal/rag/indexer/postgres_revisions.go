package indexer

import (
	"context"
	"errors"
	"strings"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

var ErrStaleIngestion = errors.New("rag: ingestion attempt has been superseded, canceled or deleted")

// DiscardAttempt 与发布争用同一文档行锁；提交结果不明时，已发布版本绝不会被补偿删除。
// 只作废指定任务，不能递增后来的新任务版本。
func (p *DocumentStore) DiscardAttempt(ctx context.Context, collectionID, documentID string, attempt int64) (bool, error) {
	if strings.TrimSpace(collectionID) == "" || strings.TrimSpace(documentID) == "" || attempt <= 0 {
		return false, errors.New("rag: collection/document id and positive attempt required")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackTransaction(ctx, tx)
	// 即使已被取消/更新也取得同一行锁，等待在途发布事务确定提交状态。
	if _, err := tx.Exec(ctx, `UPDATE rag_document_heads SET attempt=attempt WHERE collection_id=$1 AND document_id=$2`, collectionID, documentID); err != nil {
		return false, err
	}
	var published bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE collection_id=$1 AND id=$2 AND revision=$3)`, collectionID, documentID, attempt).Scan(&published); err != nil {
		return false, err
	}
	if !published {
		if _, err := tx.Exec(ctx, `UPDATE rag_document_heads SET attempt=attempt+1,deleted=true WHERE collection_id=$1 AND document_id=$2 AND attempt=$3`, collectionID, documentID, attempt); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return !published, nil
}

// rollbackTransaction 使用独立短超时回滚事务，避免请求取消后无法清理。
func rollbackTransaction(ctx context.Context, tx transaction) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), ragconfig.CleanupTimeout)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

// ReserveDocument 在解析前预留新版本，使旧任务无法发布；上一版已发布文档仍可检索。
func (p *DocumentStore) ReserveDocument(ctx context.Context, collectionID, documentID string) (int64, error) {
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
func (p *DocumentStore) InvalidateDocument(ctx context.Context, collectionID, documentID string) error {
	if strings.TrimSpace(collectionID) == "" || strings.TrimSpace(documentID) == "" {
		return errors.New("rag: stable collection/document id required")
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return errors.New("rag: invalidation requires a PostgreSQL pool")
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(ctx, tx)
	_, err = tx.Exec(ctx, `INSERT INTO rag_document_heads(collection_id,document_id,attempt,deleted)
VALUES($1,$2,1,true) ON CONFLICT(collection_id,document_id) DO UPDATE SET attempt=rag_document_heads.attempt+1,deleted=true`, collectionID, documentID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE documents SET parse_status='cancelled',error_code='cancelled',error_message='用户已取消导入',processing_deadline=NULL,updated_at=NOW() WHERE collection_id=$1 AND id=$2 AND parse_status='processing'`, collectionID, documentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteDocument 删除正文并留下墓碑，阻止迟到的入库任务复活文档。
func (p *DocumentStore) DeleteDocument(ctx context.Context, collectionID, documentID string) error {
	_, err := p.TombstoneDocument(ctx, collectionID, documentID)
	return err
}

// TombstoneDocument 原子隐藏正文并返回删除截止版本，供分离索引清理使用。
func (p *DocumentStore) TombstoneDocument(ctx context.Context, collectionID, documentID string) (int64, error) {
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
