package permission

import (
	"context"
	"errors"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	store, err := NewStore(context.Background(), t.TempDir()+"/permissions.json")
	if err != nil {
		t.Fatalf("创建 Permission Store 失败: %v", err)
	}
	engine, err := NewEngine(config.PermissionConfig{
		Enabled: true, ReadAction: "allow", WriteAction: "ask", ExecAction: "ask", ApprovalTimeoutMS: 60_000,
	}, store, logging.NewBootstrap())
	if err != nil {
		t.Fatalf("创建 Permission Engine 失败: %v", err)
	}
	return engine
}

func testBuiltinRequest(tool string, risk RiskLevel, sandboxFingerprint string) Request {
	return Request{
		AgentID: "agent-1", SessionID: "session-1", ToolName: tool, Risk: risk,
		Identity: CapabilityIdentity{
			Version: CapabilityIdentityVersion, Kind: CapabilityBuiltin, Tool: tool, Risk: risk,
			SandboxFingerprint: sandboxFingerprint,
		},
	}
}

func testCommandRequest(command, executable, sandboxFingerprint string) Request {
	return Request{
		AgentID: "agent-1", SessionID: "session-1", ToolName: "run_command", Risk: RiskExec,
		Arguments: `{"command":"` + command + `"}`,
		Identity: CapabilityIdentity{
			Version: CapabilityIdentityVersion, Kind: CapabilityCommand, Tool: "run_command", Risk: RiskExec,
			SandboxFingerprint: sandboxFingerprint, Command: command, Executable: executable,
		},
	}
}

func testMCPRequest(serverFingerprint, sandboxFingerprint string) Request {
	return Request{
		AgentID: "agent-1", SessionID: "session-1", ToolName: "mcp_github_create_issue", Risk: RiskWrite,
		Arguments:     `{"owner":"acme","repo":"demo","title":"test"}`,
		MCPServerName: "GitHub", MCPRawToolName: "create_issue",
		Identity: CapabilityIdentity{
			Version: CapabilityIdentityVersion, Kind: CapabilityMCP, Tool: "mcp_github_create_issue", Risk: RiskWrite,
			SandboxFingerprint: sandboxFingerprint, MCPServerID: "server-github",
			MCPServerFingerprint: serverFingerprint, MCPTool: "create_issue",
		},
	}
}

func TestEvaluateUsesDefaultRiskPolicy(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()

	read := testBuiltinRequest("read_file", RiskRead, "sbx1:a")
	read.Arguments = `{"path":"README.md"}`
	decision, err := engine.Evaluate(ctx, read)
	if err != nil {
		t.Fatalf("读取权限计算失败: %v", err)
	}
	if decision.Action != ActionAllow {
		t.Fatalf("读取默认策略 = %q, want %q", decision.Action, ActionAllow)
	}

	write := testBuiltinRequest("write_file", RiskWrite, "sbx1:a")
	write.Arguments = `{"path":"a.txt","overwrite":false}`
	decision, err = engine.Evaluate(ctx, write)
	if err != nil {
		t.Fatalf("写入权限计算失败: %v", err)
	}
	if decision.Action != ActionAsk || decision.ApprovalID == "" {
		t.Fatalf("写入默认策略 = %#v, want ask + approvalID", decision)
	}
}

func TestSessionGrantOnlyAffectsSameSession(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()
	request := testBuiltinRequest("write_file", RiskWrite, "sbx1:a")
	request.Arguments = `{"path":"a.txt"}`

	if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantSession, Request: request}); err != nil {
		t.Fatalf("创建 Session Rule 失败: %v", err)
	}
	decision, err := engine.Evaluate(ctx, request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Session Rule 未命中: decision=%#v err=%v", decision, err)
	}

	other := request
	other.SessionID = "session-2"
	decision, err = engine.Evaluate(ctx, other)
	if err != nil || decision.Action != ActionAsk {
		t.Fatalf("Session Rule 泄漏到其他 Session: decision=%#v err=%v", decision, err)
	}
}

func TestAllowRuleInvalidatesWhenSandboxChanges(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()
	request := testBuiltinRequest("write_file", RiskWrite, "sbx1:workspace-a")
	request.Arguments = `{"path":"a.txt"}`
	if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatalf("创建长期授权失败: %v", err)
	}

	allowed, err := engine.Evaluate(ctx, request)
	if err != nil || allowed.Action != ActionAllow {
		t.Fatalf("原 Sandbox 应命中长期授权: %#v err=%v", allowed, err)
	}

	changed := request
	changed.Identity.SandboxFingerprint = "sbx1:workspace-b"
	decision, err := engine.Evaluate(ctx, changed)
	if err != nil {
		t.Fatalf("Sandbox 变化后 Evaluate() error = %v", err)
	}
	if decision.Action != ActionAsk {
		t.Fatalf("Sandbox 变化后旧 Allow 必须失效，got %q", decision.Action)
	}
}

func TestCommandAllowBindsExecutableAndCommand(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()
	request := testCommandRequest("python3", "/usr/bin/python3", "sbx1:a")

	if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatalf("创建 command 长期授权失败: %v", err)
	}
	decision, err := engine.Evaluate(ctx, request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("原 executable 应命中 Allow: %#v err=%v", decision, err)
	}

	changedExecutable := request
	changedExecutable.Identity.Executable = "/opt/homebrew/bin/python3"
	decision, err = engine.Evaluate(ctx, changedExecutable)
	if err != nil || decision.Action != ActionAsk {
		t.Fatalf("Executable 变化后应重新询问: %#v err=%v", decision, err)
	}

	git := testCommandRequest("git", "/usr/bin/git", "sbx1:a")
	decision, err = engine.Evaluate(ctx, git)
	if err != nil || decision.Action != ActionAsk {
		t.Fatalf("python3 Rule 不应授权 git: %#v err=%v", decision, err)
	}
}

