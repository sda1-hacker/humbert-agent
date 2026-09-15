package contextengine

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func TestToolTransactionsRejectIncompleteAndDuplicateResults(t *testing.T) {
	call := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call", Function: schema.FunctionCall{Name: "write_file"}}}}
	result := schema.ToolMessage("ok", "call")
	for name, messages := range map[string][]*schema.Message{
		"missing result":   {call},
		"next user":        {call, schema.UserMessage("continue")},
		"duplicate result": {call, result, result},
		"orphan result":    {result},
		"duplicate call":   {{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call"}, {ID: "call"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateProjectedToolTransactions(messages); err == nil {
				t.Fatal("invalid transaction accepted")
			}
		})
	}
}

func TestProjectionRecoversInterruptedHistoryWithoutReplayingOrMutating(t *testing.T) {
	for _, withNextUser := range []bool{false, true} {
		branch := []transcript.Entry{
			projectionUser("u1", "write files"),
			projectionAssistant("a1", "", "",
				transcript.ContentBlock{Type: transcript.ContentToolCall, ID: "done", Name: "write_file", Arguments: []byte(`{}`)},
				transcript.ContentBlock{Type: transcript.ContentToolCall, ID: "unknown", Name: "write_file", Arguments: []byte(`{}`)}),
			{Type: transcript.EntryMessage, ID: "t1", Message: &transcript.AgentMessage{
				Role: transcript.RoleToolResult, ToolCallID: "done", ToolName: "write_file", Timestamp: 1,
				Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: "written"}},
			}},
		}
		if withNextUser {
			branch = append(branch, projectionUser("u2", "continue"))
		}
		projection, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayAuto)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateProjectedToolTransactions(projection.Messages); err != nil {
			t.Fatal(err)
		}
		if len(projection.Messages) != len(branch)+1 || projection.Messages[2].Content != "written" {
			t.Fatal("recorded results changed")
		}
		recovered := projection.Messages[3]
		if recovered.ToolCallID != "unknown" || !strings.Contains(recovered.Content, `"status":"unknown"`) {
			t.Fatalf("missing explicit unknown result: %#v", recovered)
		}
		if len(branch[1].Message.Content) != 2 || branch[2].Message.Content[0].Text != "written" {
			t.Fatal("original transcript mutated")
		}
		if len(closeInterruptedToolCalls(projection.Messages)) != len(projection.Messages) {
			t.Fatal("recovery is not idempotent")
		}
	}
}
