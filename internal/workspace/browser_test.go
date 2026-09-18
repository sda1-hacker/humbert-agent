package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

// TestWorkspaceBrowserListAndPreview 验证桌面文件浏览器和 Agent Tool 使用同一条
// Workspace 安全边界：目录可以按层读取，文本可以预览，但不能通过 ../ 逃逸根目录。
func TestWorkspaceBrowserListAndPreview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager, err := NewManager(ctx, filepath.Join(t.TempDir(), "managed"), logging.NewBootstrap())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	value, err := manager.Resolve(ctx, "00000000-0000-4000-8000-000000000123", ModeManaged, "")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(value.RootDir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(value.RootDir, "src", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(value.RootDir, "README.md"), []byte("# Humbert\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	listing, err := manager.ListDirectory(ctx, value, ".", 20)
	if err != nil {
		t.Fatalf("ListDirectory() error = %v", err)
	}
	if len(listing.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(listing.Entries))
	}
	// 目录必须排在普通文件前面，保证前端文件树顺序稳定。
	if listing.Entries[0].Type != "directory" || listing.Entries[0].Name != "src" {
		t.Fatalf("first entry = %+v, want src directory", listing.Entries[0])
	}

	preview, err := manager.PreviewFile(ctx, value, "src/main.go")
	if err != nil {
		t.Fatalf("PreviewFile() error = %v", err)
	}
	if preview.Kind != "text" || !strings.Contains(preview.Content, "package main") {
		t.Fatalf("preview = %+v, want Go text", preview)
	}

	if _, err := manager.PreviewFile(ctx, value, "../outside.txt"); err == nil {
		t.Fatal("PreviewFile() should reject path traversal")
	}
}
