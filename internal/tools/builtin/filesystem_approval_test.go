package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// 走真实 Registry → Permission Guard → Eino 文件工具 → PathGuard，
// 复现待检查项目不在 Agent 工作目录时，glob/list/read/grep 不应弹审批。
func TestRiskModeReadToolsUseExistingSandboxWithoutApproval(t *testing.T) {
	ctx := context.Background()
	b := testFilesystem(t)
	b.scope.SessionID = "session"
	project, err := sandbox.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, "main.go")
	if err := os.WriteFile(file, []byte("package example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := sandbox.WorkspaceOnlyPolicy(b.scope.Workspace.RootDir)
	policy, err = policy.WithPathAccess(project, sandbox.AccessReadOnly, sandbox.RuleSourceStandardHome)
	if err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(project, "private")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protected, "secret.go"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	policy.PathRules = append(policy.PathRules, sandbox.PathRule{Root: protected, Access: sandbox.AccessBlocked, Source: sandbox.RuleSourceProtected})
	b.scope.Sandbox = policy
	store, err := permission.NewStore(ctx, filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := permission.NewEngine(config.PermissionConfig{Mode: config.PermissionModeRisk, ApprovalTimeoutMS: 60000}, store, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := humberttools.NewRegistry(engine)
	if err != nil {
		t.Fatal(err)
	}
	factories, err := NewFilesystemFactories(b.limits, b.maxWritableBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, factory := range factories {
		if err := registry.Register(factory); err != nil {
			t.Fatal(err)
		}
	}
	b.scope.EnabledBuiltinTools = []string{"glob_files", "grep_files", "list_files", "read_file"}
	resolved, err := registry.Resolve(ctx, b.scope)
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string]map[string]any{
		"glob_files": {"path": project, "pattern": "**/*.go"},
		"grep_files": {"path": project, "pattern": "package"},
		"list_files": {"path": project},
		"read_file":  {"file_path": file},
	}
	for _, base := range resolved.Tools {
		info, err := base.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tool := base.(einotool.InvokableTool)
		arguments, _ := json.Marshal(inputs[info.Name])
		output, err := tool.InvokableRun(ctx, string(arguments))
		if err != nil {
			t.Fatalf("%s 正常只读请求仍被中断或拒绝: %v", info.Name, err)
		}
		if !strings.Contains(output, "main.go") && !strings.Contains(output, "package example") {
			t.Fatalf("%s 未读取项目内容: %s", info.Name, output)
		}
		if strings.Contains(output, "secret") {
			t.Fatalf("%s 遍历到了受保护目录: %s", info.Name, output)
		}
		// 不属于授权目录的路径仍由沙盒拒绝，不能因为免审批而获得读取权限。
		input := map[string]any{"path": protected, "pattern": "**/*.go"}
		if info.Name == "read_file" {
			input = map[string]any{"file_path": filepath.Join(protected, "secret.go")}
		}
		arguments, _ = json.Marshal(input)
		if output, err := tool.InvokableRun(ctx, string(arguments)); err == nil && !strings.Contains(output, "Error") {
			t.Fatalf("%s 放行了受保护目录: %s", info.Name, output)
		}
	}
}
