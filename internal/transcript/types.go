package transcript

import (
	"encoding/json"
	"time"
)

const (
	// CurrentVersion 是 Humbert 当前唯一支持的 Session JSONL Schema Version。
	//
	// 项目目前处于开发阶段，不维护旧版本迁移或双协议读取逻辑。删除
	// ~/.humbert-agent 后，新建 Session 全部使用 v3。
	CurrentVersion = 3
)

// EntryType 表示 Session Tree 中一条 Entry 的持久化语义。
//
// message 是当前 Agent 对话协议的核心 Entry。其他类型对齐 OpenHanako/Pi 的
// Session Tree 思路，为模型切换、Thinking Level、Compaction 等后续能力保留稳定
// 协议位置，避免未来重新发明 sidecar metadata 或第二份 Session 状态数据库。
type EntryType string

const (
	EntryMessage             EntryType = "message"
	EntryModelChange         EntryType = "model_change"
	EntryThinkingLevelChange EntryType = "thinking_level_change"
	EntryCompaction          EntryType = "compaction"
	EntryBranchSummary       EntryType = "branch_summary"
	EntryCustom              EntryType = "custom"
	EntryCustomMessage       EntryType = "custom_message"
	EntryLabel               EntryType = "label"
)

// MessageRole 是 JSONL v3 Wire Message 的角色。
//
// 这里故意不直接使用 Eino schema.RoleType。Eino 是 Runtime 协议；Transcript 是长期
// 磁盘协议。两者只能通过 codec.go 转换，避免升级 Eino 时无意改变用户磁盘格式。
type MessageRole string

const (
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleToolResult MessageRole = "toolResult"
)

// ContentType 是 JSONL v3 Message 中的有序 Content Block 类型。
type ContentType string

const (
	ContentText     ContentType = "text"
	ContentImage    ContentType = "image"
	ContentFile     ContentType = "file"
	ContentThinking ContentType = "thinking"
	ContentToolCall ContentType = "toolCall"
)

// StopReason 是持久化 AssistantMessage 的归一化终止原因。
type StopReason string

const (
	StopReasonStop     StopReason = "stop"
	StopReasonLength   StopReason = "length"
	StopReasonToolUse  StopReason = "toolUse"
	StopReasonError    StopReason = "error"
	StopReasonAborted  StopReason = "aborted"
	StopReasonDeferred StopReason = "deferred"
)

// SessionHeader 是 JSONL 第一行。
//
// Header 不参与 Conversation Tree，因此没有 id/parentId 之外的 Tree Node 身份。
// Agent 归属由目录路径 agents/<agent-id>/sessions/<session-id>/ 确定，不在 Header
// 中重复保存一份 agent_id。
type SessionHeader struct {
	Type string `json:"type"`

	Version int `json:"version"`

	ID string `json:"id"`

	Timestamp string `json:"timestamp"`

	CWD string `json:"cwd"`

	ParentSession string `json:"parentSession,omitempty"`
}

// ContentBlock 是 JSONL v3 的有序消息内容块。
//
// 不同 Type 使用不同字段：
//   - text：Text / TextSignature；
//   - thinking：Thinking / ThinkingSignature / Redacted；
//   - toolCall：ID / Name / Arguments / ThoughtSignature / Namespace。
//
// Arguments 使用 json.RawMessage，确保磁盘中保存真实 JSON Object，而不是把 JSON
// 再次包成字符串；同时避免 map[string]any 在往返编码时改变数值类型。
type ContentBlock struct {
	Type ContentType `json:"type"`

	Text string `json:"text,omitempty"`

	TextSignature string `json:"textSignature,omitempty"`

	Thinking string `json:"thinking,omitempty"`

	ThinkingSignature string `json:"thinkingSignature,omitempty"`

	Redacted bool `json:"redacted,omitempty"`

	ID string `json:"id,omitempty"`

	Name string `json:"name,omitempty"`

	Arguments json.RawMessage `json:"arguments,omitempty"`

	ThoughtSignature string `json:"thoughtSignature,omitempty"`

	Namespace string `json:"namespace,omitempty"`

	// AttachmentID 引用 Session sidecar attachments/<id>，JSONL 不保存二进制/Base64。
	AttachmentID string `json:"attachmentId,omitempty"`
	MIMEType     string `json:"mimeType,omitempty"`
	SizeBytes    int64  `json:"sizeBytes,omitempty"`

	// ExtractedText 是 Humbert 在接收文本类文件时确定性提取的 UTF-8 内容。
	// 原始文件仍保存在 sidecar；Runtime 使用该字段构造普通文本输入，不把 Eino
	// Adapter 当前不支持的 file_url 发送给 Provider。图片保持为空。
	ExtractedText string `json:"extractedText,omitempty"`
}

