package preferences

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPersonalMemoryPersistsAndDeletes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config", "preferences.json")
	store, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	added, err := store.AddMemory(ctx, "  喜欢  民谣  ")
	if err != nil || added.Text != "喜欢 民谣" {
		t.Fatalf("add=%+v err=%v", added, err)
	}
	reopened, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := reopened.ListMemories(ctx)
	if err != nil || len(items) != 1 || items[0].ID != added.ID {
		t.Fatalf("persist=%+v err=%v", items, err)
	}
	if _, err := reopened.UpdateMemory(ctx, added.ID, "喜欢爵士"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteMemory(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListMemories(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("delete=%+v err=%v", items, err)
	}
}
