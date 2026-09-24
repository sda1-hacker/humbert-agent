package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func TestCommandIdentityBindsArgsDirectoryAndTimeout(t *testing.T) {
	root, err := sandbox.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	scope := Scope{Workspace: workspace.Workspace{RootDir: root}}
	descriptor := Descriptor{Name: "run_command", Risk: RiskExec}
	build := func(arguments string) string {
		t.Helper()
		identity, err := buildCapabilityIdentity(descriptor, scope, arguments)
		if err != nil {
			t.Fatal(err)
		}
		return identity.InvocationFingerprint
	}
	base := build(`{"command":"go","args":["version"]}`)
	if base == "" || base != build(`{"command":"go","args":["version"],"working_directory":"."}`) {
		t.Fatal("same command invocation should have a stable fingerprint")
	}
	for _, changed := range []string{
		`{"command":"go","args":["test"]}`,
		`{"command":"go","args":["version"],"working_directory":"sub"}`,
		`{"command":"go","args":["version"],"timeout_seconds":600}`,
	} {
		if got := build(changed); got == base {
			t.Fatalf("changed invocation retained fingerprint: %s", changed)
		}
	}
}
