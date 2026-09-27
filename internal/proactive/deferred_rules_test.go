package proactive

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

type deferredTestExecutor struct {
	action       Action
	decisions    []Decision
	afterExecute func()
}

func (e *deferredTestExecutor) Action() Action { return e.action }
func (e *deferredTestExecutor) Execute(_ context.Context, _ Event, decision Decision) (ExecutionResult, error) {
	e.decisions = append(e.decisions, decision)
	if e.afterExecute != nil {
		e.afterExecute()
	}
	return ExecutionResult{}, nil
}

func deferredTestManager(t *testing.T) (*Manager, Settings, *deferredTestExecutor, *deferredTestExecutor) {
	t.Helper()
	store, err := NewStore(context.Background(), filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	notify := &deferredTestExecutor{action: ActionNotify}
	agent := &deferredTestExecutor{action: ActionRunAgent}
	m := &Manager{store: store, events: eventbus.New(), logger: logging.NewBootstrap(), decision: RuleDecisionEngine{}, executors: map[Action]Executor{ActionNotify: notify, ActionRunAgent: agent}}
	settings := DefaultSettings()
	settings.QuietHours = QuietHours{Enabled: true, Start: "00:00", End: "00:00", TimeZone: "UTC"}
	if _, err := m.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	return m, settings, notify, agent
}

func TestDeferredActionUsesCurrentRule(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rule       EventRule
		wantAction Action
	}{
		{name: "disabled", rule: EventRule{Enabled: false, Action: ActionNotify}, wantAction: ActionIgnore},
		{name: "ignore", rule: EventRule{Enabled: true, Action: ActionIgnore}, wantAction: ActionIgnore},
		{name: "changed_to_notification", rule: EventRule{Enabled: true, Action: ActionNotify}, wantAction: ActionNotify},
		{name: "changed_agent_and_prompt", rule: EventRule{Enabled: true, Action: ActionRunAgent, AgentID: "new-agent", AgentPrompt: "new prompt"}, wantAction: ActionRunAgent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m, settings, notify, agent := deferredTestManager(t)
			settings.Rules[EventTaskFailed] = EventRule{Enabled: true, Action: ActionRunAgent, AgentID: "old-agent", AgentPrompt: "old prompt"}
			if _, err := m.UpdateSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			m.processEvent(ctx, Event{Key: "deferred", Kind: EventTaskFailed, OccurredAt: time.Now().UTC()})
			if len(m.store.DeferredRecords()) != 1 || len(agent.decisions) != 0 {
				t.Fatal("event was not deferred")
			}
			settings.QuietHours.Enabled = false
			settings.Rules[EventTaskFailed] = tc.rule
			if _, err := m.UpdateSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			if err := m.heartbeat(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			record, _ := m.store.FindByEventKey("deferred")
			if record.Decision.Action != tc.wantAction || len(m.store.DeferredRecords()) != 0 {
				t.Fatalf("record=%+v", record)
			}
			switch tc.wantAction {
			case ActionIgnore:
				if record.Status != RecordIgnored || record.HandledAt == nil || len(notify.decisions)+len(agent.decisions) != 0 {
					t.Fatalf("ignored record executed: %+v", record)
				}
			case ActionNotify:
				if len(notify.decisions) != 1 || len(agent.decisions) != 0 {
					t.Fatal("old action was reused")
				}
			case ActionRunAgent:
				if len(agent.decisions) != 1 || agent.decisions[0].AgentID != "new-agent" || agent.decisions[0].Prompt != "new prompt" {
					t.Fatalf("decision=%+v", agent.decisions)
				}
			}
		})
	}
}

func TestDeferredActionsApplyCooldownAfterActualExecution(t *testing.T) {
	ctx := context.Background()
	m, settings, notify, _ := deferredTestManager(t)
	settings.Rules[EventTaskFailed] = EventRule{Enabled: true, Action: ActionNotify, CooldownSeconds: 3600}
	if _, err := m.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"first", "second"} {
		m.processEvent(ctx, Event{Key: key, Kind: EventTaskFailed})
	}
	if len(m.store.DeferredRecords()) != 2 {
		t.Fatal("pending events should not cool down themselves")
	}
	settings.QuietHours.Enabled = false
	if _, err := m.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := m.heartbeat(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if len(notify.decisions) != 1 || len(m.store.DeferredRecords()) != 0 {
		t.Fatalf("executions=%d deferred=%d", len(notify.decisions), len(m.store.DeferredRecords()))
	}
}

func TestDeferredBatchRechecksSettingsBetweenActions(t *testing.T) {
	ctx := context.Background()
	m, settings, notify, _ := deferredTestManager(t)
	for _, key := range []string{"first", "second"} {
		m.processEvent(ctx, Event{Key: key, Kind: EventTaskFailed})
	}
	settings.QuietHours.Enabled = false
	if _, err := m.UpdateSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	notify.afterExecute = func() {
		settings.Enabled = false
		if _, err := m.UpdateSettings(ctx, settings); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.heartbeat(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if len(notify.decisions) != 1 {
		t.Fatalf("disabled assistant continued batch: %d", len(notify.decisions))
	}
}
