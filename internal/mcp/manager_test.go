package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	store, err := NewStore(context.Background(), filepath.Join(t.TempDir(), "mcp", "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store, config.MCPConfig{MaxToolsPerServer: 64}, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestNormalizeAndValidateSelectionMergesAndSorts(t *testing.T) {
	manager := newTestManager(t)
	ctx := context.Background()
	server, err := manager.Create(ctx, CreateServerInput{
		Key:       "github",
		Name:      "GitHub",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "npx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.NormalizeAndValidateSelection(ctx, []ToolSelection{
		{ServerID: server.ID, Tools: []string{"z_tool", "a_tool"}},
		{ServerID: server.ID, Tools: []string{"a_tool"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || len(result[0].Tools) != 2 || result[0].Tools[0] != "a_tool" || result[0].Tools[1] != "z_tool" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestResolveRuntimeSnapshotIsEmptyForNoSelectionAndFailClosedOtherwise(t *testing.T) {
	manager := newTestManager(t)
	ctx := context.Background()
	empty, err := manager.ResolveRuntimeSnapshot(ctx, nil, humberttools.Scope{})
	if err != nil {
		t.Fatalf("empty snapshot should work: %v", err)
	}
	if empty.Enabled() {
		t.Fatal("empty snapshot should not be enabled")
	}

	server, err := manager.Create(ctx, CreateServerInput{
		Key:       "github",
		Name:      "GitHub",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "npx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.ResolveRuntimeSnapshot(ctx, []ToolSelection{{ServerID: server.ID, Tools: []string{"search_issues"}}}, humberttools.Scope{
		AgentID:   "agent-1",
		Workspace: workspace.Workspace{AgentID: "agent-1", RootDir: t.TempDir()},
	})
	if !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("error = %v, want ErrRuntimeUnavailable", err)
	}
}

func TestDisabledServerKeepsSelectionButSkipsRuntime(t *testing.T) {
	manager := newTestManager(t)
	ctx := context.Background()
	server, err := manager.Create(ctx, CreateServerInput{
		Key:       "github",
		Name:      "GitHub",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "npx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetEnabled(ctx, server.ID, false); err != nil {
		t.Fatal(err)
	}

	selection := []ToolSelection{{ServerID: server.ID, Tools: []string{"search_issues"}}}
	normalized, err := manager.NormalizeAndValidateSelection(ctx, selection)
	if err != nil {
		t.Fatalf("disabled server selection should remain valid: %v", err)
	}
	if len(normalized) != 1 || len(normalized[0].Tools) != 1 || normalized[0].Tools[0] != "search_issues" {
		t.Fatalf("unexpected normalized selection: %#v", normalized)
	}

	snapshot, err := manager.ResolveRuntimeSnapshot(ctx, selection, humberttools.Scope{
		AgentID:   "agent-1",
		Workspace: workspace.Workspace{AgentID: "agent-1", RootDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("disabled server should be skipped before requiring runtime backend: %v", err)
	}
	if snapshot.Enabled() || len(snapshot.Servers) != 0 || len(snapshot.ToolNames) != 0 {
		t.Fatalf("disabled server should not enter runtime snapshot: %#v", snapshot)
	}

	status, err := manager.RuntimeStatus(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != ConnectionDisabled || status.Connected {
		t.Fatalf("unexpected disabled runtime status: %#v", status)
	}
}

func testTime() time.Time {
	return time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
}

type fixedReferenceChecker struct {
	count int
}

func (c fixedReferenceChecker) CountAgentsUsingMCPServer(context.Context, string) (int, error) {
	return c.count, nil
}

func TestDeleteServerRejectsAgentReferences(t *testing.T) {
	manager := newTestManager(t)
	ctx := context.Background()
	server, err := manager.Create(ctx, CreateServerInput{
		Key:       "github",
		Name:      "GitHub",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "npx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetReferenceChecker(fixedReferenceChecker{count: 1})
	if err := manager.Delete(ctx, server.ID); !errors.Is(err, ErrServerInUse) {
		t.Fatalf("Delete error = %v, want ErrServerInUse", err)
	}
	if _, err := manager.Get(ctx, server.ID); err != nil {
		t.Fatalf("referenced server should remain: %v", err)
	}
}

type selectiveRuntimeBackend struct {
	failServerID string
	calls        int
}

func (b *selectiveRuntimeBackend) Resolve(_ context.Context, request ResolveRequest) (RuntimeSnapshot, error) {
	b.calls++
	if len(request.Servers) != 1 || len(request.Selections) != 1 {
		for _, server := range request.Servers {
			if server.ID == b.failServerID {
				return RuntimeSnapshot{}, errors.New("temporary MCP failure")
			}
		}
	}
	server := request.Servers[0]
	if server.ID == b.failServerID {
		return RuntimeSnapshot{}, errors.New("temporary MCP failure")
	}
	raw := request.Selections[0].Tools[0]
	name, err := NameExposedTool(server.Key, raw)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return RuntimeSnapshot{
		Descriptors: []humberttools.Descriptor{{
			Name: name,
			Risk: humberttools.RiskWrite,
			MCPOrigin: &humberttools.MCPOrigin{
				ServerID:          server.ID,
				ServerName:        server.Name,
				ServerFingerprint: ServerFingerprint(server),
				RawToolName:       raw,
			},
		}},
		ToolNames: []string{name},
	}, nil
}

func TestResolveRuntimeSnapshotAvailableSkipsBrokenServer(t *testing.T) {
	manager := newTestManager(t)
	ctx := context.Background()
	broken, err := manager.Create(ctx, CreateServerInput{
		Key:       "broken",
		Name:      "Broken",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "broken-command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := manager.Create(ctx, CreateServerInput{
		Key:       "healthy",
		Name:      "Healthy",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "healthy-command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &selectiveRuntimeBackend{failServerID: broken.ID}
	manager.SetRuntimeBackend(backend)

	scope := humberttools.Scope{
		AgentID:   "agent-1",
		Workspace: workspace.Workspace{AgentID: "agent-1", RootDir: t.TempDir()},
	}
	snapshot, err := manager.ResolveRuntimeSnapshotAvailable(ctx, []ToolSelection{
		{ServerID: broken.ID, Tools: []string{"broken_tool"}},
		{ServerID: healthy.ID, Tools: []string{"healthy_tool"}},
	}, scope)
	if err != nil {
		t.Fatalf("available Resolve should degrade a broken Server: %v", err)
	}
	if backend.calls != 2 {
		t.Fatalf("backend calls = %d, want 2", backend.calls)
	}
	if len(snapshot.Failures) != 1 || snapshot.Failures[0].ServerID != broken.ID {
		t.Fatalf("unexpected failures: %#v", snapshot.Failures)
	}
	if len(snapshot.Servers) != 1 || snapshot.Servers[0].ServerID != healthy.ID {
		t.Fatalf("unexpected healthy servers: %#v", snapshot.Servers)
	}
	if len(snapshot.ToolNames) != 1 || snapshot.ToolNames[0] != "mcp_healthy_healthy_tool" {
		t.Fatalf("unexpected tool names: %#v", snapshot.ToolNames)
	}
	if len(snapshot.AuditTools) != 1 || snapshot.AuditTools[0].ServerID != healthy.ID {
		t.Fatalf("unexpected audit tools: %#v", snapshot.AuditTools)
	}
}

func TestResolveRuntimeSnapshotBestEffortIsLocalProjection(t *testing.T) {
	manager := newTestManager(t)
	ctx := context.Background()
	broken, err := manager.Create(ctx, CreateServerInput{
		Key:       "broken",
		Name:      "Broken",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "broken-command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := manager.Create(ctx, CreateServerInput{
		Key:       "healthy",
		Name:      "Healthy",
		Transport: TransportStdio,
		Stdio:     &StdioConfig{Command: "healthy-command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &selectiveRuntimeBackend{failServerID: broken.ID}
	manager.SetRuntimeBackend(backend)

	// 模拟设置页之前读取过一次 Catalog。Context Usage 可以复用这个 schema，
	// 但即使缓存缺失也绝不能为了估算主动调用 RuntimeBackend。
	manager.mu.Lock()
	manager.catalogCache[healthy.ID] = catalogCacheEntry{
		fingerprint: ServerFingerprint(healthy),
		fetchedAt:   time.Now().UTC(),
		items: []ToolCatalogItem{{
			RawName:     "healthy_tool",
			ExposedName: "mcp_healthy_healthy_tool",
			Description: "cached healthy tool",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		}},
	}
	manager.mu.Unlock()

	scope := humberttools.Scope{
		AgentID:   "agent-1",
		Workspace: workspace.Workspace{AgentID: "agent-1", RootDir: t.TempDir()},
	}
	selections := []ToolSelection{
		{ServerID: broken.ID, Tools: []string{"broken_tool"}},
		{ServerID: healthy.ID, Tools: []string{"healthy_tool"}},
	}

	if _, err := manager.ResolveRuntimeSnapshot(ctx, selections, scope); err == nil {
		t.Fatal("strict Resolve should fail when one MCP Server fails")
	}
	if backend.calls != 1 {
		t.Fatalf("strict Resolve backend calls = %d, want 1", backend.calls)
	}

	snapshot, err := manager.ResolveRuntimeSnapshotBestEffort(ctx, selections, scope)
	if err != nil {
		t.Fatalf("projection Resolve should not depend on MCP connectivity: %v", err)
	}
	if backend.calls != 1 {
		t.Fatalf("Context projection called RuntimeBackend: calls = %d, want 1", backend.calls)
	}
	if len(snapshot.Servers) != 2 || len(snapshot.ToolNames) != 2 || len(snapshot.AuditTools) != 2 || len(snapshot.Tools) != 2 {
		t.Fatalf("unexpected projection snapshot: %#v", snapshot)
	}

	names := map[string]bool{}
	for _, name := range snapshot.ToolNames {
		names[name] = true
	}
	if !names["mcp_broken_broken_tool"] || !names["mcp_healthy_healthy_tool"] {
		t.Fatalf("unexpected projected tool names: %#v", snapshot.ToolNames)
	}

	for _, tool := range snapshot.Tools {
		info, infoErr := tool.Info(ctx)
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		if info != nil && info.Name == "mcp_healthy_healthy_tool" {
			if info.Desc != "cached healthy tool" || len(info.Extra) == 0 {
				t.Fatalf("cached catalog schema not reused: %#v", info)
			}
		}
	}
}
