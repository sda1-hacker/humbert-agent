package approval

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/permission"
)

// Status 描述一次 Human Approval 的运行时生命周期。
//
// Approval 不写入 Session Transcript：它属于“运行控制事件”，不是用户或模型消息。
// Pending 请求只存在当前应用进程；应用退出后 Eino 内存 Checkpoint 一起失效，因此不会
// 出现重启后自动恢复一个旧的高风险 Tool 调用。长期授权只由 permission.Store 持久化。
type Status string

const (
	StatusPending   Status = "pending"
	StatusResolving Status = "resolving"
	StatusResolved  Status = "resolved"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
)

// Decision 是前端能够提交的审批动作。
//
// deny 仅拒绝当前 Tool 调用；allow_once 只批准当前 checkpoint 中保存的这一份调用；
// allow_session 会在当前进程为 Session 创建临时规则；allow_agent 会持久化 Agent Allow Rule；
// deny_agent 会持久化 Agent Deny Rule，并拒绝当前 ToolCall。第一阶段不提供 Global Scope，
// 避免权限无意扩散到其他 Agent。
type Decision string

const (
	DecisionDeny         Decision = "deny"
	DecisionDenyAgent    Decision = "deny_agent"
	DecisionAllowOnce    Decision = "allow_once"
	DecisionAllowSession Decision = "allow_session"
	DecisionAllowAgent   Decision = "allow_agent"
)

// Validate 校验前端提交的审批动作。调用方应使用 errors.Is/As 判断领域错误，不能依赖
// 错误文本。
func (d Decision) Validate() error {
	switch d {
	case DecisionDeny, DecisionDenyAgent, DecisionAllowOnce, DecisionAllowSession, DecisionAllowAgent:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidDecision, d)
	}
}

// InterruptInfo 是 guarded Tool 写入 Eino Interrupt 的“用户可见状态”。
//
// 该结构只包含稳定身份、风险等级、脱敏 Presentation 和最小参数条件。绝不包含原始
// Arguments，因此 Runtime Event 和 Vue Approval Card 即使被调试工具观察，也不会泄露
// write_file 正文、Cookie、Token 等敏感输入。
type InterruptInfo struct {
	ApprovalID string `json:"approvalId"`
	RequestID  string `json:"requestId"`
	RunID      string `json:"runId"`
	SessionID  string `json:"sessionId"`
	AgentID    string `json:"agentId"`

	ToolName string               `json:"toolName"`
	Risk     permission.RiskLevel `json:"risk"`

	Identity     permission.CapabilityIdentity `json:"identity"`
	Presentation permission.Presentation       `json:"presentation"`
}

// Validate 校验从 Eino Interrupt 重新解码出来的审批元数据。
func (i InterruptInfo) Validate() error {
	if strings.TrimSpace(i.ApprovalID) == "" {
		return errors.New("Approval InterruptInfo ApprovalID 不能为空")
	}
	if strings.TrimSpace(i.RequestID) == "" {
		return errors.New("Approval InterruptInfo RequestID 不能为空")
	}
	if strings.TrimSpace(i.RunID) == "" {
		return errors.New("Approval InterruptInfo RunID 不能为空")
	}
	request := i.PermissionRequest()
	if err := request.Validate(); err != nil {
		return fmt.Errorf("Approval InterruptInfo 无效: %w", err)
	}
	return nil
}

// PermissionRequest 把安全 InterruptInfo 还原成创建授权规则所需的最小 Permission 输入。
// 原始 Arguments 故意为空；真实 Tool 参数只保存在 Eino checkpoint 的 InterruptState 中。
func (i InterruptInfo) PermissionRequest() permission.Request {
	return permission.Request{
		RequestID: i.RequestID,
		RunID:     i.RunID,
		SessionID: i.SessionID,
		AgentID:   i.AgentID,
		ToolName:  i.ToolName,
		Risk:      i.Risk,
		Identity:  i.Identity,
	}
}

// InterruptState 是只有 Eino checkpoint 可以读取的内部恢复状态。
//
// Arguments 保存第一次被审批打断时的原始 JSON。恢复时必须执行这里的值，而不能相信
// Resume 调用重新传入的参数，从而保证用户批准的正是当时展示的 Tool Invocation。
type InterruptState struct {
	InfoJSON  string `json:"info"`
	Arguments string `json:"arguments"`
}

// ResumeData 是 Runtime 定向恢复某一个 Eino Interrupt 时传给 Tool 的最小结果。
// 使用 JSON 字符串跨 checkpoint 边界，避免为自定义结构增加 gob 全局注册。
type ResumeData struct {
	Approved bool `json:"approved"`
}

// Request 是 Runtime/Frontend 可观察的一次待审批请求。
//
// Request 不保存 raw Tool Arguments；用户可见内容只能来自已经脱敏的 Presentation。
// CheckpointID/InterruptID 是 Runtime 恢复所需的内部定位信息，对前端 JSON 隐藏。
type Request struct {
	ID        string `json:"id"`
	RequestID string `json:"requestId"`
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId"`

	ToolName string               `json:"toolName"`
	Risk     permission.RiskLevel `json:"risk"`

	Presentation permission.Presentation `json:"presentation"`

	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`

	interruptID  string
	checkpointID string
	identity     permission.CapabilityIdentity
}

// InterruptID 返回 Eino root-cause interrupt ID。只允许 Runtime 内部用于 ResumeWithParams。
func (r Request) InterruptID() string { return r.interruptID }

// CheckpointID 返回保存该中断执行态的 checkpoint ID。
func (r Request) CheckpointID() string { return r.checkpointID }

// Identity 返回本次审批冻结的 Capability Identity。
func (r Request) Identity() permission.CapabilityIdentity { return r.identity }

// Resolution 是 Manager 成功进入 Resolving 状态后的不可变结果。Runtime 只有取得该对象
// 才允许调用 Eino Resume，避免同一个审批被重复点击后执行两次 Tool。
type Resolution struct {
	Request    Request
	Approved   bool
	ResumeJSON string
}
