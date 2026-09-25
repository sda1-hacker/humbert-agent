package sandbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The OS sandbox, rather than a program-name filter, must stop child processes
// and interpreters from changing the workspace.
func TestReadOnlyCommandCannotDeleteWorkspaceFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native filesystem sandbox unavailable on Windows")
	}
	manager := newTestManager(t, t.TempDir())
	if capability := manager.Capability(); !capability.Available || !capability.Filesystem {
		t.Skip("native filesystem sandbox unavailable: " + capability.Reason)
	}
	workspace := t.TempDir()
	victim := filepath.Join(workspace, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := manager.Resolve(context.Background(), workspace, AgentPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := NewRunner(manager)
	for _, script := range []string{"rm victim.txt", "find . -name victim.txt -exec rm {} +", "python3 -c 'import os; os.remove(\"victim.txt\")'"} {
		var commandStderr bytes.Buffer
		result, err := runner.Run(ctx, policy, ProcessSpec{Executable: "/bin/sh", Args: []string{"-c", script}, Dir: workspace, Env: []string{"PATH=/usr/bin:/bin"}, Stderr: &commandStderr, ReadOnlyWorkspace: true})
		if strings.Contains(commandStderr.String(), "sandbox_apply: Operation not permitted") {
			t.Skip("host sandbox prevents nested Seatbelt execution")
		}
		if err != nil {
			t.Fatalf("%q: %v", script, err)
		}
		if result.ExitCode == 0 && !strings.HasPrefix(script, "find ") {
			t.Fatalf("%q unexpectedly succeeded", script)
		}
		if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
			t.Fatalf("%q changed workspace file: %q, %v", script, data, err)
		}
	}
	for _, executable := range []string{"/bin/pwd", "/usr/bin/find"} {
		var queryOutput bytes.Buffer
		args := []string(nil)
		if strings.HasSuffix(executable, "/find") {
			args = []string{".", "-name", "victim.txt"}
		}
		result, err := runner.Run(ctx, policy, ProcessSpec{Executable: executable, Args: args, Dir: workspace, Env: []string{"PATH=/usr/bin:/bin"}, Stdout: &queryOutput, ReadOnlyWorkspace: true})
		if err != nil || result.ExitCode != 0 || strings.TrimSpace(queryOutput.String()) == "" {
			t.Fatalf("query command %s failed: result=%+v output=%q err=%v", executable, result, queryOutput.String(), err)
		}
	}
	var output bytes.Buffer
	var stderr bytes.Buffer
	result, err := runner.Run(ctx, policy, ProcessSpec{Executable: "/bin/cat", Args: []string{"victim.txt"}, Dir: workspace, Env: []string{"PATH=/usr/bin:/bin"}, Stdout: &output, Stderr: &stderr, ReadOnlyWorkspace: true})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(output.String()) != "keep" {
		t.Fatalf("read command failed: result=%+v output=%q stderr=%q err=%v", result, output.String(), stderr.String(), err)
	}
}
