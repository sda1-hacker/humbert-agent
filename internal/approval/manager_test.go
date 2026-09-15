package approval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
)

func newApprovalManager(t *testing.T, timeout time.Duration) (*Manager, *permission.Engine) {
	t.Helper()
	ctx := context.Background()
	store, err := permission.NewStore(ctx, t.TempDir()+"/permissions.json")
	if err != nil {
		t.Fatalf("创建 Permission Store 失败: %v", err)
	}
	engine, err := permission.NewEngine(config.PermissionConfig{
		Enabled: true, ReadAction: "allow", WriteAction: "ask", ExecAction: "ask", ApprovalTimeoutMS: int(timeout.Milliseconds()),
	}, store, logging.NewBootstrap())
	if err != nil {
		t.Fatalf("创建 Permission Engine 失败: %v", err)
	}
	manager, err := NewManager(timeout, engine, logging.NewBootstrap())
	if err != nil {
		t.Fatalf("创建 Approval Manager 失败: %v", err)
	}
	return manager, engine
}

func approvalInfo() InterruptInfo {
	return InterruptInfo{
		ApprovalID: "approval-1", RequestID: "request-1", RunID: "run-1", SessionID: "session-1", AgentID: "agent-1",
		ToolName: "run_command", Risk: permission.RiskExec,
		Identity: permission.CapabilityIdentity{
			Version: permission.CapabilityIdentityVersion, Kind: permission.CapabilityCommand,
			Tool: "run_command", Risk: permission.RiskExec,
			SandboxFingerprint: "sbx1:test", Command: "go", Executable: "/usr/bin/go",
		},
		Presentation: permission.Presentation{Title: "请求执行本地程序"},
	}
}

func TestResolveAllowSessionCreatesReusableRule(t *testing.T) {
	manager, engine := newApprovalManager(t, time.Minute)
	ctx := context.Background()
	request, err := manager.Register(ctx, approvalInfo(), "interrupt-1", "checkpoint-1")
	if err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	resolution, err := manager.Resolve(ctx, request.ID, DecisionAllowSession)
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if !resolution.Approved {
		t.Fatal("allow_session 应返回 Approved=true")
	}

	decision, err := engine.Evaluate(ctx, permission.Request{
		AgentID: "agent-1", SessionID: "session-1", ToolName: "run_command", Risk: permission.RiskExec,
		Arguments: `{"command":"go","args":["test"]}`,
		Identity:  approvalInfo().Identity,
	})
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if decision.Action != permission.ActionAllow {
		t.Fatalf("Session Grant 未生效: %q", decision.Action)
	}

	if _, err := manager.Resolve(ctx, request.ID, DecisionAllowOnce); !errors.Is(err, ErrNotPending) {
		t.Fatalf("重复 Resolve 应返回 ErrNotPending，got %v", err)
	}
}

func TestResolveAllowAgentPreservesMCPIdentity(t *testing.T) {
	manager, engine := newApprovalManager(t, time.Minute)
	ctx := context.Background()
	info := InterruptInfo{
		ApprovalID: "approval-mcp", RequestID: "request-mcp", RunID: "run-mcp", SessionID: "session-1", AgentID: "agent-1",
		ToolName: "mcp_github_create_issue", Risk: permission.RiskWrite,
		Identity: permission.CapabilityIdentity{
			Version: permission.CapabilityIdentityVersion, Kind: permission.CapabilityMCP,
			Tool: "mcp_github_create_issue", Risk: permission.RiskWrite, SandboxFingerprint: "sbx1:a",
			MCPServerID: "server-github", MCPServerFingerprint: "mcp1:a", MCPTool: "create_issue",
		},
		Presentation: permission.Presentation{Title: "请求调用 MCP Tool"},
	}
	request, err := manager.Register(ctx, info, "interrupt-mcp", "checkpoint-mcp")
	if err != nil {
		t.Fatalf("Register MCP 失败: %v", err)
	}
	resolution, err := manager.Resolve(ctx, request.ID, DecisionAllowAgent)
	if err != nil {
		t.Fatalf("Resolve MCP allow_agent 失败: %v", err)
	}
	if !resolution.Approved {
		t.Fatal("MCP allow_agent 应返回 Approved=true")
	}

	permissionRequest := info.PermissionRequest()
	decision, err := engine.Evaluate(ctx, permissionRequest)
	if err != nil || decision.Action != permission.ActionAllow {
		t.Fatalf("MCP Identity 未被 Approval 完整保留: %#v err=%v", decision, err)
	}

	changed := permissionRequest
	changed.Identity.MCPServerFingerprint = "mcp1:b"
	decision, err = engine.Evaluate(ctx, changed)
	if err != nil || decision.Action != permission.ActionAsk {
		t.Fatalf("MCP Fingerprint 变化后旧 Agent Allow 必须失效: %#v err=%v", decision, err)
	}
}

