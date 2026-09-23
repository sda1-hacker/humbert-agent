package transcript

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLargeSessionLocationIndexReadsOnlyCurrentWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("large JSONL regression")
	}
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, CreateSessionInput{ID: "session", AgentID: "agent", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var first, last Entry
	body := strings.Repeat("x", 7_500_000)
	for i := 0; i < 9; i++ {
		message := testUserWireMessage(body)
		if i == 0 {
			message.Content[0].Text = "needle-old" + body
		}
		entry, err := store.AppendMessage(ctx, "agent", "session", message)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = entry
		}
		last = entry
	}
	path, _ := store.sessionPath("agent", "session")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= defaultDocumentCacheBytes {
		t.Fatalf("fixture too small: %d", info.Size())
	}
	_, err = store.AppendCompaction(ctx, "agent", "session", AppendCompactionInput{
		ExpectedLeafID: last.ID, FirstKeptEntryID: last.ID, Summary: "checkpoint", TokensBefore: 100, TokensAfter: 50,
		Details: CompactionDetails{Reason: "test", WindowGeneration: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	recent, err := store.AppendMessage(ctx, "agent", "session", testUserWireMessage("recent-marker"))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	view, err := reopened.LoadContextSession(ctx, "agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	if view.ReadStats.IndexRebuilt || view.ReadStats.CacheHit {
		t.Fatalf("expected persisted index load: %#v", view.ReadStats)
	}
	if view.ReadStats.BytesRead > 16_000_000 {
		t.Fatalf("read entire transcript: %#v", view.ReadStats)
	}
	if len(view.ActiveBranch) != 3 || len(view.Lineage) != 11 || view.LeafID != recent.ID || view.ContextWindow.LatestCompactionIndex != 1 {
		t.Fatalf("bad window: branch=%d lineage=%d window=%#v", len(view.ActiveBranch), len(view.Lineage), view.ContextWindow)
	}
	again, err := reopened.LoadContextSession(ctx, "agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	if !again.ReadStats.CacheHit {
		t.Fatalf("index cache did not hit: %#v", again.ReadStats)
	}
	page, err := reopened.LoadMessagePage(ctx, "agent", "session", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[1].ID != recent.ID {
		t.Fatalf("large session message page=%#v", page)
	}
	visited := 0
	err = reopened.VisitActiveBranchReverse(ctx, "agent", "session", func(entry Entry) bool { visited++; return false })
	if err != nil {
		t.Fatal(err)
	}
	if visited != 1 {
		t.Fatalf("visited %d entries", visited)
	}
	old, err := reopened.ReadActiveBranchRange(ctx, "agent", "session", first.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 1 || !strings.HasPrefix(old[0].Message.Content[0].Text, "needle-old") {
		t.Fatal("failed to recover old original entry")
	}
	// A sidecar that misses a durable append must be rebuilt from authoritative JSONL.
	sidecar := sidecarPath(path)
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	cut := strings.LastIndex(string(data[:len(data)-1]), "\n") + 1
	if err := os.WriteFile(sidecar, data[:cut], 0o600); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := rebuilt.LoadContextSession(ctx, "agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.ReadStats.IndexRebuilt || repaired.LeafID != recent.ID {
		t.Fatalf("stale sidecar was trusted: %#v", repaired.ReadStats)
	}
	data, err = os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	headerEnd := strings.IndexByte(string(data), '\n') + 1
	if err := os.WriteFile(sidecar, data[:headerEnd], 0o600); err != nil {
		t.Fatal(err)
	}
	headerOnly, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	view, err = headerOnly.LoadContextSession(ctx, "agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	if !view.ReadStats.IndexRebuilt || view.LeafID != recent.ID {
		t.Fatal("header-only sidecar was trusted")
	}
}
