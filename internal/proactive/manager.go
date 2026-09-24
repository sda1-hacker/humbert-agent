package proactive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
)

const managerTickInterval = 10 * time.Second

type Manager struct {
	store         *Store
	tasks         *tasks.Manager
	events        *eventbus.Bus
	notifications *notifications.Service
	decision      DecisionEngine
	workspace     *WorkspaceMonitor
	logger        *logging.Logger

	executors map[Action]Executor

	rootCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	mu               sync.RWMutex
	running          bool
	heartbeatRunning bool
	nextHeartbeatAt  time.Time
	unsubscribers    []func()

	wake chan struct{}
}

func NewManager(
	store *Store,
	taskManager *tasks.Manager,
	events *eventbus.Bus,
	notificationService *notifications.Service,
	workspaceMonitor *WorkspaceMonitor,
	logger *logging.Logger,
) (*Manager, error) {
	if store == nil || taskManager == nil || events == nil || notificationService == nil || logger == nil {
		return nil, errors.New("主动助手 Manager 依赖不完整")
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		store:         store,
		tasks:         taskManager,
		events:        events,
		notifications: notificationService,
		decision:      RuleDecisionEngine{},
		workspace:     workspaceMonitor,
		logger:        logger,
		rootCtx:       ctx,
		cancel:        cancel,
		wake:          make(chan struct{}, 1),
		executors:     make(map[Action]Executor),
	}
	manager.registerExecutor(NewNotificationExecutor(notificationService))
	manager.registerExecutor(NewAgentExecutor(taskManager))
	return manager, nil
}

func (m *Manager) registerExecutor(executor Executor) {
	if executor != nil {
		m.executors[executor.Action()] = executor
	}
}

func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	// 先订阅后恢复，避免 Scheduler 在启动窗口内完成 Run 时丢失唯一的终态事件。
	unsubTask, err := m.events.Subscribe(tasks.TopicEvent, m.handleTaskPayload)
	if err != nil {
		return fmt.Errorf("订阅 Task Event 失败: %w", err)
	}
	unsubRuntime, err := m.events.Subscribe(agentruntime.TopicEvent, m.handleRuntimePayload)
	if err != nil {
		unsubTask()
		return fmt.Errorf("订阅 Runtime Event 失败: %w", err)
	}
	if err := m.reconcileRecords(ctx); err != nil {
		unsubRuntime()
		unsubTask()
		return fmt.Errorf("恢复主动助手处理记录失败: %w", err)
	}
	settings := m.store.Settings()
	m.mu.Lock()
	m.unsubscribers = []func(){unsubTask, unsubRuntime}
	m.running = true
	if settings.Enabled {
		m.nextHeartbeatAt = time.Now().UTC().Add(time.Duration(settings.HeartbeatIntervalMinutes) * time.Minute)
	} else {
		m.nextHeartbeatAt = time.Time{}
	}
	m.mu.Unlock()

	m.wg.Add(1)
	go m.loop()
	if m.store.PendingEventCount() > 0 {
		m.signalWake()
	}
	m.publishStatus()
	return nil
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = false
	unsubscribers := append([]func(){}, m.unsubscribers...)
	m.unsubscribers = nil
	m.mu.Unlock()
	for _, unsubscribe := range unsubscribers {
		if unsubscribe != nil {
			unsubscribe()
		}
	}
	m.cancel()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		m.publishStatus()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) Settings() Settings { return m.store.Settings() }

func (m *Manager) UpdateSettings(ctx context.Context, value Settings) (Settings, error) {
	updated, err := m.store.UpdateSettings(ctx, value)
	if err != nil {
		return Settings{}, err
	}
	m.mu.Lock()
	if updated.Enabled {
		m.nextHeartbeatAt = time.Now().UTC().Add(time.Duration(updated.HeartbeatIntervalMinutes) * time.Minute)
	} else {
		m.nextHeartbeatAt = time.Time{}
	}
	m.mu.Unlock()
	m.publishStatus()
	return updated, nil
}

