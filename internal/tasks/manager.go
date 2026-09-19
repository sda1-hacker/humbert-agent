package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
)

const TopicEvent = "tasks.event"

type Event struct {
	Type       string `json:"type"`
	TaskID     string `json:"taskID"`
	RunID      string `json:"runID,omitempty"`
	Task       *Task  `json:"task,omitempty"`
	Run        *Run   `json:"run,omitempty"`
	OccurredAt string `json:"occurredAt"`
}

type Manager struct {
	store    *Store
	agents   *agents.Service
	sessions *sessions.Service
	runtime  *agentruntime.Service
	events   *eventbus.Bus
	logger   *logging.Logger

	notifications *notifications.Service

	rootCtx     context.Context
	cancel      context.CancelFunc
	unsubscribe func()

	mu              sync.Mutex
	closed          bool
	activeBySession map[string]string
	activeByRun     map[string]string
	activeAgents    map[string]int
	deletingAgents  map[string]int
	cancelPending   map[string]bool

	cycleMu       sync.Mutex
	wg            sync.WaitGroup
	maxConcurrent int
	tickInterval  time.Duration
}

func NewManager(store *Store, agentService *agents.Service, sessionService *sessions.Service, runtimeService *agentruntime.Service, events *eventbus.Bus, logger *logging.Logger) (*Manager, error) {
	if store == nil || agentService == nil || sessionService == nil || runtimeService == nil || events == nil || logger == nil {
		return nil, errors.New("Task Manager 依赖不完整")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		store: store, agents: agentService, sessions: sessionService, runtime: runtimeService,
		events: events, logger: logger, rootCtx: ctx, cancel: cancel,
		activeBySession: make(map[string]string), activeByRun: make(map[string]string),
		activeAgents: make(map[string]int), deletingAgents: make(map[string]int),
		cancelPending: make(map[string]bool),
		maxConcurrent: 2, tickInterval: 5 * time.Second,
	}, nil
}

func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	now := time.Now()
	interrupted, err := m.store.ReconcileInterrupted(ctx, now)
	if err != nil {
		return fmt.Errorf("恢复 TaskRun 状态失败: %w", err)
	}
	queuedAutomation, err := m.store.ReconcileQueuedAutomationRuns(ctx, now)
	if err != nil {
		return fmt.Errorf("恢复主动助手运行状态失败: %w", err)
	}
	for _, run := range interrupted {
		m.logger.Warn(ctx, "TaskRun 已在启动恢复时标记为 interrupted", "operation", "task.run.reconcile", "task_id", run.TaskID, "run_id", run.ID)
	}
	for _, run := range queuedAutomation {
		m.logger.Warn(ctx, "未启动的内部自动运行已在恢复时标记为 interrupted", "operation", "task.automation.reconcile", "task_id", run.TaskID, "run_id", run.ID)
	}
	if err := m.recoverRetries(ctx); err != nil {
		return fmt.Errorf("恢复 TaskRun 重试队列失败: %w", err)
	}
	unsubscribe, err := m.events.Subscribe(agentruntime.TopicEvent, m.handleRuntimePayload)
	if err != nil {
		return fmt.Errorf("订阅 Runtime Event 失败: %w", err)
	}
	m.unsubscribe = unsubscribe
	m.wg.Add(1)
	go m.schedulerLoop()
	return nil
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	requests := make([]string, 0, len(m.activeByRun))
	runIDs := make([]string, 0, len(m.activeByRun))
	for runID, requestID := range m.activeByRun {
		runIDs = append(runIDs, runID)
		requests = append(requests, requestID)
	}
	m.mu.Unlock()

	if m.unsubscribe != nil {
		m.unsubscribe()
	}
	m.cancel()
	now := time.Now().UTC()
	for _, runID := range runIDs {
		if run, err := m.store.GetRun(context.Background(), runID); err == nil && !run.Status.Terminal() {
			run.Status = RunInterrupted
			run.Error = "应用关闭时任务仍在运行；为避免重放工具副作用，本次运行已中断。"
			run.Approval = nil
			run.FinishedAt = &now
			_ = m.store.UpdateRun(context.Background(), run)
		}
	}
	for _, requestID := range requests {
		_ = m.runtime.CancelTurn(requestID)
	}

	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) List(ctx context.Context) ([]Task, error) {
	values, err := m.store.ListTasks(ctx, false)
	if err != nil {
		return nil, err
	}
	result := make([]Task, 0, len(values))
	for _, value := range values {
		if value.Internal {
			continue
		}
		result = append(result, value)
	}
	return result, nil
}

func (m *Manager) Runs(ctx context.Context, taskID string) ([]Run, error) {
	return m.store.ListRuns(ctx, taskID)
}

func (m *Manager) Run(ctx context.Context, runID string) (Run, error) {
	return m.store.GetRun(ctx, runID)
}

// ActiveRunForSession 用于主动助手区分普通聊天审批与 TaskRun 审批，避免同一
// approval 同时产生两条通知。它只暴露当前活动映射，不改变 Task 生命周期。
func (m *Manager) ActiveRunForSession(ctx context.Context, sessionID string) (Run, bool) {
	m.mu.Lock()
	runID := m.activeBySession[strings.TrimSpace(sessionID)]
	m.mu.Unlock()
	if runID == "" {
		return Run{}, false
	}
	run, err := m.store.GetRun(ctx, runID)
	if err != nil {
		return Run{}, false
	}
	return run, true
}

