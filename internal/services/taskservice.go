package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const TaskEventName = "humbert:task:event"

func init() {
	application.RegisterEvent[tasks.Event](TaskEventName)
}

type TaskScheduleDTO struct {
	Type            string `json:"type"`
	TimeZone        string `json:"timeZone"`
	RunAt           string `json:"runAt,omitempty"`
	IntervalMinutes int    `json:"intervalMinutes,omitempty"`
	TimeOfDay       string `json:"timeOfDay,omitempty"`
	Weekdays        []int  `json:"weekdays,omitempty"`
	MisfirePolicy   string `json:"misfirePolicy"`
	OverlapPolicy   string `json:"overlapPolicy"`
}

type TaskLimitsDTO struct {
	MaxDurationSeconds int `json:"maxDurationSeconds"`
	MaxModelCalls      int `json:"maxModelCalls"`
	MaxToolCalls       int `json:"maxToolCalls"`
	MaxTotalTokens     int `json:"maxTotalTokens"`
	MaxAttempts        int `json:"maxAttempts"`
	RetryDelaySeconds  int `json:"retryDelaySeconds"`
}

type TaskDTO struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentID"`
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"originRef,omitempty"`
	Name      string `json:"name"`
	Prompt    string `json:"prompt"`
	Execution string `json:"execution"`

	// ConversationMode / PersistentSessionID 让桌面端明确展示任务的会话策略。
	// PersistentSessionID 只用于状态展示，不要求前端直接操作 Session 生命周期。
	ConversationMode    string `json:"conversationMode"`
	PersistentSessionID string `json:"persistentSessionID,omitempty"`

	Status    string          `json:"status"`
	Schedule  TaskScheduleDTO `json:"schedule"`
	Limits    TaskLimitsDTO   `json:"limits"`
	NextRunAt string          `json:"nextRunAt,omitempty"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}

type TaskApprovalDTO struct {
	ID           string `json:"id"`
	ToolName     string `json:"toolName"`
	Risk         string `json:"risk"`
	Presentation any    `json:"presentation"`
	CreatedAt    string `json:"createdAt"`
	ExpiresAt    string `json:"expiresAt"`
}

type TaskRunDTO struct {
	ID               string           `json:"id"`
	TaskID           string           `json:"taskID"`
	AgentID          string           `json:"agentID"`
	SessionID        string           `json:"sessionID"`
	SessionAvailable bool             `json:"sessionAvailable"`
	RequestID        string           `json:"requestID,omitempty"`
	RuntimeRunID     string           `json:"runtimeRunID,omitempty"`
	Trigger          string           `json:"trigger"`
	Execution        string           `json:"execution"`
	ParentRunID      string           `json:"parentRunID,omitempty"`
	ScheduledFor     string           `json:"scheduledFor"`
	Attempt          int              `json:"attempt"`
	Status           string           `json:"status"`
	ToolCalls        int              `json:"toolCalls"`
	ModelCalls       int              `json:"modelCalls"`
	InputTokens      int              `json:"inputTokens"`
	OutputTokens     int              `json:"outputTokens"`
	TotalTokens      int              `json:"totalTokens"`
	Approval         *TaskApprovalDTO `json:"approval,omitempty"`
	ResultMessageID  string           `json:"resultMessageID,omitempty"`
	ResultPreview    string           `json:"resultPreview,omitempty"`
	Error            string           `json:"error,omitempty"`
	CreatedAt        string           `json:"createdAt"`
	StartedAt        string           `json:"startedAt,omitempty"`
	FinishedAt       string           `json:"finishedAt,omitempty"`
	DeadlineAt       string           `json:"deadlineAt,omitempty"`
}

type SaveTaskRequest struct {
	AgentID          string          `json:"agentID"`
	Name             string          `json:"name"`
	Prompt           string          `json:"prompt"`
	Execution        string          `json:"execution"`
	ConversationMode string          `json:"conversationMode"`
	Status           string          `json:"status"`
	Schedule         TaskScheduleDTO `json:"schedule"`
	Limits           TaskLimitsDTO   `json:"limits"`
}

