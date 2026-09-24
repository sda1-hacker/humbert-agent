package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
)

func (m *Manager) Create(ctx context.Context, input CreateInput) (Task, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()

	if _, err := m.agents.Get(ctx, strings.TrimSpace(input.AgentID)); err != nil {
		return Task{}, err
	}
	if m.agentSuspended(input.AgentID) {
		return Task{}, errors.New("Agent 正在删除，不能创建任务")
	}
	now := time.Now().UTC()
	value, err := normalizeTaskFields(UpdateInput{
		Name: input.Name, Prompt: input.Prompt, Execution: input.Execution,
		ConversationMode: input.ConversationMode, Status: input.Status,
		Schedule: input.Schedule, Limits: input.Limits,
	}, now)
	if err != nil {
		return Task{}, err
	}
	value.ID, value.AgentID = uuid.NewString(), strings.TrimSpace(input.AgentID)
	value.Origin, value.OriginRef = input.Origin, input.OriginRef
	value.CreatedAt, value.UpdatedAt = now, now
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
	now := time.Now().UTC()
	fields, err := normalizeTaskFields(input, now)
	if err != nil {
		return Task{}, err
	}
	previousConversationMode := existing.EffectiveConversationMode()
	existing.Name, existing.Prompt, existing.Execution, existing.Status = fields.Name, fields.Prompt, fields.Execution, fields.Status
	existing.ConversationMode = fields.ConversationMode
	existing.Schedule, existing.Limits, existing.NextRunAt = fields.Schedule, fields.Limits, fields.NextRunAt

	// 会话方式从连续切到独立（或切换成仅通知）时，仅解除 Task 对持续 Session 的引用。
	// 旧 Session 本身属于用户历史，不在保存任务配置时自动删除；用户仍可从会话列表查看。
	// 从独立切到连续也从空引用开始，避免随意挑选某个旧的独立运行会话作为持续会话。
	if fields.Execution != ExecutionAgent || fields.ConversationMode != ConversationContinuous || previousConversationMode != ConversationContinuous {
		existing.PersistentSessionID = ""
	}
	existing.UpdatedAt = now
	if err := m.store.UpdateTask(ctx, existing); err != nil {
		return Task{}, err
	}
	if err := m.cancelQueuedRunsWhenPaused(ctx, existing, now); err != nil {
		return Task{}, err
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

	if err := m.cancelQueuedRunsWhenPaused(ctx, value, now); err != nil {
		return Task{}, err
	}

	m.publish(Event{Type: "task.updated", TaskID: value.ID, Task: &value})
	return value, nil
}

func (m *Manager) Archive(ctx context.Context, id string) (Task, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()

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

// AutomationBySession 在内部 TaskRun 已经落盘 SessionID、但上层领域记录尚未来得及回写
// 关联 ID 的短窗口内提供可靠反查。它只返回指定 origin 的内部自动运行。
func (m *Manager) AutomationBySession(ctx context.Context, sessionID, origin string) (Task, Run, bool, error) {
	run, found, err := m.store.RunBySession(ctx, sessionID)
	if err != nil || !found {
		return Task{}, Run{}, false, err
	}
	task, err := m.store.GetTask(ctx, run.TaskID)
	if err != nil {
		return Task{}, Run{}, false, err
	}
	if !task.Internal || task.Origin != strings.TrimSpace(origin) {
		return Task{}, Run{}, false, nil
	}
	return task, run, true, nil
}

// DeleteConversation 删除一段 Session，以及所有明确引用该 Session 的 TaskRun。
//
// Session 与 TaskRun 虽然分别持久化，但用户从会话侧栏执行的是一个领域动作：删除任务
// 对话时，对应的运行历史也必须一起消失。连续任务可能有多条 Run 共享同一 Session，
// 因此这里在同一个调度临界区内一次性解析全部引用，并解除 PersistentSessionID。
func (m *Manager) DeleteConversation(ctx context.Context, sessionID string) ([]Run, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("Session ID 不能为空")
	}

	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()

	referencingTasks, runs, err := m.store.ReferencesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if !run.Status.Terminal() {
			return nil, ErrTaskBusy
		}
	}

	// 先删除 Session。Runtime 会拒绝删除正在执行/压缩的会话；只有该安全边界成功后，
	// 才移除运行索引，避免删除请求被拒绝时先丢失用户可见的运行历史。
	if err := m.runtime.DeleteSession(ctx, sessionID); err != nil {
		return nil, err
	}

	deleted := make([]Run, 0, len(runs))
	for _, run := range runs {
		value, deleteErr := m.store.DeleteRun(context.WithoutCancel(ctx), run.ID)
		if deleteErr != nil {
			m.logger.Error(context.Background(), "Session 已删除，但对应 TaskRun 清理失败", "operation", "task.session.run.delete", "session_id", sessionID, "run_id", run.ID, "error", deleteErr)
			return deleted, deleteErr
		}
		deleted = append(deleted, value)
		m.publish(Event{Type: "run.deleted", TaskID: value.TaskID, RunID: value.ID})
	}

	for _, task := range referencingTasks {
		if strings.TrimSpace(task.PersistentSessionID) != sessionID {
			continue
		}
		task.PersistentSessionID = ""
		task.UpdatedAt = time.Now().UTC()
		if updateErr := m.store.UpdateTask(context.WithoutCancel(ctx), task); updateErr != nil {
			m.logger.Error(context.Background(), "Session 已删除，但连续任务引用清理失败", "operation", "task.session.reference.clear", "session_id", sessionID, "task_id", task.ID, "error", updateErr)
			return deleted, updateErr
		}
		m.publish(Event{Type: "task.updated", TaskID: task.ID, Task: &task})
	}
	return deleted, nil
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

// normalizeTaskFields 统一创建和更新的配置校验顺序；返回值只包含可编辑字段。
func normalizeTaskFields(input UpdateInput, now time.Time) (Task, error) {
	name, prompt, status, schedule, limits, err := normalizeTaskInput(input.Name, input.Prompt, input.Status, input.Schedule, input.Limits)
	if err != nil {
		return Task{}, err
	}
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
	mode, err := normalizeConversationMode(input.ConversationMode, execution)
	if err != nil {
		return Task{}, err
	}
	return Task{
		Name: name, Prompt: prompt, Status: status, Schedule: schedule,
		Limits: limits, NextRunAt: next, Execution: execution, ConversationMode: mode,
	}, nil
}

// 暂停任务时同步取消尚未执行的自动运行，并保持原有事件通知语义。
func (m *Manager) cancelQueuedRunsWhenPaused(ctx context.Context, task Task, now time.Time) error {
	if task.Status != TaskStatusPaused {
		return nil
	}
	cancelled, err := m.store.CancelQueuedAutomaticRuns(ctx, task.ID, now)
	if err != nil {
		return err
	}
	for _, run := range cancelled {
		m.publish(Event{Type: "run.cancelled", TaskID: run.TaskID, RunID: run.ID, Run: &run})
	}
	return nil
}

// normalizeExecution 将旧任务的空执行方式解释为 Agent 运行。
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
