package einoadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

func TestUpdatesPreserveFrozenToolsUntilRunFinishes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := sdk.NewServer(&sdk.Implementation{Name: "review", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "Review echo"}, func(_ context.Context, _ *sdk.CallToolRequest, input echoInput) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: input.Text}}}, nil, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer httpServer.Close()
	cfg := config.MCPConfig{ConnectTimeoutMS: 2000, MaxToolsPerServer: 64, MaxToolPages: 10, MaxToolDescriptionChars: 2000}
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
	saved, err := manager.Create(ctx, hmcp.CreateServerInput{Key: "review", Name: "Before", Transport: hmcp.TransportStreamableHTTP, HTTP: &hmcp.HTTPConfig{Endpoint: httpServer.URL}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	scope := tools.Scope{RequestID: "first-turn", AgentID: "agent", Workspace: workspace.Workspace{AgentID: "agent", RootDir: root}, Sandbox: sandbox.EffectivePolicy{WorkspaceRoot: root, NetworkMode: sandbox.NetworkAll, Profile: sandbox.ProfileFullAccess}}
	snapshot, err := manager.ResolveRuntimeSnapshot(ctx, []hmcp.ToolSelection{{ServerID: saved.ID, Tools: []string{"echo"}}}, scope)
	if err != nil || len(snapshot.Tools) != 1 {
		t.Fatalf("resolve: %v", err)
	}
	tool := snapshot.Tools[0].(einotool.InvokableTool)
	if _, err := tool.InvokableRun(ctx, `{"text":"before"}`); err != nil {
		t.Fatal(err)
	}
	// 仅修改显示名称，端点、风险、凭据均保持原配置。
	if _, err := manager.Update(ctx, saved.ID, hmcp.UpdateServerInput{Name: "After", Transport: saved.Transport, HTTP: saved.HTTP}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.InvokableRun(ctx, `{"text":"after"}`); err != nil {
		t.Errorf("resolved tool stopped working after metadata update: %v", err)
	}

	// 连接更新后旧 Turn 仍使用原连接，新 Turn 使用新端点。
	replacement := sdk.NewServer(&sdk.Implementation{Name: "replacement", Version: "1"}, nil)
	sdk.AddTool(replacement, &sdk.Tool{Name: "echo", Description: "replacement echo"}, func(_ context.Context, _ *sdk.CallToolRequest, input echoInput) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "new:" + input.Text}}}, nil, nil
	})
	nextHTTP := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return replacement }, nil))
	defer func() { _ = backend.Close(); nextHTTP.Close() }()
	if _, err := manager.Update(ctx, saved.ID, hmcp.UpdateServerInput{Name: "After", Transport: saved.Transport, HTTP: &hmcp.HTTPConfig{Endpoint: nextHTTP.URL}}); err != nil {
		t.Fatal(err)
	}
	if result, err := tool.InvokableRun(ctx, `{"text":"old-turn"}`); err != nil || strings.Contains(result, "new:") {
		t.Fatalf("old tool changed: %s %v", result, err)
	}
	scope.RequestID = "next-turn"
	next, err := manager.ResolveRuntimeSnapshot(ctx, []hmcp.ToolSelection{{ServerID: saved.ID, Tools: []string{"echo"}}}, scope)
	if err != nil {
		t.Fatal(err)
	}
	nextTool := next.Tools[0].(einotool.InvokableTool)
	if result, err := nextTool.InvokableRun(ctx, `{"text":"next"}`); err != nil || !strings.Contains(result, "new:next") {
		t.Fatalf("new tool did not use new endpoint: %s %v", result, err)
	}
	manager.ParentRunFinished(ctx, "first-turn")
	manager.ParentRunFinished(ctx, "first-turn") // 重复收尾不重复关闭。
	if _, err := tool.InvokableRun(ctx, `{"text":"finished"}`); err == nil {
		t.Fatal("retired session remains usable after run finished")
	}
	if _, err := nextTool.InvokableRun(ctx, `{"text":"still-running"}`); err != nil {
		t.Fatal(err)
	}
	manager.ParentRunFinished(ctx, "next-turn")
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.retired) != 0 || len(backend.runSessions) != 0 {
		t.Fatal("run leases were not released")
	}
}

func TestRetiredSessionClosesOnceDuringConcurrentReleaseAndDisconnect(t *testing.T) {
	backend, err := NewBackend(config.MCPConfig{}, integrationAuthorizer{permission.ActionAllow}, nil, &sandbox.Manager{}, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	var closed atomic.Int64
	entry := &sessionEntry{serverID: "server", activeRuns: 2, cleanup: func() { closed.Add(1) }}
	backend.sessions["server|control"] = entry
	backend.runSessions["first"] = map[*sessionEntry]struct{}{entry: {}}
	backend.runSessions["second"] = map[*sessionEntry]struct{}{entry: {}}
	backend.Retire("server")
	if closed.Load() != 0 {
		t.Fatal("retire closed an in-use connection")
	}
	var workers sync.WaitGroup
	for _, finish := range []func(){func() { backend.ReleaseRun("first") }, func() { backend.ReleaseRun("second") }, func() { backend.Invalidate("server") }} {
		workers.Go(finish)
	}
	workers.Wait()
	if closed.Load() != 1 {
		t.Fatalf("closed %d times", closed.Load())
	}
	if len(backend.retired) != 0 || len(backend.runSessions) != 0 {
		t.Fatal("run resources remain")
	}
}
