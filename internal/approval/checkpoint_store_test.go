package approval

import (
	"context"
	"testing"
)

func TestCheckpointStoreCopiesPayload(t *testing.T) {
	store := NewCheckpointStore()
	ctx := context.Background()
	original := []byte("checkpoint")
	if err := store.Set(ctx, "run", original); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	original[0] = 'X'

	loaded, ok, err := store.Get(ctx, "run")
	if err != nil || !ok {
		t.Fatalf("Get = ok:%v err:%v", ok, err)
	}
	if string(loaded) != "checkpoint" {
		t.Fatalf("Store 共享了调用方 slice: %q", loaded)
	}
	loaded[0] = 'Y'
	loadedAgain, _, _ := store.Get(ctx, "run")
	if string(loadedAgain) != "checkpoint" {
		t.Fatalf("Get 返回值共享了内部 slice: %q", loadedAgain)
	}
}

func TestCheckpointStoreHasTracksLifecycle(t *testing.T) {
	store := NewCheckpointStore()
	ctx := context.Background()

	exists, err := store.Has(ctx, "run")
	if err != nil || exists {
		t.Fatalf("Has before Set = %v, %v", exists, err)
	}
	if err := store.Set(ctx, "run", []byte("checkpoint")); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	exists, err = store.Has(ctx, "run")
	if err != nil || !exists {
		t.Fatalf("Has after Set = %v, %v", exists, err)
	}
	if err := store.Delete(ctx, "run"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	exists, err = store.Has(ctx, "run")
	if err != nil || exists {
		t.Fatalf("Has after Delete = %v, %v", exists, err)
	}
}