func (m *Manager) Create(ctx context.Context, input CreateInput) (Task, error) {
	if _, err := m.agents.Get(ctx, strings.TrimSpace(input.AgentID)); err != nil {
		return Task{}, err
	}
	name, prompt, status, schedule, limits, err := normalizeTaskInput(input.Name, input.Prompt, input.Status, input.Schedule, input.Limits)
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	next, err := initialNextRun(schedule, now)
	if err != nil {
		return Task{}, err
	}
	if status != TaskStatusActive {
		next = nil
	}
	execution, err := normalizeExecution(input.Execution)
	if err != nil {
		return Task{}, err
	}
	conversationMode, err := normalizeConversationMode(input.ConversationMode, execution)
	if err != nil {
		return Task{}, err
	}
	value := Task{
		ID: uuid.NewString(), AgentID: strings.TrimSpace(input.AgentID),
		Name: name, Prompt: prompt, Execution: execution, ConversationMode: conversationMode,
		Status: status, Schedule: schedule, Limits: limits, NextRunAt: next,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := m.store.CreateTask(ctx, value); err != nil {
		return Task{}, err
	}
	m.publish(Event{Type: "task.created", TaskID: value.ID, Task: &value})
	return value, nil
}

func (m *Manager) Update(ctx context.Context, id string, input UpdateInput) (Task, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()

	existing, err := m.store.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	name, prompt, status, schedule, limits, err := normalizeTaskInput(input.Name, input.Prompt, input.Status, input.Schedule, input.Limits)
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	next, err := initialNextRun(schedule, now)
	if err != nil {
		return Task{}, err
	}
	if status != TaskStatusActive {
		next = nil
	}
	execution, err := normalizeExecution(input.Execution)
	if err != nil {
		return Task{}, err
	}
	conversationMode, err := normalizeConversationMode(input.ConversationMode, execution)
	if err != nil {
		return Task{}, err
	}
	previousConversationMode := existing.EffectiveConversationMode()
	existing.Name, existing.Prompt, existing.Execution, existing.Status = name, prompt, execution, status
	existing.ConversationMode = conversationMode
	existing.Schedule, existing.Limits, existing.NextRunAt = schedule, limits, next

	// 会话方式从连续切到独立（或切换成仅通知）时，仅解除 Task 对持续 Session 的引用。
	// 旧 Session 本身属于用户历史，不在保存任务配置时自动删除；用户仍可从会话列表查看。
	// 从独立切到连续也从空引用开始，避免随意挑选某个旧的独立运行会话作为持续会话。
	if execution != ExecutionAgent || conversationMode != ConversationContinuous || previousConversationMode != ConversationContinuous {
		existing.PersistentSessionID = ""
	}
	existing.UpdatedAt = now
	if err := m.store.UpdateTask(ctx, existing); err != nil {
		return Task{}, err
	}
	if existing.Status == TaskStatusPaused {
		cancelled, cancelErr := m.store.CancelQueuedAutomaticRuns(ctx, existing.ID, now)
		if cancelErr != nil {
			return Task{}, cancelErr
		}
		for _, run := range cancelled {
			m.publish(Event{Type: "run.cancelled", TaskID: run.TaskID, RunID: run.ID, Run: &run})
		}
	}
	m.publish(Event{Type: "task.updated", TaskID: existing.ID, Task: &existing})
	return existing, nil
}

// SetStatus 只切换计划启用状态，不会用页面里的未保存表单覆盖任务配置。
// 状态切换与调度周期共用 cycleMu，确保调度器不能用暂停前读取的旧 Task 快照
// 在暂停完成后继续入队并把 active 状态写回。
func (m *Manager) SetStatus(ctx context.Context, id string, status TaskStatus) (Task, error) {
	if status != TaskStatusActive && status != TaskStatusPaused {
		return Task{}, fmt.Errorf("无效任务状态: %s", status)
	}

	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()

	value, err := m.store.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if value.Status == TaskStatusArchived {
		return Task{}, errors.New("已归档任务不能暂停或恢复")
	}

	now := time.Now().UTC()
	value.Status = status
	value.UpdatedAt = now
	if status == TaskStatusPaused {
		value.NextRunAt = nil
	} else {
		value.NextRunAt, err = initialNextRun(value.Schedule, now)
		if err != nil {
			return Task{}, err
		}
	}
	if err := m.store.UpdateTask(ctx, value); err != nil {
		return Task{}, err
	}

	if status == TaskStatusPaused {
		cancelled, cancelErr := m.store.CancelQueuedAutomaticRuns(ctx, value.ID, now)
		if cancelErr != nil {
			return Task{}, cancelErr
		}
		for _, run := range cancelled {
			m.publish(Event{Type: "run.cancelled", TaskID: run.TaskID, RunID: run.ID, Run: &run})
		}
	}

	m.publish(Event{Type: "task.updated", TaskID: value.ID, Task: &value})
	return value, nil
}

func (m *Manager) Archive(ctx context.Context, id string) (Task, error) {
	value, err := m.store.ArchiveTask(ctx, id, time.Now())
	if err != nil {
		return Task{}, err
	}
	m.publish(Event{Type: "task.archived", TaskID: value.ID, Task: &value})
	return value, nil
}

// Delete 永久删除任务、运行历史以及由该任务使用的会话。
//
// 连续对话可能让多个 Run 指向同一个 Session，因此这里先收集唯一 Session ID，
// 再在 Task 元数据删除后逐个清理，避免重复删除。同样会包含当前 PersistentSessionID，
// 即使这个持续会话还没有对应任何 Run，也会随整个任务一起删除。
func (m *Manager) Delete(ctx context.Context, id string) ([]string, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	task, err := m.store.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	runs, err := m.store.ListRuns(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if run.Status.Active() {
			return nil, ErrTaskBusy
		}
	}
	now := time.Now().UTC()
	for index := range runs {
		if runs[index].Status != RunQueued {
			continue
		}
		runs[index].Status = RunCancelled
		runs[index].Error = "任务已删除，尚未开始的运行已取消。"
		runs[index].FinishedAt = &now
		if err := m.store.UpdateRun(ctx, runs[index]); err != nil {
			return nil, err
		}
	}
	deletedRuns, err := m.store.DeleteTask(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	m.publish(Event{Type: "task.deleted", TaskID: task.ID})

	candidates := taskRunSessionIDs(deletedRuns)
	if persistentID := strings.TrimSpace(task.PersistentSessionID); persistentID != "" {
		candidates = append(candidates, persistentID)
	}
	return m.deleteSessionIDs(candidates), nil
}

// DeleteRun 只删除一条终态运行。
//
// 对独立对话，这个 Run 通常是该 Session 的唯一引用，因此会一起清理 Session。
// 对连续对话，多个 Run 共用一个 Session；只要仍有其它 Run 引用，或者 Task 当前仍把
// 它作为 PersistentSessionID，删除单条 Run 都不会破坏共享对话。
func (m *Manager) DeleteRun(ctx context.Context, runID string) ([]string, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	run, err := m.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	task, err := m.store.GetTask(ctx, run.TaskID)
	if err != nil {
		return nil, err
	}
	deleted, err := m.store.DeleteRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	m.publish(Event{Type: "run.deleted", TaskID: deleted.TaskID, RunID: deleted.ID})
	return m.deleteUnreferencedRunSessions(ctx, task, []Run{deleted}), nil
}

// ClearRuns 原子清空一个 Task 的终态运行历史。
// 连续对话的当前 PersistentSessionID 会保留，避免用户只是清空“运行记录”却丢失
// 长期跟踪对话；已经不再被 Task 引用的旧 Session 才会清理。
func (m *Manager) ClearRuns(ctx context.Context, taskID string) ([]string, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	task, err := m.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	runs, err := m.store.DeleteRuns(ctx, taskID)
	if err != nil {
		return nil, err
	}
	m.publish(Event{Type: "runs.deleted", TaskID: taskID})
	return m.deleteUnreferencedRunSessions(ctx, task, runs), nil
}

func (m *Manager) RunBySession(ctx context.Context, sessionID string) (Run, bool, error) {
	return m.store.RunBySession(ctx, sessionID)
}

// deleteUnreferencedRunSessions 清理因为删除运行记录而变成“无人引用”的专用 Session。
// 它刻意把 Task 当前持续会话和其它剩余 Run 的 Session 视为保护引用，避免连续模式
// 下删一条历史记录就把所有运行共用的对话一起删掉。
func (m *Manager) deleteUnreferencedRunSessions(ctx context.Context, task Task, candidates []Run) []string {
	remaining, err := m.store.ListRuns(ctx, task.ID)
	if err != nil {
		m.logger.Warn(context.Background(), "读取剩余 TaskRun 失败；为避免误删共享会话，本次跳过会话清理", "operation", "task.run.session.cleanup", "task_id", task.ID, "error", err)
		return nil
	}
	return m.deleteSessionIDs(unreferencedRunSessionIDs(task, remaining, candidates))
}

// unreferencedRunSessionIDs 只做纯粹的引用计算，方便用单元测试覆盖共享 Session 的
// 生命周期规则。返回值里的 Session 才允许被物理删除。
func unreferencedRunSessionIDs(task Task, remaining, candidates []Run) []string {
	protected := make(map[string]struct{})
	if persistentID := strings.TrimSpace(task.PersistentSessionID); persistentID != "" {
		protected[persistentID] = struct{}{}
	}
	for _, run := range remaining {
		if sessionID := strings.TrimSpace(run.SessionID); sessionID != "" {
			protected[sessionID] = struct{}{}
		}
	}

	seen := make(map[string]struct{})
	ids := make([]string, 0, len(candidates))
	for _, run := range candidates {
		sessionID := strings.TrimSpace(run.SessionID)
		if sessionID == "" {
			continue
		}
		if _, keep := protected[sessionID]; keep {
			continue
		}
		if _, duplicate := seen[sessionID]; duplicate {
			continue
		}
		seen[sessionID] = struct{}{}
		ids = append(ids, sessionID)
	}
	return ids
}

// deleteSessionIDs 按唯一 ID best-effort 删除 Session，并返回桌面端应从缓存中忘记的 ID。
// Session 已经被用户手动删除也算“已清理”，这样前端仍能可靠移除陈旧缓存。
func (m *Manager) deleteSessionIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	deleted := make([]string, 0, len(ids))
	for _, raw := range ids {
		sessionID := strings.TrimSpace(raw)
		if sessionID == "" {
			continue
		}
		if _, exists := seen[sessionID]; exists {
			continue
		}
		seen[sessionID] = struct{}{}
		err := m.runtime.DeleteSession(context.Background(), sessionID)
		if err != nil && !errors.Is(err, sessions.ErrSessionNotFound) {
			m.logger.Warn(context.Background(), "清理 Task Session 失败", "operation", "task.run.session.delete", "session_id", sessionID, "error", err)
			continue
		}
		deleted = append(deleted, sessionID)
	}
	return deleted
}

func taskRunSessionIDs(runs []Run) []string {
	result := make([]string, 0, len(runs))
	for _, run := range runs {
		if sessionID := strings.TrimSpace(run.SessionID); sessionID != "" {
			result = append(result, sessionID)
		}
	}
	return result
}

// SetNotificationService 注入统一通知服务。必须在 Manager.Start 之前调用，
// 使应用启动时补跑的 notification 任务也不会启动模型。
func (m *Manager) SetNotificationService(service *notifications.Service) {
	m.mu.Lock()
	m.notifications = service
	m.mu.Unlock()
}

func (m *Manager) RunNow(ctx context.Context, taskID string) (Run, error) {
	task, err := m.store.GetTask(ctx, taskID)
	if err != nil {
		return Run{}, err
	}
	if task.Status == TaskStatusArchived {
		return Run{}, errors.New("已归档任务不能运行")
	}
	if m.agentSuspended(task.AgentID) {
		return Run{}, errors.New("Agent 正在删除，不能启动任务")
	}
	now := time.Now().UTC()
	run := Run{ID: uuid.NewString(), TaskID: task.ID, AgentID: task.AgentID, Trigger: TriggerManual, Execution: task.EffectiveExecution(), ScheduledFor: now, Attempt: 1, Status: RunQueued, CreatedAt: now}
	created, _, err := m.store.CreateRun(ctx, run)
	if err != nil {
		return Run{}, err
	}
	m.publish(Event{Type: "run.queued", TaskID: task.ID, RunID: created.ID, Run: &created})
	if err := m.dispatch(ctx); err != nil {
		m.logger.Warn(ctx, "立即调度 TaskRun 失败", "operation", "task.dispatch", "run_id", created.ID, "error", err)
	}
	return m.store.GetRun(ctx, created.ID)
}

// RunAutomation 让 Humbert 内部子系统复用现有 Task Runtime 执行一次 Agent 工作。
// 内部任务会完整持久化用于审计，但不会出现在普通 Task 列表；应用重启后未完成
// 的 automation 不会自动重放。
func (m *Manager) RunAutomation(ctx context.Context, input AutomationInput) (Task, Run, error) {
	agentID := strings.TrimSpace(input.AgentID)
	if _, err := m.agents.Get(ctx, agentID); err != nil {
		return Task{}, Run{}, err
	}
	name, prompt, status, schedule, limits, err := normalizeTaskInput(
		input.Name, input.Prompt, TaskStatusActive, Schedule{Type: ScheduleManual}, input.Limits,
	)
	if err != nil {
		return Task{}, Run{}, err
	}
	now := time.Now().UTC()
	task := Task{
		ID: uuid.NewString(), AgentID: agentID, Internal: true,
		Origin: strings.TrimSpace(input.Origin), OriginRef: strings.TrimSpace(input.OriginRef),
		Name: name, Prompt: prompt, Execution: ExecutionAgent, Status: status, Schedule: schedule, Limits: limits,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := m.store.CreateTask(ctx, task); err != nil {
		return Task{}, Run{}, err
	}
	run := Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: task.AgentID, Trigger: TriggerAutomation, Execution: ExecutionAgent,
		ScheduledFor: now, Attempt: 1, Status: RunQueued, CreatedAt: now,
	}
	created, _, err := m.store.CreateRun(ctx, run)
	if err != nil {
		_, _ = m.store.ArchiveTask(context.WithoutCancel(ctx), task.ID, time.Now().UTC())
		return Task{}, Run{}, err
	}
	m.publish(Event{Type: "run.queued", TaskID: task.ID, RunID: created.ID, Task: &task, Run: &created})
	if err := m.dispatch(ctx); err != nil {
		m.logger.Warn(ctx, "立即调度内部自动运行失败", "operation", "task.automation.dispatch", "run_id", created.ID, "error", err)
	}
	current, err := m.store.GetRun(ctx, created.ID)
	if err != nil {
		return task, created, nil
	}
	return task, current, nil
}

func (m *Manager) CancelRun(ctx context.Context, runID string) (Run, error) {
	run, err := m.store.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Status.Terminal() {
		return run, nil
	}
	if run.Status == RunQueued {
		now := time.Now().UTC()
		run.Status, run.Error, run.FinishedAt = RunCancelled, "用户在运行开始前取消了任务。", &now
		if err := m.store.UpdateRun(ctx, run); err != nil {
			return Run{}, err
		}
		m.publish(Event{Type: "run.cancelled", TaskID: run.TaskID, RunID: run.ID, Run: &run})
		return run, nil
	}
	if strings.TrimSpace(run.RequestID) == "" {
		m.mu.Lock()
		_, starting := m.activeByRun[run.ID]
		if starting {
			m.cancelPending[run.ID] = true
		}
		m.mu.Unlock()
		if starting {
			return run, nil
		}
		return Run{}, errors.New("TaskRun 尚未绑定 Runtime Request")
	}
	if err := m.runtime.CancelTurn(run.RequestID); err != nil {
		return Run{}, err
	}
	return run, nil
}

// SuspendAgent 阻止删除过程中新建 TaskRun。返回的 release 必须调用；已有运行仍由
// Runtime.DeleteAgent 的 Session reservation 拒绝删除。
func (m *Manager) SuspendAgent(agentID string) func() {
	m.mu.Lock()
	m.deletingAgents[agentID]++
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.deletingAgents[agentID]--
			if m.deletingAgents[agentID] <= 0 {
				delete(m.deletingAgents, agentID)
			}
			m.mu.Unlock()
		})
	}
}

