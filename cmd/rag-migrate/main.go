// rag-migrate installs extensions and migrates an explicitly configured RAG DB.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag/indexer/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, logging.SafeErrorText(err, 2048))
		os.Exit(1)
	}
}
func run() error {
	dsn := os.Getenv("HUMBERT_RAG_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("HUMBERT_RAG_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := postgres.EnsureExtensions(ctx, dsn); err != nil {
		return err
	}
	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	fmt.Printf("RAG schema ready: version %d\n", postgres.SchemaVersion)
	return nil
}
