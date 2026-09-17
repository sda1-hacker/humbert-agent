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
	interrupted, err := m.store.ReconcileInterrupted(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("恢复 TaskRun 状态失败: %w", err)
	}
	for _, run := range interrupted {
		m.logger.Warn(ctx, "TaskRun 已在启动恢复时标记为 interrupted", "operation", "task.run.reconcile", "task_id", run.TaskID, "run_id", run.ID)
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
	return m.store.ListTasks(ctx, false)
}

func (m *Manager) Runs(ctx context.Context, taskID string) ([]Run, error) {
	return m.store.ListRuns(ctx, taskID)
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
	value := Task{ID: uuid.NewString(), AgentID: strings.TrimSpace(input.AgentID), Name: name, Prompt: prompt, Status: status, Schedule: schedule, Limits: limits, NextRunAt: next, CreatedAt: now, UpdatedAt: now}
	if err := m.store.CreateTask(ctx, value); err != nil {
		return Task{}, err
	}
	m.publish(Event{Type: "task.created", TaskID: value.ID, Task: &value})
	return value, nil
}

func (m *Manager) Update(ctx context.Context, id string, input UpdateInput) (Task, error) {
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
	existing.Name, existing.Prompt, existing.Status = name, prompt, status
	existing.Schedule, existing.Limits, existing.NextRunAt = schedule, limits, next
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

func (m *Manager) Archive(ctx context.Context, id string) (Task, error) {
	value, err := m.store.ArchiveTask(ctx, id, time.Now())
	if err != nil {
		return Task{}, err
	}
	m.publish(Event{Type: "task.archived", TaskID: value.ID, Task: &value})
	return value, nil
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
	run := Run{ID: uuid.NewString(), TaskID: task.ID, AgentID: task.AgentID, Trigger: TriggerManual, ScheduledFor: now, Attempt: 1, Status: RunQueued, CreatedAt: now}
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
		run := Run{ID: uuid.NewString(), TaskID: task.ID, AgentID: task.AgentID, Trigger: TriggerSchedule, ScheduledFor: scheduledFor, Attempt: 1, Status: status, Error: errText, CreatedAt: now, FinishedAt: finishedAt}
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

func (m *Manager) startRun(ctx context.Context, task Task, run Run) error {
	now := time.Now().UTC()
	session, err := m.sessions.Create(ctx, sessions.CreateSessionInput{AgentID: task.AgentID, Title: "任务 · " + task.Name})
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
	m.publish(Event{Type: "run." + string(run.Status), TaskID: run.TaskID, RunID: run.ID, Run: &run})
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
	m.publish(Event{Type: "run.failed", TaskID: run.TaskID, RunID: run.ID, Run: &run})
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
	retry := Run{ID: uuid.NewString(), TaskID: task.ID, AgentID: task.AgentID, Trigger: TriggerRetry, ParentRunID: run.ID, ScheduledFor: retryAt, Attempt: run.Attempt + 1, Status: RunQueued, CreatedAt: now}
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