func (m *Manager) schedulerLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.tickInterval)
	defer ticker.Stop()
	m.runCycle()
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case <-ticker.C:
			m.runCycle()
		}
	}
}

func (m *Manager) runCycle() {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	ctx, cancel := context.WithTimeout(m.rootCtx, 30*time.Second)
	defer cancel()
	if err := m.enqueueDue(ctx, time.Now().UTC()); err != nil {
		m.logger.Warn(context.Background(), "扫描到期任务失败", "operation", "task.schedule", "error", err)
	}
	if err := m.dispatchLocked(ctx); err != nil {
		m.logger.Warn(context.Background(), "分派排队任务失败", "operation", "task.dispatch", "error", err)
	}
}

func (m *Manager) enqueueDue(ctx context.Context, now time.Time) error {
	tasks, err := m.store.ListTasks(ctx, false)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Status != TaskStatusActive || task.NextRunAt == nil || task.NextRunAt.After(now) {
			continue
		}
		// 删除失败时还会释放 Suspend；此时保留原 NextRunAt，下一轮按原计划的
		// Misfire Policy 决定补跑或跳过。删除成功时整个 Agent 目录会被状态机清理。
		if m.agentSuspended(task.AgentID) {
			continue
		}
		scheduledFor := task.NextRunAt.UTC()
		next, nextErr := advanceOccurrence(task.Schedule, scheduledFor, now)
		if nextErr != nil {
			return nextErr
		}
		overdue := now.Sub(scheduledFor)
		status := RunQueued
		errText := ""
		if task.Schedule.MisfirePolicy == MisfireSkip && overdue > time.Minute {
			status, errText = RunSkipped, "任务错过计划时间，按照 skip 策略跳过。"
		}
		active, queued := m.taskRunOccupancy(ctx, task.ID)
		if task.Schedule.OverlapPolicy == OverlapSkip && (active || queued > 0) {
			status, errText = RunSkipped, "上一轮仍在运行或排队，按照 skip 策略跳过重叠执行。"
		}
		if task.Schedule.OverlapPolicy == OverlapQueueOne && queued > 0 {
			status, errText = RunSkipped, "已有一轮等待运行，按照 queue_one 策略不再增加候补。"
		}
		finishedAt := (*time.Time)(nil)
		if status == RunSkipped {
			finished := now
			finishedAt = &finished
		}
		run := Run{ID: uuid.NewString(), TaskID: task.ID, AgentID: task.AgentID, Trigger: TriggerSchedule, Execution: task.EffectiveExecution(), ScheduledFor: scheduledFor, Attempt: 1, Status: status, Error: errText, CreatedAt: now, FinishedAt: finishedAt}
		created, _, createErr := m.store.CreateRun(ctx, run)
		if createErr != nil {
			return createErr
		}
		task.NextRunAt, task.UpdatedAt = next, now
		if task.Schedule.Type == ScheduleOnce && next == nil {
			task.Status = TaskStatusPaused
		}
		if updateErr := m.store.UpdateTask(ctx, task); updateErr != nil {
			return updateErr
		}
		m.publish(Event{Type: "run." + string(created.Status), TaskID: task.ID, RunID: created.ID, Run: &created})
	}
	return nil
}

