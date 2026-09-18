package contextartifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testDirectoryResolver struct{ root string }

func (r testDirectoryResolver) SessionDirectory(_ context.Context, sessionID string) (string, error) {
	return filepath.Join(r.root, sessionID), nil
}

func TestStoreArchiveAndRead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := NewStore(testDirectoryResolver{root: root})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	content := strings.Repeat("工具输出中间内容", 200)
	id, err := store.Archive(context.Background(), "session-1", "read_file", content)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if !strings.HasPrefix(id, "artifact_") {
		t.Fatalf("unexpected artifact id: %s", id)
	}

	artifact, err := store.Read(context.Background(), "session-1", id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if artifact.Content != content || artifact.ToolName != "read_file" {
		t.Fatalf("artifact mismatch: %#v", artifact)
	}
	if artifact.Chars != len([]rune(content)) {
		t.Fatalf("chars mismatch: got=%d want=%d", artifact.Chars, len([]rune(content)))
	}

	path := filepath.Join(root, "session-1", "context-artifacts", id+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("artifact path is directory")
	}
}

func TestStoreRejectsInvalidArtifactID(t *testing.T) {
	t.Parallel()
	store, err := NewStore(testDirectoryResolver{root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := store.Read(context.Background(), "session-1", "../secret"); err == nil {
		t.Fatalf("expected invalid artifact id error")
	}
}
