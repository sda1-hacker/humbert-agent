package sessions

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func TestStoreRecoversMissingConfigWithoutChangingTranscript(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Truncate(time.Millisecond)
	if err := tr.CreateSession(ctx, transcript.CreateSessionInput{ID: "interrupted", AgentID: "agent", CWD: t.TempDir(), CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.AppendMessage(ctx, "agent", "interrupted", transcript.AgentMessage{
		Role: transcript.RoleUser, Timestamp: created.UnixMilli(), Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: "keep my history"}},
	}); err != nil {
		t.Fatal(err)
	}
	dir, err := tr.SessionDirectory("agent", "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatalf("incomplete session blocked startup: %v", err)
	}
	got, err := s.GetSession(ctx, "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "恢复的会话" || !got.CreatedAt.Equal(created) {
		t.Fatalf("unexpected recovery: %#v", got)
	}
	if err := s.RenameSession(ctx, got.ID, "my title"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	list, err := reopened.ListSessions(ctx, "agent")
	if err != nil || len(list) != 1 || list[0].Title != "my title" {
		t.Fatalf("restart: %#v, %v", list, err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("recovery changed transcript")
	}
}

func TestRetryReusesOnlyTheLastUnansweredUserMessage(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, Session{ID: "session", AgentID: "agent", Title: "test", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: store, logger: logging.NewBootstrap()}
	first, err := svc.PrepareUserMessage(ctx, "session", UserInput{Text: "hello"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		retry, err := svc.PrepareUserMessage(ctx, "session", UserInput{Text: "hello"}, first.EntryID)
		if err != nil || retry.EntryID != first.EntryID {
			t.Fatalf("retry: %#v, %v", retry, err)
		}
	}
	for _, input := range []struct{ content, id string }{{"changed", first.EntryID}, {"hello", "unrelated"}} {
		if _, err := svc.PrepareUserMessage(ctx, "session", UserInput{Text: input.content}, input.id); err == nil {
			t.Fatal("invalid retry accepted")
		}
	}
	all, err := store.ListMessages(ctx, "session", 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("duplicate user messages: %d, %v", len(all), err)
	}
	if _, err := svc.AppendAssistantMessage(ctx, "session", schema.AssistantMessage("done", nil), AssistantPersistence{Provider: "test", Model: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PrepareUserMessage(ctx, "session", UserInput{Text: "hello"}, first.EntryID); err == nil {
		t.Fatal("answered message retried")
	}
}

func TestStoreDoesNotOverwriteCorruptConfig(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.CreateSession(ctx, transcript.CreateSessionInput{ID: "broken", AgentID: "agent"}); err != nil {
		t.Fatal(err)
	}
	dir, err := tr.SessionDirectory("agent", "broken")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	before := []byte(`{"partial":`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, tr); err == nil {
		t.Fatal("corrupt config silently replaced")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("corrupt config was overwritten")
	}
}