func (m *Manager) dispatch(ctx context.Context) error {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	return m.dispatchLocked(ctx)
}

func (m *Manager) dispatchLocked(ctx context.Context) error {
	runs, err := m.store.ListDispatchableRuns(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, run := range runs {
		task, taskErr := m.store.GetTask(ctx, run.TaskID)
		if taskErr != nil {
			continue
		}
		// 手动 RunNow 是用户的显式操作，允许在计划暂停时执行；计划运行和重试
		// 则必须在真正启动前再次确认 Task 仍为 active。该校验是暂停时取消队列
		// 之外的第二道防线，避免旧队列或异常数据绕过暂停状态。
		if task.Status == TaskStatusArchived || (task.Status != TaskStatusActive && run.Trigger != TriggerManual && run.Trigger != TriggerAutomation) {
			now := time.Now().UTC()
			run.Status = RunCancelled
			run.Error = "任务计划已暂停，排队中的自动运行不再启动。"
			run.FinishedAt = &now
			if updateErr := m.store.UpdateRun(ctx, run); updateErr != nil {
				return updateErr
			}
			m.publish(Event{Type: "run.cancelled", TaskID: run.TaskID, RunID: run.ID, Run: &run})
			continue
		}
		execution := run.Execution
		if execution == "" {
			execution = task.EffectiveExecution()
		}
		run.Execution = execution
		if execution == ExecutionNotification {
			if err := m.startNotificationRun(ctx, task, run); err != nil {
				m.failRun(ctx, run, fmt.Errorf("发送任务通知失败: %w", err))
			}
			continue
		}

		m.mu.Lock()
		blocked := m.closed || len(m.activeByRun) >= m.maxConcurrent || m.activeAgents[run.AgentID] > 0 || m.deletingAgents[run.AgentID] > 0
		m.mu.Unlock()
		if blocked {
			continue
		}
		if err := m.startRun(ctx, task, run); err != nil {
			// startRun 可能已经把 SessionID/Deadline 写入持久层。失败收敛时重新读取，
			// 避免用调度前的 queued 快照覆盖这些诊断信息。
			if current, getErr := m.store.GetRun(context.WithoutCancel(ctx), run.ID); getErr == nil {
				run = current
			}
			m.failRun(ctx, run, fmt.Errorf("启动 TaskRun 失败: %w", err))
		}
	}
	return nil
}

func (m *Manager) startNotificationRun(ctx context.Context, task Task, run Run) error {
	m.mu.Lock()
	notifier := m.notifications
	m.mu.Unlock()
	if notifier == nil {
		return errors.New("Task 通知服务未初始化")
	}
	now := time.Now().UTC()
	run.Execution = ExecutionNotification
	run.Status = RunStarting
	run.StartedAt = &now
	if err := m.store.UpdateRun(ctx, run); err != nil {
		return err
	}
	if err := notifier.Send(ctx, notifications.Notification{
		Level:   notifications.LevelInfo,
		Title:   task.Name,
		Body:    task.Prompt,
		AgentID: task.AgentID,
		TaskID:  task.ID,
		RunID:   run.ID,
	}); err != nil {
		return err
	}
	finished := time.Now().UTC()
	run.Status = RunSucceeded
	run.ResultPreview = task.Prompt
	run.FinishedAt = &finished
	if err := m.store.UpdateRun(context.WithoutCancel(ctx), run); err != nil {
		return err
	}
	m.publish(Event{Type: "run.succeeded", TaskID: task.ID, RunID: run.ID, Task: &task, Run: &run})
	return nil
}

func (m *Manager) startRun(ctx context.Context, task Task, run Run) error {
	now := time.Now().UTC()
	session, err := m.resolveRunSession(ctx, &task, now)
	if err != nil {
		return err
	}
	deadline := now.Add(time.Duration(task.Limits.MaxDurationSeconds) * time.Second)
	run.SessionID, run.Status, run.StartedAt, run.DeadlineAt = session.ID, RunStarting, &now, &deadline
	if err := m.store.UpdateRun(ctx, run); err != nil {
		return err
	}

	m.mu.Lock()
	if m.closed || m.deletingAgents[run.AgentID] > 0 {
		m.mu.Unlock()
		return errors.New("Task Manager 正在关闭或 Agent 正在删除")
	}
	m.activeBySession[session.ID] = run.ID
	m.activeByRun[run.ID] = ""
	m.activeAgents[run.AgentID]++
	m.mu.Unlock()

	result, err := m.runtime.StartTurn(ctx, agentruntime.StartTurnInput{
		SessionID: session.ID,
		Input:     sessions.UserInput{Text: task.Prompt},
		Limits: agentruntime.ExecutionLimits{
			MaxDuration:   time.Duration(task.Limits.MaxDurationSeconds) * time.Second,
			Deadline:      deadline,
			MaxModelCalls: task.Limits.MaxModelCalls,
			MaxToolCalls:  task.Limits.MaxToolCalls,
		},
	})
	if err != nil {
		m.releaseActive(run)
		return err
	}
	m.mu.Lock()
	// 极短 Turn 可能在 StartTurn 返回前已经发布终态并由事件处理器释放映射。
	// 只有 Session 仍属于当前 Run 时才补写 RequestID，避免把已完成 Run 重新变成
	// Manager 眼中的活动运行。
	if m.activeBySession[session.ID] == run.ID {
		m.activeByRun[run.ID] = result.RequestID
	}
	m.mu.Unlock()
	current, getErr := m.store.GetRun(ctx, run.ID)
	if getErr != nil {
		_ = m.runtime.CancelTurn(result.RequestID)
		return getErr
	}
	current.RequestID, current.RuntimeRunID = result.RequestID, result.RunID
	if current.Status == RunStarting {
		current.Status = RunRunning
	}
	if err := m.store.UpdateRun(ctx, current); err != nil {
		_ = m.runtime.CancelTurn(result.RequestID)
		return err
	}
	if current.Status.Terminal() {
		return nil
	}
	m.mu.Lock()
	cancelRequested := m.cancelPending[run.ID]
	delete(m.cancelPending, run.ID)
	m.mu.Unlock()
	if cancelRequested {
		if err := m.runtime.CancelTurn(result.RequestID); err != nil {
			return err
		}
		return nil
	}
	m.publish(Event{Type: "run.started", TaskID: run.TaskID, RunID: run.ID, Run: &current})
	return nil
}

// resolveRunSession 根据任务配置选择本次运行的 Session。
//
// 独立模式永远新建；连续模式优先复用 PersistentSessionID。该 ID 是弱引用，所以用户
// 从会话侧栏手动删除 Session 后，这里会把“not found”视为自然换代，而不是任务失败。
// 只有真正的读取错误（例如损坏数据）才会上抛，避免把异常悄悄伪装成新会话。
func (m *Manager) resolveRunSession(ctx context.Context, task *Task, startedAt time.Time) (sessions.Session, error) {
	if task == nil {
		return sessions.Session{}, errors.New("Task 不能为空")
	}
	if task.EffectiveConversationMode() != ConversationContinuous {
		return m.sessions.Create(ctx, sessions.CreateSessionInput{
			AgentID: task.AgentID,
			Title:   taskSessionTitle(*task, startedAt),
		})
	}

	if persistentID := strings.TrimSpace(task.PersistentSessionID); persistentID != "" {
		existing, err := m.sessions.Get(ctx, persistentID)
		if err == nil {
			if existing.AgentID != task.AgentID {
				return sessions.Session{}, errors.New("连续任务引用的 Session 不属于当前 Agent")
			}
			return existing, nil
		}
		if !errors.Is(err, sessions.ErrSessionNotFound) {
			return sessions.Session{}, fmt.Errorf("读取连续任务 Session 失败: %w", err)
		}
	}

	created, err := m.sessions.Create(ctx, sessions.CreateSessionInput{
		AgentID: task.AgentID,
		Title:   continuousTaskSessionTitle(*task),
	})
	if err != nil {
		return sessions.Session{}, err
	}

	// 先持久化 Task -> Session 引用，再让 Runtime 往这个 Session 写消息。
	// 如果配置落盘失败，立即删除刚建的空 Session，避免产生无法解释的孤儿会话。
	task.PersistentSessionID = created.ID
	task.ConversationMode = ConversationContinuous
	if err := m.store.UpdateTask(ctx, *task); err != nil {
		_ = m.runtime.DeleteSession(context.WithoutCancel(ctx), created.ID)
		return sessions.Session{}, fmt.Errorf("保存连续任务 Session 引用失败: %w", err)
	}
	m.publish(Event{Type: "task.session.bound", TaskID: task.ID, Task: task})
	return created, nil
}

// continuousTaskSessionTitle 不包含某一次运行的日期，因为这个 Session 会跨多次计划运行
// 长期复用；标题只表达它属于哪个持续任务。
func continuousTaskSessionTitle(task Task) string {
	return fmt.Sprintf("持续任务·%s", task.Name)
}

// taskSessionTitle 使用任务配置的时区展示实际启动日期。手动任务没有计划时区时使用
// 应用所在系统时区，标题示例：任务·9月18日测试任务。
func taskSessionTitle(task Task, startedAt time.Time) string {
	location := time.Local
	if timeZone := strings.TrimSpace(task.Schedule.TimeZone); timeZone != "" {
		if configured, err := time.LoadLocation(timeZone); err == nil {
			location = configured
		}
	}
	localTime := startedAt.In(location)
	if task.Internal && task.Origin == "proactive" {
		return fmt.Sprintf("主动·%d月%d日%s·", localTime.Month(), localTime.Day(), task.Name)
	}
	return fmt.Sprintf("任务·%d月%d日%s·", localTime.Month(), localTime.Day(), task.Name)
}

func (m *Manager) handleRuntimePayload(ctx context.Context, payload any) {
	event, ok := payload.(agentruntime.Event)
	if !ok || strings.TrimSpace(event.SessionID) == "" {
		return
	}
	m.mu.Lock()
	runID := m.activeBySession[event.SessionID]
	m.mu.Unlock()
	if runID == "" {
		return
	}
	run, err := m.store.GetRun(context.Background(), runID)
	if err != nil || run.Status.Terminal() {
		return
	}

	now := time.Now().UTC()
	switch event.Type {
	case agentruntime.EventTurnStarted:
		run.Status = RunRunning
	case agentruntime.EventToolStarted:
		run.ToolCalls++
	case agentruntime.EventModelStarted:
		run.ModelCalls++
	case agentruntime.EventApprovalRequested:
		run.Status = RunWaitingApproval
		if event.Approval != nil {
			run.Approval = &ApprovalSnapshot{ID: event.Approval.ID, ToolName: event.Approval.ToolName, Risk: string(event.Approval.Risk), Presentation: event.Approval.Presentation, CreatedAt: event.Approval.CreatedAt, ExpiresAt: event.Approval.ExpiresAt}
		}
	case agentruntime.EventApprovalResolved, agentruntime.EventApprovalExpired:
		run.Status, run.Approval = RunRunning, nil
	case agentruntime.EventTurnCompleted:
		run.Status, run.ResultMessageID, run.FinishedAt, run.Approval = RunSucceeded, event.MessageID, &now, nil
		run.ResultPreview = m.resultPreview(event.SessionID, event.MessageID)
	case agentruntime.EventTurnFailed:
		if run.DeadlineAt != nil && !now.Before(*run.DeadlineAt) {
			run.Status = RunTimedOut
		} else {
			run.Status = RunFailed
		}
		run.Error, run.ResultMessageID, run.FinishedAt, run.Approval = event.Error, event.MessageID, &now, nil
	case agentruntime.EventTurnCancelled:
		if run.DeadlineAt != nil && !now.Before(*run.DeadlineAt) {
			run.Status = RunTimedOut
		} else {
			run.Status = RunCancelled
		}
		run.Error, run.ResultMessageID, run.FinishedAt, run.Approval = event.Error, event.MessageID, &now, nil
	default:
		return
	}
	if err := m.store.UpdateRun(context.Background(), run); err != nil {
		m.logger.Error(context.Background(), "更新 TaskRun 事件状态失败", "run_id", run.ID, "error", err)
		return
	}
	var taskSnapshot *Task
	if task, taskErr := m.store.GetTask(context.Background(), run.TaskID); taskErr == nil {
		taskSnapshot = &task
	}
	m.publish(Event{Type: "run." + string(run.Status), TaskID: run.TaskID, RunID: run.ID, Task: taskSnapshot, Run: &run})
	if run.Status.Terminal() {
		m.releaseActive(run)
		m.maybeRetry(run)
		go m.runCycle()
	}
	_ = ctx
}

func (m *Manager) resultPreview(sessionID, messageID string) string {
	messages, err := m.sessions.Messages(context.Background(), sessionID, 20)
	if err != nil {
		return ""
	}
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Message == nil || message.Message.Role != schema.Assistant {
			continue
		}
		if messageID != "" && message.EntryID != messageID {
			continue
		}
		value := strings.TrimSpace(message.Message.Content)
		if len([]rune(value)) > 500 {
			value = string([]rune(value)[:500]) + "…"
		}
		return value
	}
	return ""
}

