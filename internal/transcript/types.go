package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
// message 是当前 Agent 对话协议的核心 Entry。其他类型对齐 Pi 的Session
// Tree 思路，为模型切换、Thinking Level、Compaction 等后续能力保留稳定
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

	// DocumentOnDemand 表示原件按需转换成 Markdown，正文不写入 Transcript。
	DocumentOnDemand bool `json:"documentOnDemand,omitempty"`
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
// 字段命名与 Pi 风格 Session 保持接近。Codec 负责与 Eino
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
	// Degraded marks a local emergency checkpoint. The next maintenance pass
	// must summarize its original source again before treating it as recovered.
	Degraded bool `json:"degraded,omitempty"`

	ReadFiles []string `json:"readFiles,omitempty"`

	ModifiedFiles []string `json:"modifiedFiles,omitempty"`

	// WindowGeneration 表示提交此检查点后进入的工作窗口编号。0 是初始窗口，
	// 第一次持久化压缩产生 generation=1。
	WindowGeneration int `json:"windowGeneration,omitempty"`

	// Source* 是被该检查点吸收的原始历史范围。摘要只是导航与交接信息，真正事实仍可
	// 通过 session_history 的 read 动作从这些 Entry 中追回。
	SourceFirstEntryID string `json:"sourceFirstEntryId,omitempty"`
	SourceLastEntryID  string `json:"sourceLastEntryId,omitempty"`
	SourceEntryCount   int    `json:"sourceEntryCount,omitempty"`
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
// SQLite 元数据库。Transcript 只保存 Agent 对话协议。
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

// Document 是 Session JSONL 的内存投影。LoadSession 返回完整历史；大文件的
// LoadContextSession 只解码最新压缩窗口，并用 Lineage 保留轻量分支身份。
//
// Entries 保存全部 Tree Node；ActiveBranch 保存从当前 Leaf 沿 parentId 回溯得到的当前
// 分支。模型上下文只能从 ActiveBranch 构造，不能按文件物理顺序把被放弃的 Branch
// 一起发送给模型。
type Document struct {
	Header SessionHeader

	Entries []Entry

	ActiveBranch []Entry

	// Lineage is a lightweight full branch used to validate derived memory cursors when
	// ActiveBranch contains only the decoded compaction window of a large session.
	Lineage []Entry

	ReadStats ReadStats

	LeafID string

	Repair RepairResult

	// ContextWindow 是从当前 ActiveBranch 预先计算的投影边界。缓存命中时，
	// ContextEngine 可直接从 FirstKeptIndex 开始解码，无需每轮重新扫描旧历史。
	ContextWindow ContextWindowIndex
}

// ReadStats measures one context read without exposing message content.
type ReadStats struct {
	CacheHit     bool
	BytesRead    int64
	IndexRebuilt bool
}

// ContextWindowIndex 只记录当前分支上的位置，不持有消息正文。
// LatestCompactionIndex 为 -1 时，FirstKeptIndex 为 0。
type ContextWindowIndex struct {
	Valid                 bool
	LatestCompactionIndex int
	FirstKeptIndex        int
	Generation            int
}

var (
	// ErrInvalidIdentifier 表示 AgentID / SessionID 不能安全地作为 Humbert 内部
	// Transcript 路径的一部分。
	ErrInvalidIdentifier = errors.New("Transcript 标识无效")

	// ErrSessionExists 表示目标 Session Transcript 已存在。
	ErrSessionExists = errors.New("Session Transcript 已存在")

	// ErrSessionNotFound 表示目标 Session Transcript 不存在。
	ErrSessionNotFound = errors.New("Session Transcript 不存在")

	// ErrMessageCursorNotFound 表示分页游标不属于当前 Active Branch 的 Message 序列。
	ErrMessageCursorNotFound = errors.New("Transcript Message 分页游标不存在")

	// ErrCompactionStale 表示 Compaction 摘要生成期间 ActiveBranch 已经发生变化。
	// 调用方必须丢弃旧摘要并基于新的分支重新准备，不能把过期切点强行写入 JSONL。
	ErrCompactionStale = errors.New("Session Compaction 已过期")

	// ErrCorrupted 表示 Transcript 存在无法安全自动恢复的数据损坏。
	//
	// 当前只自动修复文件尾部因进程异常退出产生的残缺 JSON；中间损坏绝不会被静默
	// 删除或跳过。
	ErrCorrupted = errors.New("Session Transcript 已损坏")
)

