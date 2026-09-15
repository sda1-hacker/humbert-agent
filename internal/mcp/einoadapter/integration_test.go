package einoadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
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

type integrationAuthorizer struct{ action permission.Action }

func (a integrationAuthorizer) Evaluate(context.Context, permission.Request) (permission.Decision, error) {
	return permission.Decision{Action: a.action, Reason: "integration test"}, nil
}

type echoInput struct {
	Text string `json:"text"`
}

// 使用真实 SDK HTTP Server，覆盖保存配置、发现、运行时解析、授权与 tools/call。
func TestHTTPMCPConfigurationDiscoveryAndInvocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var calls atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "Echo test input"}, func(_ context.Context, _ *sdk.CallToolRequest, input echoInput) (*sdk.CallToolResult, any, error) {
		calls.Add(1)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: input.Text}}}, nil, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer httpServer.Close()
	cfg := config.MCPConfig{ConnectTimeoutMS: 2000, MaxToolsPerServer: 64, MaxToolPages: 10, MaxToolDescriptionChars: 2000, MaxToolResultChars: 2000}
	store, err := hmcp.NewStore(ctx, filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := hmcp.NewManager(store, cfg, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewBackend(cfg, integrationAuthorizer{permission.ActionAllow}, nil, &sandbox.Manager{}, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	manager.SetRuntimeBackend(backend)
	saved, err := manager.Create(ctx, hmcp.CreateServerInput{Key: "integration", Name: "Integration", Transport: hmcp.TransportStreamableHTTP, HTTP: &hmcp.HTTPConfig{Endpoint: httpServer.URL}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := backend.DiscoverTools(ctx, saved)
	if err != nil || len(catalog) != 1 {
		t.Fatalf("discovery: %v", err)
	}
	root := t.TempDir()
	scope := tools.Scope{AgentID: "agent", Workspace: workspace.Workspace{AgentID: "agent", RootDir: root}, Sandbox: sandbox.EffectivePolicy{WorkspaceRoot: root, NetworkMode: sandbox.NetworkAll, Profile: sandbox.ProfileFullAccess}}
	selection := []hmcp.ToolSelection{{ServerID: saved.ID, Tools: []string{"echo"}}}
	snapshot, err := manager.ResolveRuntimeSnapshot(ctx, selection, scope)
	if err != nil || len(snapshot.Tools) != 1 {
		t.Fatalf("resolve: %v", err)
	}
	result, err := snapshot.Tools[0].(einotool.InvokableTool).InvokableRun(ctx, `{"text":"round trip works"}`)
	if err != nil || !strings.Contains(result, "round trip works") || calls.Load() != 1 {
		t.Fatalf("call: %s, %v, count=%d", result, err, calls.Load())
	}
	// 连接测试允许显式本机端点，但 Public-only Agent 不能调用该端点。
	scope.Sandbox.NetworkMode = sandbox.NetworkPublic
	if _, err := manager.ResolveRuntimeSnapshot(ctx, selection, scope); err == nil {
		t.Fatal("public-only policy allowed loopback runtime")
	}
	if _, err := manager.SetEnabled(ctx, saved.ID, false); err != nil {
		t.Fatal(err)
	}
	empty, err := manager.ResolveRuntimeSnapshot(ctx, selection, scope)
	if err != nil || len(empty.Tools) != 0 {
		t.Fatalf("disabled server exposed tools: %v", err)
	}
}
