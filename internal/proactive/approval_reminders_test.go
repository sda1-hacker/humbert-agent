package proactive

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
)

type reminderApprovals struct {
	mu       sync.RWMutex
	requests map[string]approval.Request
}

func (r *reminderApprovals) Get(id string) (approval.Request, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.requests[id]
	return v, ok
}
func (r *reminderApprovals) set(v approval.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests[v.ID] = v
}
func (r *reminderApprovals) forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.requests, id)
}

func reminderTestManager(t *testing.T) (*Manager, Settings, *deferredTestExecutor, *reminderApprovals, approval.Request) {
	t.Helper()
	m, settings, notify, _ := deferredTestManager(t)
	reader := &reminderApprovals{requests: make(map[string]approval.Request)}
	req := approval.Request{ID: "approval-1", SessionID: "session", ToolName: "write_file", Status: approval.StatusPending, ExpiresAt: time.Now().Add(time.Hour)}
	reader.set(req)
	m.approvals = reader
	m.tasks = &tasks.Manager{}
	m.rootCtx = context.Background()
	return m, settings, notify, reader, req
}
func queueApprovalReminder(m *Manager, req approval.Request, taskSource bool) {
	if taskSource {
		m.handleTaskPayload(context.Background(), tasks.Event{Type: "run.waiting_approval", Run: &tasks.Run{
			ID: "run", SessionID: req.SessionID, Status: tasks.RunWaitingApproval, Trigger: tasks.TriggerSchedule,
			Approval: &tasks.ApprovalSnapshot{ID: req.ID, ToolName: req.ToolName, ExpiresAt: req.ExpiresAt},
		}})
	} else {
		m.handleRuntimePayload(context.Background(), agentruntime.Event{Type: agentruntime.EventApprovalRequested, SessionID: req.SessionID, Approval: &req})
	}
}
func endReminderQuietHours(t *testing.T, m *Manager, settings Settings) {
	t.Helper()
	settings.QuietHours.Enabled = false
	if _, err := m.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err := m.heartbeat(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalTerminationRemovesInboxAndDeferredReminders(t *testing.T) {
	for _, source := range []string{"chat", "task"} {
		for _, stage := range []string{"inbox", "deferred"} {
			for _, status := range []approval.Status{approval.StatusResolved, approval.StatusExpired, approval.StatusCancelled} {
				t.Run(source+"/"+stage+"/"+string(status), func(t *testing.T) {
					m, settings, notify, reader, req := reminderTestManager(t)
					queueApprovalReminder(m, req, source == "task")
					pending := m.store.PendingEvents()
					if len(pending) != 1 || pending[0].ApprovalID != req.ID {
						t.Fatalf("invalid pending event: %+v", pending)
					}
					if stage == "deferred" {
						m.drainPendingEvents()
						if len(m.store.DeferredRecords()) != 1 {
							t.Fatal("reminder not deferred")
						}
					}
					stale := m.store.DeferredRecords()
					req.Status = status
					reader.set(req)
					kind := agentruntime.EventApprovalResolved
					if status == approval.StatusExpired {
						kind = agentruntime.EventApprovalExpired
					}
					event := agentruntime.Event{Type: kind, SessionID: req.SessionID, Approval: &req}
					m.handleRuntimePayload(context.Background(), event)
					m.handleRuntimePayload(context.Background(), event) // 重复终结事件必须幂等。
					if m.store.PendingEventCount() != 0 || len(m.store.DeferredRecords()) != 0 {
						t.Fatal("finished reminder remained queued")
					}
					// 处理器可能已经取得旧队列快照；执行前仍须查询真实审批状态。
					for _, record := range stale {
						m.executeRecord(context.Background(), &record)
					}
					m.drainPendingEvents()
					endReminderQuietHours(t, m, settings)
					if len(notify.decisions) != 0 {
						t.Fatalf("finished approval sent %d reminders", len(notify.decisions))
					}
				})
			}
		}
	}
}

func TestReminderChecksLiveApprovalEvenWithoutTerminalEvent(t *testing.T) {
	for _, state := range []string{"resolved", "resolving", "expired", "cancelled", "missing", "deadline"} {
		t.Run(state, func(t *testing.T) {
			m, settings, notify, reader, req := reminderTestManager(t)
			queueApprovalReminder(m, req, false)
			m.drainPendingEvents()
			switch state {
			case "missing":
				reader.forget(req.ID)
			case "deadline":
				req.ExpiresAt = time.Now().Add(-time.Second)
				reader.set(req)
			default:
				req.Status = approval.Status(state)
				reader.set(req)
			}
			endReminderQuietHours(t, m, settings)
			if len(notify.decisions) != 0 {
				t.Fatal("stale reminder executed")
			}
			record, exists := m.store.FindByEventKey("approval:" + req.ID)
			if !exists || record.Status != RecordIgnored || record.HandledAt == nil {
				t.Fatalf("stale reminder not finalized: %+v", record)
			}
		})
	}
}

func TestPendingReminderStillSendsOnceAndLateRequestedEventCannotReviveIt(t *testing.T) {
	m, settings, notify, reader, req := reminderTestManager(t)
	queueApprovalReminder(m, req, false)
	m.drainPendingEvents()
	endReminderQuietHours(t, m, settings)
	if len(notify.decisions) != 1 {
		t.Fatal("valid approval reminder was lost")
	}
	req.Status = approval.StatusResolved
	reader.set(req)
	m.handleRuntimePayload(context.Background(), agentruntime.Event{Type: agentruntime.EventApprovalResolved, Approval: &req})
	queueApprovalReminder(m, req, false)
	m.drainPendingEvents()
	endReminderQuietHours(t, m, settings)
	record, _ := m.store.FindByEventKey("approval:" + req.ID)
	if len(notify.decisions) != 1 || record.Status != RecordSucceeded {
		t.Fatalf("sent reminder was replayed or rewritten: %+v", record)
	}
	// 终结先于请求事件到达时，也不能仅凭旧 requested 事件发送通知。
	req.ID = "late-request"
	reader.set(req)
	queueApprovalReminder(m, req, false)
	m.drainPendingEvents()
	if len(notify.decisions) != 1 {
		t.Fatal("late requested event revived a finished approval")
	}
}

func TestStartupDiscardsApprovalRemindersFromPreviousProcess(t *testing.T) {
	m, _, notify, reader, req := reminderTestManager(t)
	queueApprovalReminder(m, req, false)
	m.drainPendingEvents()
	// 重启后内存审批和 checkpoint 不存在；即使还在免打扰时间也应清理旧延迟记录。
	reader.forget(req.ID)
	if err := m.reconcileRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(m.store.DeferredRecords()) != 0 || len(notify.decisions) != 0 {
		t.Fatal("startup replayed an old approval reminder")
	}
}

func TestDismissApprovalIsAtomicAndKeepsUnrelatedEvents(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		event := Event{Key: "approval:" + id, Kind: EventApprovalPending, ApprovalID: id}
		if _, err := store.EnqueueEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
		if err := store.PutRecord(ctx, Record{ID: id, Event: event, Status: RecordDeferred}); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.DismissApproval(cancelled, "a"); err == nil {
		t.Fatal("cancelled write should fail")
	}
	if store.PendingEventCount() != 2 || len(store.DeferredRecords()) != 2 {
		t.Fatal("failed persistence mutated memory")
	}
	changed, err := store.DismissApproval(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0].Status != RecordIgnored || store.PendingEventCount() != 1 || len(store.DeferredRecords()) != 1 {
		t.Fatalf("unexpected dismissal: %+v", changed)
	}
	if store.PendingEvents()[0].ApprovalID != "b" || store.DeferredRecords()[0].Event.ApprovalID != "b" {
		t.Fatal("unrelated reminder removed")
	}
	reloaded, err := NewStore(ctx, store.path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PendingEventCount() != 1 || len(reloaded.DeferredRecords()) != 1 {
		t.Fatal("dismissal was not durable")
	}
}
