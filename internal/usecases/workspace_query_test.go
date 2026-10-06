package usecases

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// 看板查询应读取当前 Profile 和磁盘事实，删除文件或切换工作区后不能继续返回旧内容。
func TestWorkspaceQueryReadsCurrentProfileAndFilesystem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	transcripts, err := transcript.NewStore(filepath.Join(root, "agents"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := agents.NewStore(ctx, transcripts.AgentsRoot(), transcripts)
	if err != nil {
		t.Fatal(err)
	}
	first, second := t.TempDir(), t.TempDir()
	for directory, content := range map[string]string{first: "first", second: "second"} {
		if err := os.WriteFile(filepath.Join(directory, "note.md"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	agent := agents.Agent{ID: "00000000-0000-4000-8000-000000000123", Name: "query", WorkspaceMode: workspace.ModeCustom, WorkspacePath: first, CreatedAt: time.Now().UTC()}
	if err := store.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(ctx, filepath.Join(root, "managed"), logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	query, err := NewWorkspaceQuery(agents.NewService(store, nil, nil), manager)
	if err != nil {
		t.Fatal(err)
	}
	if overview, err := query.Overview(ctx, agent.ID); err != nil || overview.FileCount != 1 || overview.TotalBytes != 5 {
		t.Fatalf("overview=%+v, err=%v", overview, err)
	}
	if preview, err := query.PreviewFile(ctx, agent.ID, "note.md"); err != nil || preview.Content != "first" {
		t.Fatalf("preview=%+v, err=%v", preview, err)
	}
	if err := os.Remove(filepath.Join(first, "note.md")); err != nil {
		t.Fatal(err)
	}
	if listing, err := query.ListDirectory(ctx, agent.ID, "."); err != nil || len(listing.Entries) != 0 {
		t.Fatalf("deleted file remains visible: %+v, err=%v", listing, err)
	}
	if _, err := query.PreviewFile(ctx, agent.ID, "note.md"); err == nil {
		t.Fatal("preview returned a deleted file")
	}
	if _, err := store.Mutate(ctx, agent.ID, func(value *agents.Agent) error { value.WorkspacePath = second; return nil }); err != nil {
		t.Fatal(err)
	}
	if preview, err := query.PreviewFile(ctx, agent.ID, "note.md"); err != nil || preview.Content != "second" {
		t.Fatalf("profile change ignored: %+v, err=%v", preview, err)
	}
	if _, err := query.ListDirectory(ctx, agent.ID, "../"); err == nil {
		t.Fatal("workspace query allowed path escape")
	}
}