type TaskStatusRequest struct {
	Status string `json:"status"`
}

type TaskService struct {
	core        *coreapp.Application
	mu          sync.Mutex
	unsubscribe func()
}

func NewTaskService(core *coreapp.Application) *TaskService { return &TaskService{core: core} }
func (s *TaskService) ServiceName() string                  { return "TaskService" }

func (s *TaskService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	unsubscribe, err := s.core.Events().Subscribe(tasks.TopicEvent, func(ctx context.Context, payload any) {
		event, ok := payload.(tasks.Event)
		if !ok {
			return
		}
		if app := application.Get(); app != nil {
			app.Event.Emit(TaskEventName, event)
		}
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.unsubscribe = unsubscribe
	s.mu.Unlock()
	_ = ctx
	_ = options
	return nil
}

func (s *TaskService) ServiceShutdown() error {
	s.mu.Lock()
	unsubscribe := s.unsubscribe
	s.unsubscribe = nil
	s.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
	return nil
}

func (s *TaskService) List() ([]TaskDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	values, err := s.core.Tasks().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取任务列表失败: %w", err)
	}
	result := make([]TaskDTO, 0, len(values))
	for _, value := range values {
		result = append(result, taskDTO(value))
	}
	return result, nil
}

func (s *TaskService) Runs(taskID string) ([]TaskRunDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	values, err := s.core.Tasks().Runs(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("读取任务运行历史失败: %w", err)
	}
	result := make([]TaskRunDTO, 0, len(values))
	// 连续任务可能有几十条 Run 指向同一个 Session。这里按 SessionID 缓存存在性，
	// 避免打开运行历史时为同一份持续对话反复读取 config.json。
	sessionAvailability := make(map[string]bool)
	for _, value := range values {
		dto := taskRunDTO(value)
		if value.SessionID != "" {
			available, known := sessionAvailability[value.SessionID]
			if !known {
				_, sessionErr := s.core.Sessions().Get(ctx, value.SessionID)
				available = sessionErr == nil
				sessionAvailability[value.SessionID] = available
			}
			dto.SessionAvailable = available
		}
		result = append(result, dto)
	}
	return result, nil
}

func (s *TaskService) Create(request SaveTaskRequest) (TaskDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	schedule, err := scheduleFromDTO(request.Schedule)
	if err != nil {
		return TaskDTO{}, err
	}
	value, err := s.core.Tasks().Create(ctx, tasks.CreateInput{AgentID: request.AgentID, Name: request.Name, Prompt: request.Prompt, Execution: tasks.ExecutionType(request.Execution), ConversationMode: tasks.ConversationMode(request.ConversationMode), Status: tasks.TaskStatus(request.Status), Schedule: schedule, Limits: limitsFromDTO(request.Limits)})
	if err != nil {
		return TaskDTO{}, fmt.Errorf("创建任务失败: %w", err)
	}
	return taskDTO(value), nil
}

func (s *TaskService) Update(id string, request SaveTaskRequest) (TaskDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	schedule, err := scheduleFromDTO(request.Schedule)
	if err != nil {
		return TaskDTO{}, err
	}
	value, err := s.core.Tasks().Update(ctx, id, tasks.UpdateInput{Name: request.Name, Prompt: request.Prompt, Execution: tasks.ExecutionType(request.Execution), ConversationMode: tasks.ConversationMode(request.ConversationMode), Status: tasks.TaskStatus(request.Status), Schedule: schedule, Limits: limitsFromDTO(request.Limits)})
	if err != nil {
		return TaskDTO{}, fmt.Errorf("更新任务失败: %w", err)
	}
	return taskDTO(value), nil
}

// SetStatus 立即暂停或恢复任务计划，不会覆盖其它尚未保存的编辑字段。
func (s *TaskService) SetStatus(id string, request TaskStatusRequest) (TaskDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := s.core.Tasks().SetStatus(ctx, id, tasks.TaskStatus(request.Status))
	if err != nil {
		return TaskDTO{}, fmt.Errorf("切换任务状态失败: %w", err)
	}
	return taskDTO(value), nil
}

func (s *TaskService) Archive(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.core.Tasks().Archive(ctx, id); err != nil {
		return fmt.Errorf("归档任务失败: %w", err)
	}
	return nil
}

func (s *TaskService) Delete(id string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	deletedSessionIDs, err := s.core.Tasks().Delete(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("删除任务失败: %w", err)
	}
	s.clearSessionRules(deletedSessionIDs)
	return deletedSessionIDs, nil
}

func (s *TaskService) DeleteRun(id string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	deletedSessionIDs, err := s.core.Tasks().DeleteRun(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("删除运行记录失败: %w", err)
	}
	s.clearSessionRules(deletedSessionIDs)
	return deletedSessionIDs, nil
}

func (s *TaskService) ClearRuns(taskID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	deletedSessionIDs, err := s.core.Tasks().ClearRuns(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("清空运行历史失败: %w", err)
	}
	s.clearSessionRules(deletedSessionIDs)
	return deletedSessionIDs, nil
}

// clearSessionRules 只清理后端已经真正删除的 Session。连续任务删除单条运行记录或
// 清空运行历史时，共享 Session 仍然存在，因此不能顺手撤销它的会话级权限。
func (s *TaskService) clearSessionRules(sessionIDs []string) {
	if s.core.Permissions() == nil {
		return
	}
	for _, sessionID := range sessionIDs {
		if strings.TrimSpace(sessionID) != "" {
			s.core.Permissions().ClearSessionRules(sessionID)
		}
	}
}

func (s *TaskService) RunNow(id string) (TaskRunDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	value, err := s.core.Tasks().RunNow(ctx, id)
	if err != nil {
		return TaskRunDTO{}, fmt.Errorf("启动任务失败: %w", err)
	}
	return taskRunDTO(value), nil
}

func (s *TaskService) CancelRun(id string) (TaskRunDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := s.core.Tasks().CancelRun(ctx, id)
	if err != nil {
		return TaskRunDTO{}, fmt.Errorf("取消任务运行失败: %w", err)
	}
	return taskRunDTO(value), nil
}

func scheduleFromDTO(value TaskScheduleDTO) (tasks.Schedule, error) {
	var runAt *time.Time
	if value.RunAt != "" {
		parsed, err := time.Parse(time.RFC3339, value.RunAt)
		if err != nil {
			locationName := value.TimeZone
			if locationName == "" {
				locationName = time.Local.String()
			}
			location, locationErr := time.LoadLocation(locationName)
			if locationErr != nil {
				return tasks.Schedule{}, fmt.Errorf("解析单次运行时区失败: %w", locationErr)
			}
			parsed, err = time.ParseInLocation("2006-01-02T15:04", value.RunAt, location)
			if err != nil {
				return tasks.Schedule{}, fmt.Errorf("解析单次运行时间失败: %w", err)
			}
			if parsed.In(location).Format("2006-01-02T15:04") != value.RunAt {
				return tasks.Schedule{}, errors.New("单次运行时间在所选时区中不存在（可能处于夏令时跳时区间）")
			}
		}
		runAt = &parsed
	}
	return tasks.Schedule{Type: tasks.ScheduleType(value.Type), TimeZone: value.TimeZone, RunAt: runAt, IntervalMinutes: value.IntervalMinutes, TimeOfDay: value.TimeOfDay, Weekdays: append([]int(nil), value.Weekdays...), MisfirePolicy: tasks.MisfirePolicy(value.MisfirePolicy), OverlapPolicy: tasks.OverlapPolicy(value.OverlapPolicy)}, nil
}

func limitsFromDTO(value TaskLimitsDTO) tasks.Limits {
	return tasks.Limits{MaxDurationSeconds: value.MaxDurationSeconds, MaxModelCalls: value.MaxModelCalls, MaxToolCalls: value.MaxToolCalls, MaxTotalTokens: value.MaxTotalTokens, MaxAttempts: value.MaxAttempts, RetryDelaySeconds: value.RetryDelaySeconds}
}

func taskDTO(value tasks.Task) TaskDTO {
	return TaskDTO{
		ID: value.ID, AgentID: value.AgentID, Name: value.Name, Prompt: value.Prompt,
		Origin: value.Origin, OriginRef: value.OriginRef,
		Execution:           string(value.EffectiveExecution()),
		ConversationMode:    string(value.EffectiveConversationMode()),
		PersistentSessionID: value.PersistentSessionID,
		Status:              string(value.Status), Schedule: scheduleDTO(value.Schedule),
		Limits: TaskLimitsDTO{
			MaxDurationSeconds: value.Limits.MaxDurationSeconds, MaxModelCalls: value.Limits.MaxModelCalls,
			MaxToolCalls: value.Limits.MaxToolCalls, MaxTotalTokens: value.Limits.MaxTotalTokens, MaxAttempts: value.Limits.MaxAttempts,
			RetryDelaySeconds: value.Limits.RetryDelaySeconds,
		},
		NextRunAt: formatOptionalTime(value.NextRunAt),
		CreatedAt: value.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: value.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func scheduleDTO(value tasks.Schedule) TaskScheduleDTO {
	return TaskScheduleDTO{Type: string(value.Type), TimeZone: value.TimeZone, RunAt: formatOptionalTime(value.RunAt), IntervalMinutes: value.IntervalMinutes, TimeOfDay: value.TimeOfDay, Weekdays: append([]int(nil), value.Weekdays...), MisfirePolicy: string(value.MisfirePolicy), OverlapPolicy: string(value.OverlapPolicy)}
}

func taskRunDTO(value tasks.Run) TaskRunDTO {
	result := TaskRunDTO{ID: value.ID, TaskID: value.TaskID, AgentID: value.AgentID, SessionID: value.SessionID, RequestID: value.RequestID, RuntimeRunID: value.RuntimeRunID, Trigger: string(value.Trigger), Execution: string(value.Execution), ParentRunID: value.ParentRunID, ScheduledFor: value.ScheduledFor.Format(time.RFC3339Nano), Attempt: value.Attempt, Status: string(value.Status), ToolCalls: value.ToolCalls, ModelCalls: value.ModelCalls, InputTokens: value.InputTokens, OutputTokens: value.OutputTokens, TotalTokens: value.TotalTokens, ResultMessageID: value.ResultMessageID, ResultPreview: value.ResultPreview, Error: value.Error, CreatedAt: value.CreatedAt.Format(time.RFC3339Nano), StartedAt: formatOptionalTime(value.StartedAt), FinishedAt: formatOptionalTime(value.FinishedAt), DeadlineAt: formatOptionalTime(value.DeadlineAt)}
	result.Approval = taskApprovalDTO(value.Approval)
	return result
}

// taskApprovalDTO 是后台任务页面使用的安全审批投影。ApprovalSnapshot 已经只包含
// Permission Presenter 生成的脱敏信息；原始 Tool Arguments 始终留在 Eino checkpoint 中。
func taskApprovalDTO(value *tasks.ApprovalSnapshot) *TaskApprovalDTO {
	if value == nil {
		return nil
	}
	return &TaskApprovalDTO{
		ID: value.ID, ToolName: value.ToolName, Risk: value.Risk,
		Presentation: value.Presentation,
		CreatedAt:    value.CreatedAt.Format(time.RFC3339Nano),
		ExpiresAt:    value.ExpiresAt.Format(time.RFC3339Nano),
	}
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
