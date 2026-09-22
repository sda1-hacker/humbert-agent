package tasks

import (
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/permission"
)

type ExecutionType string

const (
	ExecutionAgent        ExecutionType = "agent"
	ExecutionNotification ExecutionType = "notification"
)

// ConversationMode 决定 Agent 类型任务在多次运行之间如何使用 Session。
//
// isolated（独立对话）：每个 TaskRun 都创建自己的 Session，运行之间完全隔离。
// continuous（连续对话）：同一个 Task 的所有运行尽量复用一个持久 Session；如果该
// Session 被用户手动删除，下次运行会自动创建新的 Session 并更新任务引用。
type ConversationMode string

const (
	ConversationIsolated   ConversationMode = "isolated"
	ConversationContinuous ConversationMode = "continuous"
)

type TaskStatus string

const (
	TaskStatusActive   TaskStatus = "active"
	TaskStatusPaused   TaskStatus = "paused"
	TaskStatusArchived TaskStatus = "archived"
)

type ScheduleType string

const (
	ScheduleManual   ScheduleType = "manual"
	ScheduleOnce     ScheduleType = "once"
	ScheduleInterval ScheduleType = "interval"
	ScheduleDaily    ScheduleType = "daily"
	ScheduleWeekly   ScheduleType = "weekly"
)

type MisfirePolicy string

const (
	MisfireSkip    MisfirePolicy = "skip"
	MisfireRunOnce MisfirePolicy = "run_once"
)

type OverlapPolicy string

const (
	OverlapSkip     OverlapPolicy = "skip"
	OverlapQueueOne OverlapPolicy = "queue_one"
)

// Schedule 是用户可编辑的结构化日程。第一版刻意不暴露原始 Cron，避免时区、DST 和
// 错过执行语义散落到 UI 字符串中。
type Schedule struct {
	Type ScheduleType `json:"type"`

	TimeZone string     `json:"time_zone,omitempty"`
	RunAt    *time.Time `json:"run_at,omitempty"`

	IntervalMinutes int    `json:"interval_minutes,omitempty"`
	TimeOfDay       string `json:"time_of_day,omitempty"`
	Weekdays        []int  `json:"weekdays,omitempty"`

	MisfirePolicy MisfirePolicy `json:"misfire_policy"`
	OverlapPolicy OverlapPolicy `json:"overlap_policy"`
}

type Limits struct {
	MaxDurationSeconds int `json:"max_duration_seconds"`
	MaxModelCalls      int `json:"max_model_calls"`
	MaxToolCalls       int `json:"max_tool_calls"`
	MaxTotalTokens     int `json:"max_total_tokens"`
	MaxAttempts        int `json:"max_attempts"`
	RetryDelaySeconds  int `json:"retry_delay_seconds"`
}

