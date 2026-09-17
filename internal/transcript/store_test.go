package transcript

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestLoadSessionRepairsOnlyIncompleteTail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, CreateSessionInput{ID: "session", AgentID: "agent", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	path, err := store.sessionPath("agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"message"`); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	document, err := store.LoadSession(ctx, "agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	if !document.Repair.Repaired || document.Repair.TruncatedBytes == 0 {
		t.Fatalf("repair = %#v", document.Repair)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("repaired transcript does not end in newline: %q", data)
	}
}

func TestLoadSessionCacheIsIsolatedFromCallerMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, CreateSessionInput{ID: "session", AgentID: "agent", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	first, err := store.AppendMessage(ctx, "agent", "session", testUserWireMessage("original"))
	if err != nil {
		t.Fatal(err)
	}

	document, err := store.LoadSession(ctx, "agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	document.Entries[0].ID = "mutated"
	document.Entries[0].Message.Content[0].Text = "mutated"
	document.ActiveBranch[0].ID = "mutated-branch"

	reloaded, err := store.LoadSession(ctx, "agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LeafID != first.ID || reloaded.Entries[0].ID != first.ID || reloaded.Entries[0].Message.Content[0].Text != "original" {
		t.Fatalf("caller mutation polluted cache: %#v", reloaded)
	}
	second, err := store.AppendMessage(ctx, "agent", "session", testUserWireMessage("second"))
	if err != nil {
		t.Fatal(err)
	}
	if second.ParentID == nil || *second.ParentID != first.ID {
		t.Fatalf("cached leaf was corrupted: %#v", second.ParentID)
	}
}

func TestLoadSessionCacheInvalidatesAfterExternalCompleteCorruption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, CreateSessionInput{ID: "session", AgentID: "agent", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, "agent", "session", testUserWireMessage("cached")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadSession(ctx, "agent", "session"); err != nil {
		t.Fatal(err)
	}

	path, err := store.sessionPath("agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{not-json}\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := store.LoadSession(ctx, "agent", "session"); err == nil {
		t.Fatal("external corruption was hidden by document cache")
	}
}

func TestLoadMessagePageUsesIncrementalCacheIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, CreateSessionInput{ID: "session", AgentID: "agent", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	entries := make([]Entry, 0, 6)
	for _, text := range []string{"one", "two", "three", "four", "five"} {
		entry, appendErr := store.AppendMessage(ctx, "agent", "session", testUserWireMessage(text))
		if appendErr != nil {
			t.Fatal(appendErr)
		}
		entries = append(entries, entry)
	}

	latest, err := store.LoadMessagePage(ctx, "agent", "session", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if latest.StartIndex != 3 || !latest.HasMore || latest.NextBeforeID != entries[3].ID || len(latest.Entries) != 2 {
		t.Fatalf("unexpected latest page: %#v", latest)
	}
	if latest.Entries[0].Message.Content[0].Text != "four" || latest.Entries[1].Message.Content[0].Text != "five" {
		t.Fatalf("unexpected latest messages: %#v", latest.Entries)
	}

	middle, err := store.LoadMessagePage(ctx, "agent", "session", latest.NextBeforeID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if middle.StartIndex != 1 || !middle.HasMore || len(middle.Entries) != 2 || middle.Entries[0].Message.Content[0].Text != "two" {
		t.Fatalf("unexpected middle page: %#v", middle)
	}
	if _, err := store.LoadMessagePage(ctx, "agent", "session", "missing", 2); !errors.Is(err, ErrMessageCursorNotFound) {
		t.Fatalf("missing cursor error: %v", err)
	}

	// 返回页必须与缓存隔离；随后追加还要能增量更新 Message 位置索引。
	latest.Entries[0].Message.Content[0].Text = "mutated"
	sixth, err := store.AppendMessage(ctx, "agent", "session", testUserWireMessage("six"))
	if err != nil {
		t.Fatal(err)
	}
	afterAppend, err := store.LoadMessagePage(ctx, "agent", "session", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if afterAppend.StartIndex != 4 || len(afterAppend.Entries) != 2 || afterAppend.Entries[0].ID != entries[4].ID || afterAppend.Entries[1].ID != sixth.ID {
		t.Fatalf("incremental page index was not advanced: %#v", afterAppend)
	}
	if afterAppend.Entries[0].Message.Content[0].Text != "five" || afterAppend.Entries[1].Message.Content[0].Text != "six" {
		t.Fatalf("page mutation polluted cache: %#v", afterAppend.Entries)
	}
}

func testUserWireMessage(text string) AgentMessage {
	return AgentMessage{
		Role: RoleUser, Timestamp: time.Now().UnixMilli(),
		Content: []ContentBlock{{Type: ContentText, Text: text}},
	}
}

func BenchmarkLoadMessagePageCached(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	if err := store.CreateSession(ctx, CreateSessionInput{ID: "session", AgentID: "agent", CreatedAt: time.Now()}); err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 1000; index++ {
		if _, err := store.AppendMessage(ctx, "agent", "session", testUserWireMessage("benchmark message")); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		page, err := store.LoadMessagePage(ctx, "agent", "session", "", 80)
		if err != nil || len(page.Entries) != 80 {
			b.Fatalf("page=%d err=%v", len(page.Entries), err)
		}
	}
}

func TestLoadSessionFastTailCheckStillRejectsCompleteCorruptLine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, CreateSessionInput{ID: "session", AgentID: "agent", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	path, err := store.sessionPath("agent", "session")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{not-json}\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.LoadSession(ctx, "agent", "session"); err == nil {
		t.Fatal("complete corrupt line was accepted")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() {
		t.Fatalf("complete corrupt line was truncated: before=%d after=%d", before.Size(), after.Size())
	}
}
