package sessions

import (
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// Session 表示一个 Agent Conversation。
//
// Session 本身不复制模型配置。每条 Assistant Message 都在 JSONL 中记录当时实际使用
// 的 Provider/Model，因此未来同一 Session 中途切换模型时历史仍然可解释。
type Session struct {
	ID string

	AgentID string

	Title string

	CWD string

	CreatedAt time.Time

	UpdatedAt time.Time
}

// Message 是 Active Branch 上一条已经持久化的 Runtime Message。
//
// Message 本体直接复用 Eino *schema.Message；Humbert 不再定义 sessions.AgentMessage
// 或 sessions.ContentBlock。EntryID/ParentID 是 JSONL Tree 身份，Persistence 只携带
// schema.Message 中不存在的持久化上下文。
type Message struct {
	EntryID string

	ParentID *string

	SessionID string

	AgentID string

	Message *schema.Message

	Persistence transcript.EncodeOptions

	StopReason transcript.StopReason

	CreatedAt time.Time
}

// CreateSessionInput 描述创建 Session 所需参数。
type CreateSessionInput struct {
	AgentID string

	Title string
}

// AssistantPersistence 是保存 Assistant Step 时补充的持久化上下文。
//
// Content、Reasoning、ToolCalls、ResponseMeta/Usage 全部直接来自 Eino schema.Message，
// 不在这里重复定义。
type AssistantPersistence struct {
	API string

	Provider string

	Model string

	ResponseModel string

	ResponseID string

	ForcedFinishReason string

	Cost transcript.UsageCost
}

// ToolResultPersistence 是 ToolResult 在 Eino Message 之外需要保存的附加信息。
//
// Details 只用于后续 Process UI 展示结构化 Tool 信息，不是模型上下文的第二份文本。
type ToolResultPersistence struct {
	Details json.RawMessage

	IsError bool
}

// UserInput 是一次用户输入。Text 与 Attachments 至少一个非空。
type UserInput struct {
	Text        string
	Attachments []AttachmentInput
}

// AttachmentInput 是 Desktop 边界解码前的附件内容。
type AttachmentInput struct {
	Name       string
	MIMEType   string
	Base64Data string
}

// Attachment 是已经写入 Session sidecar 的稳定引用。
type Attachment struct {
	ID        string
	Name      string
	MIMEType  string
	SizeBytes int64
	Kind      string
}
