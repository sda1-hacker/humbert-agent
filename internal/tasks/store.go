package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const (
	taskSchemaVersion = 1
	taskConfigName    = "config.json"
	taskRunsDirName   = "runs"
)

var (
	ErrTaskNotFound = errors.New("任务不存在")
	ErrRunNotFound  = errors.New("任务运行不存在")
	ErrTaskBusy     = errors.New("任务仍有活动运行")
)

type taskDocument struct {
	SchemaVersion int  `json:"schema_version"`
	Task          Task `json:"task"`
}

type runDocument struct {
	SchemaVersion int `json:"schema_version"`
	Run           Run `json:"run"`
}

// Store 使用每 Task 一个 config.json、每 Run 一个 JSON 文档。Task 编辑不会重写历史 Run，
// 不同 Agent 的主动任务也不会竞争同一全局配置文件。
type Store struct {
	agentsRoot string

	mu         sync.RWMutex
	taskAgents map[string]string
	runTasks   map[string]runRef
	issues     map[string]Issue
}

type runRef struct {
	AgentID string
	TaskID  string
}

func NewStore(ctx context.Context, agentsRoot string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(strings.TrimSpace(agentsRoot))
	if err != nil || strings.TrimSpace(agentsRoot) == "" {
		return nil, errors.New("Task Store agents root 无效")
	}
	store := &Store{
		agentsRoot: filepath.Clean(absolute),
		taskAgents: make(map[string]string),
		runTasks:   make(map[string]runRef),
		issues:     make(map[string]Issue),
	}
	if _, err := store.ListTasks(ctx, true); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Issues() []Issue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Issue, 0, len(s.issues))
	for _, issue := range s.issues {
		result = append(result, issue)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AgentID != result[j].AgentID {
			return result[i].AgentID < result[j].AgentID
		}
		if result[i].TaskID != result[j].TaskID {
			return result[i].TaskID < result[j].TaskID
		}
		return result[i].RunID < result[j].RunID
	})
	return result
}

func (s *Store) ListTasks(ctx context.Context, includeArchived bool) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listTasksLocked(ctx, includeArchived)
}

