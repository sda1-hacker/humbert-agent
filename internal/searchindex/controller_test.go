package searchindex

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestControllerDeduplicatesRefreshAndClosesAfterReaders(t *testing.T) {
	c := NewController(filepath.Join(t.TempDir(), "search.sqlite"))
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var calls atomic.Int32
	refresh := func(ctx context.Context, _ *Index) error {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}
	_, status, release, err := c.Acquire("key", refresh)
	if err != nil || !status.Updating {
		t.Fatalf("acquire: %#v %v", status, err)
	}
	<-started
	_, _, release2, err := c.Acquire("key", refresh)
	if err != nil {
		t.Fatal(err)
	}
	release2()
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	<-cancelled
	select {
	case <-done:
		t.Fatal("closed before query released")
	case <-time.After(10 * time.Millisecond):
	}
	release()
	release() // 多次清理不会使 WaitGroup 变为负数。
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls: %d", calls.Load())
	}
	if _, _, _, err := c.Acquire("key", refresh); !errors.Is(err, context.Canceled) {
		t.Fatalf("read after close: %v", err)
	}
}
