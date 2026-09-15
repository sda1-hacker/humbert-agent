package projects

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func TestStoreProjectRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := NewStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	value := Project{ID: "project-a", Name: "Project A", AgentID: "agent-a", WorkspaceMode: workspace.ModeManaged, CreatedAt: now, UpdatedAt: now}
	if err := store.Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != value.ID || got.AgentID != value.AgentID || got.WorkspaceMode != workspace.ModeManaged {
		t.Fatalf("unexpected project: %#v", got)
	}
	got.Name = "Renamed"
	got.UpdatedAt = got.UpdatedAt.Add(time.Second)
	if err := store.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Name != "Renamed" {
		t.Fatalf("unexpected project list: %#v", listed)
	}
	if err := store.Delete(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, value.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
