package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type rowQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type BackendStatus struct {
	Postgres int
	Vector   string
	PGSearch string
}

func CheckExtensions(ctx context.Context, db rowQueryer) error {
	var status BackendStatus
	err := db.QueryRow(ctx, `SELECT current_setting('server_version_num')::int,
COALESCE((SELECT extversion FROM pg_extension WHERE extname='vector'),''),
COALESCE((SELECT extversion FROM pg_extension WHERE extname='pg_search'),'')`).Scan(&status.Postgres, &status.Vector, &status.PGSearch)
	if err != nil {
		return fmt.Errorf("rag backend health: %w", err)
	}
	return status.Validate()
}

func (s BackendStatus) Validate() error {
	if s.Postgres < 150000 {
		return fmt.Errorf("rag backend: PostgreSQL 15+ required for column-specific ON DELETE SET NULL")
	}
	if !versionAtLeast(s.Vector, 0, 7, 0) {
		return fmt.Errorf("rag backend: vector 0.7.0+ required for halfvec (installed %q)", s.Vector)
	}
	if !versionAtLeast(s.PGSearch, 0, 25, 0) {
		return fmt.Errorf("rag backend: pg_search 0.25.0+ required for USING paradedb (installed %q)", s.PGSearch)
	}
	return nil
}

func versionAtLeast(version string, major, minor, patch int) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	want := []int{major, minor, patch}
	var actual [3]int
	for i := range actual {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return false
		}
		actual[i] = n
	}
	for i := range actual {
		if actual[i] != want[i] {
			return actual[i] > want[i]
		}
	}
	return true
}

func CheckSchema(ctx context.Context, db rowQueryer) error {
	var version, count int
	if err := db.QueryRow(ctx, `SELECT COALESCE(MAX(version),0),COUNT(*) FROM rag_schema_migrations`).Scan(&version, &count); err != nil {
		return fmt.Errorf("rag schema is not initialized; run cmd/rag-migrate: %w", err)
	}
	if version != SchemaVersion || count != SchemaVersion {
		return fmt.Errorf("rag schema version %d, expected %d; run cmd/rag-migrate", version, SchemaVersion)
	}
	return nil
}
