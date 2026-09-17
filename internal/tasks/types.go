package tasks

import (
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/permission"
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
	MaxAttempts        int `json:"max_attempts"`
	RetryDelaySeconds  int `json:"retry_delay_seconds"`
}

type Task struct {
	ID      string `json:"id"`
	AgentID string `json:"agent_id"`

	Name   string     `json:"name"`
	Prompt string     `json:"prompt"`
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
	TriggerManual   RunTrigger = "manual"
	TriggerSchedule RunTrigger = "schedule"
	TriggerRetry    RunTrigger = "retry"
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

	Trigger      RunTrigger `json:"trigger"`
	ParentRunID  string     `json:"parent_run_id,omitempty"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	Attempt      int        `json:"attempt"`
	Status       RunStatus  `json:"status"`

	ToolCalls  int `json:"tool_calls"`
	ModelCalls int `json:"model_calls"`

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
	AgentID  string
	Name     string
	Prompt   string
	Status   TaskStatus
	Schedule Schedule
	Limits   Limits
}

type UpdateInput struct {
	Name     string
	Prompt   string
	Status   TaskStatus
	Schedule Schedule
	Limits   Limits
}

type Issue struct {
	AgentID string `json:"agent_id"`
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id,omitempty"`
	Error   string `json:"error"`
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