func (m *Manager) Records(limit int) []Record { return m.store.Records(limit) }

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := Status{Running: m.running, QueuedEvents: m.store.PendingEventCount()}
	status.LastHeartbeatAt = m.store.LastHeartbeat()
	if m.running && !m.nextHeartbeatAt.IsZero() {
		next := m.nextHeartbeatAt
		status.NextHeartbeatAt = &next
	}
	return status
}

func (m *Manager) RunHeartbeat(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if !m.store.Settings().Enabled {
		return errors.New("主动助手已关闭")
	}
	if !m.beginHeartbeat() {
		return errors.New("主动助手巡检正在运行")
	}
	defer m.endHeartbeat()
	return m.heartbeat(ctx, time.Now().UTC())
}

func (m *Manager) loop() {
	defer m.wg.Done()
	ticker := time.NewTicker(managerTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case <-m.wake:
			m.drainPendingEvents()
		case <-ticker.C:
			now := time.Now().UTC()
			m.mu.RLock()
			due := !m.nextHeartbeatAt.IsZero() && !now.Before(m.nextHeartbeatAt)
			m.mu.RUnlock()
			if due {
				if !m.beginHeartbeat() {
					continue
				}
				if err := m.heartbeat(m.rootCtx, now); err != nil {
					m.logger.Warn(context.Background(), "主动助手心跳失败", "operation", "proactive.heartbeat", "error", err)
				}
				m.endHeartbeat()
			}
		}
	}
}

func (m *Manager) enqueue(event Event) error {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	added, err := m.store.EnqueueEvent(context.Background(), event)
	if err != nil {
		m.logger.Warn(context.Background(), "持久化主动助手事件失败", "operation", "proactive.enqueue", "event_kind", event.Kind, "event_key", event.Key, "error", err)
		return err
	}
	if added {
		m.signalWake()
		m.publishStatus()
	}
	return nil
}

func (m *Manager) signalWake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) drainPendingEvents() {
	for _, event := range m.store.PendingEvents() {
		if err := m.rootCtx.Err(); err != nil {
			return
		}
		m.processEvent(m.rootCtx, event)
		// processEvent 在执行任何动作前都会创建 Record；只有看到记录后才确认 Inbox，
		// 持久化失败时事件会留到下一次唤醒或重启继续处理。
		if _, handled := m.store.FindByEventKey(event.Key); !handled {
			continue
		}
		if err := m.store.RemovePendingEvent(context.Background(), event.Key); err != nil {
			m.logger.Warn(context.Background(), "确认主动助手事件失败", "operation", "proactive.inbox.ack", "event_key", event.Key, "error", err)
			return
		}
	}
	m.publishStatus()
}

func (m *Manager) beginHeartbeat() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running || m.heartbeatRunning {
		return false
	}
	m.heartbeatRunning = true
	return true
}

func (m *Manager) endHeartbeat() {
	m.mu.Lock()
	m.heartbeatRunning = false
	m.mu.Unlock()
}

func (m *Manager) handleTaskPayload(_ context.Context, payload any) {
	event, ok := payload.(tasks.Event)
	if !ok || event.Run == nil {
		return
	}
	run := *event.Run
	if run.Trigger == tasks.TriggerAutomation {
		if err := m.finalizeAutomationRun(run); err != nil {
			m.logger.Warn(context.Background(), "保存主动 Agent 运行结果失败", "operation", "proactive.automation.finalize", "run_id", run.ID, "error", err)
		}
		return
	}
	converted, ok := proactiveEventFromTask(event)
	if ok {
		m.enqueue(converted)
	}
}

func (m *Manager) handleRuntimePayload(_ context.Context, payload any) {
	event, ok := payload.(agentruntime.Event)
	if !ok || event.Type != agentruntime.EventApprovalRequested || event.Approval == nil {
		return
	}
	if _, isTaskRun := m.tasks.ActiveRunForSession(context.Background(), event.SessionID); isTaskRun {
		return
	}
	approvalID := strings.TrimSpace(event.Approval.ID)
	if approvalID == "" {
		return
	}
	m.enqueue(Event{
		Key:        "approval:" + approvalID,
		Kind:       EventApprovalPending,
		Title:      "操作等待确认",
		Summary:    fmt.Sprintf("%s 正在等待你的操作确认。", event.Approval.ToolName),
		Level:      "warning",
		AgentID:    event.AgentID,
		SessionID:  event.SessionID,
		ApprovalID: approvalID,
		OccurredAt: time.Now().UTC(),
	})
}

