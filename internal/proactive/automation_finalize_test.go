package proactive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

type automationFixture struct {
	manager       *Manager
	tasks         *tasks.Manager
	taskStore     *tasks.Store
	bus           *eventbus.Bus
	notifications *notifications.Service
	agentID       string
	customRoot    string
}

func requireAutomationOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newAutomationFixture(t *testing.T) *automationFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	agentsRoot := filepath.Join(root, "agents")
	logger := logging.NewBootstrap()
	tr, err := transcript.NewStore(agentsRoot)
	requireAutomationOK(t, err)
	ast, err := agents.NewStore(ctx, agentsRoot, tr)
	requireAutomationOK(t, err)
	ws, err := workspace.NewManager(ctx, filepath.Join(root, "managed"), logger)
	requireAutomationOK(t, err)
	t.Cleanup(func() { requireAutomationOK(t, ws.Close()) })
	custom := filepath.Join(root, "custom")
	requireAutomationOK(t, os.Mkdir(custom, 0o700))
	aid := uuid.NewString()
	now := time.Now().UTC()
	requireAutomationOK(t, ast.Create(ctx, agents.Agent{
		ID: aid, Name: "automation regression", WorkspaceMode: workspace.ModeCustom,
		WorkspacePath: custom, CreatedAt: now, UpdatedAt: now,
	}))
	as := agents.NewService(ast, nil, logger)
	ss, err := sessions.NewStore(ctx, tr)
	requireAutomationOK(t, err)
	t.Cleanup(func() { requireAutomationOK(t, ss.Close()) })
	ses := sessions.NewService(ss, as, ws, logger)
	ts, err := tasks.NewStore(ctx, agentsRoot)
	requireAutomationOK(t, err)
	bus := eventbus.New()
	// 工作区解析失败发生在 Runtime 之前；本组测试不启动真实模型或系统通知。
	tm, err := tasks.NewManager(ts, as, ses, &agentruntime.Service{}, bus, logger)
	requireAutomationOK(t, err)
	notifier := notifications.New()
	tm.SetNotificationService(notifier)
	ps, err := NewStore(ctx, filepath.Join(root, "proactive.json"))
	requireAutomationOK(t, err)
	pm, err := NewManager(ps, tm, &reminderApprovals{}, bus, notifier, nil, logger)
	requireAutomationOK(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := pm.Close(ctx); err != nil {
			t.Errorf("close proactive worker: %v", err)
		}
		pm.cancel()
		if err := tm.Close(ctx); err != nil {
			t.Errorf("close task manager: %v", err)
		}
	})
	return &automationFixture{manager: pm, tasks: tm, taskStore: ts, bus: bus, notifications: notifier, agentID: aid, customRoot: custom}
}

func (f *automationFixture) newRun(t *testing.T, status tasks.RunStatus) (tasks.Task, tasks.Run) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	task := tasks.Task{
		ID: uuid.NewString(), AgentID: f.agentID, Name: "queued automation", Prompt: "check workspace",
		Internal: true, Origin: "proactive", OriginRef: uuid.NewString(),
		Status: tasks.TaskStatusActive, Execution: tasks.ExecutionAgent,
		Schedule: tasks.Schedule{Type: tasks.ScheduleManual},
		Limits:   tasks.Limits{MaxAttempts: 1, MaxDurationSeconds: 60}, CreatedAt: now, UpdatedAt: now,
	}
	requireAutomationOK(t, f.taskStore.CreateTask(ctx, task))
	run := tasks.Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: f.agentID, Trigger: tasks.TriggerAutomation,
		Execution: tasks.ExecutionAgent, ScheduledFor: now, Status: status, Attempt: 1, CreatedAt: now,
	}
	if status.Terminal() {
		run.FinishedAt = &now
		if status != tasks.RunSucceeded {
			run.Error = "fixture run failed"
		}
	}
	created, _, err := f.taskStore.CreateRun(ctx, run)
	requireAutomationOK(t, err)
	return task, created
}

func (f *automationFixture) linkRun(t *testing.T, task tasks.Task, run tasks.Run) Record {
	t.Helper()
	now := time.Now().UTC()
	record := Record{
		ID: uuid.NewString(), Event: Event{Key: task.OriginRef, Kind: EventWorkspaceChanged},
		Decision: Decision{Action: ActionRunAgent}, Status: RecordExecuting,
		AutomationTaskID: task.ID, AutomationRunID: run.ID, CreatedAt: now, UpdatedAt: now,
	}
	requireAutomationOK(t, f.manager.store.PutRecord(context.Background(), record))
	return record
}