// UsageCost 保存 Provider 明确返回的货币成本。
//
// Eino schema.Message 当前没有统一 Cost 字段，所以没有真实成本数据时保持零值；
// Humbert 不根据价格表自行估算后写入历史。
type UsageCost struct {
	Input float64 `json:"input"`

	Output float64 `json:"output"`

	CacheRead float64 `json:"cacheRead"`

	CacheWrite float64 `json:"cacheWrite"`

	Total float64 `json:"total"`
}

// Usage 保存一次 Assistant Completion 的 Token 使用量。
//
// 字段命名与 OpenHanako/Pi 风格 Session 保持接近。Codec 负责与 Eino
// schema.TokenUsage 双向映射。
type Usage struct {
	Input int `json:"input"`

	Output int `json:"output"`

	CacheRead int `json:"cacheRead"`

	CacheWrite int `json:"cacheWrite"`

	Reasoning int `json:"reasoning,omitempty"`

	TotalTokens int `json:"totalTokens"`

	Cost UsageCost `json:"cost"`
}

// AgentMessage 是 JSONL v3 的 Wire Message，而不是 Humbert Runtime Message。
//
// Runtime 中的唯一消息模型是 Eino *schema.Message。只有 transcript.Codec 会把
// schema.Message 编码成这里的结构，或从这里恢复 schema.Message。
//
// User：role + content + timestamp。
// Assistant：role + ordered content[] + api/provider/model + usage + stopReason。
// ToolResult：role + toolCallId/toolName + content[] + details + isError。
//
// 因此 Thinking 与 ToolCall 都属于 Assistant content[]，不存在第二份 Thinking Store、
// ToolCall Store 或 Runtime Trace Store。
type AgentMessage struct {
	Role MessageRole `json:"role"`

	Content []ContentBlock `json:"content"`

	API string `json:"api,omitempty"`

	Provider string `json:"provider,omitempty"`

	Model string `json:"model,omitempty"`

	ResponseModel string `json:"responseModel,omitempty"`

	Usage *Usage `json:"usage,omitempty"`

	StopReason StopReason `json:"stopReason,omitempty"`

	Timestamp int64 `json:"timestamp"`

	ResponseID string `json:"responseId,omitempty"`

	ToolCallID string `json:"toolCallId,omitempty"`

	ToolName string `json:"toolName,omitempty"`

	Details json.RawMessage `json:"details,omitempty"`

	IsError bool `json:"isError,omitempty"`
}

// CompactionDetails 保存一次上下文压缩的确定性元数据。
//
// Summary 负责语义恢复，而 Details 负责记录“为什么压缩、是否在单个 User Turn 内
// 分割、压缩区域涉及哪些文件”等可验证事实。文件列表由 ToolCall 参数提取，不能完全
// 依赖 LLM 摘要，避免摘要遗漏后丢失关键工作状态。
type CompactionDetails struct {
	Reason string `json:"reason"`

	SplitTurn bool `json:"splitTurn,omitempty"`

	ReadFiles []string `json:"readFiles,omitempty"`

	ModifiedFiles []string `json:"modifiedFiles,omitempty"`
}

