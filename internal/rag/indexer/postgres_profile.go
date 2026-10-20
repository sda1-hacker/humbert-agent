package indexer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrEmbeddingProfileMismatch = errors.New("rag: collection embedding profile mismatch; rebuild into a new collection before switching models")
var ErrUnboundLegacyIndex = errors.New("rag: existing vectors have no verified embedding profile; rebuild into a new collection")

// EnsureCollectionProfile 绑定知识库的向量空间，不推断历史向量模型，也不删除现有索引数据。
func EnsureCollectionProfile(ctx context.Context, pool *pgxpool.Pool, collectionID, profileID, profileJSON string) error {
	if pool == nil {
		return errors.New("rag: nil profile database pool")
	}
	return ensureCollectionProfile(ctx, pool, collectionID, profileID, profileJSON)
}

// ensureCollectionProfile 共用首次绑定与后续校验；同一知识库的模型档案受数据库锁保护。
func ensureCollectionProfile(ctx context.Context, db database, collectionID, profileID, profileJSON string) error {
	if strings.TrimSpace(collectionID) == "" || profileID == "" {
		return errors.New("rag: missing collection/profile id")
	}
	if profileJSON == "" {
		profileJSON = "{}"
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(ctx, tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "humbert-rag-profile:"+collectionID); err != nil {
		return err
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT profile_id FROM rag_collection_profiles WHERE collection_id=$1`, collectionID).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) {
		var legacy bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE collection_id=$1)`, collectionID).Scan(&legacy); err != nil {
			return err
		}
		if legacy {
			return ErrUnboundLegacyIndex
		}
		if _, err = tx.Exec(ctx, `INSERT INTO rag_collection_profiles(collection_id,profile_id,descriptor) VALUES($1,$2,$3::jsonb)`, collectionID, profileID, profileJSON); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if existing != profileID {
		return fmt.Errorf("%w: collection %q", ErrEmbeddingProfileMismatch, collectionID)
	}
	return tx.Commit(ctx)
}

// ensureProfile 校验知识库绑定的向量空间档案，防止更换模型后继续使用旧索引。
func (p *DocumentStore) ensureProfile(ctx context.Context, collectionID string) error {
	if p.config.ProfileID == "" {
		return nil
	}
	return ensureCollectionProfile(ctx, p.db, collectionID, p.config.ProfileID, p.config.ProfileJSON)
}

// CheckCollectionProfile 是查询期的只读校验，已绑定的知识库不再每次搜索取得排他咨询锁。
func CheckCollectionProfile(ctx context.Context, pool *pgxpool.Pool, collectionID, profileID string) error {
	if pool == nil {
		return errors.New("rag: nil profile database pool")
	}
	if strings.TrimSpace(collectionID) == "" || profileID == "" {
		return errors.New("rag: missing collection/profile id")
	}
	var existing string
	if err := pool.QueryRow(ctx, `SELECT profile_id FROM rag_collection_profiles WHERE collection_id=$1`, collectionID).Scan(&existing); err != nil {
		return err
	}
	if existing != profileID {
		return ErrEmbeddingProfileMismatch
	}
	return nil
}
