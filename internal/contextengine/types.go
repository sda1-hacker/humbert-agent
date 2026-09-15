package contextengine

import (
	"errors"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ReasoningReplayPolicy 决定历史 Assistant Thinking 是否继续发送给主模型。
//
// Transcript 永远完整保存 Thinking；本策略只影响“当前模型请求看到什么”。默认 Auto
// 采用兼容优先策略：保留 Eino schema.Message.ReasoningContent，让现有 Provider Adapter
// 自己决定如何编码。未来如果某个 Provider 明确不能回放 Reasoning，可在 Provider 能力层
// 将其解析为 Omit，而不需要修改 Session JSONL。
type ReasoningReplayPolicy string

const (
	ReasoningReplayAuto    ReasoningReplayPolicy = "auto"
	ReasoningReplayInclude ReasoningReplayPolicy = "include"
	ReasoningReplayOmit    ReasoningReplayPolicy = "omit"
)

// CompactionReason 描述一次压缩的触发来源。
//
// 该值会写入 Transcript CompactionEntry.details.reason，便于 UI 和后续问题定位区分
// 自动阈值、同 Turn 紧急压缩和用户手动操作。
type CompactionReason string

const (
	CompactionReasonThreshold CompactionReason = "threshold"
	CompactionReasonMidRun    CompactionReason = "mid_run"
	CompactionReasonOverflow  CompactionReason = "overflow"
	CompactionReasonManual    CompactionReason = "manual"
)

// Budget 是针对一个具体模型计算出的 Context Token 预算。
//
// ThresholdTokens = ContextWindow - ReserveTokens。UsedTokens 达到阈值时自动压缩；
// Percent 始终以 ContextWindow 为分母，和 UI 中“已用 xk (y%)”的直觉一致，而不是以
// ThresholdTokens 为分母。
type Budget struct {
	ContextWindow int `json:"contextWindow"`

	MaxOutputTokens int `json:"maxOutputTokens"`

	ReserveTokens int `json:"reserveTokens"`

	ThresholdTokens int `json:"thresholdTokens"`

	KeepRecentTokens int `json:"keepRecentTokens"`
}

// Usage 描述某个 Session 在当前模型下的上下文使用情况。
//
// UsedTokens 是 Humbert 的本地估算值。Provider 最近一次真实 prompt usage 会继续保存在
// AssistantMessage 中，但 UI/压缩规划不能只依赖它，因为最新 ToolResult、System Prompt
// 和 Tool Schema 可能尚未包含在该 usage 中。
type Usage struct {
	ContextWindow int `json:"contextWindow"`

	// UsedTokens 是下一次模型请求预计占用的总 Context Token。它必须始终等于下面
	// System/Tool/Memory/Checkpoint/Message 五类占用之和，前端据此展示环形进度。
	UsedTokens int `json:"usedTokens"`

	// SystemTokens 是稳定 Agent/Runtime Instruction 的估算占用。新 Session 即使没有
	// Message，也会因为这部分以及 Tool Schema 存在基础 Context 开销。
	SystemTokens int `json:"systemTokens"`

	// ToolTokens 是本 Turn 冻结 Tool Definitions/JSON Schema 的估算占用。这里不包含
	// ToolCall/ToolResult 历史；后者属于 MessageTokens。
	ToolTokens int `json:"toolTokens"`

	// MemoryTokens 是当前有效 Session Memory Key Facts 及其注入标题/分隔符的占用。
	// Timeline 不注入主模型，因此不会计入该字段。
	MemoryTokens int `json:"memoryTokens"`

	// CheckpointTokens 是最新持久化 Compaction Checkpoint 的模型可见占用。没有发生过
	// durable compaction 时为 0。旧 checkpoint 已被最新 checkpoint 递归吸收，不重复计。
	CheckpointTokens int `json:"checkpointTokens"`

	// MessageTokens 是当前 firstKeptEntryId 之后 Recent ActiveBranch 原始消息的占用，
	// 包括 User/Assistant、按策略回放的 Thinking、ToolCall 与 ToolResult。
	MessageTokens int `json:"messageTokens"`

	ReserveTokens int `json:"reserveTokens"`

	ThresholdTokens int `json:"thresholdTokens"`

	KeepRecentTokens int `json:"keepRecentTokens"`

	Percent float64 `json:"percent"`

	NeedsCompaction bool `json:"needsCompaction"`

	LatestCompactionID string `json:"latestCompactionID,omitempty"`

	UpdatedAt time.Time `json:"updatedAt"`
}

// Assembly 描述下一次模型调用实际装配出来的 Context 结构，但不暴露 Prompt、Memory、
// Message 正文或 Tool Arguments。它用于 Runtime/UI 检查“Context 由什么组成”，并与 Usage
// 来自同一次 Engine.Build 投影，避免前端依据 Transcript 重新猜测。
type Assembly struct {
	VisibleMessageCount int `json:"visibleMessageCount"`

	RecentMessageCount int `json:"recentMessageCount"`

	UserMessageCount int `json:"userMessageCount"`

	AssistantMessageCount int `json:"assistantMessageCount"`

	ToolResultCount int `json:"toolResultCount"`

	ToolCallCount int `json:"toolCallCount"`

	MemoryInjected bool `json:"memoryInjected"`

	CheckpointInjected bool `json:"checkpointInjected"`

	LatestCompactionID string `json:"latestCompactionID,omitempty"`
}

// Snapshot 是一次模型调用所需的 Context 投影。
//
// Instruction 已经包含 Agent Runtime Instruction 与当前 Session Memory 的 Key Facts；
// Messages 则由最新 Compaction Checkpoint + Recent ActiveBranch 原始消息构成。Snapshot
// 本身不持久化，任何时候都可从 Transcript + memory.json 重建。
type Snapshot struct {
	Instruction string

	Messages []*schema.Message

	Usage Usage

	Assembly Assembly
}

// BuildRequest 是 Engine.Build 的输入。
type BuildRequest struct {
	SessionID string

	Instruction string

	ContextWindow int

	MaxOutputTokens int

	ToolTokenEstimate int

	ReasoningPolicy ReasoningReplayPolicy
}

// CompactRequest 描述一次持久化 Compaction。
type CompactRequest struct {
	SessionID string

	// Model 使用当前 Turn 已冻结的基础模型实例生成内部 Checkpoint。调用时不绑定
	// Tool，因此摘要模型无法在压缩过程中触发副作用。
	Model einomodel.BaseChatModel

	Instruction string

	ContextWindow int

	MaxOutputTokens int

	ToolTokenEstimate int

	Reason CompactionReason

	Force bool
}

// CompactResult 是压缩完成后的稳定结果。
type CompactResult struct {
	Compacted bool `json:"compacted"`

	CompactionID string `json:"compactionID,omitempty"`

	FirstKeptEntryID string `json:"firstKeptEntryID,omitempty"`

	Before Usage `json:"before"`

	After Usage `json:"after"`
}

// ErrNothingToCompact 表示当前分支没有足够的旧历史可安全压缩。
//
// 手动“压缩”时这是用户可理解的正常业务状态；自动压缩路径通常只记录 Debug，不应
// 当作 Runtime 失败。
var (
	// ErrNothingToCompact 表示当前分支没有足够的旧历史可安全压缩。
	// 手动压缩把它视为正常业务状态；自动保护路径则会结合当前 Usage 判断是否应升级
	// 为 ErrContextBudgetExceeded。
	ErrNothingToCompact = errors.New("当前 Session 没有可安全压缩的历史")

	// ErrContextBudgetExceeded 表示 Context 已达到安全阈值，但没有更多历史能够在保持
	// ToolCall/ToolResult 完整性的前提下继续压缩。调用方应停止向 Provider 发送请求，
	// 而不是依赖 Provider 的 context_length_exceeded 作为正常控制流。
	ErrContextBudgetExceeded = errors.New("当前 Context 已超过安全预算且无法继续压缩")
)