// CorruptionError 描述 Transcript 中无法安全恢复的具体损坏位置。
//
// Reason 只描述结构问题，不包含原始 JSON。原始内容可能包含用户消息、Tool 参数或
// Tool Result，不允许进入普通日志。
type CorruptionError struct {
	Line int

	Offset int64

	Reason string
}

// Error 返回 Transcript 损坏的结构化描述。
func (e *CorruptionError) Error() string {
	if e == nil {
		return ErrCorrupted.Error()
	}

	return fmt.Sprintf(
		"%s: line=%d offset=%d reason=%s",
		ErrCorrupted.Error(),
		e.Line,
		e.Offset,
		e.Reason,
	)
}

// Unwrap 允许调用方使用 errors.Is(err, transcript.ErrCorrupted)。
func (e *CorruptionError) Unwrap() error {
	return ErrCorrupted
}

// 以下校验属于持久协议：写入与重放共用，避免读写接受不同的消息形态。
func validateAgentMessage(message AgentMessage) error {
	if message.Timestamp <= 0 {
		return errors.New("AgentMessage timestamp 必须大于 0")
	}
	if len(message.Content) == 0 {
		return errors.New("AgentMessage content 不能为空")
	}

	for index, block := range message.Content {
		if err := validateContentBlock(block); err != nil {
			return fmt.Errorf("AgentMessage content[%d] 无效: %w", index, err)
		}
	}

	switch message.Role {
	case RoleUser:
		for _, block := range message.Content {
			if block.Type != ContentText && block.Type != ContentImage && block.Type != ContentFile {
				return errors.New("UserMessage 只允许 text/image/file ContentBlock")
			}
		}

	case RoleAssistant:
		if strings.TrimSpace(message.Model) == "" {
			return errors.New("AssistantMessage model 不能为空")
		}
		if strings.TrimSpace(message.Provider) == "" {
			return errors.New("AssistantMessage provider 不能为空")
		}
		if !validStopReason(message.StopReason) {
			return fmt.Errorf("AssistantMessage stopReason 无效: %q", message.StopReason)
		}

	case RoleToolResult:
		if strings.TrimSpace(message.ToolCallID) == "" {
			return errors.New("ToolResultMessage toolCallId 不能为空")
		}
		if strings.TrimSpace(message.ToolName) == "" {
			return errors.New("ToolResultMessage toolName 不能为空")
		}
		for _, block := range message.Content {
			if block.Type != ContentText {
				return errors.New("ToolResultMessage 当前只允许 text ContentBlock")
			}
		}

	default:
		return fmt.Errorf("AgentMessage role 不支持: %q", message.Role)
	}

	return nil
}