func TestSkillAllowBindsPackageIdentityAndScript(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()
	request := Request{
		AgentID: "agent-1", SessionID: "session-1", ToolName: "run_skill_script", Risk: RiskExec,
		Arguments: `{"skill":"kami","script":"scripts/render.py"}`,
		Identity: CapabilityIdentity{
			Version: CapabilityIdentityVersion, Kind: CapabilitySkillScript, Tool: "run_skill_script", Risk: RiskExec,
			SandboxFingerprint: "sbx1:a", SkillName: "kami", SkillIdentity: "skill-v1", Script: "scripts/render.py",
			Command: "python3", Executable: "/usr/bin/python3",
		},
	}
	if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatalf("创建 Skill 长期授权失败: %v", err)
	}
	changed := request
	changed.Identity.SkillIdentity = "skill-v2"
	decision, err := engine.Evaluate(ctx, changed)
	if err != nil || decision.Action != ActionAsk {
		t.Fatalf("Skill Identity 变化后应重新询问: %#v err=%v", decision, err)
	}
}

func TestMCPAllowBindsServerFingerprintAndSandbox(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()
	request := testMCPRequest("mcp1:server-a", "sbx1:a")

	if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatalf("创建 MCP 长期授权失败: %v", err)
	}
	decision, err := engine.Evaluate(ctx, request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("原 MCP 身份应命中 Allow: %#v err=%v", decision, err)
	}

	changedServer := request
	changedServer.Identity.MCPServerFingerprint = "mcp1:server-b"
	decision, err = engine.Evaluate(ctx, changedServer)
	if err != nil || decision.Action != ActionAsk {
		t.Fatalf("MCP Server Fingerprint 变化后应重新询问: %#v err=%v", decision, err)
	}

	changedSandbox := request
	changedSandbox.Identity.SandboxFingerprint = "sbx1:b"
	decision, err = engine.Evaluate(ctx, changedSandbox)
	if err != nil || decision.Action != ActionAsk {
		t.Fatalf("MCP Sandbox 变化后应重新询问: %#v err=%v", decision, err)
	}
}

func TestDenyRemainsEffectiveWhenSandboxChanges(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()
	request := testCommandRequest("python3", "/usr/bin/python3", "sbx1:a")
	if _, err := engine.DenyAgent(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatalf("创建长期拒绝失败: %v", err)
	}

	changed := request
	changed.Identity.SandboxFingerprint = "sbx1:b"
	changed.Identity.Executable = "/opt/homebrew/bin/python3"
	decision, err := engine.Evaluate(ctx, changed)
	if err != nil || decision.Action != ActionDeny {
		t.Fatalf("Deny 不应因 Sandbox/Executable 变化失效: %#v err=%v", decision, err)
	}
}

func TestInstallSkillOnlyAllowsOneTimeGrant(t *testing.T) {
	engine := newTestEngine(t)
	request := testBuiltinRequest("install_skill", RiskWrite, "sbx1:a")
	request.Arguments = `{"source_url":"https://example.com/skill.zip"}`

	if _, err := engine.Grant(context.Background(), ApprovalGrant{Scope: GrantOnce, Request: request}); err != nil {
		t.Fatalf("install_skill 单次批准应允许: %v", err)
	}
	for _, scope := range []GrantScope{GrantSession, GrantAgent} {
		if _, err := engine.Grant(context.Background(), ApprovalGrant{Scope: scope, Request: request}); !errors.Is(err, ErrInvalidApprovalScope) {
			t.Fatalf("install_skill scope=%q 应拒绝可复用 Allow，实际: %v", scope, err)
		}
	}
}

func TestAgentDenyOverridesSessionAllow(t *testing.T) {
	engine := newTestEngine(t)
	ctx := context.Background()
	request := testBuiltinRequest("write_file", RiskWrite, "sbx1:a")
	request.Arguments = `{"path":"a.txt"}`

	if _, err := engine.Grant(ctx, ApprovalGrant{Scope: GrantSession, Request: request}); err != nil {
		t.Fatalf("创建 Session Allow Rule 失败: %v", err)
	}
	if _, err := engine.DenyAgent(ctx, ApprovalGrant{Scope: GrantAgent, Request: request}); err != nil {
		t.Fatalf("创建 Agent Deny Rule 失败: %v", err)
	}
	decision, err := engine.Evaluate(ctx, request)
	if err != nil || decision.Action != ActionDeny {
		t.Fatalf("显式 Deny 应优先: %#v err=%v", decision, err)
	}
}

func TestUpdateConfigChangesDefaultPolicy(t *testing.T) {
	engine := newTestEngine(t)
	if err := engine.UpdateConfig(config.PermissionConfig{
		Enabled: true, ReadAction: "ask", WriteAction: "deny", ExecAction: "deny", ApprovalTimeoutMS: 120_000,
	}); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	request := testBuiltinRequest("write_file", RiskWrite, "sbx1:a")
	request.Arguments = `{"path":"a.txt"}`
	decision, err := engine.Evaluate(context.Background(), request)
	if err != nil || decision.Action != ActionDeny {
		t.Fatalf("更新后 write 默认策略 = %#v err=%v", decision, err)
	}
}
