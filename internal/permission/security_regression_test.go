package permission

import (
	"context"
	"testing"
)

// TestSecurityRegressionReusableAllowIdentityMatrix 固化 Permission v2 的核心安全承诺：
// 可复用 Allow 只能在“同一个能力身份 + 同一个 Sandbox 安全边界”中复用。
// 任何 executable、Skill package、MCP server 或 Sandbox identity 变化都必须重新询问。
func TestSecurityRegressionReusableAllowIdentityMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("builtin-sandbox-change", func(t *testing.T) {
		engine := newTestEngine(t)
		request := testBuiltinRequest("write_file", RiskWrite, "sbx1:a")
		request.Arguments = `{"path":"a.txt"}`
		if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
			t.Fatal(err)
		}
		changed := request
		changed.Identity.SandboxFingerprint = "sbx1:b"
		decision, err := engine.Evaluate(ctx, changed)
		if err != nil || decision.Action != ActionAsk {
			t.Fatalf("sandbox change must re-ask: decision=%#v err=%v", decision, err)
		}
	})

	t.Run("command-executable-change", func(t *testing.T) {
		engine := newTestEngine(t)
		request := testCommandRequest("python3", "/usr/bin/python3", "sbx1:a")
		if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
			t.Fatal(err)
		}
		changed := request
		changed.Identity.Executable = "/opt/runtime/python3"
		decision, err := engine.Evaluate(ctx, changed)
		if err != nil || decision.Action != ActionAsk {
			t.Fatalf("executable change must re-ask: decision=%#v err=%v", decision, err)
		}
	})

	t.Run("skill-package-change", func(t *testing.T) {
		engine := newTestEngine(t)
		request := Request{
			AgentID: "agent-1", SessionID: "session-1", ToolName: "run_skill_script", Risk: RiskExec,
			Arguments: `{"skill":"demo","script":"scripts/run.py"}`,
			Identity: CapabilityIdentity{
				Version: CapabilityIdentityVersion, Kind: CapabilitySkillScript,
				Tool: "run_skill_script", Risk: RiskExec, SandboxFingerprint: "sbx1:a",
				SkillName: "demo", SkillIdentity: "skill-v1", Script: "scripts/run.py",
				Command: "python3", Executable: "/usr/bin/python3",
			},
		}
		if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
			t.Fatal(err)
		}
		changed := request
		changed.Identity.SkillIdentity = "skill-v2"
		decision, err := engine.Evaluate(ctx, changed)
		if err != nil || decision.Action != ActionAsk {
			t.Fatalf("skill identity change must re-ask: decision=%#v err=%v", decision, err)
		}
	})

	t.Run("mcp-server-change", func(t *testing.T) {
		engine := newTestEngine(t)
		request := testMCPRequest("mcp1:a", "sbx1:a")
		if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
			t.Fatal(err)
		}
		changed := request
		changed.Identity.MCPServerFingerprint = "mcp1:b"
		decision, err := engine.Evaluate(ctx, changed)
		if err != nil || decision.Action != ActionAsk {
			t.Fatalf("MCP fingerprint change must re-ask: decision=%#v err=%v", decision, err)
		}
	})

	t.Run("deny-does-not-weaken", func(t *testing.T) {
		engine := newTestEngine(t)
		request := testCommandRequest("python3", "/usr/bin/python3", "sbx1:a")
		if _, err := engine.DenyAgent(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
			t.Fatal(err)
		}
		changed := request
		changed.Identity.Executable = "/opt/runtime/python3"
		changed.Identity.SandboxFingerprint = "sbx1:b"
		decision, err := engine.Evaluate(ctx, changed)
		if err != nil || decision.Action != ActionDeny {
			t.Fatalf("deny must remain effective after environment change: decision=%#v err=%v", decision, err)
		}
	})
}
