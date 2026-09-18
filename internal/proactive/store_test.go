package proactive

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistsSettingsAndRecord(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config", "proactive.json")
	store, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.Settings()
	settings.HeartbeatIntervalMinutes = 11
	if _, err := store.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := Record{ID: "r1", Event: Event{Key: "event-1", Kind: EventTaskFailed, OccurredAt: now}, Decision: Decision{Action: ActionNotify}, Status: RecordSucceeded, CreatedAt: now, UpdatedAt: now, HandledAt: &now}
	if err := store.PutRecord(ctx, record); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Settings().HeartbeatIntervalMinutes != 11 {
		t.Fatalf("heartbeat interval not persisted")
	}
	if got, ok := reloaded.FindByEventKey("event-1"); !ok || got.ID != "r1" {
		t.Fatalf("record not persisted: %#v %v", got, ok)
	}
}
