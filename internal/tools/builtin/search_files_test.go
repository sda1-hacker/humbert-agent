package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func testFilesystem(t *testing.T) *FilesystemBackend {
	t.Helper()
	root, err := sandbox.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &FilesystemBackend{scope: humberttools.Scope{AgentID: "agent", Workspace: workspace.Workspace{RootDir: root}}, limits: FileLimits{MaxReadableFileBytes: 1 << 20, MaxReadLines: 1000, MaxListEntries: 500}, maxWritableBytes: 1 << 20}
}
func TestNativeFilesystemToolsAndSandbox(t *testing.T) {
	b := testFilesystem(t)
	ctx := context.Background()
	factories, err := NewFilesystemFactories(b.limits, b.maxWritableBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range factories {
		tool, err := f.Build(ctx, b.scope)
		if err != nil {
			t.Fatal(err)
		}
		switch f.Descriptor().Name {
		case "write_file":
			if _, err := tool.InvokableRun(ctx, `{"file_path":"src/main.go","content":"first\nsecond\n"}`); err != nil {
				t.Fatal(err)
			}
		case "read_file":
			info, err := tool.Info(ctx)
			if err != nil || info.Name != "read_file" {
				t.Fatalf("native schema: %v", err)
			}
		}
	}
	out, err := b.Read(ctx, &filesystem.ReadRequest{FilePath: "src/main.go", Offset: 2, Limit: 1})
	if err != nil || out.Content != "second" {
		t.Fatalf("paging: %#v %v", out, err)
	}
	if err := b.Edit(ctx, &filesystem.EditRequest{FilePath: "src/main.go", OldString: "first", NewString: "updated"}); err != nil {
		t.Fatal(err)
	}
	entries, err := b.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "**/*.go"})
	if err != nil || len(entries) != 1 || entries[0].Path != "src/main.go" {
		t.Fatalf("glob: %#v %v", entries, err)
	}
	matches, err := b.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "UPDATED", CaseInsensitive: true, Glob: "*.go"})
	if err != nil || len(matches) != 1 || matches[0].Line != 1 {
		t.Fatalf("grep: %#v %v", matches, err)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(b.scope.Workspace.RootDir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Read(ctx, &filesystem.ReadRequest{FilePath: "link"}); err == nil {
		t.Fatal("read escaped sandbox")
	}
	if err := b.Write(ctx, &filesystem.WriteRequest{FilePath: "link", Content: "replace"}); err == nil {
		t.Fatal("write followed symlink")
	}
	if _, err := b.Read(ctx, &filesystem.ReadRequest{FilePath: outside}); err == nil {
		t.Fatal("absolute path escaped sandbox")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "secret" {
		t.Fatal("external file changed")
	}
}
func TestFilesystemRejectsAmbiguousEditAndBoundsRead(t *testing.T) {
	b := testFilesystem(t)
	ctx := context.Background()
	if err := b.Write(ctx, &filesystem.WriteRequest{FilePath: "a.txt", Content: "same same"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Edit(ctx, &filesystem.EditRequest{FilePath: "a.txt", OldString: "same", NewString: "new"}); err == nil {
		t.Fatal("ambiguous edit accepted")
	}
	if err := b.Edit(ctx, &filesystem.EditRequest{FilePath: "a.txt", OldString: "same", NewString: "new", ReplaceAll: true}); err != nil {
		t.Fatal(err)
	}
	out, _ := b.Read(ctx, &filesystem.ReadRequest{FilePath: "a.txt"})
	if out.Content != "new new" {
		t.Fatal(out)
	}
	b.limits.MaxReadableFileBytes = 2
	if _, err := b.Read(ctx, &filesystem.ReadRequest{FilePath: "a.txt"}); err == nil {
		t.Fatal("oversized file accepted")
	}
	if err := b.Write(ctx, &filesystem.WriteRequest{FilePath: "a.txt", Content: strings.Repeat("x", int(b.maxWritableBytes)+1)}); err == nil {
		t.Fatal("oversized write accepted")
	}
}

func TestNativeReadDefaultRespectsBackendLimit(t *testing.T) {
	b := testFilesystem(t)
	ctx := context.Background()
	if err := b.Write(ctx, &filesystem.WriteRequest{FilePath: "a.txt", Content: "第一行\n第二行"}); err != nil {
		t.Fatal(err)
	}
	factories, err := NewFilesystemFactories(b.limits, b.maxWritableBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, factory := range factories {
		if factory.Descriptor().Name != "read_file" {
			continue
		}
		tool, err := factory.Build(ctx, b.scope)
		if err != nil {
			t.Fatal(err)
		}
		result, err := tool.InvokableRun(ctx, `{"file_path":"a.txt"}`)
		if err != nil || !strings.Contains(result, "第一行") || !strings.Contains(result, "第二行") {
			t.Fatalf("native default pagination failed: %q %v", result, err)
		}
	}
}