// 按内容块类型检查必需字段；Tool 参数必须是 JSON Object，附件必须包含稳定引用。
func validateContentBlock(block ContentBlock) error {
	switch block.Type {
	case ContentText:
		if block.Text == "" {
			return errors.New("text block 内容不能为空")
		}

	case ContentImage, ContentFile:
		if strings.TrimSpace(block.AttachmentID) == "" {
			return errors.New("attachmentId 不能为空")
		}
		if strings.ContainsAny(block.AttachmentID, `/\`) {
			return errors.New("attachmentId 非法")
		}
		if strings.TrimSpace(block.Name) == "" {
			return errors.New("attachment name 不能为空")
		}
		if strings.TrimSpace(block.MIMEType) == "" {
			return errors.New("attachment mimeType 不能为空")
		}
		if block.SizeBytes < 0 {
			return errors.New("attachment sizeBytes 非法")
		}
		if block.Type == ContentFile && strings.TrimSpace(block.ExtractedText) == "" && !block.DocumentOnDemand {
			return errors.New("file attachment 缺少 extractedText")
		}
		if block.DocumentOnDemand && (block.Type != ContentFile || block.ExtractedText != "") {
			return errors.New("documentOnDemand 只能用于未提取的文件附件")
		}
		if block.Type == ContentImage && block.ExtractedText != "" {
			return errors.New("image attachment 不允许 extractedText")
		}

	case ContentThinking:
		if block.Thinking == "" && !block.Redacted {
			return errors.New("thinking block 内容不能为空")
		}

	case ContentToolCall:
		if strings.TrimSpace(block.ID) == "" {
			return errors.New("toolCall id 不能为空")
		}
		if strings.TrimSpace(block.Name) == "" {
			return errors.New("toolCall name 不能为空")
		}
		if len(block.Arguments) == 0 || !json.Valid(block.Arguments) {
			return errors.New("toolCall arguments 必须是合法 JSON")
		}
		trimmed := bytes.TrimSpace(block.Arguments)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return errors.New("toolCall arguments 必须是 JSON Object")
		}

	default:
		return fmt.Errorf("ContentBlock type 不支持: %q", block.Type)
	}
	return nil
}

func validStopReason(reason StopReason) bool {
	switch reason {
	case StopReasonStop,
		StopReasonLength,
		StopReasonToolUse,
		StopReasonError,
		StopReasonAborted,
		StopReasonDeferred:
		return true
	default:
		return false
	}
}

// 此处验证节点自身；父子关系和当前分支约束由日志重放或 Store 的锁内提交验证。
func validateEntryPayload(entry Entry) error {
	if strings.TrimSpace(entry.ID) == "" {
		return errors.New("Session Entry id 不能为空")
	}
	if _, err := parseTime(entry.Timestamp); err != nil {
		return fmt.Errorf("Session Entry timestamp 无效: %w", err)
	}

	switch entry.Type {
	case EntryMessage:
		if entry.Message == nil {
			return errors.New("message Entry 缺少 message")
		}
		return validateAgentMessage(*entry.Message)

	case EntryModelChange:
		if strings.TrimSpace(entry.Provider) == "" || strings.TrimSpace(entry.ModelID) == "" {
			return errors.New("model_change provider/modelId 不能为空")
		}
		return nil

	case EntryThinkingLevelChange:
		if strings.TrimSpace(entry.ThinkingLevel) == "" {
			return errors.New("thinking_level_change thinkingLevel 不能为空")
		}
		return nil

	case EntryCompaction:
		if strings.TrimSpace(entry.Summary) == "" {
			return errors.New("compaction summary 不能为空")
		}
		if strings.TrimSpace(entry.FirstKeptEntryID) == "" {
			return errors.New("compaction firstKeptEntryId 不能为空")
		}
		if entry.TokensBefore <= 0 {
			return errors.New("compaction tokensBefore 必须大于 0")
		}
		if entry.TokensAfter < 0 {
			return errors.New("compaction tokensAfter 不能小于 0")
		}
		if entry.Details == nil || strings.TrimSpace(entry.Details.Reason) == "" {
			return errors.New("compaction details.reason 不能为空")
		}
		return nil

	case EntryBranchSummary,
		EntryCustom,
		EntryCustomMessage,
		EntryLabel:
		return nil

	default:
		return fmt.Errorf("未知 Session Entry Type: %q", entry.Type)
	}
}

func validateHeader(header SessionHeader, expectedSessionID string) error {
	if header.Type != "session" {
		return errors.New("第一行不是 Session Header")
	}
	if header.Version != CurrentVersion {
		return fmt.Errorf("只支持 Session JSONL v%d，实际为 v%d", CurrentVersion, header.Version)
	}
	if header.ID != expectedSessionID {
		return errors.New("Session Header id 与文件名不一致")
	}
	if _, err := parseTime(header.Timestamp); err != nil {
		return errors.New("Session Header timestamp 无效")
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
