package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

func testManagerForSchedule(store *Store) *Manager {
	return &Manager{
		store:           store,
		events:          eventbus.New(),
		logger:          logging.NewBootstrap(),
		activeBySession: make(map[string]string),
		activeByRun:     make(map[string]string),
		activeAgents:    make(map[string]int),
		deletingAgents:  make(map[string]int),
		cancelPending:   make(map[string]bool),
	}
}

func createScheduledTestTask(t *testing.T, store *Store, agentID string, schedule Schedule, next time.Time) Task {
	t.Helper()
	now := next.Add(-time.Hour)
	value := Task{
		ID: uuid.NewString(), AgentID: agentID, Name: "scheduled", Prompt: "do it",
		Status: TaskStatusActive, Schedule: schedule,
		Limits:    Limits{MaxDurationSeconds: 60, MaxModelCalls: 2, MaxToolCalls: 3, MaxAttempts: 1, RetryDelaySeconds: 1},
		NextRunAt: &next, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTask(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestEnqueueDueQueueOneDoesNotAccumulateCandidates(t *testing.T) {
	store, agentID := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	task := createScheduledTestTask(t, store, agentID, Schedule{
		Type: ScheduleInterval, TimeZone: "UTC", IntervalMinutes: 5,
		MisfirePolicy: MisfireRunOnce, OverlapPolicy: OverlapQueueOne,
	}, now)
	parent, _, err := store.CreateRun(context.Background(), Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: agentID,
		Trigger: TriggerManual, ScheduledFor: now.Add(-2 * time.Second), Attempt: 1,
		Status: RunFailed, Error: "test", CreatedAt: now.Add(-2 * time.Second), FinishedAt: timePointer(now.Add(-time.Second)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateRun(context.Background(), Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: agentID,
		Trigger: TriggerRetry, ParentRunID: parent.ID, ScheduledFor: now.Add(-time.Second), Attempt: 2,
		Status: RunQueued, CreatedAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	if err := testManagerForSchedule(store).enqueueDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, skipped := 0, 0
	for _, run := range runs {
		switch run.Status {
		case RunQueued:
			queued++
		case RunSkipped:
			skipped++
		}
	}
	if queued != 1 || skipped != 1 {
		t.Fatalf("queued=%d skipped=%d want 1/1", queued, skipped)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestEnqueueDueMisfireSkipCreatesAuditableTerminalRun(t *testing.T) {
	store, agentID := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	task := createScheduledTestTask(t, store, agentID, Schedule{
		Type: ScheduleInterval, TimeZone: "UTC", IntervalMinutes: 5,
		MisfirePolicy: MisfireSkip, OverlapPolicy: OverlapSkip,
	}, now.Add(-2*time.Minute))

	if err := testManagerForSchedule(store).enqueueDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != RunSkipped || runs[0].FinishedAt == nil {
		t.Fatalf("unexpected runs: %+v", runs)
	}
	updated, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(now) {
		t.Fatalf("next run was not advanced: %v", updated.NextRunAt)
	}
}

func TestEnqueueDueDoesNotRaceAgentDeletion(t *testing.T) {
	store, agentID := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	task := createScheduledTestTask(t, store, agentID, Schedule{
		Type: ScheduleInterval, TimeZone: "UTC", IntervalMinutes: 5,
		MisfirePolicy: MisfireRunOnce, OverlapPolicy: OverlapSkip,
	}, now.Add(-time.Minute))
	manager := testManagerForSchedule(store)
	release := manager.SuspendAgent(agentID)
	defer release()

	if err := manager.enqueueDue(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("created runs while agent was suspended: %+v", runs)
	}
	updated, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.Equal(*task.NextRunAt) {
		t.Fatalf("next run moved while deletion was in progress: %v", updated.NextRunAt)
	}
}

func TestRecoverRetriesIsIdempotent(t *testing.T) {
	store, agentID := newTestStore(t)
	task := createTestTask(t, store, agentID)
	task.Limits.MaxAttempts = 3
	task.Limits.RetryDelaySeconds = 1
	if err := store.UpdateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	failed, _, err := store.CreateRun(context.Background(), Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: agentID,
		Trigger: TriggerManual, ScheduledFor: now, Attempt: 1,
		Status: RunFailed, Error: "provider unavailable", CreatedAt: now, FinishedAt: timePointer(now),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := testManagerForSchedule(store)
	if err := manager.recoverRetries(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.recoverRetries(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	retries := 0
	for _, run := range runs {
		if run.Trigger == TriggerRetry {
			retries++
			if run.ParentRunID != failed.ID || run.Attempt != 2 {
				t.Fatalf("unexpected retry: %+v", run)
			}
		}
	}
	if retries != 1 {
		t.Fatalf("retries=%d want=1", retries)
	}
}
