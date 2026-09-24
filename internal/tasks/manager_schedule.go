package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
)

func (m *Manager) RunNow(ctx context.Context, taskID string) (Run, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()

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
	if err := m.dispatchLocked(ctx); err != nil {
		m.logger.Warn(ctx, "立即调度 TaskRun 失败", "operation", "task.dispatch", "run_id", created.ID, "error", err)
	}
	return m.store.GetRun(ctx, created.ID)
}

// RunAutomation 让 Humbert 内部子系统复用现有 Task Runtime 执行一次 Agent 工作。
// 内部任务会完整持久化用于审计，但不会出现在普通 Task 列表；应用重启后未完成
// 的 automation 不会自动重放。
func (m *Manager) RunAutomation(ctx context.Context, input AutomationInput) (Task, Run, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()

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
	origin := strings.TrimSpace(input.Origin)
	originRef := strings.TrimSpace(input.OriginRef)
	if origin == "" || originRef == "" {
		return Task{}, Run{}, errors.New("内部自动运行必须提供稳定的 Origin 与 OriginRef")
	}
	if existing, runs, found, findErr := m.store.FindInternalTaskByOrigin(ctx, origin, originRef); findErr != nil {
		return Task{}, Run{}, findErr
	} else if found && len(runs) > 0 {
		// 同一来源动作已经创建过 Run。无论它仍在执行还是已经终态，都返回原记录，
		// 绝不因为调用方重试而再次执行可能有副作用的工具。
		return existing, runs[0], nil
	} else if found {
		// Task 已经原子落盘但进程在创建 Run 前退出；此时尚未产生模型/工具副作用，
		// 可以安全地为同一个幂等 Task 补建唯一 Run。
		created, createErr := m.createAutomationRun(ctx, existing)
		return existing, created, createErr
	}
	now := time.Now().UTC()
	task := Task{
		ID: uuid.NewString(), AgentID: agentID, Internal: true,
		Origin: origin, OriginRef: originRef,
		Name: name, Prompt: prompt, Execution: ExecutionAgent, Status: status, Schedule: schedule, Limits: limits,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := m.store.CreateTask(ctx, task); err != nil {
		return Task{}, Run{}, err
	}
	created, err := m.createAutomationRun(ctx, task)
	if err != nil {
		_, _ = m.store.ArchiveTask(context.WithoutCancel(ctx), task.ID, time.Now().UTC())
		return Task{}, Run{}, err
	}
	return task, created, nil
}

func (m *Manager) createAutomationRun(ctx context.Context, task Task) (Run, error) {
	now := time.Now().UTC()
	run := Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: task.AgentID, Trigger: TriggerAutomation, Execution: ExecutionAgent,
		ScheduledFor: now, Attempt: 1, Status: RunQueued, CreatedAt: now,
	}
	created, _, err := m.store.CreateRun(ctx, run)
	if err != nil {
		return Run{}, err
	}
	m.publish(Event{Type: "run.queued", TaskID: task.ID, RunID: created.ID, Task: &task, Run: &created})
	// RunAutomation 持有 cycleMu，必须调用不重复加锁的分派实现。
	if err := m.dispatchLocked(ctx); err != nil {
		m.logger.Warn(ctx, "立即调度内部自动运行失败", "operation", "task.automation.dispatch", "run_id", created.ID, "error", err)
	}
	current, err := m.store.GetRun(ctx, created.ID)
	if err != nil {
		return created, nil
	}
	return current, nil
}

// AutomationByOrigin 暴露只读幂等关联，供主动助手在应用重启后修复尚未来得及回写的
// TaskID/RunID。
func (m *Manager) AutomationByOrigin(ctx context.Context, origin, originRef string) (Task, Run, bool, error) {
	task, runs, found, err := m.store.FindInternalTaskByOrigin(ctx, origin, originRef)
	if err != nil || !found || len(runs) == 0 {
		return task, Run{}, found, err
	}
	return task, runs[0], true, nil
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

// schedulerLoop 在应用进程运行期间扫描到期任务，关闭时随 rootCtx 停止。
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
			MaxDuration:    time.Duration(task.Limits.MaxDurationSeconds) * time.Second,
			Deadline:       deadline,
			MaxModelCalls:  task.Limits.MaxModelCalls,
			MaxToolCalls:   task.Limits.MaxToolCalls,
			MaxTotalTokens: task.Limits.MaxTotalTokens,
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
		return fmt.Sprintf("主动·%d月%d日%s", localTime.Month(), localTime.Day(), task.Name)
	}
	return fmt.Sprintf("任务·%d月%d日%s", localTime.Month(), localTime.Day(), task.Name)
}
