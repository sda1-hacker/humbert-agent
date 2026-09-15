package einoadapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	hmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// TestMCPStdioFixture 仅在测试子进程中充当 MCP Server，所有副作用写入父测试的临时目录。
func TestMCPStdioFixture(t *testing.T) {
	args := os.Args
	if len(args) < 4 || args[len(args)-3] != "humbert-mcp-fixture" {
		return
	}
	file, mode := args[len(args)-2], args[len(args)-1]
	server := sdk.NewServer(&sdk.Implementation{Name: "stdio-fixture", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "write_marker", Description: "Write a temporary test marker"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		before, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		if err := os.WriteFile(file, append(before, 'x'), 0o600); err != nil {
			return nil, nil, err
		}
		if mode == "drop-first" && len(before) == 0 {
			os.Exit(0)
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "marker written"}}}, nil, nil
	})
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func runStdioInvocation(t *testing.T, mode string) (int, hmcp.RuntimeStatus) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	marker := filepath.Join(root, "marker")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.MCPConfig{ConnectTimeoutMS: 2000, MaxToolsPerServer: 64, MaxToolPages: 10, MaxToolResultChars: 2000}
	backend, err := NewBackend(cfg, integrationAuthorizer{permission.ActionAllow}, nil, &sandbox.Manager{}, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	store, err := hmcp.NewStore(ctx, filepath.Join(root, "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := hmcp.NewManager(store, cfg, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	manager.SetRuntimeBackend(backend)
	saved, err := manager.Create(ctx, hmcp.CreateServerInput{Key: "stdio_test", Name: "Stdio Test", Transport: hmcp.TransportStdio, Stdio: &hmcp.StdioConfig{Command: executable, Args: []string{"-test.run=^TestMCPStdioFixture$", "--", "humbert-mcp-fixture", marker, mode}}})
	if err != nil {
		t.Fatal(err)
	}
	policy := sandbox.WorkspaceOnlyPolicy(root)
	policy.NetworkMode = sandbox.NetworkPublic
	scope := tools.Scope{AgentID: "agent", Workspace: workspace.Workspace{AgentID: "agent", RootDir: root}, Sandbox: policy}
	snapshot, err := manager.ResolveRuntimeSnapshot(ctx, []hmcp.ToolSelection{{ServerID: saved.ID, Tools: []string{"write_marker"}}}, scope)
	if err != nil || len(snapshot.Tools) != 1 {
		t.Fatalf("resolve: %v", err)
	}
	_, err = snapshot.Tools[0].(einotool.InvokableTool).InvokableRun(ctx, `{}`)
	if err != nil {
		if mode == "normal" {
			t.Fatal(err)
		}
		t.Logf("invocation returned: %v", err)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return len(data), backend.RuntimeStatus(saved)
}

func TestStdioMCPConnectionAndInvocation(t *testing.T) {
	if count, _ := runStdioInvocation(t, "normal"); count != 1 {
		t.Fatalf("expected one tool execution, got %d", count)
	}
}
