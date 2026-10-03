package builtin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/websearch"
)

type searchBackendProbe struct{ called bool }

func (*searchBackendProbe) Name() string { return "sdk" }
func (b *searchBackendProbe) Search(context.Context, *http.Client, string, int) ([]websearch.Result, error) {
	b.called = true
	return []websearch.Result{{Title: "Guide", URL: "https://example.org/guide", Snippet: "agent guide"}}, nil
}

// 自定义 SDK 后端也必须经过宿主网络策略；拒绝发生在调用后端之前。
func TestInjectedSearchBackendHonorsSandbox(t *testing.T) {
	backend := &searchBackendProbe{}
	factory, err := NewWebSearchFactory("auto", time.Second, 1, 3, websearch.WithBackends(backend))
	if err != nil {
		t.Fatal(err)
	}
	fixture := testFilesystem(t)
	fixture.scope.Sandbox = sandbox.WorkspaceOnlyPolicy(fixture.scope.Workspace.RootDir)
	fixture.scope.Sandbox.NetworkMode = sandbox.NetworkNone
	tool, err := factory.Build(context.Background(), fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.InvokableRun(context.Background(), `{"query":"agent guide"}`); err == nil || !strings.Contains(err.Error(), "禁用网络") || backend.called {
		t.Fatalf("network policy bypassed: err=%v called=%v", err, backend.called)
	}
	fixture.scope.Sandbox.NetworkMode = sandbox.NetworkPublic
	tool, err = factory.Build(context.Background(), fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.InvokableRun(context.Background(), `{"query":"agent guide"}`); err != nil || !backend.called {
		t.Fatalf("allowed injected backend unavailable: err=%v called=%v", err, backend.called)
	}
}
