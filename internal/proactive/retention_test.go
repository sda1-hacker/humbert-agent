package proactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/tasks"
)

func TestRetentionKeepsDeferredWork(t *testing.T) {
	ctx := context.Background()
	m, settings, notify, _ := deferredTestManager(t)
	for i := 0; i <= maxStoredRecords; i++ {
		m.processEvent(ctx, Event{Key: fmt.Sprintf("quiet-%d", i), Kind: EventTaskFailed})
	}
	// 重载也不能按展示历史上限裁剪延期动作。
	reloaded, err := NewStore(ctx, m.store.path)
	requireAutomationOK(t, err)
	m.store = reloaded
	settings.QuietHours.Enabled = false
	if _, err := m.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := m.heartbeat(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if len(notify.decisions) != maxStoredRecords+1 {
		t.Fatalf("quiet hours accepted %d events, only %d reminders survived", maxStoredRecords+1, len(notify.decisions))
	}
	if len(m.store.Records(0)) != maxStoredRecords {
		t.Fatal("completed history must still be bounded")
	}
}
func TestRetentionKeepsRunningAutomation(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	task, run := f.newRun(t, tasks.RunRunning)
	f.linkRun(t, task, run)
	for i := 0; i < maxStoredRecords; i++ {
		requireAutomationOK(t, f.manager.store.PutRecord(ctx, Record{ID: fmt.Sprint(i), Event: Event{Key: fmt.Sprint(i)}, Status: RecordIgnored}))
	}
	if _, exists := f.manager.store.FindByAutomationRunID(run.ID); !exists {
		t.Fatal("running record must not be evicted")
	}
	_, err := f.taskStore.MutateRun(ctx, run.ID, func(r *tasks.Run) error {
		r.Status = tasks.RunSucceeded
		now := time.Now().UTC()
		r.FinishedAt = &now
		return nil
	})
	requireAutomationOK(t, err)
	f.manager.finalizeCompletedAutomations(ctx)
	_, exists := f.manager.store.FindByAutomationRunID(run.ID)
	current, err := f.taskStore.GetTask(ctx, task.ID)
	requireAutomationOK(t, err)
	if !exists || current.Status != tasks.TaskStatusArchived {
		t.Fatalf("running record evicted=%v; completed task remains %s", !exists, current.Status)
	}
}
func TestFailedArchiveIsRetried(t *testing.T) {
	f := newAutomationFixture(t)
	ctx := context.Background()
	task, run := f.newRun(t, tasks.RunSucceeded)
	f.linkRun(t, task, run)
	config := filepath.Join(filepath.Dir(f.customRoot), "agents", f.agentID, "tasks", task.ID, "config.json")
	// 临时隐藏测试 Task 元数据，模拟收尾阶段的瞬时文件访问失败。
	requireAutomationOK(t, os.Rename(config, config+".saved"))
	if err := f.manager.finalizeAutomationRun(run); err == nil {
		t.Fatal("expected transient archive error")
	}
	// 启动恢复遇到同一瞬时错误也保留 executing 待办，不阻止整个应用启动。
	requireAutomationOK(t, f.manager.reconcileRecords(ctx))
	before, _ := f.manager.store.FindByAutomationRunID(run.ID)
	if before.Status != RecordExecuting {
		t.Fatal("archive failure consumed finalization")
	}
	requireAutomationOK(t, os.Rename(config+".saved", config))
	f.manager.finalizeCompletedAutomations(ctx)
	requireAutomationOK(t, f.manager.reconcileRecords(ctx))
	current, err := f.taskStore.GetTask(ctx, task.ID)
	requireAutomationOK(t, err)
	record, _ := f.manager.store.FindByAutomationRunID(run.ID)
	if current.Status != tasks.TaskStatusArchived {
		t.Fatalf("record=%s but internal task=%s after retry and startup reconciliation", record.Status, current.Status)
	}
}
