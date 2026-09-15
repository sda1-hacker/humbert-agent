package runtime

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/memory"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const TopicEvent = "runtime.event"

var (
	ErrSessionBusy       = errors.New("当前 Session 已有正在执行的 Turn")
	ErrRunNotFound       = errors.New("运行中的 Turn 不存在")
	ErrClosed            = errors.New("RuntimeService 已关闭")
	ErrAgentModelMissing = errors.New("Agent 尚未配置默认模型")
)

// EventType 是 Runtime -> Desktop 的瞬时事件类型。
//
// 这些事件只用于流式 UI，不属于 Session 持久化协议。assistant.delta、reasoning.delta
// 和 tool.* 都不会逐条写入 JSONL；只有完整 schema.Message 终态进入 SessionManager。
type EventType string

const (
	EventTurnStarted             EventType = "turn.started"
	EventTurnMaintaining         EventType = "turn.maintaining"
	EventAssistantReasoningDelta EventType = "assistant.reasoning.delta"
	EventAssistantDelta          EventType = "assistant.delta"
	EventToolStarted             EventType = "tool.started"
	EventToolCompleted           EventType = "tool.completed"
	EventToolFailed              EventType = "tool.failed"
	EventApprovalRequested       EventType = "approval.requested"
	EventApprovalResolved        EventType = "approval.resolved"
	EventApprovalExpired         EventType = "approval.expired"
	EventTurnCompleted           EventType = "turn.completed"
	EventTurnFailed              EventType = "turn.failed"
	EventTurnCancelled           EventType = "turn.cancelled"
)

// Event 是 Runtime -> Desktop Adapter 的统一实时协议。
type Event struct {
	Type EventType `json:"type"`

	RequestID string `json:"requestID"`

	RunID string `json:"runID"`

	SessionID string `json:"sessionID"`

	AgentID string `json:"agentID"`

	ModelID string `json:"modelID"`

	ModelRevision uint64 `json:"modelRevision"`

	ToolRevision         uint64   `json:"toolRevision"`
	BuiltinToolNames     []string `json:"builtinToolNames,omitempty"`
	SandboxProfile       string   `json:"sandboxProfile,omitempty"`
	SandboxNetworkMode   string   `json:"sandboxNetworkMode,omitempty"`
	SandboxNativeBackend string   `json:"sandboxNativeBackend,omitempty"`

	SkillRevision string                             `json:"skillRevision,omitempty"`
	SkillNames    []string                           `json:"skillNames,omitempty"`
	MCPRevision   uint64                             `json:"mcpRevision,omitempty"`
	MCPServers    []humbertmcp.RuntimeServerSnapshot `json:"mcpServers,omitempty"`
	MCPTools      []humbertmcp.RuntimeToolSnapshot   `json:"mcpTools,omitempty"`
	MCPToolNames  []string                           `json:"mcpToolNames,omitempty"`

	// Runtime 只在 turn.started 中提供，表示该 Turn 已经冻结的统一 Runtime Manifest。
	// 上面的扁平字段继续保留兼容现有前端/日志消费者。
	Runtime *RuntimeManifest `json:"runtime,omitempty"`

	Delta string `json:"delta,omitempty"`

	MessageID string `json:"messageID,omitempty"`

	ToolCallID string `json:"toolCallID,omitempty"`

	ToolName string `json:"toolName,omitempty"`

	ToolArguments string `json:"toolArguments,omitempty"`

	DurationMS int64 `json:"durationMS,omitempty"`

	Error string `json:"error,omitempty"`

	// Approval 只在 approval.* Runtime Event 中存在。Request 本身不包含 raw Tool
	// Arguments，只包含 Permission 层已经脱敏的 Presentation。
	Approval *approval.Request `json:"approval,omitempty"`

	// ApprovalDecision 只在 approval.resolved 中存在，用于前端把卡片切换到终态。
	ApprovalDecision approval.Decision `json:"approvalDecision,omitempty"`

	OccurredAt string `json:"occurredAt,omitempty"`
}

