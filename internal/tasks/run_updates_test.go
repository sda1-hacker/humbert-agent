package tasks

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
)

func newStartingTestRun(t *testing.T, store *Store, agent string) Run {
	t.Helper()
	task := createTestTask(t, store, agent)
	now := time.Now().UTC()
	run, _, err := store.CreateRun(context.Background(), Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: agent, Trigger: TriggerManual,
		ScheduledFor: now, Attempt: 1, Status: RunStarting, CreatedAt: now, StartedAt: &now, SessionID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestStartupIdentityUpdatePreservesTerminalResult(t *testing.T) {
	store, agent := newTestStore(t)
	run := newStartingTestRun(t, store, agent)
	now := time.Now().UTC()
	_, err := store.MutateRun(context.Background(), run.ID, func(current *Run) error {
		current.Status, current.FinishedAt, current.ResultMessageID = RunSucceeded, &now, "final-message"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 完成事件先落盘，随后才回填启动身份，不能回写旧的 starting/running 快照。
	current, err := store.MutateRun(context.Background(), run.ID, func(current *Run) error {
		current.RequestID, current.RuntimeRunID = "request", "runtime"
		if current.Status == RunStarting {
			current.Status = RunRunning
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != RunSucceeded || current.FinishedAt == nil || current.ResultMessageID != "final-message" || current.RequestID != "request" {
		t.Fatalf("terminal result lost: %+v", current)
	}
	if _, err := store.MutateRun(context.Background(), run.ID, func(current *Run) error { current.Status = RunRunning; return nil }); err == nil {
		t.Fatal("terminal state was reopened")
	}
	if _, err := store.MutateRun(context.Background(), run.ID, func(current *Run) error { current.ResultMessageID = ""; return nil }); err == nil {
		t.Fatal("terminal result was erased")
	}
	if _, err := store.MutateRun(context.Background(), run.ID, func(current *Run) error { *current.FinishedAt = current.FinishedAt.Add(time.Hour); return nil }); err == nil {
		t.Fatal("terminal timestamp was mutated through pointer")
	}

}

func TestConcurrentRuntimeEventsPreserveUsage(t *testing.T) {
	store, agent := newTestStore(t)
	run := newStartingTestRun(t, store, agent)
	manager := testManagerForSchedule(store)
	manager.activeBySession[run.SessionID] = run.ID
	manager.activeByRun[run.ID] = "request"
	manager.activeAgents[agent] = 1
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			manager.handleRuntimePayload(context.Background(), agentruntime.Event{SessionID: run.SessionID, Type: agentruntime.EventModelStarted})
			manager.handleRuntimePayload(context.Background(), agentruntime.Event{SessionID: run.SessionID, Type: agentruntime.EventModelUsage, InputTokens: 2, OutputTokens: 1, TotalTokens: 3})
		})
	}
	wg.Wait()
	current, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ModelCalls != 20 || current.InputTokens != 40 || current.OutputTokens != 20 || current.TotalTokens != 60 {
		t.Fatalf("lost event updates: %+v", current)
	}
	// 释放两次不能误扣同 Agent 另一运行的占用。
	manager.activeAgents[agent] = 2
	manager.releaseActive(run)
	manager.releaseActive(run)
	if manager.activeAgents[agent] != 1 {
		t.Fatal("duplicate release decremented another active run")
	}
}

func TestCancelQueuedRunDoesNotWaitForSchedulerCycle(t *testing.T) {
	store, agent := newTestStore(t)
	task := createTestTask(t, store, agent)
	now := time.Now().UTC()
	run, _, err := store.CreateRun(context.Background(), Run{ID: uuid.NewString(), TaskID: task.ID, AgentID: agent, Trigger: TriggerManual, ScheduledFor: now, Attempt: 1, Status: RunQueued, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	manager := testManagerForSchedule(store)
	manager.cycleMu.Lock()
	defer manager.cycleMu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := manager.CancelRun(context.Background(), run.ID); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked behind scheduler")
	}
	current, err := store.GetRun(context.Background(), run.ID)
	if err != nil || current.Status != RunCancelled {
		t.Fatalf("cancelled run=%+v error=%v", current, err)
	}
}
