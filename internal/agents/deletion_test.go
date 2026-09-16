package agents_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func TestDeletePreservesCustomWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newDeletionFixture(t)

	customRoot := t.TempDir()
	sentinel := filepath.Join(customRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	value := testAgent("00000000-0000-4000-8000-000000000001", workspace.ModeCustom, customRoot)
	if err := fixture.store.Create(ctx, value); err != nil {
		t.Fatal(err)
	}

	if err := fixture.service.Delete(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("Custom Workspace 被删除或修改: %v", err)
	}
	if _, err := fixture.store.Get(ctx, value.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("Agent 删除后仍可读取: %v", err)
	}
}

func TestRecoverPendingManagedDeletionAfterRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newDeletionFixture(t)
	value := testAgent("00000000-0000-4000-8000-000000000002", workspace.ModeManaged, "")
	if err := fixture.store.Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	resolved, err := fixture.workspaces.Resolve(ctx, value.ID, value.WorkspaceMode, value.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolved.RootDir, "generated.txt"), []byte("managed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.BeginDelete(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Get(ctx, value.ID); !errors.Is(err, agents.ErrDeleting) {
		t.Fatalf("deleting Agent 没有立即隐藏: %v", err)
	}

	restarted := agents.NewService(
		fixture.store,
		nil,
		logging.NewBootstrap(),
		agents.WithWorkspaceManager(fixture.workspaces),
	)
	if err := restarted.RecoverDeletions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resolved.RootDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Managed Workspace 未在恢复流程中删除: %v", err)
	}
	if _, err := fixture.store.Get(ctx, value.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("恢复后 Agent 目录仍存在: %v", err)
	}
}

func TestConcurrentSessionCreateAndAgentDeleteLeavesNoOrphan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newDeletionFixture(t)
	customRoot := t.TempDir()
	value := testAgent("00000000-0000-4000-8000-000000000003", workspace.ModeCustom, customRoot)
	if err := fixture.store.Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	sessionStore, err := sessions.NewStore(ctx, fixture.transcripts)
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	createDone := make(chan error, 1)
	go func() {
		createDone <- fixture.service.WithActiveAgent(ctx, value.ID, func(_ agents.AgentInfo) error {
			close(entered)
			<-release
			now := time.Now().UTC()
			return sessionStore.CreateSession(ctx, sessions.Session{
				ID: "concurrent-session", AgentID: value.ID, Title: "test", CWD: customRoot,
				CreatedAt: now, UpdatedAt: now,
			})
		})
	}()
	<-entered

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- fixture.service.Delete(ctx, value.ID) }()
	select {
	case err := <-deleteDone:
		t.Fatalf("删除没有等待正在创建的 Session: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-createDone; err != nil {
		t.Fatal(err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}

	agentDir := filepath.Join(fixture.agentsRoot, value.ID)
	if _, err := os.Stat(agentDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("并发创建留下了 Agent/Session 孤儿目录: %v", err)
	}
	if _, err := os.Stat(customRoot); err != nil {
		t.Fatalf("并发删除触碰了 Custom Workspace: %v", err)
	}
}

type deletionFixture struct {
	agentsRoot  string
	transcripts *transcript.Store
	store       *agents.Store
	service     *agents.Service
	workspaces  *workspace.Manager
}

func newDeletionFixture(t *testing.T) deletionFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	agentsRoot := filepath.Join(root, "agents")
	transcripts, err := transcript.NewStore(agentsRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := agents.NewStore(ctx, agentsRoot, transcripts)
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := workspace.NewManager(ctx, filepath.Join(root, "workspaces"), logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspaces.Close() })
	service := agents.NewService(store, nil, logging.NewBootstrap(), agents.WithWorkspaceManager(workspaces))
	return deletionFixture{
		agentsRoot: agentsRoot, transcripts: transcripts, store: store,
		service: service, workspaces: workspaces,
	}
}

func testAgent(id string, mode workspace.Mode, path string) agents.Agent {
	now := time.Now().UTC()
	return agents.Agent{
		ID: id, Name: id, WorkspaceMode: mode, WorkspacePath: path,
		CreatedAt: now, UpdatedAt: now,
	}
}
