package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func newArtifactTestSession(t *testing.T) (*Service, Session, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	transcripts, err := transcript.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, transcripts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := Session{ID: "session-artifact", AgentID: "agent-a", Title: "test", CWD: root, CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	directory, err := store.SessionDirectory(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{store: store}, session, directory
}

// 迁移后的入口继续写入同一 sidecar 协议，并通过工具最小接口返回完整 Unicode 内容。
func TestContextArtifactArchiveAndRead(t *testing.T) {
	t.Parallel()
	service, session, directory := newArtifactTestSession(t)
	content := strings.Repeat("工具输出中间内容", 200)
	id, err := service.Archive(context.Background(), session.ID, "read_file", content)
	if err != nil || !strings.HasPrefix(id, "artifact_") {
		t.Fatalf("Archive: id=%q, err=%v", id, err)
	}
	tool, got, chars, err := service.ReadContextArtifact(context.Background(), session.ID, id)
	if err != nil || tool != "read_file" || got != content || chars != len([]rune(content)) {
		t.Fatalf("ReadContextArtifact: tool=%q, chars=%d, err=%v", tool, chars, err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "context-artifacts", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var document contextArtifact
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 || document.ID != id || document.SessionID != session.ID || document.Content != content || document.CreatedAt.IsZero() {
		t.Fatalf("persisted artifact changed: %+v", document)
	}
}

func TestContextArtifactRejectsInvalidIDAndIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, session, directory := newArtifactTestSession(t)
	if _, _, _, err := service.ReadContextArtifact(ctx, session.ID, "../secret"); err == nil {
		t.Fatal("accepted an invalid artifact ID")
	}
	id, err := service.Archive(ctx, session.ID, "read_file", "private result")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "context-artifacts", id+".json")
	for _, change := range []func(*contextArtifact){
		func(d *contextArtifact) { d.Version = 2 },
		func(d *contextArtifact) { d.SessionID = "another-session" },
		func(d *contextArtifact) { d.ID = "artifact_another" },
	} {
		document := contextArtifact{Version: 1, ID: id, SessionID: session.ID, Content: "private result"}
		change(&document)
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := service.ReadContextArtifact(ctx, session.ID, id); err == nil {
			t.Fatalf("accepted mismatched identity: %+v", document)
		}
	}
	if err := service.store.DeleteSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Archive(ctx, session.ID, "read_file", "late result"); err == nil {
		t.Fatal("recreated artifacts for a deleted session")
	}
}

// UUID 文档互不修改；去掉旧全局锁后，并发首次创建目录和读取不能丢失或串写结果。
func TestContextArtifactsCanBeArchivedConcurrently(t *testing.T) {
	t.Parallel()
	service, session, _ := newArtifactTestSession(t)
	type result struct {
		id, content string
		err         error
	}
	const count = 24
	results := make(chan result, count)
	for index := range count {
		go func() {
			content := fmt.Sprintf("并发结果-%d", index)
			id, err := service.Archive(context.Background(), session.ID, "read_file", content)
			results <- result{id, content, err}
		}()
	}
	ids := make(map[string]bool, count)
	for range count {
		result := <-results
		if result.err != nil || ids[result.id] {
			t.Errorf("concurrent archive: id=%q, err=%v", result.id, result.err)
			continue
		}
		ids[result.id] = true
		_, got, _, err := service.ReadContextArtifact(context.Background(), session.ID, result.id)
		if err != nil || got != result.content {
			t.Errorf("concurrent read: got=%q, want=%q, err=%v", got, result.content, err)
		}
	}
}
