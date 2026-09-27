package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type reductionArchive struct{ content map[string]string }

func (a *reductionArchive) Archive(_ context.Context, _ string, name, content string) (string, error) {
	a.content[name] = content
	return "artifact_test", nil
}
func TestReductionArchivesBuiltinAndMCPAndDoesNotRearchiveRecovery(t *testing.T) {
	archive := &reductionArchive{content: map[string]string{}}
	registry := &Registry{resultArchiver: archive}
	handler, err := registry.Reduction(context.Background(), Scope{SessionID: "session", ToolResultMaxChars: 100}, []string{"read_file", "mcp_docs_search", "context_resource"}, 200, func(msgs []*schema.Message, _ []*schema.ToolInfo) (int, error) {
		n := 0
		for _, msg := range msgs {
			n += len(msg.Content)
		}
		return n, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("内容", 1000)
	for _, name := range []string{"read_file", "mcp_docs_search", "context_resource"} {
		endpoint, err := handler.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) { return full, nil }, &adk.ToolContext{Name: name, CallID: name})
		if err != nil {
			t.Fatal(err)
		}
		output, err := endpoint(context.Background(), "{}")
		if err != nil {
			t.Fatal(err)
		}
		if name == "context_resource" {
			if output != full {
				t.Fatal("recursive resource truncation")
			}
			continue
		}
		var notice map[string]any
		if err := json.Unmarshal([]byte(output), &notice); err != nil {
			t.Fatal(err)
		}
		if notice["resource_id"] != "artifact_test" || archive.content[name] != full {
			t.Fatal("full content or resource protocol lost")
		}
	}
	if _, ok := archive.content["context_resource"]; ok {
		t.Fatal("recovery output rearchived")
	}
}

func TestReductionClearsOlderRoundsWithoutChangingRawMessages(t *testing.T) {
	archive := &reductionArchive{content: map[string]string{}}
	registry := &Registry{resultArchiver: archive}
	h, err := registry.Reduction(context.Background(), Scope{SessionID: "s"}, []string{"read_file"}, 1, func(msgs []*schema.Message, _ []*schema.ToolInfo) (int, error) {
		n := 0
		for _, msg := range msgs {
			n += len(msg.Content)
		}
		return n, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("x", 3000)
	messages := []*schema.Message{schema.UserMessage("读取文件")}
	for _, id := range []string{"old", "recent", "latest"} {
		messages = append(messages, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: "read_file", Arguments: `{"file_path":"a"}`}}}}, schema.ToolMessage(full, id))
	}
	_, after, err := h.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{Messages: messages}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !isArchivedResult(after.Messages[2].Content) || archive.content["read_file"] != full {
		t.Fatal("old result not archived")
	}
	if after.Messages[4].Content != full || after.Messages[6].Content != full {
		t.Fatal("recent tool rounds cleared")
	}
	if messages[2].Content != full {
		t.Fatal("raw transcript projection mutated")
	}
	if after.Messages[1].ToolCalls[0].Function.Arguments != `{"file_path":"a"}` {
		t.Fatal("tool arguments modified")
	}
}