func (m *Manager) failRun(ctx context.Context, run Run, err error) {
	now := time.Now().UTC()
	run.Status, run.Error, run.FinishedAt, run.Approval = RunFailed, logging.SafeErrorText(err, 2048), &now, nil
	_ = m.store.UpdateRun(context.WithoutCancel(ctx), run)
	m.releaseActive(run)
	var taskSnapshot *Task
	if task, taskErr := m.store.GetTask(context.Background(), run.TaskID); taskErr == nil {
		taskSnapshot = &task
	}
	m.publish(Event{Type: "run.failed", TaskID: run.TaskID, RunID: run.ID, Task: taskSnapshot, Run: &run})
	m.maybeRetry(run)
}

func (m *Manager) maybeRetry(run Run) {
	if run.Status != RunFailed && run.Status != RunTimedOut {
		return
	}
	task, err := m.store.GetTask(context.Background(), run.TaskID)
	if err != nil || task.Status != TaskStatusActive || run.Attempt >= task.Limits.MaxAttempts {
		return
	}
	now := time.Now().UTC()
	retryAt := now.Add(time.Duration(task.Limits.RetryDelaySeconds) * time.Second)
	if run.FinishedAt != nil {
		retryAt = run.FinishedAt.Add(time.Duration(task.Limits.RetryDelaySeconds) * time.Second)
		if retryAt.Before(now) {
			retryAt = now
		}
	}
	retry := Run{ID: uuid.NewString(), TaskID: task.ID, AgentID: task.AgentID, Trigger: TriggerRetry, Execution: task.EffectiveExecution(), ParentRunID: run.ID, ScheduledFor: retryAt, Attempt: run.Attempt + 1, Status: RunQueued, CreatedAt: now}
	created, _, err := m.store.CreateRun(context.Background(), retry)
	if err == nil {
		m.publish(Event{Type: "run.queued", TaskID: task.ID, RunID: created.ID, Run: &created})
	}
}

