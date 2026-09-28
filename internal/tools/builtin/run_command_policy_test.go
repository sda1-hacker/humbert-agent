package builtin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/commandenv"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
)

// 审批负责决定是否允许运行；执行器必须遵守真实沙盒范围，而非再用一套删除黑名单拒绝已批准操作。
func TestRunCommandUsesExecutablePathAndApprovedWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("此测试使用 POSIX shell")
	}
	b := testFilesystem(t)
	root := b.scope.Workspace.RootDir
	manager, err := sandbox.NewManager(sandbox.Config{DefaultProfile: sandbox.ProfileWorkspaceOnly, DefaultNetworkMode: sandbox.NetworkPublic, DefaultNativeMode: sandbox.NativePreferred, CommandGracePeriod: time.Second}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !manager.Capability().Available || !manager.Capability().Filesystem {
		t.Skip("原生文件沙盒不可用")
	}
	b.scope.Sandbox, err = manager.Resolve(context.Background(), root, sandbox.AgentPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	shell, err := commandenv.Resolve("sh", root)
	if err != nil {
		t.Fatal(err)
	}
	factory := &RunCommandFactory{runner: manager.Runner(), limits: CommandLimits{DefaultTimeout: time.Second * 10, MaxTimeout: time.Second * 10, MaxArgs: 20, MaxArgBytes: 4096, MaxOutputBytes: 16000}, environment: []string{"PATH=" + commandenv.Path()}}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	output, err := factory.run(context.Background(), b.scope, &RunCommandInput{Command: shell, Args: []string{"-c", `printf allowed > inside.txt; printf blocked > "$1"`, "test", outside}, WorkingDirectory: root})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.Output, "sandbox_apply: Operation not permitted") {
		t.Skip("宿主限制嵌套 Seatbelt 执行")
	}
	data, err := os.ReadFile(filepath.Join(root, "inside.txt"))
	if err != nil || string(data) != "allowed" {
		t.Fatalf("已批准的工作区写入失败: %s %v %+v", data, err, output)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("越过原生文件边界: %v", err)
	}
}

// 复现桌面应用的精简 PATH，以及待检查项目并非 Agent 工作目录的场景。
func TestRunCommandGoVetOutsideWorkspace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("此回归场景使用 macOS Homebrew Go")
	}
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	goExecutable, err := commandenv.Resolve("go", "")
	if err != nil {
		t.Skipf("未安装 Go: %v", err)
	}
	b := testFilesystem(t)
	project, err := sandbox.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"go.mod":  "module example.com/desktop-check\n\ngo 1.23\n",
		"main.go": "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hello\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := sandbox.NewManager(sandbox.Config{DefaultProfile: sandbox.ProfileStandard, DefaultNetworkMode: sandbox.NetworkPublic, DefaultNativeMode: sandbox.NativePreferred, CommandGracePeriod: time.Second}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !manager.Capability().Available || !manager.Capability().Filesystem {
		t.Skip("原生文件沙盒不可用")
	}
	b.scope.Sandbox, err = manager.Resolve(context.Background(), b.scope.Workspace.RootDir, sandbox.AgentPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	// 测试项目在临时目录，显式添加只读授权，模拟 standard 模式下可读的用户桌面。
	b.scope.Sandbox, err = b.scope.Sandbox.WithPathAccess(project, sandbox.AccessReadOnly, sandbox.RuleSourceStandardHome)
	if err != nil {
		t.Fatal(err)
	}
	factory := &RunCommandFactory{runner: manager.Runner(), limits: CommandLimits{DefaultTimeout: 120 * time.Second, MaxTimeout: 120 * time.Second, MaxArgs: 20, MaxArgBytes: 4096, MaxOutputBytes: 16000}, environment: []string{"PATH=" + commandenv.Path(), "HOME=" + os.Getenv("HOME"), "GOTOOLCHAIN=local", "GOPROXY=off"}}
	output, err := factory.run(context.Background(), b.scope, &RunCommandInput{Command: goExecutable, Args: []string{"vet", "./..."}, WorkingDirectory: project})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.Output, "sandbox_apply: Operation not permitted") {
		t.Skip("宿主限制嵌套 Seatbelt 执行")
	}
	if output.ExitCode != 0 || !output.NativeSandbox {
		t.Fatalf("外部项目 go vet 失败: %+v", output)
	}
}
