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

func TestStorePersistsPendingEventUntilAcknowledged(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config", "proactive.json")
	store, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Key: "pending-1", Kind: EventWorkspaceChanged, OccurredAt: time.Now().UTC()}
	if enqueued, err := store.EnqueueEvent(ctx, event); err != nil || !enqueued {
		t.Fatalf("first enqueue=(%v,%v), want (true,nil)", enqueued, err)
	}
	// 同一事件键只允许入队一次，防止 watcher 重试制造重复 Agent Run。
	if enqueued, err := store.EnqueueEvent(ctx, event); err != nil || enqueued {
		t.Fatalf("duplicate enqueue=(%v,%v), want (false,nil)", enqueued, err)
	}

	reloaded, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.PendingEvents(); len(got) != 1 || got[0].Key != event.Key {
		t.Fatalf("pending event not recovered: %#v", got)
	}
	if err := reloaded.RemovePendingEvent(ctx, event.Key); err != nil {
		t.Fatal(err)
	}
	reloadedAgain, err := NewStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloadedAgain.PendingEventCount(); got != 0 {
		t.Fatalf("pending event count = %d, want 0", got)
	}
}