// recoverRetries 修复“失败终态已经持久化，但对应重试尚未创建”这一进程崩溃窗口。
// Store 以 ParentRunID 幂等去重，因此重复启动不会产生多个相同重试。
func (m *Manager) recoverRetries(ctx context.Context) error {
	values, err := m.store.ListTasks(ctx, false)
	if err != nil {
		return err
	}
	for _, task := range values {
		if task.Status != TaskStatusActive || task.Limits.MaxAttempts <= 1 {
			continue
		}
		runs, listErr := m.store.ListRuns(ctx, task.ID)
		if listErr != nil {
			return listErr
		}
		for _, run := range runs {
			if (run.Status == RunFailed || run.Status == RunTimedOut) && run.Attempt < task.Limits.MaxAttempts {
				m.maybeRetry(run)
			}
		}
	}
	return nil
}

func (m *Manager) releaseActive(run Run) {
	m.mu.Lock()
	delete(m.activeByRun, run.ID)
	delete(m.cancelPending, run.ID)
	if run.SessionID != "" {
		delete(m.activeBySession, run.SessionID)
	}
	if m.activeAgents[run.AgentID] > 0 {
		m.activeAgents[run.AgentID]--
	}
	if m.activeAgents[run.AgentID] <= 0 {
		delete(m.activeAgents, run.AgentID)
	}
	m.mu.Unlock()
}

