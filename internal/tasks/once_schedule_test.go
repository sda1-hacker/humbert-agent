package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/notifications"
)

func createDueOnceTask(t *testing.T, store *Store, agentID string, now time.Time) Task {
	t.Helper()
	task := createScheduledTestTask(t, store, agentID, Schedule{Type: ScheduleOnce, RunAt: &now, TimeZone: "UTC", MisfirePolicy: MisfireRunOnce, OverlapPolicy: OverlapSkip}, now)
	task.Execution = ExecutionNotification
	task.Limits.MaxAttempts = 2
	if err := store.UpdateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestOnceScheduleDispatchesAndDoesNotRepeat(t *testing.T) {
	ctx := context.Background()
	store, agentID := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	task := createDueOnceTask(t, store, agentID, now)
	m := testManagerForSchedule(store)
	notifier := notifications.New()
	m.SetNotificationService(notifier)
	if err := m.enqueueDue(ctx, now); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != TaskStatusActive || saved.NextRunAt != nil {
		t.Fatalf("once schedule confused with user pause: %+v", saved)
	}
	// 模拟入队后重启：新的 Manager 必须能继续分派已持久化的唯一运行。
	m = testManagerForSchedule(store)
	m.SetNotificationService(notifier)
	if err := m.dispatchLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.enqueueDue(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := m.dispatchLocked(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != RunSucceeded || len(notifier.Recent(0)) != 1 {
		t.Fatalf("runs=%+v notifications=%d", runs, len(notifier.Recent(0)))
	}
}

type onceFailingNotification struct{ calls int }

func (p *onceFailingNotification) Send(context.Context, notifications.Notification) error {
	p.calls++
	if p.calls == 1 {
		return errors.New("temporary notification failure")
	}
	return nil
}

func TestOnceScheduleRetriesFailureAndRecoveryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, agentID := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	task := createDueOnceTask(t, store, agentID, now)
	m := testManagerForSchedule(store)
	provider := &onceFailingNotification{}
	m.SetNotificationService(notifications.New(provider))
	if err := m.enqueueDue(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := m.dispatchLocked(ctx); err != nil {
		t.Fatal(err)
	}
	// 重启恢复与实时失败处理都可能创建重试，必须保持同一 ParentRunID 只有一条重试。
	if err := m.recoverRetries(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("once failure did not create exactly one retry: %+v", runs)
	}
	for _, run := range runs {
		if run.Trigger != TriggerRetry {
			continue
		}
		if run.Attempt != 2 || run.Status != RunQueued {
			t.Fatalf("invalid retry: %+v", run)
		}
		// 直接推进持久化的待执行时间，不依赖 sleep 或墙上时钟等待。
		if _, err := store.MutateRun(ctx, run.ID, func(current *Run) error { current.ScheduledFor = now.Add(-time.Second); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.dispatchLocked(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err = store.ListRuns(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || provider.calls != 2 {
		t.Fatalf("runs=%+v calls=%d", runs, provider.calls)
	}
	for _, run := range runs {
		if run.Trigger == TriggerRetry && run.Status != RunSucceeded {
			t.Fatalf("retry did not complete: %+v", run)
		}
	}
}

func TestOnceScheduleHonorsUserPauseAndCapacityQueue(t *testing.T) {
	for _, pause := range []bool{false, true} {
		t.Run(map[bool]string{false: "capacity_queue", true: "user_pause"}[pause], func(t *testing.T) {
			ctx := context.Background()
			store, agentID := newTestStore(t)
			now := time.Now().UTC().Truncate(time.Second)
			task := createDueOnceTask(t, store, agentID, now)
			task.Execution = ExecutionAgent
			if err := store.UpdateTask(ctx, task); err != nil {
				t.Fatal(err)
			}
			m := testManagerForSchedule(store) // 容量为零，使 Agent 运行留在队列，避免调用真实模型。
			if err := m.enqueueDue(ctx, now); err != nil {
				t.Fatal(err)
			}
			if pause {
				if _, err := m.SetStatus(ctx, task.ID, TaskStatusPaused); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.dispatchLocked(ctx); err != nil {
				t.Fatal(err)
			}
			runs, err := store.ListRuns(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := RunQueued
			if pause {
				want = RunCancelled
			}
			if len(runs) != 1 || runs[0].Status != want {
				t.Fatalf("want %s, runs=%+v", want, runs)
			}
		})
	}
}