func TestExpireRejectsPendingInvocation(t *testing.T) {
	manager, _ := newApprovalManager(t, time.Minute)
	ctx := context.Background()
	request, err := manager.Register(ctx, approvalInfo(), "interrupt-1", "checkpoint-1")
	if err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	resolution, ok, err := manager.Expire(request.ID)
	if err != nil || !ok {
		t.Fatalf("Expire = ok:%v err:%v", ok, err)
	}
	if resolution.Approved {
		t.Fatal("超时恢复必须按拒绝处理")
	}
	stored, exists := manager.Get(request.ID)
	if !exists || stored.Status != StatusExpired {
		t.Fatalf("Approval 状态 = %#v, want expired", stored)
	}
}

// TestCompleteNormalizesApprovalID 是对审批 ID 边界空白的回归测试。Desktop Adapter 和
// Manager 都会规范化外部 ID，Complete 必须使用同一个 canonical key，否则会留下一个
// 永远停在 Resolving 的内存状态。
func TestCompleteNormalizesApprovalID(t *testing.T) {
	manager, _ := newApprovalManager(t, time.Minute)
	ctx := context.Background()
	request, err := manager.Register(ctx, approvalInfo(), "interrupt-1", "checkpoint-1")
	if err != nil {
		t.Fatalf("Register 失败: %v", err)
	}
	if _, err := manager.Resolve(ctx, request.ID, DecisionAllowOnce); err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}

	manager.Complete("  " + request.ID + "  ")
	stored, exists := manager.Get(request.ID)
	if !exists {
		t.Fatal("Complete 后 Approval 不应在 Forget 前消失")
	}
	if stored.Status != StatusResolved {
		t.Fatalf("Approval status = %q, want %q", stored.Status, StatusResolved)
	}
}

func TestResolveDenyAgentPersistsDenyRule(t *testing.T) {
	manager, engine := newApprovalManager(t, time.Minute)
	ctx := context.Background()
	request, err := manager.Register(ctx, approvalInfo(), "interrupt-1", "checkpoint-1")
	if err != nil {
		t.Fatalf("Register 失败: %v", err)
	}

	resolution, err := manager.Resolve(ctx, request.ID, DecisionDenyAgent)
	if err != nil {
		t.Fatalf("Resolve deny_agent 失败: %v", err)
	}
	if resolution.Approved {
		t.Fatal("deny_agent 必须拒绝当前 ToolCall")
	}

	decision, err := engine.Evaluate(ctx, permission.Request{
		AgentID: "agent-1", SessionID: "session-2", ToolName: "run_command", Risk: permission.RiskExec,
		Arguments: `{"command":"go","args":["test"]}`,
		Identity:  approvalInfo().Identity,
	})
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if decision.Action != permission.ActionDeny {
		t.Fatalf("Agent 长期拒绝未生效: %q", decision.Action)
	}
}

func TestUpdateTimeoutOnlyAffectsNewRequests(t *testing.T) {
	manager, _ := newApprovalManager(t, time.Minute)
	ctx := context.Background()
	first, err := manager.Register(ctx, approvalInfo(), "interrupt-1", "checkpoint-1")
	if err != nil {
		t.Fatalf("Register first 失败: %v", err)
	}
	if err := manager.UpdateTimeout(2 * time.Minute); err != nil {
		t.Fatalf("UpdateTimeout() error = %v", err)
	}

	info := approvalInfo()
	info.ApprovalID = "approval-2"
	info.RequestID = "request-2"
	info.RunID = "run-2"
	second, err := manager.Register(ctx, info, "interrupt-2", "checkpoint-2")
	if err != nil {
		t.Fatalf("Register second 失败: %v", err)
	}

	firstDuration := first.ExpiresAt.Sub(first.CreatedAt)
	secondDuration := second.ExpiresAt.Sub(second.CreatedAt)
	if firstDuration != time.Minute {
		t.Fatalf("已有 Approval deadline 被修改: %v", firstDuration)
	}
	if secondDuration != 2*time.Minute {
		t.Fatalf("新 Approval timeout = %v, want 2m", secondDuration)
	}
}
