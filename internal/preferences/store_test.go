package preferences

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStorePersistsUserProfile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "preferences.json")
	store, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Name != "你" {
		t.Fatalf("default name = %q", initial.Name)
	}
	if _, err := store.Update(ctx, UserProfile{Name: " Alice "}); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := reopened.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "Alice" {
		t.Fatalf("name = %q", profile.Name)
	}
}
