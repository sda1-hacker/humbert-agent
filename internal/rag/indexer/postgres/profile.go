package postgres

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

// EnsureCollectionProfile pins a collection's embedding space. It never
// guesses the model used by legacy vectors, or deletes existing index data.
func EnsureCollectionProfile(ctx context.Context, pool *pgxpool.Pool, collectionID, profileID, profileJSON string) error {
	if pool == nil {
		return errors.New("rag: nil profile database pool")
	}
	if strings.TrimSpace(collectionID) == "" || profileID == "" {
		return errors.New("rag: missing collection/profile id")
	}
	if profileJSON == "" {
		profileJSON = "{}"
	}
	tx, err := pool.Begin(ctx)
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
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM retrieval_index WHERE collection_id=$1)`, collectionID).Scan(&legacy); err != nil {
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

func (p *Indexer) ensureProfile(ctx context.Context, collectionID string) error {
	if p.config.ProfileID == "" {
		return nil
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return errors.New("rag: profile binding requires a PostgreSQL pool")
	}
	return EnsureCollectionProfile(ctx, db.pool, collectionID, p.config.ProfileID, p.config.ProfileJSON)
}
