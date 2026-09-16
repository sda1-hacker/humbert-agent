package sessions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestListMessagePageUsesStableBeforeCursor(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, Session{ID: "paged", AgentID: "agent", Title: "test", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	for index := 1; index <= 7; index++ {
		message := schema.UserMessage(fmt.Sprintf("message-%d", index))
		options := transcript.EncodeOptions{}
		if index%2 == 0 {
			message = schema.AssistantMessage(fmt.Sprintf("message-%d", index), nil)
			options.Provider = "test"
			options.Model = "test"
		}
		if _, err := store.AppendMessage(ctx, "paged", message, options); err != nil {
			t.Fatal(err)
		}
	}

	latest, err := store.ListMessagePage(ctx, "paged", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if latest.StartIndex != 4 || !latest.HasMore || len(latest.Messages) != 3 {
		t.Fatalf("unexpected latest page: %#v", latest)
	}
	if latest.Messages[0].Message.Content != "message-5" || latest.Messages[2].Message.Content != "message-7" {
		t.Fatalf("unexpected latest contents: %#v", latest.Messages)
	}

	middle, err := store.ListMessagePage(ctx, "paged", latest.NextBeforeID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if middle.StartIndex != 1 || !middle.HasMore || len(middle.Messages) != 3 {
		t.Fatalf("unexpected middle page: %#v", middle)
	}
	if middle.Messages[0].Message.Content != "message-2" || middle.Messages[2].Message.Content != "message-4" {
		t.Fatalf("unexpected middle contents: %#v", middle.Messages)
	}

	oldest, err := store.ListMessagePage(ctx, "paged", middle.NextBeforeID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if oldest.StartIndex != 0 || oldest.HasMore || len(oldest.Messages) != 1 || oldest.Messages[0].Message.Content != "message-1" {
		t.Fatalf("unexpected oldest page: %#v", oldest)
	}
	if _, err := store.ListMessagePage(ctx, "paged", "missing", 3); !errors.Is(err, ErrMessageCursorNotFound) {
		t.Fatalf("invalid cursor error: %v", err)
	}
}

func TestListMessagePageKeepsToolTransactionTogether(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, Session{ID: "tools", AgentID: "agent", Title: "test", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	messages := []*schema.Message{
		schema.UserMessage("run tools"),
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call-1", Function: schema.FunctionCall{Name: "first", Arguments: `{}`}},
			{ID: "call-2", Function: schema.FunctionCall{Name: "second", Arguments: `{}`}},
		}),
		schema.ToolMessage("first", "call-1"),
		schema.ToolMessage("second", "call-2"),
		schema.AssistantMessage("finished", nil),
	}
	for _, message := range messages {
		options := transcript.EncodeOptions{}
		if message.Role == schema.Assistant {
			options.Provider = "test"
			options.Model = "test"
		}
		if message.Role == schema.Tool {
			message.ToolName = "test-tool"
		}
		if _, err := store.AppendMessage(ctx, "tools", message, options); err != nil {
			t.Fatal(err)
		}
	}

	page, err := store.ListMessagePage(ctx, "tools", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 4 || page.Messages[0].Message.Role != schema.Assistant || page.Messages[3].Message.Content != "finished" {
		t.Fatalf("tool transaction was split: %#v", page.Messages)
	}
}

func TestFirstUserInputAutomaticallyNamesDefaultSession(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, Session{ID: "auto-title", AgentID: "agent", Title: defaultSessionTitle, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: store, logger: logging.NewBootstrap()}
	if _, err := svc.AppendUserMessage(ctx, "auto-title", "  帮我   整理今天的工作计划  "); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(ctx, "auto-title")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "帮我 整理今天的工作计划" {
		t.Fatalf("unexpected automatic title: %q", got.Title)
	}
	if _, err := svc.AppendUserMessage(ctx, "auto-title", "第二条不能改标题"); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetSession(ctx, "auto-title")
	if err != nil || got.Title != "帮我 整理今天的工作计划" {
		t.Fatalf("automatic title changed again: %#v, %v", got, err)
	}
}

func TestConcurrentFirstInputsStillAutomaticallyNameSession(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, Session{ID: "concurrent-title", AgentID: "agent", Title: defaultSessionTitle, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: store, logger: logging.NewBootstrap()}
	errs := make(chan error, 2)
	for _, content := range []string{"第一条并发输入", "第二条并发输入"} {
		content := content
		go func() {
			_, appendErr := svc.AppendUserMessage(ctx, "concurrent-title", content)
			errs <- appendErr
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.GetSession(ctx, "concurrent-title")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "第一条并发输入" && got.Title != "第二条并发输入" {
		t.Fatalf("concurrent first inputs left default title: %q", got.Title)
	}
}

func TestNormalizeTitleCountsCharactersInsteadOfUTF8Bytes(t *testing.T) {
	title := strings.Repeat("会", maxSessionTitleLength)
	got, err := normalizeTitle(title)
	if err != nil || got != title {
		t.Fatalf("valid multibyte title rejected: %q, %v", got, err)
	}
	if _, err := normalizeTitle(title + "话"); err == nil {
		t.Fatal("overlong title accepted")
	}
}

func TestStoreDoesNotOverwriteCorruptConfig(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initial, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.CreateSession(ctx, Session{
		ID: "healthy", AgentID: "agent", Title: "healthy", CWD: t.TempDir(), CreatedAt: time.Now().UTC(),
	}); err != nil {
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
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatalf("corrupt session blocked startup: %v", err)
	}
	issues := store.Issues()
	if len(issues) != 1 || issues[0].SessionID != "broken" || issues[0].AgentID != "agent" {
		t.Fatalf("unexpected isolated session diagnostics: %#v", issues)
	}
	if _, err := store.GetSession(ctx, "broken"); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatalf("corrupt session did not return ErrSessionUnavailable: %v", err)
	}
	list, err := store.ListSessions(ctx, "agent")
	if err != nil || len(list) != 1 || list[0].ID != "healthy" {
		t.Fatalf("healthy session listing was not isolated: %#v, %v", list, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("corrupt config was overwritten")
	}
}
