package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	agentID := uuid.NewString()
	if err := os.MkdirAll(filepath.Join(root, agentID), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return store, agentID
}

func createTestTask(t *testing.T, store *Store, agentID string) Task {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	value := Task{
		ID: uuid.NewString(), AgentID: agentID, Name: "test", Prompt: "do it",
		Status: TaskStatusActive, Schedule: Schedule{Type: ScheduleManual, MisfirePolicy: MisfireRunOnce, OverlapPolicy: OverlapSkip},
		Limits:    Limits{MaxDurationSeconds: 60, MaxModelCalls: 2, MaxToolCalls: 3, MaxAttempts: 1, RetryDelaySeconds: 1},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTask(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestStoreScheduledRunCreationIsConcurrentAndIdempotent(t *testing.T) {
	store, agentID := newTestStore(t)
	task := createTestTask(t, store, agentID)
	scheduledFor := time.Now().UTC().Truncate(time.Second)

	const workers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := store.CreateRun(context.Background(), Run{
				ID: uuid.NewString(), TaskID: task.ID, AgentID: agentID,
				Trigger: TriggerSchedule, ScheduledFor: scheduledFor,
				Attempt: 1, Status: RunQueued, CreatedAt: time.Now().UTC(),
			})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	runs, err := store.ListRuns(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
}

func TestStoreReconcileInterruptsOnlyStartedRuns(t *testing.T) {
	store, agentID := newTestStore(t)
	task := createTestTask(t, store, agentID)
	now := time.Now().UTC()
	for _, status := range []RunStatus{RunQueued, RunRunning, RunWaitingApproval} {
		run := Run{
			ID: uuid.NewString(), TaskID: task.ID, AgentID: agentID,
			Trigger: TriggerManual, ScheduledFor: now, Attempt: 1,
			Status: status, CreatedAt: now,
		}
		if status == RunWaitingApproval {
			run.Approval = &ApprovalSnapshot{
				ID: "approval-1", ToolName: "test", Risk: "write",
				CreatedAt: now, ExpiresAt: now.Add(time.Minute),
			}
		}
		if _, _, err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	interrupted, err := store.ReconcileInterrupted(context.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(interrupted) != 2 {
		t.Fatalf("interrupted=%d want=2", len(interrupted))
	}
	runs, err := store.ListRuns(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued := 0
	for _, run := range runs {
		if run.Status == RunQueued {
			queued++
		}
		if run.Status.Active() {
			t.Fatalf("active run survived reconciliation: %+v", run)
		}
	}
	if queued != 1 {
		t.Fatalf("queued=%d want=1", queued)
	}
}

func TestArchiveCancelsQueuedRunsAndRejectsActiveRun(t *testing.T) {
	store, agentID := newTestStore(t)
	task := createTestTask(t, store, agentID)
	now := time.Now().UTC()
	queued, _, err := store.CreateRun(context.Background(), Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: agentID,
		Trigger: TriggerManual, ScheduledFor: now, Attempt: 1, Status: RunQueued, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ArchiveTask(context.Background(), task.ID, now); err != nil {
		t.Fatal(err)
	}
	archivedRun, err := store.GetRun(context.Background(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archivedRun.Status != RunCancelled || archivedRun.FinishedAt == nil {
		t.Fatalf("queued run not cancelled: %+v", archivedRun)
	}

	other := createTestTask(t, store, agentID)
	_, _, err = store.CreateRun(context.Background(), Run{
		ID: uuid.NewString(), TaskID: other.ID, AgentID: agentID,
		Trigger: TriggerManual, ScheduledFor: now, Attempt: 1, Status: RunRunning, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ArchiveTask(context.Background(), other.ID, now); !errors.Is(err, ErrTaskBusy) {
		t.Fatalf("archive error=%v want ErrTaskBusy", err)
	}
}

func TestCancelQueuedAutomaticRunsKeepsManualRun(t *testing.T) {
	store, agentID := newTestStore(t)
	task := createTestTask(t, store, agentID)
	now := time.Now().UTC()
	parent, _, err := store.CreateRun(context.Background(), Run{
		ID: uuid.NewString(), TaskID: task.ID, AgentID: agentID,
		Trigger: TriggerManual, ScheduledFor: now.Add(-time.Second), Attempt: 1,
		Status: RunFailed, Error: "test", CreatedAt: now.Add(-time.Second), FinishedAt: timePointer(now),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []RunTrigger{TriggerManual, TriggerSchedule, TriggerRetry} {
		parentRunID := ""
		attempt := 1
		if trigger == TriggerRetry {
			parentRunID = parent.ID
			attempt = 2
		}
		_, _, err := store.CreateRun(context.Background(), Run{
			ID: uuid.NewString(), TaskID: task.ID, AgentID: agentID,
			Trigger: trigger, ParentRunID: parentRunID, ScheduledFor: now, Attempt: attempt, Status: RunQueued, CreatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	cancelled, err := store.CancelQueuedAutomaticRuns(context.Background(), task.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(cancelled) != 2 {
		t.Fatalf("cancelled=%d want=2", len(cancelled))
	}
	runs, err := store.ListRuns(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	manualQueued := 0
	for _, run := range runs {
		if run.Trigger == TriggerManual && run.Status == RunQueued {
			manualQueued++
		}
	}
	if manualQueued != 1 {
		t.Fatalf("manual queued=%d want=1", manualQueued)
	}
}

func TestStoreIsolatesCorruptTask(t *testing.T) {
	store, agentID := newTestStore(t)
	valid := createTestTask(t, store, agentID)
	corruptID := uuid.NewString()
	directory := filepath.Join(store.agentsRoot, agentID, "tasks", corruptID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, taskConfigName), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := store.ListTasks(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].ID != valid.ID {
		t.Fatalf("valid tasks were blocked: %+v", values)
	}
	issues := store.Issues()
	if len(issues) != 1 || issues[0].TaskID != corruptID {
		t.Fatalf("issues=%+v", issues)
	}
}