// AppendCompactionInput 描述向 Session Tree 追加 CompactionEntry 所需的数据。
//
// FirstKeptEntryID 指向压缩完成后第一个仍以原始 Entry 形式进入模型上下文的节点。
// ExpectedLeafID 则是 Prepare 阶段观察到的 Leaf。TranscriptStore 会在持有 Session 文件锁
// 时同时确认 Leaf 完全一致且 FirstKeptEntryID 仍位于当前 ActiveBranch，从而避免过期摘要
// 被提交到已经追加新节点或发生 Retry/Fork 的分支。
type AppendCompactionInput struct {
	// ExpectedLeafID 是摘要开始生成时观察到的 Session Leaf。Compaction 的模型调用可能
	// 持续数秒甚至更久，提交时必须要求 Leaf 仍完全一致；只验证 firstKeptEntryId 仍在
	// ActiveBranch 不足以发现“同一分支已经追加了新消息”的陈旧摘要。
	ExpectedLeafID string

	Summary string

	FirstKeptEntryID string

	TokensBefore int

	TokensAfter int

	Details CompactionDetails
}

// Entry 是 SessionHeader 之后的 Tree Node。
//
// 每个 Entry 都拥有稳定 ID，并通过 ParentID 指向父节点。当前阶段普通追加总是挂到
// 当前 Leaf；未来实现 Retry/Fork 时，只需允许显式指定历史 ParentID，不需要再次升级
// JSONL Schema。
type Entry struct {
	Type EntryType `json:"type"`

	ID string `json:"id"`

	ParentID *string `json:"parentId"`

	Timestamp string `json:"timestamp"`

	Message *AgentMessage `json:"message,omitempty"`

	Provider string `json:"provider,omitempty"`

	ModelID string `json:"modelId,omitempty"`

	ThinkingLevel string `json:"thinkingLevel,omitempty"`

	Summary string `json:"summary,omitempty"`

	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`

	TokensBefore int `json:"tokensBefore,omitempty"`

	TokensAfter int `json:"tokensAfter,omitempty"`

	Details *CompactionDetails `json:"details,omitempty"`

	FromID *string `json:"fromId,omitempty"`

	TargetID string `json:"targetId,omitempty"`

	Label *string `json:"label,omitempty"`

	CustomType string `json:"customType,omitempty"`

	Data json.RawMessage `json:"data,omitempty"`

	Display bool `json:"display,omitempty"`
}

// CreateSessionInput 描述创建 Transcript JSONL Header 所需的不可变信息。
//
// Session 的可变控制面配置（例如 title）不属于 Transcript 协议，由 sessions 包写入
// 每个 Session 目录自己的 config.json。Transcript 只保存 Agent 对话协议。
type CreateSessionInput struct {
	ID string

	AgentID string

	CWD string

	CreatedAt time.Time
}

// SessionRef 是 TranscriptStore 扫描到的物理 Session 引用。
//
// 它只包含定位 JSONL 所需的 AgentID/SessionID，不携带 title 等 Session 配置，避免
// Transcript 再次拥有 Session Control Plane。
type SessionRef struct {
	ID string

	AgentID string
}

// RepairResult 描述 Transcript Tail Repair 结果。
type RepairResult struct {
	Repaired bool

	TruncatedBytes int64

	AddedFinalNewline bool
}

// MessageEntryPage 是 Active Branch 上的一页 Wire Message Entry。
// Entries 只包含当前页，StartIndex 是它在完整 Message 序列中的零基位置。
type MessageEntryPage struct {
	Entries []Entry

	StartIndex int

	HasMore bool

	NextBeforeID string
}

// Document 是完整 Session JSONL 的内存投影。
//
// Entries 保存全部 Tree Node；ActiveBranch 保存从当前 Leaf 沿 parentId 回溯得到的当前
// 分支。模型上下文只能从 ActiveBranch 构造，不能按文件物理顺序把被放弃的 Branch
// 一起发送给模型。
type Document struct {
	Header SessionHeader

	Entries []Entry

	ActiveBranch []Entry

	LeafID string

	Repair RepairResult
}
