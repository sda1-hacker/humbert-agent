package memory

import (
	"strings"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func TestNormalizeSummaryRequiresTwoStableSections(t *testing.T) {
	t.Parallel()

	got, err := normalizeSummary("### 重要事实\n- A\n\n### 事情经过\n- B")
	if err != nil {
		t.Fatalf("normalizeSummary() error = %v", err)
	}
	if !strings.Contains(got, "- A") || !strings.Contains(got, "- B") {
		t.Fatalf("normalized summary lost content: %q", got)
	}
	if _, err := normalizeSummary("### 重要事实\n- A"); err == nil {
		t.Fatal("expected missing timeline heading to fail")
	}

	fenced, err := normalizeSummary("```markdown\n### 重要事实\n- A\n\n### 事情经过\n- B\n```")
	if err != nil {
		t.Fatalf("fenced memory rejected: %v", err)
	}
	if !strings.Contains(fenced, "- A") || !strings.Contains(fenced, "- B") {
		t.Fatalf("fenced memory normalization lost content: %q", fenced)
	}
}

func TestSerializeSegmentExcludesThinkingAndSuccessfulToolResultBody(t *testing.T) {
	t.Parallel()

	entries := []transcript.Entry{
		{
			Type: transcript.EntryMessage,
			ID:   "assistant",
			Message: &transcript.AgentMessage{
				Role:      transcript.RoleAssistant,
				Provider:  "test",
				Model:     "test",
				Timestamp: 1,
				Content: []transcript.ContentBlock{
					{Type: transcript.ContentThinking, Thinking: "不要进入 Memory 的推理"},
					{Type: transcript.ContentText, Text: "已经完成读取"},
					{Type: transcript.ContentToolCall, ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"internal/a.go","content":"secret-body"}`)},
				},
			},
		},
		{
			Type: transcript.EntryMessage,
			ID:   "tool",
			Message: &transcript.AgentMessage{
				Role:       transcript.RoleToolResult,
				ToolCallID: "call-1",
				ToolName:   "read_file",
				Timestamp:  1,
				Content:    []transcript.ContentBlock{{Type: transcript.ContentText, Text: "完整文件正文不应进入 Memory"}},
			},
		},
	}

	serialized, artifacts := serializeSegment(entries, 512)
	if strings.Contains(serialized, "不要进入 Memory 的推理") {
		t.Fatal("thinking leaked into memory input")
	}
	if strings.Contains(serialized, "完整文件正文不应进入 Memory") || strings.Contains(serialized, "secret-body") {
		t.Fatal("successful tool body leaked into memory input")
	}
	if !strings.Contains(serialized, "已经完成读取") {
		t.Fatal("assistant outcome missing from memory input")
	}
	if len(artifacts.ReadFiles) != 1 || artifacts.ReadFiles[0] != "internal/a.go" {
		t.Fatalf("ReadFiles = %#v", artifacts.ReadFiles)
	}
}

func TestCompactArgumentsRecursivelyOmitsSensitiveAndBodyFields(t *testing.T) {
	t.Parallel()

	arguments := []byte(`{
		"path":"internal/runtime/service.go",
		"content":"file-body",
		"Authorization":"Bearer secret-value",
		"nested":{"apiKey":"sk-sensitive","query":"keep-me"},
		"items":[{"refresh-token":"refresh-secret","name":"visible"}]
	}`)

	serialized := compactArguments(arguments, 2048)
	for _, secret := range []string{"file-body", "secret-value", "sk-sensitive", "refresh-secret"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("sensitive value %q leaked into memory arguments: %s", secret, serialized)
		}
	}
	for _, expected := range []string{"internal/runtime/service.go", "keep-me", "visible", "[omitted]"} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("expected %q in sanitized arguments: %s", expected, serialized)
		}
	}
}

func TestRebuildSegmentUsesCheckpointOnceAndPreservesLogicalOrder(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{
		{Type: transcript.EntryMessage, ID: "u1", Message: &transcript.AgentMessage{Role: transcript.RoleUser}},
		{Type: transcript.EntryMessage, ID: "u2", Message: &transcript.AgentMessage{Role: transcript.RoleUser}},
		{Type: transcript.EntryMessage, ID: "a2", Message: &transcript.AgentMessage{Role: transcript.RoleAssistant}},
		{Type: transcript.EntryCompaction, ID: "cmp1", Summary: "checkpoint", FirstKeptEntryID: "u2"},
		{Type: transcript.EntryMessage, ID: "u3", Message: &transcript.AgentMessage{Role: transcript.RoleUser}},
	}

	segment := rebuildSegment(branch)
	got := make([]string, 0, len(segment))
	for _, entry := range segment {
		got = append(got, entry.ID)
	}
	want := []string{"cmp1", "u2", "a2", "u3"}
	if len(got) != len(want) {
		t.Fatalf("rebuildSegment IDs = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("rebuildSegment IDs = %#v, want %#v", got, want)
		}
	}
}