type Task struct {
	ID      string `json:"id"`
	AgentID string `json:"agent_id"`

	// Internal 标识由 Humbert 子系统创建的隐藏任务。它仍然完整持久化并复用
	// Task Runtime，但不会出现在普通用户任务列表中。
	Internal  bool   `json:"internal,omitempty"`
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"origin_ref,omitempty"`

	Name      string        `json:"name"`
	Prompt    string        `json:"prompt"`
	Execution ExecutionType `json:"execution,omitempty"`

	// ConversationMode 只对 Agent 执行生效。旧任务没有该字段时按 isolated 处理，
	// 保持升级前“每次运行创建新 Session”的行为不变。
	ConversationMode ConversationMode `json:"conversation_mode,omitempty"`

	// PersistentSessionID 是连续对话当前正在使用的 Session 引用。它是弱引用：
	// Session 可以被用户从会话侧栏独立删除；运行时发现引用失效后会自动创建新会话。
	PersistentSessionID string `json:"persistent_session_id,omitempty"`

	Status TaskStatus `json:"status"`

	Schedule Schedule `json:"schedule"`
	Limits   Limits   `json:"limits"`

	NextRunAt *time.Time `json:"next_run_at,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

type RunStatus string

const (
	RunQueued          RunStatus = "queued"
	RunStarting        RunStatus = "starting"
	RunRunning         RunStatus = "running"
	RunWaitingApproval RunStatus = "waiting_approval"
	RunSucceeded       RunStatus = "succeeded"
	RunFailed          RunStatus = "failed"
	RunCancelled       RunStatus = "cancelled"
	RunTimedOut        RunStatus = "timed_out"
	RunInterrupted     RunStatus = "interrupted"
	RunSkipped         RunStatus = "skipped"
)

type RunTrigger string

const (
	TriggerManual     RunTrigger = "manual"
	TriggerSchedule   RunTrigger = "schedule"
	TriggerRetry      RunTrigger = "retry"
	TriggerAutomation RunTrigger = "automation"
)

// ApprovalSnapshot 只持久化可安全展示的审批投影，不含 checkpoint、原始 Tool 参数或
// capability identity。应用重启后对应 Run 会转为 interrupted 并清除此字段。
type ApprovalSnapshot struct {
	ID           string                  `json:"id"`
	ToolName     string                  `json:"tool_name"`
	Risk         string                  `json:"risk"`
	Presentation permission.Presentation `json:"presentation"`
	CreatedAt    time.Time               `json:"created_at"`
	ExpiresAt    time.Time               `json:"expires_at"`
}

type Run struct {
	ID      string `json:"id"`
	TaskID  string `json:"task_id"`
	AgentID string `json:"agent_id"`

	SessionID    string `json:"session_id"`
	RequestID    string `json:"request_id,omitempty"`
	RuntimeRunID string `json:"runtime_run_id,omitempty"`

	Trigger      RunTrigger    `json:"trigger"`
	Execution    ExecutionType `json:"execution,omitempty"`
	ParentRunID  string        `json:"parent_run_id,omitempty"`
	ScheduledFor time.Time     `json:"scheduled_for"`
	Attempt      int           `json:"attempt"`
	Status       RunStatus     `json:"status"`

	ToolCalls    int `json:"tool_calls"`
	ModelCalls   int `json:"model_calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	Approval        *ApprovalSnapshot `json:"approval,omitempty"`
	ResultMessageID string            `json:"result_message_id,omitempty"`
	ResultPreview   string            `json:"result_preview,omitempty"`
	Error           string            `json:"error,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DeadlineAt *time.Time `json:"deadline_at,omitempty"`
}

type CreateInput struct {
	AgentID          string
	Name             string
	Prompt           string
	Execution        ExecutionType
	ConversationMode ConversationMode
	Status           TaskStatus
	Schedule         Schedule
	Limits           Limits
}

type UpdateInput struct {
	Name             string
	Prompt           string
	Execution        ExecutionType
	ConversationMode ConversationMode
	Status           TaskStatus
	Schedule         Schedule
	Limits           Limits
}

type Issue struct {
	AgentID string `json:"agent_id"`
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id,omitempty"`
	Error   string `json:"error"`
}

// EffectiveConversationMode 返回任务真正使用的会话方式。
// 仅通知任务不会创建 Session，因此始终视为独立模式；旧版本任务缺少字段时也默认独立，
// 从而保证配置文件向后兼容。
func (t Task) EffectiveConversationMode() ConversationMode {
	if t.EffectiveExecution() != ExecutionAgent {
		return ConversationIsolated
	}
	if t.ConversationMode == "" {
		return ConversationIsolated
	}
	return t.ConversationMode
}

func (t Task) EffectiveExecution() ExecutionType {
	if t.Execution == "" {
		return ExecutionAgent
	}
	return t.Execution
}

func (s RunStatus) Terminal() bool {
	switch s {
	case RunSucceeded, RunFailed, RunCancelled, RunTimedOut, RunInterrupted, RunSkipped:
		return true
	default:
		return false
	}
}

func (s RunStatus) Active() bool {
	return s == RunStarting || s == RunRunning || s == RunWaitingApproval
}

// AutomationInput 是 Humbert 内部子系统复用 Task Runtime 的入口。
// 这类任务不会进入普通 Task 列表，也不会自动重试，避免应用重启后重放副作用。
type AutomationInput struct {
	AgentID   string
	Name      string
	Prompt    string
	Origin    string
	OriginRef string
	Limits    Limits
}
