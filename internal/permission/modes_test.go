package permission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/commandenv"
	"github.com/sda1-hacker/humbert-agent/internal/config"
)

func TestApprovalModesAndStoredRules(t *testing.T) {
	ctx := context.Background()
	engine := newTestEngine(t)
	root := t.TempDir()
	request := testBuiltinRequest("write_file", RiskWrite, "sbx:test")
	request.Arguments, request.WorkspaceRoot = `{"file_path":"README.md"}`, root
	setMode := func(mode string) {
		t.Helper()
		cfg := engine.Config()
		cfg.Mode = mode
		if err := engine.UpdateConfig(cfg); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want Action) {
		t.Helper()
		got, err := engine.Evaluate(ctx, request)
		if err != nil || got.Action != want {
			t.Fatalf("mode=%s: %+v %v", engine.Config().Mode, got, err)
		}
	}
	setMode(config.PermissionModeRisk)
	check(ActionAllow)
	request.Arguments = `{"file_path":"../outside.txt"}`
	check(ActionAsk)
	if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatal(err)
	}
	setMode(config.PermissionModeAlways)
	check(ActionAsk)
	setMode(config.PermissionModeFull)
	check(ActionAllow)
	if _, err := engine.DenyAgent(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{config.PermissionModeFull, config.PermissionModeAlways, config.PermissionModeRisk} {
		setMode(mode)
		check(ActionDeny)
	}
}

func TestRiskModeDoesNotAskForBuiltinReadToolsOutsideWorkspace(t *testing.T) {
	engine := newTestEngine(t)
	root, external := t.TempDir(), t.TempDir()
	for _, name := range []string{"glob_files", "grep_files", "list_files", "read_file"} {
		t.Run(name, func(t *testing.T) {
			request := testBuiltinRequest(name, RiskRead, "sbx:read-test")
			request.WorkspaceRoot = root
			pathKey := "path"
			if name == "read_file" {
				pathKey = "file_path"
			}
			arguments, _ := json.Marshal(map[string]string{pathKey: external, "pattern": "**/*.go"})
			request.Arguments = string(arguments)
			for _, mode := range []string{config.PermissionModeRisk, config.PermissionModeAlways} {
				cfg := engine.Config()
				cfg.Mode = mode
				if err := engine.UpdateConfig(cfg); err != nil {
					t.Fatal(err)
				}
				decision, err := engine.Evaluate(context.Background(), request)
				want := ActionAllow
				if mode == config.PermissionModeAlways {
					want = ActionAsk
				}
				if err != nil || decision.Action != want {
					t.Fatalf("%s: %+v %v", mode, decision, err)
				}
			}
			if _, err := engine.DenyAgent(context.Background(), ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
				t.Fatal(err)
			}
			cfg := engine.Config()
			cfg.Mode = config.PermissionModeRisk
			if err := engine.UpdateConfig(cfg); err != nil {
				t.Fatal(err)
			}
			decision, err := engine.Evaluate(context.Background(), request)
			if err != nil || decision.Action != ActionDeny {
				t.Fatalf("只读自动执行覆盖了显式拒绝: %+v %v", decision, err)
			}
		})
	}
}

func TestRiskModeDistinguishesRoutineAndDestructiveCommands(t *testing.T) {
	engine := newTestEngine(t)
	cfg := engine.Config()
	cfg.Mode = config.PermissionModeRisk
	if err := engine.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, tc := range []struct {
		command string
		args    []string
		routine bool
	}{
		{"go", []string{"vet", "./..."}, true}, {"go", []string{"test", "./..."}, true},
		{"go", []string{"vet", "-vettool=custom"}, false}, {"go", []string{"generate", "./..."}, false},
		{"go", []string{"env", "-w", "GOPROXY=example.invalid"}, false},
		{"rm", []string{"file"}, false}, {"find", []string{".", "-delete"}, false},
		{"find", []string{".", "-exec", "rm", "{}", ";"}, false}, {"find", []string{".", "-name", "*.go"}, true},
		{"sh", []string{"-c", "echo hello"}, false},
	} {
		t.Run(tc.command+"/"+tc.args[0], func(t *testing.T) {
			executable, err := commandenv.Resolve(tc.command, "")
			if err != nil {
				t.Skip(err)
			}
			request := testCommandRequest(tc.command, executable, "sbx:test")
			request.WorkspaceRoot, request.NativeSandbox = root, true
			args, _ := json.Marshal(map[string]any{"command": tc.command, "args": tc.args, "working_directory": "."})
			request.Arguments = string(args)
			decision, err := engine.Evaluate(context.Background(), request)
			if err != nil || (decision.Action == ActionAllow) != tc.routine {
				t.Fatalf("%+v: %+v %v", tc, decision, err)
			}
			request.NativeSandbox = false
			decision, err = engine.Evaluate(context.Background(), request)
			if err != nil || decision.Action != ActionAsk {
				t.Fatalf("无文件隔离不能自动执行: %+v %v", decision, err)
			}
		})
	}
}

func TestRiskModeDetectsSymlinkAndBatchEditsOutsideWorkspace(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	request := testBuiltinRequest("apply_patch", RiskWrite, "sbx:test")
	request.WorkspaceRoot = root
	request.Arguments = `{"changes":[{"path":"new.txt"},{"path":"link/new.txt"}]}`
	if routineOperation(request) {
		t.Fatal("批量编辑的越界路径被当成普通修改")
	}
}

func TestExternalReadOnlyRiskDoesNotInheritBuiltinPathExemption(t *testing.T) {
	request := testMCPRequest("mcp:test", "sbx:test")
	request.Risk, request.Identity.Risk = RiskRead, RiskRead
	request.WorkspaceRoot = t.TempDir()
	arguments, _ := json.Marshal(map[string]string{"path": t.TempDir()})
	request.Arguments = string(arguments)
	if routineOperation(request) {
		t.Fatal("外部 MCP 声明的 read 风险套用了内置工具的目录豁免")
	}
}
