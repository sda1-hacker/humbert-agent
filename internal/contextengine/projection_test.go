package contextengine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func projectionUser(id, text string) transcript.Entry {
	return transcript.Entry{Type: transcript.EntryMessage, ID: id, Message: &transcript.AgentMessage{
		Role: transcript.RoleUser, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: text}}, Timestamp: 1,
	}}
}

func projectionAssistant(id, text, thinking string, calls ...transcript.ContentBlock) transcript.Entry {
	content := make([]transcript.ContentBlock, 0, len(calls)+2)
	if thinking != "" {
		content = append(content, transcript.ContentBlock{Type: transcript.ContentThinking, Thinking: thinking})
	}
	if text != "" {
		content = append(content, transcript.ContentBlock{Type: transcript.ContentText, Text: text})
	}
	content = append(content, calls...)
	return transcript.Entry{Type: transcript.EntryMessage, ID: id, Message: &transcript.AgentMessage{
		Role: transcript.RoleAssistant, Content: content, Provider: "test", Model: "test", StopReason: transcript.StopReasonStop, Timestamp: 1,
	}}
}

func TestProjectActiveBranchUsesLatestCheckpointAndRecentRawMessages(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{
		projectionUser("u1", "old-user"),
		projectionAssistant("a1", "old-assistant", "old-thinking"),
		projectionUser("u2", "recent-user"),
		projectionAssistant("a2", "recent-assistant", "recent-thinking"),
		{Type: transcript.EntryCompaction, ID: "cmp1", Summary: "## Goal\nkeep working", FirstKeptEntryID: "u2"},
	}

	projection, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayAuto)
	if err != nil {
		t.Fatalf("projectActiveBranch() error = %v", err)
	}
	messages := projection.Messages
	if projection.LatestCompactionID != "cmp1" {
		t.Fatalf("compactionID = %q", projection.LatestCompactionID)
	}
	if len(messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(messages))
	}
	if messages[0].Role != schema.User || !strings.Contains(messages[0].Content, "keep working") {
		t.Fatalf("first message is not checkpoint: %#v", messages[0])
	}
	if strings.Contains(messages[0].Content, "old-user") || messages[1].Content != "recent-user" {
		t.Fatal("compressed raw history leaked into projection")
	}
	if messages[2].ReasoningContent != "recent-thinking" {
		t.Fatalf("reasoning was not preserved in auto mode: %q", messages[2].ReasoningContent)
	}
}

func TestProjectActiveBranchCanOmitReasoning(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{projectionAssistant("a1", "answer", "private-reasoning")}
	projection, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayOmit)
	if err != nil {
		t.Fatalf("projectActiveBranch() error = %v", err)
	}
	messages := projection.Messages
	if len(messages) != 1 || messages[0].ReasoningContent != "" {
		t.Fatalf("reasoning should be omitted: %#v", messages)
	}
	if messages[0].Content != "answer" {
		t.Fatalf("visible answer was changed: %q", messages[0].Content)
	}
}

func TestValidateProjectedToolTransactionsRejectsDanglingResult(t *testing.T) {
	t.Parallel()

	tool := schema.ToolMessage("result", "call-1", schema.WithToolName("read_file"))
	if err := validateProjectedToolTransactions([]*schema.Message{tool}); err == nil {
		t.Fatal("expected dangling ToolResult to be rejected")
	}

	assistant := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		ID: "call-1", Type: "function", Function: schema.FunctionCall{Name: "read_file", Arguments: string(json.RawMessage(`{"path":"a"}`))},
	}}}
	if err := validateProjectedToolTransactions([]*schema.Message{assistant, tool}); err != nil {
		t.Fatalf("valid tool transaction rejected: %v", err)
	}
}
