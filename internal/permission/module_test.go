package permission

import (
	"context"
	"strings"
	"testing"
)

// 新版模块或新配置不能继承旧 Allow；用户明确保存的 Deny 则应持续生效。
func TestModuleVersionChangesInvalidateAllowAndPreserveDeny(t *testing.T) {
	ctx := context.Background()
	request := testBuiltinRequest("demo_write", RiskWrite, "sandbox:v1")
	request.Identity.Kind = CapabilityModule
	request.Identity.ModuleID = "demo"
	request.Identity.ModuleRevision = "v1"
	engine := newTestEngine(t)
	if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatal(err)
	}
	decision, err := engine.Evaluate(ctx, request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("original grant failed: %#v %v", decision, err)
	}
	changed := request
	changed.Identity.ModuleRevision = "v2"
	decision, err = engine.Evaluate(ctx, changed)
	if err != nil || decision.Action != ActionAsk {
		t.Fatalf("old grant matched new module: %#v %v", decision, err)
	}
	if _, err := engine.DenyAgent(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatal(err)
	}
	decision, err = engine.Evaluate(ctx, changed)
	if err != nil || decision.Action != ActionDeny {
		t.Fatalf("deny did not survive module upgrade: %#v %v", decision, err)
	}
}

func TestModuleApprovalShowsOriginAndHidesParameters(t *testing.T) {
	request := testBuiltinRequest("demo_write", RiskWrite, "sandbox:v1")
	request.Identity.Kind, request.Identity.ModuleID, request.Identity.ModuleRevision = CapabilityModule, "demo", "v1"
	request.Arguments = `{"content":"private document","token":"private token"}`
	presentation, err := BuildPresentation(request)
	if err != nil {
		t.Fatal(err)
	}
	joined := presentation.Title + presentation.Description
	for _, field := range presentation.Fields {
		joined += field.Label + field.Value
	}
	if !strings.Contains(joined, "demo") || !strings.Contains(joined, "v1") || strings.Contains(joined, "private") {
		t.Fatalf("unsafe or incomplete module presentation: %#v", presentation)
	}
}
