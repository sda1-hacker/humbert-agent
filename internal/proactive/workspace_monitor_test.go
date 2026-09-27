package proactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceScanLimitDoesNotInventAdditionsOrDeletions(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	for i := 0; i < maxWorkspaceFingerprintFiles; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d.txt", i)), []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := scanWorkspace(ctx, "agent", root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Truncated {
		t.Fatal("exactly 3000 files should be a complete snapshot")
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	partial, err := scanWorkspace(ctx, "agent", root)
	if err != nil {
		t.Fatal(err)
	}
	if !partial.Truncated || len(partial.Files) != maxWorkspaceFingerprintFiles {
		t.Fatalf("unexpected partial snapshot: files=%d truncated=%v", len(partial.Files), partial.Truncated)
	}
	summary := workspaceChangeSummary(before, partial)
	if !strings.Contains(summary, "新增 1，修改 0，删除 0") || !strings.Contains(summary, "扫描不完整") {
		t.Fatalf("new file invented deletion: %s", summary)
	}
	if _, err := os.Stat(filepath.Join(root, "file-2999.txt")); err != nil {
		t.Fatal(err)
	}
	// 删除一个已扫描文件后恢复完整快照。原先落在上限之外的文件不能被误报为新增。
	if err := os.Remove(filepath.Join(root, "file-0000.txt")); err != nil {
		t.Fatal(err)
	}
	after, err := scanWorkspace(ctx, "agent", root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Truncated {
		t.Fatal("snapshot should be complete again")
	}
	summary = workspaceChangeSummary(partial, after)
	if !strings.Contains(summary, "新增 0，修改 0，删除 1") || !strings.Contains(summary, "扫描不完整") {
		t.Fatalf("newly observed file invented addition: %s", summary)
	}
}

func TestCompleteWorkspaceSnapshotsKeepExactCounts(t *testing.T) {
	stamp := WorkspaceFileStamp{Size: 1}
	previous := WorkspaceSnapshot{Files: map[string]WorkspaceFileStamp{"changed": stamp, "deleted": stamp}}
	current := WorkspaceSnapshot{Files: map[string]WorkspaceFileStamp{"changed": {Size: 2}, "added": stamp}}
	want := "工作区发生变化：新增 1，修改 1，删除 1。"
	if got := workspaceChangeSummary(previous, current); got != want {
		t.Fatalf("summary=%q", got)
	}
	previous.Truncated = true
	current.Truncated = true
	if got := workspaceChangeSummary(previous, current); !strings.Contains(got, "新增 0，修改 1，删除 0") {
		t.Fatalf("partial snapshots made unsupported claims: %s", got)
	}
}

func TestWorkspaceScanRejectsMissingRootAndCancellation(t *testing.T) {
	root := t.TempDir()
	if _, err := scanWorkspace(context.Background(), "agent", filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing root must not become an empty complete snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanWorkspace(ctx, "agent", root); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
}