// EventReporter 是 Executor 与 Wails/EventBus 之间的边界。
type EventReporter interface {
	Report(ctx context.Context, event Event)
}

// SessionWriter 是 Executor 写入完整 Eino Runtime Message 的最小边界。
//
// 接口直接接收 *schema.Message，不经过 sessions.ContentBlock 或其他中间消息模型。
type SessionWriter interface {
	AppendAssistantMessage(
		ctx context.Context,
		sessionID string,
		message *schema.Message,
		persistence sessions.AssistantPersistence,
	) (sessions.Message, error)

	AppendToolResult(
		ctx context.Context,
		sessionID string,
		message *schema.Message,
		persistence sessions.ToolResultPersistence,
	) (sessions.Message, error)
}

// RuntimeManifest 是一次 Runtime Resolve 后对“这个 Agent 下一次请求会携带什么能力”的
// 稳定只读描述。它不包含可执行 Model/Tool 实例，也不包含 Prompt 正文，因此可以安全地
// 暴露给 Desktop UI 做 Context/Capability 检查。
//
// Manifest 与真正的 Turn Snapshot 由同一个 Resolver 生成；Settings 修改只会影响下一次
// Resolve，正在执行的 Turn 仍然使用已经冻结的 Snapshot。
type RuntimeManifest struct {
	ProjectID   string `json:"projectID"`
	ProjectName string `json:"projectName"`

	AgentID   string `json:"agentID"`
	AgentName string `json:"agentName"`

	ModelID          string `json:"modelID"`
	ModelDisplayName string `json:"modelDisplayName"`
	ModelRevision    uint64 `json:"modelRevision"`

	ToolRevision     uint64   `json:"toolRevision"`
	BuiltinToolNames []string `json:"builtinToolNames"`

	SkillRevision string   `json:"skillRevision,omitempty"`
	SkillNames    []string `json:"skillNames"`

	MCPRevision    uint64                             `json:"mcpRevision"`
	MCPServers     []humbertmcp.RuntimeServerSnapshot `json:"mcpServers"`
	MCPUnavailable []humbertmcp.RuntimeServerFailure  `json:"mcpUnavailable,omitempty"`
	MCPTools       []humbertmcp.RuntimeToolSnapshot   `json:"mcpTools"`
	MCPToolNames   []string                           `json:"mcpToolNames"`

	// ExposedToolNames 是模型侧最终可见 Tool 名称的稳定并集：Builtin + MCP + Skill。
	// 该字段只用于检查/展示，不代替真正的 []tool.BaseTool。
	ExposedToolNames []string `json:"exposedToolNames"`

	Workspace RuntimeWorkspaceManifest `json:"workspace"`
	Sandbox   RuntimeSandboxManifest   `json:"sandbox"`
}

// RuntimeWorkspaceManifest 是 Runtime Snapshot 中 Workspace 的安全只读投影。
type RuntimeWorkspaceManifest struct {
	Mode    string `json:"mode"`
	RootDir string `json:"rootDir"`
}

// RuntimeSandboxManifest 描述本 Turn 真正生效的 Sandbox，而不是 Agent Profile 中的原始覆盖值。
type RuntimeSandboxManifest struct {
	Profile       string `json:"profile"`
	NetworkMode   string `json:"networkMode"`
	NativeMode    string `json:"nativeMode"`
	NativeBackend string `json:"nativeBackend,omitempty"`
	NativeReady   bool   `json:"nativeReady"`
}

// RunPhase 是一个活动 User Turn 的进程内生命周期状态。
//
// 它不是 Transcript 历史状态：Turn 进入 completed/failed/cancelled 后会从活动表移除，
// 终态仍由 runtime event + Session JSONL 表达。该状态主要用于 UI 重连/重新挂载时恢复。
type RunPhase string

const (
	RunPhaseRunning         RunPhase = "running"
	RunPhaseMaintaining     RunPhase = "maintaining"
	RunPhaseWaitingApproval RunPhase = "waiting_approval"
	RunPhaseCancelling      RunPhase = "cancelling"
)

