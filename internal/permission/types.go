package permission

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// RiskLevel 描述一次 Capability 调用对用户数据和本机环境可能造成的影响。
type RiskLevel string

const (
	RiskRead  RiskLevel = "read"
	RiskWrite RiskLevel = "write"
	RiskExec  RiskLevel = "exec"
)

// Action 是 PermissionEngine 对一次调用给出的最终动作。
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
	ActionAsk   Action = "ask"
)

// GrantScope 是用户批准一次 Ask 请求时可以选择的授权范围。
type GrantScope string

const (
	GrantOnce    GrantScope = "once"
	GrantSession GrantScope = "session"
	GrantAgent   GrantScope = "agent"
)

// Request 描述一次真实 Capability 调用的 Permission 输入。
//
// Arguments 只用于当前调用的 Approval Presentation，不会持久化到 Rule。可复用授权身份由
// Identity 单独承载；Identity 必须在进入 PermissionEngine 前由 Tool Guard 根据冻结的
// Runtime Scope 构建完成。
type Request struct {
	RequestID string
	RunID     string
	SessionID string
	AgentID   string

	ToolName string
	Risk     RiskLevel

	Arguments string

	Identity CapabilityIdentity

	// 以下字段只用于 MCP Approval 展示，不参与规则匹配；真正匹配使用 Identity。
	MCPServerName  string
	MCPRawToolName string
}

// Validate 校验 PermissionRequest 的稳定身份字段。
func (r Request) Validate() error {
	if strings.TrimSpace(r.AgentID) == "" {
		return errors.New("Permission Request AgentID 不能为空")
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("Permission Request SessionID 不能为空")
	}
	if strings.TrimSpace(r.ToolName) == "" {
		return errors.New("Permission Request ToolName 不能为空")
	}
	switch r.Risk {
	case RiskRead, RiskWrite, RiskExec:
	default:
		return fmt.Errorf("Permission Request Risk %q 不合法", r.Risk)
	}

	identity := r.Identity.Normalize()
	if err := identity.Validate(); err != nil {
		return fmt.Errorf("Permission Request Capability Identity 无效: %w", err)
	}
	if identity.Tool != strings.TrimSpace(r.ToolName) {
		return fmt.Errorf("Permission Request ToolName=%q 与 Capability Identity Tool=%q 不一致", r.ToolName, identity.Tool)
	}
	if identity.Risk != r.Risk {
		return fmt.Errorf("Permission Request Risk=%q 与 Capability Identity Risk=%q 不一致", r.Risk, identity.Risk)
	}
	return nil
}

// PresentationField 是 Approval UI 可以安全展示的一项参数摘要。
type PresentationField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Presentation 是一次 Ask 请求的用户可见描述。
type Presentation struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Fields      []PresentationField `json:"fields,omitempty"`
}

// Decision 是 PermissionEngine 对单次调用的不可变判断结果。
type Decision struct {
	Action Action
	Reason string

	RuleID string

	ApprovalID string

	Identity CapabilityIdentity

	Presentation Presentation
}

// Rule 是可复用 Permission Policy。
//
// Allow Rule 保存完整 CapabilityIdentity；Deny Rule 保存 DenyScope() 后的稳定限制身份。
// 因此 Allow 会在安全边界变化时自动失效，而 Deny 不会因为 Workspace/Runtime 变化被静默放宽。
type Rule struct {
	ID string `json:"id"`

	AgentID string `json:"agentId"`

	SessionID string `json:"sessionId,omitempty"`

	ToolName string `json:"tool"`

	Action Action `json:"action"`

	Scope GrantScope `json:"scope"`

	Identity CapabilityIdentity `json:"identity"`

	CreatedAt time.Time `json:"createdAt"`
}

// Validate 校验 v2 Rule。
func (r Rule) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("Permission Rule ID 不能为空")
	}
	if strings.TrimSpace(r.AgentID) == "" {
		return errors.New("Permission Rule AgentID 不能为空")
	}
	if strings.TrimSpace(r.ToolName) == "" {
		return errors.New("Permission Rule ToolName 不能为空")
	}
	if r.Action != ActionAllow && r.Action != ActionDeny {
		return fmt.Errorf("Permission Rule Action %q 不合法", r.Action)
	}
	if r.Scope != GrantSession && r.Scope != GrantAgent {
		return fmt.Errorf("Permission Rule Scope %q 不合法", r.Scope)
	}
	if r.Scope == GrantSession && strings.TrimSpace(r.SessionID) == "" {
		return errors.New("Session Scope Permission Rule 必须包含 SessionID")
	}

	identity := r.Identity.Normalize()
	if identity.Tool != strings.TrimSpace(r.ToolName) {
		return fmt.Errorf("Permission Rule ToolName=%q 与 Identity Tool=%q 不一致", r.ToolName, identity.Tool)
	}
	if r.Action == ActionAllow {
		if err := identity.Validate(); err != nil {
			return fmt.Errorf("Permission Allow Rule Identity 无效: %w", err)
		}
		return nil
	}

	// Deny Identity 是有意收敛后的匹配模式，不要求完整 SandboxFingerprint/Risk。
	if identity.Version != CapabilityIdentityVersion {
		return fmt.Errorf("Permission Deny Rule Identity Version %d 不受支持", identity.Version)
	}
	if identity.Tool == "" {
		return errors.New("Permission Deny Rule Identity Tool 不能为空")
	}
	switch identity.Kind {
	case CapabilityBuiltin, CapabilityCommand, CapabilitySkillScript, CapabilityMCP:
	default:
		return fmt.Errorf("Permission Deny Rule Identity Kind %q 不受支持", identity.Kind)
	}
	return nil
}

// ApprovalGrant 是用户批准 Ask 请求后交给 PermissionEngine 的授权指令。
type ApprovalGrant struct {
	Scope   GrantScope
	Request Request
}
