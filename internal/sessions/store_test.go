package sessions

import (
	"archive/zip"
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
	"github.com/sda1-hacker/humbert-agent/internal/databackup"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func TestStoreRecoversUnindexedTranscriptWithoutChangingMessages(t *testing.T) {
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
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("恢复过程不应生成 config.json: %v", err)
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

func TestLegacyConfigIsIgnored(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tr, err := transcript.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Truncate(time.Millisecond)
	if err := tr.CreateSession(ctx, transcript.CreateSessionInput{ID: "legacy", AgentID: "agent", CWD: root, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	dir, err := tr.SessionDirectory("agent", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`{"schema_version":3,"session":{"id":"legacy","agent_id":"agent","title":"旧标题","cwd":%q,"created_at":%q}}`, root, created.Format(time.RFC3339Nano))), 0o600); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(dir, "session.jsonl")
	before, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.GetSession(ctx, "legacy")
	if err != nil || initial.Title != "恢复的会话" || initial.Archived {
		t.Fatalf("旧 config.json 不应导入: %+v, %v", initial, err)
	}
	if err := store.RenameSession(ctx, "legacy", "新标题"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetArchived(ctx, "legacy", true); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// 旧文件即使仍在目录中，也不影响 SQLite 中的新状态。
	reopened, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	listed, err := reopened.ListSessions(ctx, "agent")
	if err != nil || len(listed) != 1 || listed[0].Title != "新标题" || !listed[0].Archived {
		t.Fatalf("迁移后元数据错误: %+v, %v", listed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "session-metadata.sqlite")); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(transcriptPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("元数据操作修改了消息事实: %v", err)
	}
	legacyConfig, err := os.ReadFile(config)
	if err != nil || !bytes.Contains(legacyConfig, []byte("旧标题")) {
		t.Fatalf("旧配置被修改: %v", err)
	}
}

func TestSessionMetadataSortAndAgentPurge(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"first", "second"} {
		if err := store.CreateSession(ctx, Session{ID: id, AgentID: "agent", Title: id, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AppendMessage(ctx, "first", schema.UserMessage("latest"), transcript.EncodeOptions{}); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListSessions(ctx, "agent")
	if err != nil || len(listed) != 2 || listed[0].ID != "first" {
		t.Fatalf("会话列表未按最新消息排序: %+v, %v", listed, err)
	}
	if err := store.PurgeAgentMetadata(ctx, "agent"); err != nil {
		t.Fatal(err)
	}
	listed, err = store.ListSessions(ctx, "agent")
	if err != nil || len(listed) != 0 {
		t.Fatalf("Agent 元数据未清理: %+v, %v", listed, err)
	}
}

func TestSessionMetadataIncludedInOfflineBackup(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	home := filepath.Join(parent, "home")
	tr, err := transcript.NewStore(filepath.Join(home, "agents"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, Session{ID: "saved", AgentID: "agent", Title: "需要备份", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "backup.zip")
	if err := databackup.Create(ctx, home, archive); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name == "agents/session-metadata.sqlite" {
			return
		}
	}
	t.Fatal("离线备份缺少会话元数据库")
}

func TestArchivePersistsWithoutChangingTranscript(t *testing.T) {
	ctx := context.Background()
	tr, err := transcript.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC()
	if err := store.CreateSession(ctx, Session{ID: "archived", AgentID: "agent", Title: "归档测试", CWD: t.TempDir(), CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	dir, err := tr.SessionDirectory("agent", "archived")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetArchived(ctx, "archived", true); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetSession(ctx, "archived")
	if err != nil || !got.Archived {
		t.Fatalf("archive=%+v err=%v", got, err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "session.jsonl"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("archive changed transcript: %v", err)
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

func TestStoreIgnoresCorruptLegacyConfig(t *testing.T) {
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
	if issues := store.Issues(); len(issues) != 0 {
		t.Fatalf("旧配置不应影响会话: %#v", issues)
	}
	if recovered, err := store.GetSession(ctx, "broken"); err != nil || recovered.Title != "恢复的会话" {
		t.Fatalf("JSONL Header 恢复失败: %+v, %v", recovered, err)
	}
	list, err := store.ListSessions(ctx, "agent")
	if err != nil || len(list) != 2 {
		t.Fatalf("旧配置影响会话列表: %#v, %v", list, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("corrupt config was overwritten")
	}
}

func TestStoreIsolatesInvalidTranscriptHeader(t *testing.T) {
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
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session"`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, tr)
	if err != nil {
		t.Fatalf("单个损坏会话不应阻止启动: %v", err)
	}
	defer store.Close()
	if issues := store.Issues(); len(issues) != 1 || issues[0].SessionID != "broken" {
		t.Fatalf("损坏会话未隔离: %#v", issues)
	}
	if _, err := store.GetSession(ctx, "broken"); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatalf("损坏会话错误类型: %v", err)
	}
}