func proactiveEventFromTask(value tasks.Event) (Event, bool) {
	run := value.Run
	if run == nil {
		return Event{}, false
	}
	taskName := "后台任务"
	if value.Task != nil && strings.TrimSpace(value.Task.Name) != "" {
		taskName = "“" + strings.TrimSpace(value.Task.Name) + "”"
	}
	base := Event{
		AgentID:    run.AgentID,
		SessionID:  run.SessionID,
		TaskID:     run.TaskID,
		RunID:      run.ID,
		OccurredAt: time.Now().UTC(),
	}
	switch value.Type {
	case "run.waiting_approval":
		approvalID := ""
		toolName := "任务中的操作"
		if run.Approval != nil {
			approvalID = run.Approval.ID
			if strings.TrimSpace(run.Approval.ToolName) != "" {
				toolName = run.Approval.ToolName
			}
		}
		if approvalID != "" {
			base.Key = "approval:" + approvalID
		} else {
			base.Key = "task-approval:" + run.ID
		}
		base.Kind, base.Title, base.Level = EventTaskWaitingApproval, taskName+"等待确认", "warning"
		base.Summary = fmt.Sprintf("%s 正在等待你的确认。", toolName)
	case "run.succeeded":
		if run.Execution == tasks.ExecutionNotification {
			return Event{}, false
		}
		base.Key = "task-succeeded:" + run.ID
		base.Kind, base.Title, base.Level = EventTaskSucceeded, taskName+"已完成", "success"
		if run.StartedAt != nil && run.FinishedAt != nil && run.FinishedAt.Sub(*run.StartedAt) >= 5*time.Minute {
			base.Kind = EventTaskLongRunningDone
			base.Title = taskName + "长时间运行后已完成"
		}
		base.Summary = strings.TrimSpace(run.ResultPreview)
		if base.Summary == "" {
			base.Summary = "后台任务已经成功完成。"
		}
	case "run.failed":
		base.Key = "task-failed:" + run.ID
		base.Kind, base.Title, base.Level = EventTaskFailed, taskName+"运行失败", "error"
		base.Summary = strings.TrimSpace(run.Error)
	case "run.timed_out":
		base.Key = "task-timeout:" + run.ID
		base.Kind, base.Title, base.Level = EventTaskTimedOut, taskName+"运行超时", "error"
		base.Summary = strings.TrimSpace(run.Error)
	case "run.interrupted":
		base.Key = "task-interrupted:" + run.ID
		base.Kind, base.Title, base.Level = EventTaskInterrupted, taskName+"已中断", "warning"
		base.Summary = strings.TrimSpace(run.Error)
	default:
		return Event{}, false
	}
	if base.Summary == "" {
		base.Summary = base.Title
	}
	return base, true
}

func (m *Manager) processEvent(ctx context.Context, event Event) {
	if strings.TrimSpace(event.Key) == "" {
		return
	}
	if _, exists := m.store.FindByEventKey(event.Key); exists {
		return
	}
	settings := m.store.Settings()
	var latest *Record
	if value, exists := m.store.LastHandledKind(event.Kind); exists {
		latest = &value
	}
	decision := m.decision.Decide(event, settings, time.Now().UTC(), latest)
	now := time.Now().UTC()
	record := Record{
		ID: uuid.NewString(), Event: event, Decision: decision,
		Status: RecordIgnored, CreatedAt: now, UpdatedAt: now,
	}
	if decision.Action == ActionIgnore {
		handled := now
		record.HandledAt = &handled
		if err := m.store.PutRecord(context.WithoutCancel(ctx), record); err != nil {
			m.logger.Warn(context.Background(), "保存主动事件记录失败", "operation", "proactive.process.persist", "error", err)
			return
		}
		m.publishRecord(record)
		return
	}
	if InQuietHours(settings, now) {
		record.Status = RecordDeferred
		if err := m.store.PutRecord(context.WithoutCancel(ctx), record); err != nil {
			m.logger.Warn(context.Background(), "保存主动事件记录失败", "operation", "proactive.process.persist", "error", err)
			return
		}
		m.publishRecord(record)
		return
	}
	m.executeRecord(ctx, &record)
}