func (f *automationFixture) assertFinalized(t *testing.T, task tasks.Task, run tasks.Run, status RecordStatus) Record {
	t.Helper()
	record, exists := f.manager.store.FindByEventKey(task.OriginRef)
	if !exists || record.Status != status || record.HandledAt == nil || record.AutomationRunID != run.ID {
		t.Fatalf("automation result was not finalized: %+v", record)
	}
	current, err := f.taskStore.GetTask(context.Background(), task.ID)
	requireAutomationOK(t, err)
	if current.Status != tasks.TaskStatusArchived {
		t.Fatalf("internal task not archived: %+v", current)
	}
	return record
}

func TestQueuedAutomationFailureDoesNotBlockScheduler(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	// 即使用户关闭主动规则，已执行任务的失败收尾也必须继续。
	settings := f.manager.Settings()
	settings.Enabled = false
	_, err := f.manager.UpdateSettings(ctx, settings)
	requireAutomationOK(t, err)
	requireAutomationOK(t, f.manager.Start(ctx))
	task, run := f.newRun(t, tasks.RunQueued)
	f.linkRun(t, task, run)
	finished := make(chan struct{}, 1)
	unsubscribe, err := f.bus.Subscribe(TopicEvent, func(_ context.Context, payload any) {
		event, ok := payload.(PublicEvent)
		if ok && event.Record != nil && event.Record.AutomationRunID == run.ID && event.Record.Status == RecordFailed {
			select {
			case finished <- struct{}{}:
			default:
			}
		}
	})
	requireAutomationOK(t, err)
	defer unsubscribe()
	// 排队期间工作区被删除：只应使这次自动运行失败，后面的通知任务仍须执行。
	requireAutomationOK(t, os.Remove(f.customRoot))
	notificationTask, err := f.tasks.Create(ctx, tasks.CreateInput{
		AgentID: f.agentID, Name: "subsequent notification", Prompt: "still works",
		Execution: tasks.ExecutionNotification, Schedule: tasks.Schedule{Type: tasks.ScheduleManual},
	})
	requireAutomationOK(t, err)
	dispatched := make(chan tasks.Run, 1)
	errors := make(chan error, 1)
	go func() {
		value, err := f.tasks.RunNow(ctx, notificationTask.ID)
		dispatched <- value
		errors <- err
	}()
	select {
	case value := <-dispatched:
		requireAutomationOK(t, <-errors)
		if value.Status != tasks.RunSucceeded {
			t.Fatalf("subsequent task did not complete: %+v", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher deadlocked while handling automation failure")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not finalize failed automation")
	}
	current, err := f.taskStore.GetRun(ctx, run.ID)
	requireAutomationOK(t, err)
	if current.Status != tasks.RunFailed || !strings.Contains(current.Error, "Workspace") {
		t.Fatalf("expected missing-workspace failure: %+v", current)
	}
	f.assertFinalized(t, task, run, RecordFailed)
	if len(f.notifications.Recent(0)) != 1 {
		t.Fatal("subsequent notification was lost or duplicated")
	}
}

func TestAutomationFinalizationReadsStoreAndCoalescesDuplicates(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	task, run := f.newRun(t, tasks.RunFailed)
	original := f.linkRun(t, task, run)
	unsubscribe, err := f.bus.Subscribe(tasks.TopicEvent, f.manager.handleTaskPayload)
	requireAutomationOK(t, err)
	defer unsubscribe()
	// 模拟同步事件发布方持有调度锁。回调必须立即返回，不能等 Worker 归档。
	release := f.tasks.SuspendAgent(f.agentID)
	defer release()
	done := make(chan error, 1)
	go func() {
		stale := run
		stale.Status = tasks.RunSucceeded
		for range 100 {
			if err := f.bus.Publish(ctx, tasks.TopicEvent, tasks.Event{Type: "run.succeeded", Run: &stale}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		requireAutomationOK(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("synchronous callback waited for scheduler lock")
	}
	release()
	before, _ := f.manager.store.FindByEventKey(task.OriginRef)
	if before.Status != RecordExecuting || !before.UpdatedAt.Equal(original.UpdatedAt) || len(f.manager.wake) != 1 {
		t.Fatal("callback changed result directly or did not coalesce wakeups")
	}
	f.manager.finalizeCompletedAutomations(ctx)
	finalized := f.assertFinalized(t, task, run, RecordFailed)
	// 事件仅用于唤醒；旧事件的 succeeded 不能覆盖 Store 中真实的 failed。
	if finalized.Error != run.Error {
		t.Fatalf("used stale event snapshot: %+v", finalized)
	}
	f.manager.finalizeCompletedAutomations(ctx)
	again := f.assertFinalized(t, task, run, RecordFailed)
	if !again.UpdatedAt.Equal(finalized.UpdatedAt) {
		t.Fatal("duplicate wake rewrote a finalized record")
	}
}

type completingAutomationExecutor struct {
	execute func(context.Context) (ExecutionResult, error)
}

func (e completingAutomationExecutor) Action() Action { return ActionRunAgent }
func (e completingAutomationExecutor) Execute(ctx context.Context, _ Event, _ Decision) (ExecutionResult, error) {
	return e.execute(ctx)
}

func TestAutomationCompletionBeforeRunIDBackfill(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	task, run := f.newRun(t, tasks.RunSucceeded)
	settings := f.manager.Settings()
	settings.Rules[EventWorkspaceChanged] = EventRule{Enabled: true, Action: ActionRunAgent, AgentID: f.agentID}
	_, err := f.manager.UpdateSettings(ctx, settings)
	requireAutomationOK(t, err)
	f.manager.registerExecutor(completingAutomationExecutor{execute: func(ctx context.Context) (ExecutionResult, error) {
		f.manager.handleTaskPayload(ctx, tasks.Event{Type: "run.succeeded", Run: &run})
		<-f.manager.wake
		// 手动巡检可能与 Worker 并发；模拟 Worker 在 Run ID 回填前已消费唯一终态事件。
		f.manager.finalizeCompletedAutomations(ctx)
		record, exists := f.manager.store.FindByEventKey(task.OriginRef)
		if !exists || record.Status != RecordExecuting || record.AutomationRunID != "" {
			t.Fatalf("unbound execution was prematurely finalized: %+v", record)
		}
		return ExecutionResult{AutomationTaskID: task.ID, AutomationRunID: run.ID}, nil
	}})
	f.manager.processEvent(ctx, Event{Key: task.OriginRef, Kind: EventWorkspaceChanged, AgentID: f.agentID})
	if len(f.manager.wake) != 1 {
		t.Fatal("Run ID backfill did not wake worker again")
	}
	f.manager.finalizeCompletedAutomations(ctx)
	f.assertFinalized(t, task, run, RecordSucceeded)
}

func TestAutomationFinalizationRecoversAfterRestart(t *testing.T) {
	for _, status := range []tasks.RunStatus{tasks.RunSucceeded, tasks.RunFailed} {
		for _, associated := range []bool{true, false} {
			name := string(status) + "/before_backfill"
			if associated {
				name = string(status) + "/after_backfill"
			}
			t.Run(name, func(t *testing.T) {
				f := newAutomationFixture(t)
				ctx := context.Background()
				task, run := f.newRun(t, status)
				record := f.linkRun(t, task, run)
				if !associated {
					record.AutomationTaskID, record.AutomationRunID = "", ""
					requireAutomationOK(t, f.manager.store.PutRecord(ctx, record))
				}
				// 没有内存事件，重新加载磁盘文档后必须仅靠原有恢复路径完成收尾。
				reloaded, err := NewStore(ctx, f.manager.store.path)
				requireAutomationOK(t, err)
				f.manager.store = reloaded
				requireAutomationOK(t, f.manager.Start(ctx))
				want := RecordFailed
				if status == tasks.RunSucceeded {
					want = RecordSucceeded
				}
				f.assertFinalized(t, task, run, want)
			})
		}
	}
}

func TestAutomationFinalizationRetriesFailedPersistence(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	task, run := f.newRun(t, tasks.RunFailed)
	f.linkRun(t, task, run)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	f.manager.finalizeCompletedAutomations(cancelled)
	record, _ := f.manager.store.FindByEventKey(task.OriginRef)
	if record.Status != RecordExecuting {
		t.Fatal("cancelled worker changed the record")
	}
	path := f.manager.store.path
	// 用目录作为目标文件，稳定模拟落盘失败，不依赖执行用户的权限。
	f.manager.store.path = t.TempDir()
	f.manager.finalizeCompletedAutomations(ctx)
	record, _ = f.manager.store.FindByEventKey(task.OriginRef)
	if record.Status != RecordExecuting {
		t.Fatal("failed persistence consumed pending finalization")
	}
	f.manager.store.path = path
	f.manager.finalizeCompletedAutomations(ctx)
	f.assertFinalized(t, task, run, RecordFailed)
}
