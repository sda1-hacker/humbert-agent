package runtime

import (
	"context"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// 最小组合不装配 Skills/MCP 也能正常组装普通能力，已有绑定则必须明确失败。
func TestOptionalSourcesDoNotRequireConcreteManagers(t *testing.T) {
	registry, err := humberttools.NewRegistry(&extensionAuthorizer{action: permission.ActionAllow})
	if err != nil {
		t.Fatal(err)
	}
	assembler := &capabilityAssembler{tools: registry}
	if err := assembler.validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := assembler.resolve(context.Background(), agents.Agent{ID: "minimal"}, humberttools.Scope{AgentID: "minimal", Workspace: workspace.Workspace{AgentID: "minimal", RootDir: t.TempDir()}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := assembler.resolveSkills(context.Background(), []string{"notes"}); err == nil {
		t.Fatal("selected but unmounted Skills ignored")
	}
	if _, err := assembler.resolveMCP(context.Background(), []humbertmcp.ToolSelection{{ServerID: "server", Tools: []string{"read"}}}, humberttools.Scope{}, true); err == nil {
		t.Fatal("selected but unmounted MCP ignored")
	}
}

type sourceProbe struct{ preview, actual int }

func (p *sourceProbe) ResolveRuntimeSnapshotBestEffort(context.Context, []humbertmcp.ToolSelection, humberttools.Scope) (humbertmcp.RuntimeSnapshot, error) {
	p.preview++
	return humbertmcp.RuntimeSnapshot{Revision: 7}, nil
}
func (p *sourceProbe) ResolveRuntimeSnapshotAvailable(context.Context, []humbertmcp.ToolSelection, humberttools.Scope) (humbertmcp.RuntimeSnapshot, error) {
	p.actual++
	return humbertmcp.RuntimeSnapshot{Revision: 8}, nil
}

func TestReplacedMCPSourceKeepsPreviewSeparateFromExecution(t *testing.T) {
	probe := &sourceProbe{}
	assembler := &capabilityAssembler{mcp: probe}
	preview, err := assembler.resolveMCP(context.Background(), nil, humberttools.Scope{}, true)
	if err != nil || probe.actual != 0 || preview.Revision != 7 {
		t.Fatalf("preview connected live source: %+v %v", probe, err)
	}
	actual, err := assembler.resolveMCP(context.Background(), nil, humberttools.Scope{}, false)
	if err != nil || probe.preview != 1 || probe.actual != 1 || actual.Revision != 8 {
		t.Fatalf("actual source not used: %+v %v", probe, err)
	}
}