func (m *Manager) executeRecord(ctx context.Context, record *Record) {
	if record == nil {
		return
	}
	executor := m.executors[record.Decision.Action]
	if executor == nil {
		now := time.Now().UTC()
		record.Status, record.Error, record.UpdatedAt, record.HandledAt = RecordFailed, "没有对应的主动动作执行器", now, &now
		if err := m.store.PutRecord(context.WithoutCancel(ctx), *record); err != nil {
			m.logger.Warn(context.Background(), "保存主动动作结果失败", "operation", "proactive.execute.result", "error", err)
			return
		}
		m.publishRecord(*record)
		return
	}
	now := time.Now().UTC()
	record.Status, record.UpdatedAt = RecordExecuting, now
	if err := m.store.PutRecord(context.WithoutCancel(ctx), *record); err != nil {
		m.logger.Warn(context.Background(), "持久化主动动作前置状态失败", "operation", "proactive.execute.persist", "error", err)
		return
	}
	result, err := executor.Execute(ctx, record.Event, record.Decision)
	now = time.Now().UTC()
	record.UpdatedAt = now
	record.AutomationTaskID = result.AutomationTaskID
	record.AutomationRunID = result.AutomationRunID
	if err != nil {
		record.Status, record.Error, record.HandledAt = RecordFailed, err.Error(), &now
	} else if record.Decision.Action == ActionRunAgent {
		record.Status = RecordExecuting
	} else {
		record.Status, record.HandledAt = RecordSucceeded, &now
	}
	if err := m.store.PutRecord(context.WithoutCancel(ctx), *record); err != nil {
		m.logger.Warn(context.Background(), "保存主动动作结果失败", "operation", "proactive.execute.result", "error", err)
		return
	}
	m.publishRecord(*record)
	if record.Decision.Action == ActionRunAgent && record.AutomationRunID != "" {
		if run, runErr := m.tasks.Run(context.WithoutCancel(ctx), record.AutomationRunID); runErr == nil && run.Status.Terminal() {
			if err := m.finalizeAutomationRun(run); err != nil {
				m.logger.Warn(context.Background(), "保存主动 Agent 运行结果失败", "operation", "proactive.automation.finalize", "run_id", run.ID, "error", err)
			}
		}
	}
}