func (s *Store) listTasksLocked(ctx context.Context, includeArchived bool) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	agents, err := os.ReadDir(s.agentsRoot)
	if err != nil {
		return nil, fmt.Errorf("读取 Agent 目录失败: %w", err)
	}
	result := make([]Task, 0)
	for _, agentEntry := range agents {
		if !agentEntry.IsDir() || uuid.Validate(agentEntry.Name()) != nil {
			continue
		}
		agentID := agentEntry.Name()
		tasksDir := filepath.Join(s.agentsRoot, agentID, "tasks")
		entries, readErr := os.ReadDir(tasksDir)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("读取 Agent %s Task 目录失败: %w", agentID, readErr)
		}
		for _, entry := range entries {
			if !entry.IsDir() || uuid.Validate(entry.Name()) != nil {
				continue
			}
			taskID := entry.Name()
			value, readErr := s.readTaskLocked(ctx, agentID, taskID)
			if readErr != nil {
				s.issues["task:"+taskID] = Issue{AgentID: agentID, TaskID: taskID, Error: readErr.Error()}
				continue
			}
			delete(s.issues, "task:"+taskID)
			s.taskAgents[taskID] = agentID
			if includeArchived || value.Status != TaskStatusArchived {
				result = append(result, value)
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (s *Store) GetTask(ctx context.Context, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getTaskLocked(ctx, id)
}

// FindInternalTaskByOrigin 按稳定来源键查找内部任务。Origin + OriginRef 是主动动作的
// 幂等键，用来关闭“领域记录已落盘、内部 Task 已创建、关联 ID 尚未回写”这一崩溃窗口。
func (s *Store) FindInternalTaskByOrigin(ctx context.Context, origin, originRef string) (Task, []Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	origin = strings.TrimSpace(origin)
	originRef = strings.TrimSpace(originRef)
	if origin == "" || originRef == "" {
		return Task{}, nil, false, nil
	}
	values, err := s.listTasksLocked(ctx, true)
	if err != nil {
		return Task{}, nil, false, err
	}
	for _, task := range values {
		if !task.Internal || task.Origin != origin || task.OriginRef != originRef {
			continue
		}
		runs, readErr := s.listRunsLocked(ctx, task.AgentID, task.ID)
		if readErr != nil {
			return Task{}, nil, false, readErr
		}
		return task, runs, true, nil
	}
	return Task{}, nil, false, nil
}

func (s *Store) getTaskLocked(ctx context.Context, id string) (Task, error) {
	id = strings.TrimSpace(id)
	if uuid.Validate(id) != nil {
		return Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	agentID := s.taskAgents[id]
	if agentID == "" {
		if _, err := s.listTasksLocked(ctx, true); err != nil {
			return Task{}, err
		}
		agentID = s.taskAgents[id]
	}
	if agentID == "" {
		return Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return s.readTaskLocked(ctx, agentID, id)
}

func (s *Store) CreateTask(ctx context.Context, value Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if uuid.Validate(value.ID) != nil || uuid.Validate(value.AgentID) != nil {
		return errors.New("Task ID 或 Agent ID 无效")
	}
	if err := validateStoredTask(value); err != nil {
		return fmt.Errorf("Task 内容无效: %w", err)
	}
	path := s.taskConfigPath(value.AgentID, value.ID)
	if _, err := os.Lstat(path); err == nil {
		return errors.New("Task ID 已存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建 Task 目录失败: %w", err)
	}
	if err := os.MkdirAll(s.runsDir(value.AgentID, value.ID), 0o700); err != nil {
		return fmt.Errorf("创建 Task Runs 目录失败: %w", err)
	}
	if err := s.writeTaskLocked(ctx, value); err != nil {
		return err
	}
	s.taskAgents[value.ID] = value.AgentID
	return nil
}

func (s *Store) UpdateTask(ctx context.Context, value Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateStoredTask(value); err != nil {
		return fmt.Errorf("Task 内容无效: %w", err)
	}
	if _, err := s.getTaskLocked(ctx, value.ID); err != nil {
		return err
	}
	return s.writeTaskLocked(ctx, value)
}

func (s *Store) ArchiveTask(ctx context.Context, id string, now time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.getTaskLocked(ctx, id)
	if err != nil {
		return Task{}, err
	}
	runs, err := s.listRunsLocked(ctx, value.AgentID, value.ID)
	if err != nil {
		return Task{}, err
	}
	for _, run := range runs {
		if run.Status.Active() {
			return Task{}, ErrTaskBusy
		}
	}
	now = now.UTC()
	for _, run := range runs {
		if run.Status != RunQueued {
			continue
		}
		run.Status = RunCancelled
		run.Error = "任务已归档，尚未开始的运行已取消。"
		run.FinishedAt = &now
		if err := s.writeRunLocked(ctx, run); err != nil {
			return Task{}, err
		}
	}
	value.Status = TaskStatusArchived
	value.NextRunAt = nil
	value.UpdatedAt = now
	value.ArchivedAt = &now
	if err := s.writeTaskLocked(ctx, value); err != nil {
		return Task{}, err
	}
	return value, nil
}

// DeleteTask 永久删除 Task 配置与全部运行记录。调用方必须先把排队运行收敛为终态；
// 正在启动、运行或等待确认的 Task 不允许删除，避免 Runtime Event 写回已经消失的记录。
func (s *Store) DeleteTask(ctx context.Context, id string) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.getTaskLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	runs, err := s.listRunsLocked(ctx, value.AgentID, value.ID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if !run.Status.Terminal() {
			return nil, ErrTaskBusy
		}
	}
	if err := os.RemoveAll(s.taskDir(value.AgentID, value.ID)); err != nil {
		return nil, fmt.Errorf("删除 Task 目录失败: %w", err)
	}
	delete(s.taskAgents, value.ID)
	delete(s.issues, "task:"+value.ID)
	for _, run := range runs {
		delete(s.runTasks, run.ID)
		delete(s.issues, "run:"+run.ID)
	}
	return runs, nil
}

func (s *Store) CreateRun(ctx context.Context, value Run) (Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if uuid.Validate(value.ID) != nil || uuid.Validate(value.TaskID) != nil || uuid.Validate(value.AgentID) != nil {
		return Run{}, false, errors.New("TaskRun ID 无效")
	}
	if err := validateStoredRun(value); err != nil {
		return Run{}, false, fmt.Errorf("TaskRun 内容无效: %w", err)
	}
	if _, err := s.getTaskLocked(ctx, value.TaskID); err != nil {
		return Run{}, false, err
	}
	values, err := s.listRunsLocked(ctx, value.AgentID, value.TaskID)
	if err != nil {
		return Run{}, false, err
	}
	if value.Trigger == TriggerSchedule {
		for _, existing := range values {
			if existing.Trigger == TriggerSchedule && existing.ScheduledFor.Equal(value.ScheduledFor) {
				return existing, false, nil
			}
		}
	}
	if value.Trigger == TriggerRetry && value.ParentRunID != "" {
		parentFound := false
		for _, existing := range values {
			if existing.Trigger == TriggerRetry && existing.ParentRunID == value.ParentRunID {
				return existing, false, nil
			}
			if existing.ID == value.ParentRunID && value.Attempt == existing.Attempt+1 {
				parentFound = true
			}
		}
		if !parentFound {
			return Run{}, false, errors.New("TaskRun 重试父运行不存在或 attempt 不连续")
		}
	}
	if err := os.MkdirAll(s.runsDir(value.AgentID, value.TaskID), 0o700); err != nil {
		return Run{}, false, err
	}
	if err := s.writeRunLocked(ctx, value); err != nil {
		return Run{}, false, err
	}
	s.runTasks[value.ID] = runRef{AgentID: value.AgentID, TaskID: value.TaskID}
	return value, true, nil
}

func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getRunLocked(ctx, id)
}

func (s *Store) getRunLocked(ctx context.Context, id string) (Run, error) {
	id = strings.TrimSpace(id)
	if uuid.Validate(id) != nil {
		return Run{}, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	ref, exists := s.runTasks[id]
	if !exists {
		tasks, err := s.listTasksLocked(ctx, true)
		if err != nil {
			return Run{}, err
		}
		for _, task := range tasks {
			if _, err := s.listRunsLocked(ctx, task.AgentID, task.ID); err != nil {
				continue
			}
		}
		ref, exists = s.runTasks[id]
	}
	if !exists {
		return Run{}, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	return s.readRunLocked(ctx, ref.AgentID, ref.TaskID, id)
}

func (s *Store) UpdateRun(ctx context.Context, value Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateStoredRun(value); err != nil {
		return fmt.Errorf("TaskRun 内容无效: %w", err)
	}
	if _, err := s.getRunLocked(ctx, value.ID); err != nil {
		return err
	}
	return s.writeRunLocked(ctx, value)
}

func (s *Store) ListRuns(ctx context.Context, taskID string) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, err := s.getTaskLocked(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return s.listRunsLocked(ctx, task.AgentID, task.ID)
}

// DeleteRun 永久删除一条已经结束的运行记录。非终态 Run 仍可能被 Scheduler 或
// Runtime 更新，因此必须先取消或等待结束。
func (s *Store) DeleteRun(ctx context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.getRunLocked(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if !run.Status.Terminal() {
		return Run{}, ErrTaskBusy
	}
	if err := s.deleteRunLocked(ctx, run); err != nil {
		return Run{}, err
	}
	return run, nil
}

// DeleteRuns 删除一个 Task 的全部终态历史。只要存在排队或活动运行就整体拒绝，
// 避免 UI 显示“清空成功”但仍留下部分记录。
func (s *Store) DeleteRuns(ctx context.Context, taskID string) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, err := s.getTaskLocked(ctx, taskID)
	if err != nil {
		return nil, err
	}
	runs, err := s.listRunsLocked(ctx, task.AgentID, task.ID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if !run.Status.Terminal() {
			return nil, ErrTaskBusy
		}
	}
	for _, run := range runs {
		if err := s.deleteRunLocked(ctx, run); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

// RunBySession 返回任意一条引用指定 Session 的运行记录。
// 连续对话模式下可能有多条 Run 共享同一个 Session，因此本方法只用于查询关联关系，
// 不再表示 Run 拥有 Session 的生命周期。删除 Session 不会反向删除 TaskRun。
func (s *Store) RunBySession(ctx context.Context, sessionID string) (Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	if uuid.Validate(sessionID) != nil {
		return Run{}, false, nil
	}
	values, err := s.listTasksLocked(ctx, true)
	if err != nil {
		return Run{}, false, err
	}
	for _, task := range values {
		runs, readErr := s.listRunsLocked(ctx, task.AgentID, task.ID)
		if readErr != nil {
			continue
		}
		for _, run := range runs {
			if run.SessionID == sessionID {
				return run, true, nil
			}
		}
	}
	return Run{}, false, nil
}

// ReferencesBySession 返回所有引用指定 Session 的 Task 与 TaskRun。连续任务可能让多条
// Run 共享一段对话，因此删除链路不能使用只返回任意一条记录的 RunBySession。
func (s *Store) ReferencesBySession(ctx context.Context, sessionID string) ([]Task, []Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	if uuid.Validate(sessionID) != nil {
		return nil, nil, nil
	}
	values, err := s.listTasksLocked(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	tasksResult := make([]Task, 0)
	runsResult := make([]Run, 0)
	for _, task := range values {
		referenced := strings.TrimSpace(task.PersistentSessionID) == sessionID
		runs, readErr := s.listRunsLocked(ctx, task.AgentID, task.ID)
		if readErr != nil {
			return nil, nil, readErr
		}
		for _, run := range runs {
			if strings.TrimSpace(run.SessionID) != sessionID {
				continue
			}
			referenced = true
			runsResult = append(runsResult, run)
		}
		if referenced {
			tasksResult = append(tasksResult, task)
		}
	}
	return tasksResult, runsResult, nil
}

func (s *Store) deleteRunLocked(ctx context.Context, run Run) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(s.runPath(run.AgentID, run.TaskID, run.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除 TaskRun 失败: %w", err)
	}
	delete(s.runTasks, run.ID)
	delete(s.issues, "run:"+run.ID)
	return nil
}

func (s *Store) listRunsLocked(ctx context.Context, agentID, taskID string) ([]Run, error) {
	directory := s.runsDir(agentID, taskID)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []Run{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Run, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), ".json")
		if uuid.Validate(runID) != nil {
			continue
		}
		value, readErr := s.readRunLocked(ctx, agentID, taskID, runID)
		if readErr != nil {
			s.issues["run:"+runID] = Issue{AgentID: agentID, TaskID: taskID, RunID: runID, Error: readErr.Error()}
			continue
		}
		delete(s.issues, "run:"+runID)
		s.runTasks[runID] = runRef{AgentID: agentID, TaskID: taskID}
		result = append(result, value)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (s *Store) ListDispatchableRuns(ctx context.Context, now time.Time) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.listTasksLocked(ctx, false)
	if err != nil {
		return nil, err
	}
	result := make([]Run, 0)
	for _, task := range tasks {
		runs, readErr := s.listRunsLocked(ctx, task.AgentID, task.ID)
		if readErr != nil {
			continue
		}
		for _, run := range runs {
			if run.Status == RunQueued && !run.ScheduledFor.After(now) {
				result = append(result, run)
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].ScheduledFor.Before(result[j].ScheduledFor) })
	return result, nil
}

// CancelQueuedAutomaticRuns 在暂停 Task 时收敛尚未开始的计划/重试运行。手动 RunNow
// 明确来自用户操作，即使 Task 当前暂停也允许继续排队。
func (s *Store) CancelQueuedAutomaticRuns(ctx context.Context, taskID string, now time.Time) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, err := s.getTaskLocked(ctx, taskID)
	if err != nil {
		return nil, err
	}
	runs, err := s.listRunsLocked(ctx, task.AgentID, task.ID)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	result := make([]Run, 0)
	for _, run := range runs {
		if run.Status != RunQueued || run.Trigger == TriggerManual {
			continue
		}
		run.Status = RunCancelled
		run.Error = "任务已暂停，尚未开始的自动运行已取消。"
		run.FinishedAt = &now
		if err := s.writeRunLocked(ctx, run); err != nil {
			return result, err
		}
		result = append(result, run)
	}
	return result, nil
}

// ReconcileQueuedAutomationRuns 把应用崩溃前尚未开始的内部 automation 收敛为
// interrupted。普通计划任务的 queued 仍按原有调度语义恢复；内部主动动作则禁止
// 自动重放，避免重复发送消息或再次执行带副作用的工具。
func (s *Store) ReconcileQueuedAutomationRuns(ctx context.Context, now time.Time) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.listTasksLocked(ctx, true)
	if err != nil {
		return nil, err
	}
	result := make([]Run, 0)
	for _, task := range values {
		if !task.Internal {
			continue
		}
		runs, readErr := s.listRunsLocked(ctx, task.AgentID, task.ID)
		if readErr != nil {
			continue
		}
		for _, run := range runs {
			if run.Status != RunQueued || run.Trigger != TriggerAutomation {
				continue
			}
			finished := now.UTC()
			run.Status = RunInterrupted
			run.Error = "应用在内部自动运行开始前退出；为避免重复主动执行，本次运行未恢复。"
			run.FinishedAt = &finished
			if err := s.writeRunLocked(ctx, run); err != nil {
				return result, err
			}
			result = append(result, run)
		}
	}
	return result, nil
}

// ReconcileInterrupted 把上次进程遗留的非终态 Run 安全收敛为 interrupted。Approval
// checkpoint 是进程态，重启后绝不恢复或自动执行旧的高风险调用。
func (s *Store) ReconcileInterrupted(ctx context.Context, now time.Time) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.listTasksLocked(ctx, true)
	if err != nil {
		return nil, err
	}
	result := make([]Run, 0)
	for _, task := range tasks {
		runs, readErr := s.listRunsLocked(ctx, task.AgentID, task.ID)
		if readErr != nil {
			continue
		}
		for _, run := range runs {
			if !run.Status.Active() {
				continue
			}
			finished := now.UTC()
			run.Status = RunInterrupted
			run.Error = "应用在任务运行期间退出；为避免重放工具副作用，本次运行未自动恢复。"
			run.Approval = nil
			run.FinishedAt = &finished
			if writeErr := s.writeRunLocked(ctx, run); writeErr != nil {
				return result, writeErr
			}
			result = append(result, run)
		}
	}
	return result, nil
}

func (s *Store) readTaskLocked(ctx context.Context, agentID, taskID string) (Task, error) {
	var document taskDocument
	if err := atomicfile.ReadJSON(ctx, s.taskConfigPath(agentID, taskID), &document); err != nil {
		return Task{}, err
	}
	if document.SchemaVersion != taskSchemaVersion || document.Task.ID != taskID || document.Task.AgentID != agentID {
		return Task{}, errors.New("Task config.json 身份或 schema_version 无效")
	}
	if err := validateStoredTask(document.Task); err != nil {
		return Task{}, fmt.Errorf("Task config.json 内容无效: %w", err)
	}
	// 历史任务没有 Token 上限字段；读取时补齐默认值，保证未重新保存的任务也受限。
	limits, err := normalizeLimits(document.Task.Limits)
	if err != nil {
		return Task{}, err
	}
	document.Task.Limits = limits
	return document.Task, nil
}

func (s *Store) readRunLocked(ctx context.Context, agentID, taskID, runID string) (Run, error) {
	var document runDocument
	if err := atomicfile.ReadJSON(ctx, s.runPath(agentID, taskID, runID), &document); err != nil {
		return Run{}, err
	}
	if document.SchemaVersion != taskSchemaVersion || document.Run.ID != runID || document.Run.TaskID != taskID || document.Run.AgentID != agentID {
		return Run{}, errors.New("TaskRun JSON 身份或 schema_version 无效")
	}
	if err := validateStoredRun(document.Run); err != nil {
		return Run{}, fmt.Errorf("TaskRun JSON 内容无效: %w", err)
	}
	return document.Run, nil
}

func validateStoredTask(value Task) error {
	execution, err := normalizeExecution(value.Execution)
	if err != nil {
		return err
	}
	conversationMode, err := normalizeConversationMode(value.ConversationMode, execution)
	if err != nil {
		return err
	}
	if persistentID := strings.TrimSpace(value.PersistentSessionID); persistentID != "" {
		if execution != ExecutionAgent || conversationMode != ConversationContinuous {
			return errors.New("只有连续 Agent 任务可以保存 persistent_session_id")
		}
		if uuid.Validate(persistentID) != nil {
			return errors.New("persistent_session_id 无效")
		}
	}
	status := value.Status
	if status == TaskStatusArchived {
		status = TaskStatusPaused
	}
	if _, _, _, _, _, err := normalizeTaskInput(value.Name, value.Prompt, status, value.Schedule, value.Limits); err != nil {
		return err
	}
	if value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		return errors.New("created_at 或 updated_at 为空")
	}
	if value.Status == TaskStatusArchived && value.ArchivedAt == nil {
		return errors.New("archived Task 缺少 archived_at")
	}
	if value.Schedule.Type == ScheduleManual && value.NextRunAt != nil {
		return errors.New("manual Task 不应包含 next_run_at")
	}
	return nil
}

func validateStoredRun(value Run) error {
	if value.Execution != "" {
		if _, err := normalizeExecution(value.Execution); err != nil {
			return err
		}
	}
	switch value.Status {
	case RunQueued, RunStarting, RunRunning, RunWaitingApproval, RunSucceeded, RunFailed, RunCancelled, RunTimedOut, RunInterrupted, RunSkipped:
	default:
		return fmt.Errorf("无效运行状态: %s", value.Status)
	}
	switch value.Trigger {
	case TriggerManual, TriggerSchedule, TriggerRetry, TriggerAutomation:
	default:
		return fmt.Errorf("无效触发来源: %s", value.Trigger)
	}
	if value.Attempt < 1 || value.ScheduledFor.IsZero() || value.CreatedAt.IsZero() {
		return errors.New("attempt、scheduled_for 或 created_at 无效")
	}
	if value.Trigger == TriggerRetry && uuid.Validate(value.ParentRunID) != nil {
		return errors.New("retry 运行缺少有效 parent_run_id")
	}
	if value.Status.Terminal() && value.FinishedAt == nil {
		return errors.New("终态运行缺少 finished_at")
	}
	if value.Status == RunWaitingApproval && value.Approval == nil {
		return errors.New("waiting_approval 运行缺少 approval")
	}
	if value.Status != RunWaitingApproval && value.Approval != nil {
		return errors.New("非 waiting_approval 运行不应包含 approval")
	}
	return nil
}

func (s *Store) writeTaskLocked(ctx context.Context, value Task) error {
	if err := atomicfile.WriteJSON(ctx, s.taskConfigPath(value.AgentID, value.ID), 0o600, taskDocument{SchemaVersion: taskSchemaVersion, Task: value}); err != nil {
		return fmt.Errorf("写入 Task 配置失败: %w", err)
	}
	return nil
}

func (s *Store) writeRunLocked(ctx context.Context, value Run) error {
	if err := atomicfile.WriteJSON(ctx, s.runPath(value.AgentID, value.TaskID, value.ID), 0o600, runDocument{SchemaVersion: taskSchemaVersion, Run: value}); err != nil {
		return fmt.Errorf("写入 TaskRun 失败: %w", err)
	}
	return nil
}

func (s *Store) taskConfigPath(agentID, taskID string) string {
	return filepath.Join(s.taskDir(agentID, taskID), taskConfigName)
}

func (s *Store) taskDir(agentID, taskID string) string {
	return filepath.Join(s.agentsRoot, agentID, "tasks", taskID)
}

func (s *Store) runsDir(agentID, taskID string) string {
	return filepath.Join(s.agentsRoot, agentID, "tasks", taskID, taskRunsDirName)
}

func (s *Store) runPath(agentID, taskID, runID string) string {
	return filepath.Join(s.runsDir(agentID, taskID), runID+".json")
}