// ActiveRunStatus 是当前 Session 活动 Turn 的安全只读投影。
type ActiveRunStatus struct {
	RequestID string `json:"requestID"`
	RunID     string `json:"runID"`
	SessionID string `json:"sessionID"`

	Phase RunPhase `json:"phase"`

	StartedAt string `json:"startedAt"`

	WaitingApprovalID string `json:"waitingApprovalID,omitempty"`

	// Approval 仅在 waiting_approval 阶段提供 Permission 层已经脱敏的安全请求，便于 UI
	// 在重新挂载后恢复审批卡片；raw Tool Arguments 仍不会通过该状态接口暴露。
	Approval *approval.Request `json:"approval,omitempty"`

	// Runtime 是活动 Turn 已经冻结的 Manifest。它可能与 ContextOverview.Runtime（下一 Turn）
	// 不同，因此 UI 能明确区分“正在执行什么”和“下一次会执行什么”。
	Runtime RuntimeManifest `json:"runtime"`
}

// ContextOverview 把 Context Usage、Context Assembly、下一 Turn Runtime Manifest 与当前活动
// Turn 状态放在一个只读快照中。UI 因此不需要跨多个 Store 自行推导 Runtime 生命周期。
type ContextOverview struct {
	Usage contextengine.Usage `json:"usage"`

	Assembly contextengine.Assembly `json:"assembly"`

	Runtime RuntimeManifest `json:"runtime"`

	Active *ActiveRunStatus `json:"active,omitempty"`
}

// Snapshot 是一次 User Turn 的不可变 Runtime Snapshot。
type Snapshot struct {
	// Manifest 是本 Turn 已冻结的统一能力身份。执行所需的 Model/Tools 仍保存在下方专用字段；
	// Manifest 只负责审计、事件和 UI 检查，避免这些消费者各自重新拼装。
	Manifest RuntimeManifest

	RequestID string

	RunID string

	SessionID string

	ProjectID string

	AgentID string

	AgentName string

	// BaseInstruction 是不包含 Session Memory 的稳定运行时指令。ContextEngine 每次重建
	// Context 时会在它之上动态注入当前 Session Key Facts，避免重复拼接 Memory。
	BaseInstruction string

	// Instruction 是本次 Turn 初始模型调用使用的最终指令，已经包含 Session Memory。
	Instruction string

	// ModelID 是 Humbert models.json 中的配置 ID，只用于 Runtime Event/日志。
	ModelID string

	ModelRevision uint64

	ToolRevision uint64

	BuiltinToolNames []string

	// SkillRevision 标识本 Turn 冻结的 Skill 集合及包内容身份。它不是全局递增版本，
	// 而是由启用 Skill 名称和文件哈希计算出的稳定摘要，便于日志和未来 Run 重放判断。
	SkillRevision string

	// SkillNames 是本 Turn 真正启用的 Skill 名称副本。Runtime 创建后不会再读取
	// Agent Profile，因此设置页中的修改只会影响下一 Turn。
	SkillNames []string

	// MCPRevision 是本 Turn 冻结的 MCP 控制面版本。
	MCPRevision uint64

	// MCPServers 保存当前 Turn 真正参与 Runtime 的 Server 安全身份；Disabled Server 不进入。
	MCPServers []humbertmcp.RuntimeServerSnapshot

	// MCPUnavailable 保存本 Turn 因连接/initialize 等问题被跳过的 MCP Server。
	// 它只用于诊断和 UI 提示；失败 Server 的 Tool 不会进入 Tools。
	MCPUnavailable []humbertmcp.RuntimeServerFailure

	// MCPTools 保存当前 Turn 的 raw/exposed 名称与有效 Risk，用于审计 Permission 语义。
	MCPTools []humbertmcp.RuntimeToolSnapshot

	// MCPToolNames 是真正暴露给模型的 MCP exposed tool names。
	MCPToolNames []string

	// 以下字段用于给 JSONL AssistantMessage 补充实际 Provider/Model 描述。
	ProviderID string

	ProviderAPI string

	ProviderName string

	ModelName string

	ModelDisplayName string

	Model einomodel.ToolCallingChatModel

	ContextWindow int

	MaxOutputTokens int

	ToolTokenEstimate int

	ContextBudget contextengine.Budget

	ContextUsage contextengine.Usage

	ContextAssembly contextengine.Assembly

	// PreRunCompacted 表示本 Turn 在首次 Provider 调用前已经发生过至少一次 durable
	// Compaction。该标记只存在于本 Turn Snapshot，不写入 Session；Turn 完成后的派生维护
	// 使用它强制刷新 Session Memory，避免“发送前已压缩，但完成后没有再次压缩”时 Memory
	// Cursor 长时间停留在旧历史。
	PreRunCompacted bool

	// AgentHandlers 只包含本 Turn 冻结的 Eino ChatModelAgent Handler。当前 Context Handler
	// 使用 BeforeModelRewriteState 保护同一个 ReAct tool loop 的 Context，不直接写 JSONL。
	AgentHandlers []adk.ChatModelAgentMiddleware

	Tools []einotool.BaseTool

	Messages []*schema.Message

	Workspace workspace.Workspace

	Sandbox sandbox.EffectivePolicy

	SessionWriter SessionWriter

	EventReporter EventReporter
}