func (m *Manager) taskRunOccupancy(ctx context.Context, taskID string) (bool, int) {
	m.mu.Lock()
	runIDs := make([]string, 0, len(m.activeByRun))
	for runID := range m.activeByRun {
		runIDs = append(runIDs, runID)
	}
	m.mu.Unlock()
	active := false
	for _, runID := range runIDs {
		if run, err := m.store.GetRun(context.Background(), runID); err == nil && run.TaskID == taskID {
			active = true
			break
		}
	}
	runs, err := m.store.ListRuns(ctx, taskID)
	if err != nil {
		return active, 0
	}
	queued := 0
	for _, run := range runs {
		if run.Status == RunQueued {
			queued++
		}
	}
	return active, queued
}

func (m *Manager) agentSuspended(agentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed || m.deletingAgents[agentID] > 0
}

func (m *Manager) publish(event Event) {
	event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.events.Publish(context.Background(), TopicEvent, event); err != nil && !errors.Is(err, eventbus.ErrClosed) {
		m.logger.Warn(context.Background(), "发布 Task Event 失败", "error", err)
	}
}

func normalizeExecution(value ExecutionType) (ExecutionType, error) {
	if value == "" {
		return ExecutionAgent, nil
	}
	switch value {
	case ExecutionAgent, ExecutionNotification:
		return value, nil
	default:
		return "", fmt.Errorf("无效任务执行方式: %s", value)
	}
}

// normalizeConversationMode 把旧配置与前端空值统一成明确模式。仅通知任务不创建
// Session，所以即使请求里错误携带 continuous，也会规范成 isolated。
func normalizeConversationMode(value ConversationMode, execution ExecutionType) (ConversationMode, error) {
	if value != "" && value != ConversationIsolated && value != ConversationContinuous {
		return "", fmt.Errorf("无效任务会话方式: %s", value)
	}
	if execution != ExecutionAgent || value == "" {
		return ConversationIsolated, nil
	}
	return value, nil
}

func initialNextRun(schedule Schedule, now time.Time) (*time.Time, error) {
	if schedule.Type == ScheduleOnce {
		if schedule.RunAt == nil {
			return nil, errors.New("单次任务缺少运行时间")
		}
		value := schedule.RunAt.UTC()
		return &value, nil
	}
	return nextOccurrence(schedule, now)
}
