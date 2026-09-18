package proactive

import (
	"testing"
	"time"
)

func TestRuleDecisionEngine(t *testing.T) {
	settings := DefaultSettings()
	event := Event{Kind: EventTaskFailed}
	decision := (RuleDecisionEngine{}).Decide(event, settings, time.Now(), nil)
	if decision.Action != ActionNotify {
		t.Fatalf("expected notify, got %q", decision.Action)
	}

	rule := settings.Rules[EventTaskFailed]
	rule.Action = ActionRunAgent
	rule.AgentID = "agent-1"
	rule.AgentPrompt = "检查失败原因"
	settings.Rules[EventTaskFailed] = rule
	decision = (RuleDecisionEngine{}).Decide(event, settings, time.Now(), nil)
	if decision.Action != ActionRunAgent || decision.AgentID != "agent-1" || decision.Prompt != "检查失败原因" {
		t.Fatalf("unexpected run_agent decision: %#v", decision)
	}
}

func TestQuietHoursAcrossMidnight(t *testing.T) {
	settings := DefaultSettings()
	settings.QuietHours = QuietHours{Enabled: true, Start: "22:00", End: "08:00", TimeZone: "UTC"}

	if !InQuietHours(settings, time.Date(2026, 9, 18, 23, 0, 0, 0, time.UTC)) {
		t.Fatal("23:00 should be quiet")
	}
	if !InQuietHours(settings, time.Date(2026, 9, 18, 7, 59, 0, 0, time.UTC)) {
		t.Fatal("07:59 should be quiet")
	}
	if InQuietHours(settings, time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("12:00 should not be quiet")
	}
}