// StartTurnInput 描述新的 User Turn。
type StartTurnInput struct {
	SessionID string

	Input              sessions.UserInput
	RetryUserMessageID string
}

// StartTurnResult 是异步 Turn 启动结果。
type StartTurnResult struct {
	RequestID string `json:"requestID"`

	RunID string `json:"runID"`

	SessionID string `json:"sessionID"`

	UserMessageID string `json:"userMessageID"`
	// StartError 表示用户消息已保存，但运行初始化失败；Desktop 仍须返回收据用于安全重试。
	StartError string `json:"startError,omitempty"`

	ContextUsage contextengine.Usage `json:"contextUsage"`

	ContextAssembly contextengine.Assembly `json:"contextAssembly"`

	// Runtime 是这个已经启动的 Turn 真正冻结的 Manifest；它可能与发送前最后一次
	// ContextOverview 不同（例如用户刚修改过 Agent 设置），因此由 StartTurnResult 回传。
	Runtime RuntimeManifest `json:"runtime"`
}

// ManualCompactionResult 是用户主动执行“压缩”或“压缩并更新”的结果。
//
// Memory 仅在 updateMemory=true 且刷新成功时 Updated=true；普通压缩不会隐式更新 Memory，
// 与前端两个菜单动作保持清晰语义。
type ManualCompactionResult struct {
	Compaction contextengine.CompactResult `json:"compaction"`

	Memory memory.RefreshResult `json:"memory"`

	ContextUsage contextengine.Usage `json:"contextUsage"`
}

// ExecutionResult 是 Executor 最终结果。
//
// MessageID 指向最后一个已持久化的 AssistantMessage；如果模型尚未形成任何完整
// Assistant Step 就失败，则为空。
type ExecutionResult struct {
	Content string

	MessageID string

	// Interrupted 非空表示 Eino 已保存 Checkpoint，当前 Turn 正等待 Human Approval。
	// 这不是失败终态；RuntimeService 必须保留 Session reservation，直到用户决定、超时、
	// 取消或应用关闭。
	Interrupted *InterruptedExecution
}

// InterruptedExecution 是 Executor 从 Eino root-cause Interrupt 提取出的安全审批信息。
type InterruptedExecution struct {
	InterruptID string
	Info        approval.InterruptInfo
}

// ResolveApprovalInput 是 Desktop Adapter 恢复待审批 Turn 的输入。
type ResolveApprovalInput struct {
	ApprovalID string
	Decision   approval.Decision
}

// ResolveApprovalResult 告诉前端审批已被 Runtime 接受。真正 Tool 执行和后续 Assistant
// 继续通过原有 runtime events 异步返回。
type ResolveApprovalResult struct {
	Approval approval.Request `json:"approval"`
}
