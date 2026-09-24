package tasks

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
)

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
	case agentruntime.EventModelUsage:
		run.InputTokens += event.InputTokens
		run.OutputTokens += event.OutputTokens
		run.TotalTokens += event.TotalTokens
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
		if taskSnapshot != nil {
			m.notifyChatTaskResult(*taskSnapshot, run)
		}
		m.releaseActive(run)
		m.maybeRetry(run)
		go m.runCycle()
	}
	_ = ctx
}

func (m *Manager) notifyChatTaskResult(task Task, run Run) {
	if task.Origin != "chat" || task.OriginRef == "" || task.EffectiveExecution() != ExecutionAgent {
		return
	}
	if run.Status != RunSucceeded && run.Status != RunFailed && run.Status != RunTimedOut && run.Status != RunInterrupted {
		return
	}
	m.mu.Lock()
	notifier := m.notifications
	m.mu.Unlock()
	if notifier == nil {
		return
	}
	level, title, body := notifications.LevelSuccess, "任务已完成："+task.Name, strings.TrimSpace(run.ResultPreview)
	if run.Status != RunSucceeded {
		level, title, body = notifications.LevelError, "任务未完成："+task.Name, strings.TrimSpace(run.Error)
	}
	if body == "" {
		body = "请打开主动任务查看运行记录。"
	}
	if len([]rune(body)) > 300 {
		body = string([]rune(body)[:300]) + "…"
	}
	if err := notifier.Send(context.Background(), notifications.Notification{
		Level: level, Title: title, Body: body,
		AgentID: task.AgentID, SessionID: task.OriginRef, TaskID: task.ID, RunID: run.ID,
	}); err != nil {
		m.logger.Warn(context.Background(), "发送对话任务结果通知失败", "task_id", task.ID, "run_id", run.ID, "error", err)
	}
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
	if taskSnapshot != nil {
		m.notifyChatTaskResult(*taskSnapshot, run)
	}
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
