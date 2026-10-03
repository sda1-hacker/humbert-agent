package runtime

import (
	"context"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/component"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

type extensionTestTool struct {
	name  string
	calls int
}

func (t *extensionTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "module test"}, nil
}
func (t *extensionTestTool) InvokableRun(context.Context, string, ...einotool.Option) (string, error) {
	t.calls++
	return "ok", nil
}

type extensionTestProvider struct {
	selected            []string
	contribution        component.Contribution
	described, resolved int
	request             component.Request
	selectedAgent       string
}

func (*extensionTestProvider) ID() string { return "demo" }
func (p *extensionTestProvider) Selection(_ context.Context, agentID string) ([]string, error) {
	p.selectedAgent = agentID
	return p.selected, nil
}
func (p *extensionTestProvider) Describe(_ context.Context, r component.Request) (component.Contribution, error) {
	p.described++
	p.request = r
	return p.contribution, nil
}
func (p *extensionTestProvider) Resolve(_ context.Context, r component.Request) (component.Contribution, error) {
	p.resolved++
	p.request = r
	return p.contribution, nil
}

type extensionAuthorizer struct {
	request permission.Request
	action  permission.Action
}

func (a *extensionAuthorizer) Evaluate(_ context.Context, r permission.Request) (permission.Decision, error) {
	a.request = r
	return permission.Decision{Action: a.action}, nil
}

func TestModuleSelectionPreviewAndParentAuthorization(t *testing.T) {
	ctx := context.Background()
	auth := &extensionAuthorizer{action: permission.ActionDeny}
	registry, err := humberttools.NewRegistry(auth)
	if err != nil {
		t.Fatal(err)
	}
	tool := &extensionTestTool{name: "demo_read"}
	provider := &extensionTestProvider{contribution: component.Contribution{Revision: "v1", Tools: []component.Tool{{Descriptor: humberttools.Descriptor{Name: tool.name, Risk: humberttools.RiskRead}, Tool: tool}}}}
	extensions := &extensionRegistry{}
	if err := extensions.register(provider); err != nil {
		t.Fatal(err)
	}
	scope := humberttools.Scope{AgentID: "parent", SessionID: "session", Workspace: workspace.Workspace{AgentID: "parent", RootDir: t.TempDir()}}
	_, tools, summaries, err := extensions.resolve(ctx, "child", scope, false, registry)
	if err != nil || len(tools) != 0 || len(summaries) != 0 || provider.resolved != 0 {
		t.Fatalf("module without explicit binding exposed tools: %v %v", tools, err)
	}
	provider.selected = []string{"demo_read"}
	if _, _, _, err := extensions.resolve(ctx, "child", scope, true, registry); err != nil {
		t.Fatal(err)
	}
	if provider.described != 1 || provider.resolved != 0 {
		t.Fatal("preview connected executable provider")
	}
	_, tools, _, err = extensions.resolve(ctx, "child", scope, false, registry)
	if err != nil {
		t.Fatal(err)
	}
	if provider.selectedAgent != "child" || provider.request.AgentID != "child" || provider.request.Scope.AgentID != "parent" {
		t.Fatalf("child profile or authorization changed: %#v", provider.request)
	}
	result, err := tools[0].(einotool.InvokableTool).InvokableRun(ctx, `{}`)
	if err != nil || tool.calls != 0 || !strings.Contains(result, "拒绝") {
		t.Fatal("denied module tool executed")
	}
	if auth.request.AgentID != "parent" || auth.request.Identity.ModuleID != "demo" || auth.request.Identity.ModuleRevision != "v1" {
		t.Fatalf("guard lost source or parent scope: %#v", auth.request)
	}
	auth.action = permission.ActionAllow
	if _, err = tools[0].(einotool.InvokableTool).InvokableRun(ctx, `{}`); err != nil || tool.calls != 1 {
		t.Fatalf("authorized tool failed: %v", err)
	}
	if err := extensions.register(&extensionTestProvider{}); err == nil {
		t.Fatal("registration after first snapshot accepted")
	}
}

func TestModuleRejectsUnselectedOrMalformedCapabilities(t *testing.T) {
	cases := []struct {
		name                       string
		selected                   []string
		revision, descriptor, info string
	}{
		{"missing prefix", []string{"read"}, "v1", "read", "read"},
		{"noncanonical name", []string{"demo_read "}, "v1", "demo_read ", "demo_read "},
		{"duplicate selection", []string{"demo_read", "demo_read"}, "v1", "demo_read", "demo_read"},
		{"unselected tool", []string{"demo_read"}, "v1", "demo_other", "demo_other"},
		{"schema mismatch", []string{"demo_read"}, "v1", "demo_read", "demo_other"},
		{"missing version", []string{"demo_read"}, "", "demo_read", "demo_read"},
	}
	registry, _ := humberttools.NewRegistry(&extensionAuthorizer{action: permission.ActionAllow})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &extensionTestProvider{selected: tc.selected, contribution: component.Contribution{Revision: tc.revision, Tools: []component.Tool{{Descriptor: humberttools.Descriptor{Name: tc.descriptor, Risk: humberttools.RiskRead}, Tool: &extensionTestTool{name: tc.info}}}}}
			r := &extensionRegistry{}
			if err := r.register(p); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := r.resolve(context.Background(), "agent", humberttools.Scope{}, true, registry); err == nil {
				t.Fatal("invalid capability accepted")
			}
		})
	}
}

// 同一个能力最终仍由统一冲突检查拒绝，不允许插件遮蔽现有工具。
func TestModuleDescriptorCannotShadowExistingTool(t *testing.T) {
	descriptor := humberttools.Descriptor{Name: "demo_read", Risk: humberttools.RiskRead}
	if _, err := mergeRuntimeDescriptors([]humberttools.Descriptor{descriptor}, []humberttools.Descriptor{descriptor}); err == nil {
		t.Fatal("duplicate model-side tool accepted")
	}
}

func TestModuleCannotMutateHostScopeCollections(t *testing.T) {
	scope := humberttools.Scope{
		EnabledBuiltinTools: []string{"read_file"}, EnabledSkills: []string{"notes"},
		EnabledMCPTools:     map[string][]string{"server": {"read"}},
		SkillIdentities:     map[string]string{"notes": "v1"},
		SkillScriptCommands: map[string]map[string]string{"notes": {"run.py": "python3"}},
	}
	copy := cloneCapabilityScope(scope)
	copy.EnabledBuiltinTools[0] = "write_file"
	copy.EnabledSkills[0] = "other"
	copy.EnabledMCPTools["server"][0] = "write"
	copy.SkillIdentities["notes"] = "v2"
	copy.SkillScriptCommands["notes"]["run.py"] = "sh"
	if scope.EnabledBuiltinTools[0] != "read_file" || scope.EnabledSkills[0] != "notes" ||
		scope.EnabledMCPTools["server"][0] != "read" || scope.SkillIdentities["notes"] != "v1" ||
		scope.SkillScriptCommands["notes"]["run.py"] != "python3" {
		t.Fatal("module changed host capability state")
	}
	if cloneCapabilityScope(humberttools.Scope{}).EnabledBuiltinTools != nil {
		t.Fatal("legacy nil builtin selection changed")
	}
}
