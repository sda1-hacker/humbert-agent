package contextengine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

type plannerEstimator struct{}

type reasoningAwarePlannerEstimator struct{ plannerEstimator }

func (reasoningAwarePlannerEstimator) EstimateMessage(message *schema.Message) int {
	if message == nil {
		return 0
	}
	return len(message.Content) + len(message.ReasoningContent) + 1
}

func (plannerEstimator) EstimateText(text string) int { return len(text) }
func (plannerEstimator) EstimateMessage(message *schema.Message) int {
	if message == nil {
		return 0
	}
	if message.Content != "" {
		return len(message.Content)
	}
	return 10
}
func (p plannerEstimator) EstimateMessages(messages []*schema.Message) int {
	total := 0
	for _, message := range messages {
		total += p.EstimateMessage(message)
	}
	return total
}
func (plannerEstimator) EstimateTools(context.Context, []einotool.BaseTool) (int, error) {
	return 0, nil
}

func plannerEntry(id string, role transcript.MessageRole, text string) transcript.Entry {
	message := &transcript.AgentMessage{
		Role:      role,
		Content:   []transcript.ContentBlock{{Type: transcript.ContentText, Text: text}},
		Timestamp: 1,
	}
	if role == transcript.RoleAssistant {
		message.Provider = "test"
		message.Model = "test"
		message.StopReason = transcript.StopReasonStop
	}
	return transcript.Entry{Type: transcript.EntryMessage, ID: id, Message: message}
}

func TestPlanCompactionPrefersWholeUserTurn(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{
		plannerEntry("u1", transcript.RoleUser, "1111111111"),
		plannerEntry("a1", transcript.RoleAssistant, "2222222222"),
		plannerEntry("u2", transcript.RoleUser, "3333333333"),
		plannerEntry("a2", transcript.RoleAssistant, "4444444444"),
		plannerEntry("u3", transcript.RoleUser, "5555555555"),
		plannerEntry("a3", transcript.RoleAssistant, "6666666666"),
	}

	plan, err := planCompaction(transcript.Document{ActiveBranch: branch}, 35, plannerEstimator{})
	if err != nil {
		t.Fatalf("planCompaction() error = %v", err)
	}
	if plan.FirstKeptEntryID != "u2" {
		t.Fatalf("FirstKeptEntryID = %q, want u2", plan.FirstKeptEntryID)
	}
	if plan.SplitTurn {
		t.Fatal("expected a complete user-turn boundary")
	}
	if len(plan.ToSummarize) != 2 {
		t.Fatalf("ToSummarize len = %d, want 2", len(plan.ToSummarize))
	}
}

func TestPlanCompactionDoesNotBudgetOmittedHistoricalThinking(t *testing.T) {
	t.Parallel()
	old := plannerEntry("a1", transcript.RoleAssistant, "answer")
	old.Message.Content = append(old.Message.Content, transcript.ContentBlock{Type: transcript.ContentThinking, Thinking: strings.Repeat("x", 200)})
	branch := []transcript.Entry{
		plannerEntry("u1", transcript.RoleUser, "first"), old,
		plannerEntry("u2", transcript.RoleUser, "second"),
		plannerEntry("a2", transcript.RoleAssistant, "answer"),
	}
	_, err := planCompaction(transcript.Document{ActiveBranch: branch}, 100, reasoningAwarePlannerEstimator{}, ReasoningReplayAuto)
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("omitted historical thinking caused needless compaction: %v", err)
	}
}

func TestPlanCompactionNeverStartsAtToolResult(t *testing.T) {
	t.Parallel()

	assistant := plannerEntry("a-tool", transcript.RoleAssistant, "")
	assistant.Message.Content = []transcript.ContentBlock{{
		Type:      transcript.ContentToolCall,
		ID:        "call-1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"internal/runtime/service.go"}`),
	}}
	assistant.Message.StopReason = transcript.StopReasonToolUse
	tool := plannerEntry("tool-1", transcript.RoleToolResult, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	tool.Message.ToolCallID = "call-1"
	tool.Message.ToolName = "read_file"

	branch := []transcript.Entry{
		plannerEntry("u1", transcript.RoleUser, "old-user"),
		plannerEntry("a1", transcript.RoleAssistant, "old-assistant"),
		plannerEntry("u2", transcript.RoleUser, "current-user"),
		assistant,
		tool,
		plannerEntry("a2", transcript.RoleAssistant, "final"),
	}

	plan, err := planCompaction(transcript.Document{ActiveBranch: branch}, 36, plannerEstimator{})
	if err != nil {
		t.Fatalf("planCompaction() error = %v", err)
	}
	if plan.FirstKeptEntryID == "tool-1" {
		t.Fatal("compaction boundary must not start at ToolResult")
	}
	if plan.FirstKeptEntryID != "a-tool" && plan.FirstKeptEntryID != "u2" {
		t.Fatalf("unexpected safe boundary %q", plan.FirstKeptEntryID)
	}
}

func TestPlanCompactionCarriesPreviousSummary(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{
		plannerEntry("u1", transcript.RoleUser, "old"),
		plannerEntry("a1", transcript.RoleAssistant, "old"),
		plannerEntry("u2", transcript.RoleUser, "recent-a"),
		plannerEntry("a2", transcript.RoleAssistant, "recent-b"),
		{Type: transcript.EntryCompaction, ID: "cmp1", Summary: "previous checkpoint", FirstKeptEntryID: "u2"},
		plannerEntry("u3", transcript.RoleUser, "more-a"),
		plannerEntry("a3", transcript.RoleAssistant, "more-b"),
		plannerEntry("u4", transcript.RoleUser, "latest-a"),
		plannerEntry("a4", transcript.RoleAssistant, "latest-b"),
	}

	plan, err := planCompaction(transcript.Document{ActiveBranch: branch}, 20, plannerEstimator{})
	if err != nil {
		t.Fatalf("planCompaction() error = %v", err)
	}
	if plan.PreviousSummary != "previous checkpoint" {
		t.Fatalf("PreviousSummary = %q", plan.PreviousSummary)
	}
	if plan.FirstKeptEntryID == "u1" || plan.FirstKeptEntryID == "a1" {
		t.Fatal("repeated compaction must not re-summarize history older than previous firstKept")
	}
}
