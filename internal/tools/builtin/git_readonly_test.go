package builtin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
)

func TestGitReadToolsEnforceReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("测试钩子使用 POSIX shell；Windows 无只读原生沙箱时应拒绝执行")
	}
	for _, native := range []sandbox.NativeMode{sandbox.NativeOff, sandbox.NativePreferred} {
		t.Run(string(native), func(t *testing.T) {
			b := testFilesystem(t)
			root := b.scope.Workspace.RootDir
			env := []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				cmd.Env = env
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
			}
			git("init", "-q")
			must(os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.txt diff=audit\n"), 0o600))
			must(os.WriteFile(filepath.Join(root, "note.txt"), []byte("before\n"), 0o600))
			git("add", ".gitattributes", "note.txt")
			git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "initial")
			script := filepath.Join(root, "hook.sh")
			must(os.WriteFile(script, []byte("#!/bin/sh\nprintf touched > git-read-side-effect\ncat \"$1\"\n"), 0o700))
			git("config", "diff.audit.textconv", script)
			git("config", "core.fsmonitor", script)
			must(os.WriteFile(filepath.Join(root, "note.txt"), []byte("after\n"), 0o600))
			manager, err := sandbox.NewManager(sandbox.Config{DefaultProfile: sandbox.ProfileWorkspaceOnly, DefaultNetworkMode: sandbox.NetworkPublic, DefaultNativeMode: native, CommandGracePeriod: time.Second}, t.TempDir())
			must(err)
			if native != sandbox.NativeOff && (!manager.Capability().Available || !manager.Capability().Filesystem) {
				t.Skip("平台不支持原生只读沙箱")
			}
			policy, err := manager.Resolve(context.Background(), root, sandbox.AgentPolicy{})
			must(err)
			b.scope.Sandbox = policy
			for _, name := range []string{gitDiffToolName, gitStatusToolName, gitLogToolName} {
				factory, err := newGitReadFactory(name, "regression", manager.Runner(), env, 16000)
				must(err)
				tool, err := factory.Build(context.Background(), b.scope)
				must(err)
				out, err := tool.InvokableRun(context.Background(), `{"path":"."}`)
				if native == sandbox.NativeOff {
					if err == nil || !strings.Contains(err.Error(), "只读") {
						t.Fatalf("%s did not reject missing read-only isolation: %v", name, err)
					}
				} else {
					must(err)
					if strings.Contains(out, "sandbox_apply: Operation not permitted") {
						t.Skip("宿主限制嵌套 Seatbelt 执行")
					}
					var result GitReadOutput
					must(json.Unmarshal([]byte(out), &result))
					if result.ExitCode != 0 || !result.NativeSandbox || result.Output == "" {
						t.Fatalf("%s failed: %s", name, out)
					}
				}
				if _, err := os.Stat(filepath.Join(root, "git-read-side-effect")); !os.IsNotExist(err) {
					t.Fatalf("%s ran external hook: %v", name, err)
				}
			}
		})
	}
}
