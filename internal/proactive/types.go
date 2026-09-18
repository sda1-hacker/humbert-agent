package proactive

import "time"

const TopicEvent = "proactive.event"

type EventKind string

const (
	EventTaskFailed          EventKind = "task_failed"
	EventTaskTimedOut        EventKind = "task_timed_out"
	EventTaskInterrupted     EventKind = "task_interrupted"
	EventTaskSucceeded       EventKind = "task_succeeded"
	EventTaskLongRunningDone EventKind = "task_long_running_finished"
	EventTaskWaitingApproval EventKind = "task_waiting_approval"
	EventApprovalPending     EventKind = "approval_pending"
	EventWorkspaceChanged    EventKind = "workspace_changed"
)

type Action string

const (
	ActionIgnore   Action = "ignore"
	ActionNotify   Action = "notify"
	ActionRunAgent Action = "run_agent"
)

type QuietHours struct {
	Enabled  bool   `json:"enabled"`
	Start    string `json:"start"`
	End      string `json:"end"`
	TimeZone string `json:"time_zone"`
}

type EventRule struct {
	Enabled bool `json:"enabled"`

	Action Action `json:"action"`

	// AgentID 为空时优先使用事件自身 AgentID。
	AgentID string `json:"agent_id,omitempty"`

	// AgentPrompt 仅用于 run_agent。事件上下文会由执行器自动追加，用户不需要
	// 在这里手工拼 TaskID/RunID 等内部字段。
	AgentPrompt string `json:"agent_prompt,omitempty"`

	// CooldownSeconds 用于同类型高频事件的去抖。事件本身仍按 EventKey 做精确去重。
	CooldownSeconds int `json:"cooldown_seconds,omitempty"`
}

type Settings struct {
	Enabled bool `json:"enabled"`

	HeartbeatIntervalMinutes int `json:"heartbeat_interval_minutes"`

	QuietHours QuietHours `json:"quiet_hours"`

	Rules map[EventKind]EventRule `json:"rules"`
}

func DefaultSettings() Settings {
	return Settings{
		Enabled:                  true,
		HeartbeatIntervalMinutes: 5,
		QuietHours: QuietHours{
			Enabled: false,
			Start:   "22:00",
			End:     "08:00",
		},
		Rules: map[EventKind]EventRule{
			EventTaskFailed:          {Enabled: true, Action: ActionNotify},
			EventTaskTimedOut:        {Enabled: true, Action: ActionNotify},
			EventTaskInterrupted:     {Enabled: true, Action: ActionNotify},
			EventTaskSucceeded:       {Enabled: true, Action: ActionNotify},
			EventTaskLongRunningDone: {Enabled: true, Action: ActionNotify},
			EventTaskWaitingApproval: {Enabled: true, Action: ActionNotify},
			EventApprovalPending:     {Enabled: true, Action: ActionNotify},
			// 工作区变更默认只建立指纹基线，不主动打扰用户。用户可以在设置中改成
			// notify 或 run_agent。
			EventWorkspaceChanged: {Enabled: false, Action: ActionIgnore, CooldownSeconds: 60},
		},
	}
}

type Event struct {
	Key  string    `json:"key"`
	Kind EventKind `json:"kind"`

	Title   string `json:"title"`
	Summary string `json:"summary"`
	Level   string `json:"level,omitempty"`

	AgentID    string `json:"agent_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`

	OccurredAt time.Time `json:"occurred_at"`

	Metadata map[string]string `json:"metadata,omitempty"`
}

type Decision struct {
	Action Action `json:"action"`
	Reason string `json:"reason"`

	AgentID string `json:"agent_id,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

type RecordStatus string

const (
	RecordIgnored   RecordStatus = "ignored"
	RecordDeferred  RecordStatus = "deferred"
	RecordExecuting RecordStatus = "executing"
	RecordSucceeded RecordStatus = "succeeded"
	RecordFailed    RecordStatus = "failed"
)

type Record struct {
	ID       string       `json:"id"`
	Event    Event        `json:"event"`
	Decision Decision     `json:"decision"`
	Status   RecordStatus `json:"status"`

	AutomationTaskID string `json:"automation_task_id,omitempty"`
	AutomationRunID  string `json:"automation_run_id,omitempty"`

	Error string `json:"error,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	HandledAt *time.Time `json:"handled_at,omitempty"`
}

type WorkspaceFileStamp struct {
	Size    int64  `json:"size"`
	ModUnix int64  `json:"mod_unix"`
	Mode    uint32 `json:"mode"`
}

type WorkspaceSnapshot struct {
	AgentID string `json:"agent_id"`
	RootDir string `json:"root_dir"`

	Files map[string]WorkspaceFileStamp `json:"files"`

	Fingerprint string    `json:"fingerprint"`
	ScannedAt   time.Time `json:"scanned_at"`
	Truncated   bool      `json:"truncated,omitempty"`
}

type Status struct {
	Running bool `json:"running"`

	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	NextHeartbeatAt *time.Time `json:"next_heartbeat_at,omitempty"`

	QueuedEvents int `json:"queued_events"`
}

type PublicEvent struct {
	Type string `json:"type"`

	Record *Record `json:"record,omitempty"`
	Status *Status `json:"status,omitempty"`

	OccurredAt string `json:"occurredAt"`
}
