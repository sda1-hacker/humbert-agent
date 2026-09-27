package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
)

func TestConcurrentEditsAcrossBackendsPreserveAllChanges(t *testing.T) {
	b := testFilesystem(t)
	ctx := context.Background()
	const writers = 16
	var initial, expected strings.Builder
	for i := range writers {
		fmt.Fprintf(&initial, "field-%d=0\n", i)
		fmt.Fprintf(&expected, "field-%d=1\n", i)
	}
	if err := b.Write(ctx, &filesystem.WriteRequest{FilePath: "shared.txt", Content: initial.String()}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, writers)
	for i := range writers {
		// 不同 Backend 实例模拟不同会话，确保锁不是只在某个 Factory 内生效。
		other := *b
		go func() {
			<-start
			done <- other.Edit(ctx, &filesystem.EditRequest{FilePath: "shared.txt", OldString: fmt.Sprintf("field-%d=0", i), NewString: fmt.Sprintf("field-%d=1", i)})
		}()
	}
	close(start)
	for range writers {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(b.scope.Workspace.RootDir, "shared.txt"))
	if err != nil || string(data) != expected.String() {
		t.Fatalf("lost edit: %q, %v", data, err)
	}
}

func TestFileMutationsShareCancellableLock(t *testing.T) {
	b := testFilesystem(t)
	ctx := context.Background()
	if err := b.Write(ctx, &filesystem.WriteRequest{FilePath: "shared.txt", Content: "old"}); err != nil {
		t.Fatal(err)
	}
	// Move/Delete 要求 FULL，测试中仅授权临时目录。
	b.scope.Sandbox = sandbox.WorkspaceOnlyPolicy(b.scope.Workspace.RootDir)
	b.scope.Sandbox.PathRules[0].Access = sandbox.AccessFull
	target, err := openSandboxTarget(ctx, b.scope, "shared.txt", sandbox.OpModify)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	unlock, err := lockFileTargets(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	patch, err := NewApplyPatchFactory(b.maxWritableBytes)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := NewDeleteFileFactory().Build(ctx, b.scope)
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]func(context.Context) error{
		"write": func(ctx context.Context) error {
			return b.Write(ctx, &filesystem.WriteRequest{FilePath: "shared.txt", Content: "new"})
		},
		"edit": func(ctx context.Context) error {
			return b.Edit(ctx, &filesystem.EditRequest{FilePath: "shared.txt", OldString: "old", NewString: "new"})
		},
		"patch": func(ctx context.Context) error {
			_, err := patch.run(ctx, b.scope, &ApplyPatchInput{Changes: []PatchChange{{Path: "shared.txt", OldText: "old", NewText: "new"}}})
			return err
		},
		"copy": func(ctx context.Context) error {
			_, err := copyFile(ctx, b.scope, &CopyFileInput{Source: "shared.txt", Destination: "copy.txt"}, b.maxWritableBytes, false, nil)
			return err
		},
		"move": func(ctx context.Context) error {
			_, err := copyFile(ctx, b.scope, &CopyFileInput{Source: "shared.txt", Destination: "moved.txt"}, b.maxWritableBytes, true, nil)
			return err
		},
		"delete": func(ctx context.Context) error {
			_, err := remove.InvokableRun(ctx, `{"path":"shared.txt"}`)
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			defer cancel()
			if err := operation(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("operation bypassed shared lock: %v", err)
			}
		})
	}
}

func TestAtomicWriteRejectsExternalChanges(t *testing.T) {
	for _, exists := range []bool{true, false} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			b := testFilesystem(t)
			path := filepath.Join(b.scope.Workspace.RootDir, "file.txt")
			var version fileVersion
			if exists {
				if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				version.info, err = os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				version.content = []byte("old")
			}
			target, err := openSandboxTarget(context.Background(), b.scope, "file.txt", sandbox.OpModify)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if err := os.WriteFile(path, []byte("external edit"), 0600); err != nil {
				t.Fatal(err)
			}
			err = atomicWriteWorkspaceFile(context.Background(), target.root, target.relative, []byte("overwrite"), 0600, version)
			if !errors.Is(err, errFileChanged) {
				t.Fatalf("expected conflict: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "external edit" {
				t.Fatalf("external change lost: %q %v", data, err)
			}
			entries, _ := os.ReadDir(b.scope.Workspace.RootDir)
			if len(entries) != 1 {
				t.Fatalf("temporary file leaked: %v", entries)
			}
		})
	}
}