func (m *Manager) heartbeat(ctx context.Context, now time.Time) error {
	settings := m.store.Settings()
	if !settings.Enabled {
		return nil
	}
	defer func() {
		m.mu.Lock()
		m.nextHeartbeatAt = time.Now().UTC().Add(time.Duration(m.store.Settings().HeartbeatIntervalMinutes) * time.Minute)
		m.mu.Unlock()
		m.publishStatus()
	}()
	if err := m.store.SetHeartbeat(context.WithoutCancel(ctx), now); err != nil {
		return err
	}
	if !InQuietHours(settings, now) {
		for _, record := range m.store.DeferredRecords() {
			current := record
			m.executeRecord(ctx, &current)
		}
	}
	if rule, ok := settings.Rules[EventWorkspaceChanged]; !ok || !rule.Enabled || rule.Action == ActionIgnore || m.workspace == nil {
		return nil
	}
	snapshots, err := m.workspace.Scan(ctx)
	if err != nil {
		return err
	}
	for _, current := range snapshots {
		previous, exists := m.store.WorkspaceSnapshot(current.AgentID)
		if !exists || previous.RootDir != current.RootDir {
			if err := m.store.PutWorkspaceSnapshot(context.WithoutCancel(ctx), current); err != nil {
				return err
			}
			continue
		}
		if previous.Fingerprint != current.Fingerprint {
			if err := m.enqueue(Event{
				Key:        "workspace:" + current.AgentID + ":" + current.Fingerprint,
				Kind:       EventWorkspaceChanged,
				Title:      "工作区发生变化",
				Summary:    workspaceChangeSummary(previous, current),
				Level:      "info",
				AgentID:    current.AgentID,
				OccurredAt: now,
				Metadata:   map[string]string{"workspace_root": current.RootDir},
			}); err != nil {
				return err
			}
		}
		if err := m.store.PutWorkspaceSnapshot(context.WithoutCancel(ctx), current); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) finalizeAutomationRun(run tasks.Run) error {
	record, exists := m.store.FindByAutomationRunID(run.ID)
	if !exists || !run.Status.Terminal() {
		return nil
	}
	now := time.Now().UTC()
	record.UpdatedAt, record.HandledAt = now, &now
	switch run.Status {
	case tasks.RunSucceeded:
		record.Status = RecordSucceeded
	default:
		record.Status = RecordFailed
		record.Error = strings.TrimSpace(run.Error)
		if record.Error == "" {
			record.Error = "主动 Agent 运行未成功完成"
		}
	}
	if err := m.store.PutRecord(context.Background(), record); err != nil {
		return err
	}
	if _, err := m.tasks.Archive(context.Background(), run.TaskID); err != nil {
		m.logger.Warn(context.Background(), "归档主动助手内部任务失败", "operation", "proactive.automation.archive", "task_id", run.TaskID, "run_id", run.ID, "error", err)
	}
	m.publishRecord(record)
	return nil
}

func (m *Manager) reconcileRecords(ctx context.Context) error {
	for _, value := range m.store.Records(maxStoredRecords) {
		record := value
		if record.Status != RecordExecuting {
			continue
		}
		if record.Decision.Action == ActionRunAgent && record.AutomationRunID == "" {
			task, run, found, findErr := m.tasks.AutomationByOrigin(ctx, "proactive", record.Event.Key)
			if findErr != nil {
				return findErr
			}
			if found && run.ID != "" {
				record.AutomationTaskID = task.ID
				record.AutomationRunID = run.ID
				record.UpdatedAt = time.Now().UTC()
				if err := m.store.PutRecord(ctx, record); err != nil {
					return err
				}
				if run.Status.Terminal() {
					if err := m.finalizeAutomationRun(run); err != nil {
						return err
					}
				}
				continue
			}
		}
		if record.Decision.Action != ActionRunAgent || record.AutomationRunID == "" {
			now := time.Now().UTC()
			record.Status = RecordFailed
			record.Error = "应用在主动动作完成前退出；为避免重复执行，该动作不会自动重放。"
			record.UpdatedAt, record.HandledAt = now, &now
			if err := m.store.PutRecord(ctx, record); err != nil {
				return err
			}
			continue
		}
		run, err := m.tasks.Run(ctx, record.AutomationRunID)
		if err != nil {
			now := time.Now().UTC()
			record.Status = RecordFailed
			record.Error = "主动 Agent 运行记录不存在；为避免重复执行，该动作不会自动重放。"
			record.UpdatedAt, record.HandledAt = now, &now
			if putErr := m.store.PutRecord(ctx, record); putErr != nil {
				return putErr
			}
			continue
		}
		if run.Status.Terminal() {
			if err := m.finalizeAutomationRun(run); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) publishRecord(record Record) {
	value := record
	m.publish(PublicEvent{Type: "record.updated", Record: &value})
}

func (m *Manager) publishStatus() {
	value := m.Status()
	m.publish(PublicEvent{Type: "status.updated", Status: &value})
}

func (m *Manager) publish(event PublicEvent) {
	event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.events.Publish(context.Background(), TopicEvent, event); err != nil && !errors.Is(err, eventbus.ErrClosed) {
		m.logger.Warn(context.Background(), "发布主动助手事件失败", "operation", "proactive.event.publish", "error", err)
	}
}
