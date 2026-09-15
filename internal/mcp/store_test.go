package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCRUDAndKeyUniqueness(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mcp", "servers.json")
	store, err := NewStore(ctx, path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("Stat servers.json: %v", err)
	} else if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("servers.json permissions = %o, want no group/other permissions", info.Mode().Perm())
	}
	server := Server{
		ID:        "server-1",
		Key:       "github",
		Name:      "GitHub",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "npx", Args: []string{"-y", "server"}},
		CreatedAt: testTime(),
		UpdatedAt: testTime(),
	}
	if err := store.Create(ctx, server); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, server.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Key != "github" || got.Stdio == nil || len(got.Stdio.Args) != 2 {
		t.Fatalf("unexpected server: %#v", got)
	}
	duplicate := server
	duplicate.ID = "server-2"
	if err := store.Create(ctx, duplicate); !errors.Is(err, ErrServerExists) {
		t.Fatalf("duplicate key error = %v", err)
	}
	if err := store.Delete(ctx, server.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, server.ID); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("Get deleted error = %v", err)
	}
}
