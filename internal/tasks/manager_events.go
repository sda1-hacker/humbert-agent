package tasks

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
)

func (m *Manager) handleRuntimePayload(_ context.Context, payload any) {
	event, ok := payload.(agentruntime.Event)
	if !ok || strings.TrimSpace(event.SessionID) == "" {
		return
	}
	// 文本 delta 不改变 TaskRun，避免每个字符都读磁盘状态。
	switch event.Type {
	case agentruntime.EventAssistantDelta, agentruntime.EventAssistantReasoningDelta:
		return
	}
	m.mu.Lock()
	runID := m.activeBySession[event.SessionID]
	m.mu.Unlock()
	if runID == "" {
		return
	}
	started := time.Now()
	defer func() {
		if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
			m.logger.Warn(context.Background(), "任务事件持久化较慢", "operation", "tasks.event.slow_persist", "event_type", string(event.Type), "run_id", runID, "duration_ms", elapsed.Milliseconds())
		}
	}()
	preview := ""
	if event.Type == agentruntime.EventTurnCompleted {
		preview = m.resultPreview(event.SessionID, event.MessageID)
	}
	now := time.Now().UTC()
	changed := false
	run, err := m.store.MutateRun(context.Background(), runID, func(run *Run) error {
		if run.Status.Terminal() {
			return nil
		}
		run.ToolCalls = max(run.ToolCalls, event.ToolCalls)
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
			run.ResultPreview = preview
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
			return nil
		}
		if (run.Status == RunFailed || run.Status == RunTimedOut) && run.ToolCalls > 0 {
			run.Error += " 已调用工具，自动重试已停止；请先检查已完成的操作再手动继续。"
		}
		changed = true
		return nil
	})
	if err != nil {
		m.logger.Error(context.Background(), "更新 TaskRun 事件状态失败", "run_id", runID, "error", err)
		return
	}
	if !changed {
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
		if runes := []rune(value); len(runes) > 500 {
			value = string(runes[:500]) + "…"
		}
		return value
	}
	return ""
}

func (m *Manager) failRun(ctx context.Context, run Run, err error) {
	now := time.Now().UTC()
	changed := false
	current, updateErr := m.store.MutateRun(context.WithoutCancel(ctx), run.ID, func(current *Run) error {
		if current.Status.Terminal() {
			return nil
		}
		current.Status, current.Error, current.FinishedAt, current.Approval = RunFailed, logging.SafeErrorText(err, 2048), &now, nil
		changed = true
		return nil
	})
	if updateErr != nil {
		m.logger.Error(context.WithoutCancel(ctx), "保存任务失败状态失败", "run_id", run.ID, "error", updateErr)
		return
	}
	if !changed {
		return
	}
	run = current
	m.releaseActive(run)
	var taskSnapshot *Task
	if task, taskErr := m.store.GetTask(context.Background(), run.TaskID); taskErr == nil {
		taskSnapshot = &task
	}
	m.publish(Event{Type: "run.failed", TaskID: run.TaskID, RunID: run.ID, Task: taskSnapshot, Run: &run})
	m.maybeRetry(run)
}

func (m *Manager) maybeRetry(run Run) {
	if !safeToRetry(run) {
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

func safeToRetry(run Run) bool {
	// ToolStarted 包含等待审批/拒绝，保守地阻止整轮重放；工具结果未知不能当作未执行。
	return (run.Status == RunFailed || run.Status == RunTimedOut) && run.ToolCalls == 0
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
			m.maybeRetry(run)
		}
	}
	return nil
}

func (m *Manager) releaseActive(run Run) {
	m.mu.Lock()
	if _, active := m.activeByRun[run.ID]; !active {
		m.mu.Unlock()
		return
	}
	delete(m.activeByRun, run.ID)
	delete(m.cancelPending, run.ID)
	if m.activeBySession[run.SessionID] == run.ID {
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
