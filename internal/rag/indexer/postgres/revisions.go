package postgres

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrStaleIngestion = errors.New("rag: ingestion attempt has been superseded, canceled or deleted")

func rollbackTransaction(ctx context.Context, tx transaction) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

// ReserveDocument must run before parsing. A newer reservation immediately
// invalidates older workers, while the published document remains searchable.
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

// InvalidateDocument cancels outstanding work without removing published data.
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

// DeleteDocument keeps a tombstone so a late worker cannot resurrect content.
func (p *Indexer) DeleteDocument(ctx context.Context, collectionID, documentID string) error {
	if strings.TrimSpace(collectionID) == "" || strings.TrimSpace(documentID) == "" {
		return errors.New("rag: stable collection/document id required")
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return errors.New("rag: deletion requires a PostgreSQL pool")
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(ctx, tx)
	if _, err = tx.Exec(ctx, `INSERT INTO rag_document_heads(collection_id,document_id,attempt,deleted)
VALUES($1,$2,1,true) ON CONFLICT(collection_id,document_id) DO UPDATE SET attempt=rag_document_heads.attempt+1,deleted=true`, collectionID, documentID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM documents WHERE collection_id=$1 AND id=$2`, collectionID, documentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
