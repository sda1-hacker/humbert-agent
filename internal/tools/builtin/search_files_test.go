package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func TestGlobFilesSupportsPathPatternsAndDirectories(t *testing.T) {
	root := t.TempDir()
	root, err := sandbox.CanonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"src/main.go", "src/nested/worker.go", "docs/guide.md"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("example"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tool, err := NewGlobFilesFactory().Build(context.Background(), humberttools.Scope{Workspace: workspace.Workspace{RootDir: root}})
	if err != nil {
		t.Fatal(err)
	}
	run := func(input string) GlobFilesOutput {
		t.Helper()
		text, err := tool.InvokableRun(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		var output GlobFilesOutput
		if err := json.Unmarshal([]byte(text), &output); err != nil {
			t.Fatal(err)
		}
		return output
	}
	if got := run(`{"pattern":"*.go"}`).Files; !reflect.DeepEqual(got, []string{"src/main.go", "src/nested/worker.go"}) {
		t.Fatalf("recursive name search = %q", got)
	}
	if got := run(`{"path":"src","pattern":"**/*.go"}`).Files; !reflect.DeepEqual(got, []string{"src/main.go", "src/nested/worker.go"}) {
		t.Fatalf("relative path search = %q", got)
	}
	if got := run(`{"pattern":"src/*","file_type":"directory"}`).Files; !reflect.DeepEqual(got, []string{"src/nested"}) {
		t.Fatalf("directory search = %q", got)
	}
	if _, err := tool.InvokableRun(context.Background(), `{"pattern":"*.go","file_type":"invalid"}`); err == nil {
		t.Fatal("invalid file_type should be rejected")
	}
}
