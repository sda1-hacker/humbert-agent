package workspaceview

import (
	"encoding/json"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// TestExtractArtifactCandidatesOnlyAcceptsSuccessfulTransactions 确认“产物”不是看到 ToolCall
// 就算成功。只有对应 ToolResult 明确成功后，文件才进入可审计产物集合。
func TestExtractArtifactCandidatesOnlyAcceptsSuccessfulTransactions(t *testing.T) {
	t.Parallel()
	parent := "assistant-1"
	document := transcript.Document{
		ActiveBranch: []transcript.Entry{
			{
				Type:      transcript.EntryMessage,
				ID:        parent,
				Timestamp: "2026-09-18T10:00:00Z",
				Message: &transcript.AgentMessage{
					Role: transcript.RoleAssistant,
					Content: []transcript.ContentBlock{
						{Type: transcript.ContentToolCall, ID: "call-ok", Name: "write_file", Arguments: json.RawMessage(`{"path":"report.md","content":"ok"}`)},
						{Type: transcript.ContentToolCall, ID: "call-failed", Name: "edit_file", Arguments: json.RawMessage(`{"path":"secret.md"}`)},
					},
				},
			},
			{
				Type:      transcript.EntryMessage,
				ID:        "tool-ok",
				ParentID:  &parent,
				Timestamp: "2026-09-18T10:00:01Z",
				Message: &transcript.AgentMessage{
					Role:       transcript.RoleToolResult,
					ToolCallID: "call-ok",
					ToolName:   "write_file",
					Content:    []transcript.ContentBlock{{Type: transcript.ContentText, Text: `{"path":"report.md","bytes_written":2,"created":true}`}},
				},
			},
			{
				Type:      transcript.EntryMessage,
				ID:        "tool-failed",
				Timestamp: "2026-09-18T10:00:02Z",
				Message: &transcript.AgentMessage{
					Role:       transcript.RoleToolResult,
					ToolCallID: "call-failed",
					ToolName:   "edit_file",
					IsError:    true,
					Content:    []transcript.ContentBlock{{Type: transcript.ContentText, Text: `permission denied`}},
				},
			},
		},
	}

	values := extractArtifactCandidates(document)
	if len(values) != 1 {
		t.Fatalf("artifact candidates = %d, want 1: %+v", len(values), values)
	}
	if values[0].path != "report.md" || values[0].operation != "created" {
		t.Fatalf("candidate = %+v", values[0])
	}
}

func TestNormalizeCurrentWorkspacePathRejectsOutsideAbsolutePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if value, ok := normalizeCurrentWorkspacePath(root, "docs/report.md"); !ok || value != "docs/report.md" {
		t.Fatalf("relative value = %q, ok=%v", value, ok)
	}
	if value, ok := normalizeCurrentWorkspacePath(root, root+"/docs/report.md"); !ok || value != "docs/report.md" {
		t.Fatalf("absolute value = %q, ok=%v", value, ok)
	}
	if _, ok := normalizeCurrentWorkspacePath(root, t.TempDir()+"/outside.md"); ok {
		t.Fatal("outside absolute path should be rejected")
	}
}
