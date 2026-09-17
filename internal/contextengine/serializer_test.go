package contextengine

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func TestSerializeCompactionPlanIncludesPreviousCheckpointAndTruncatesToolResult(t *testing.T) {
	t.Parallel()

	plan := Plan{
		PreviousSummary: "old checkpoint",
		ToSummarize: []transcript.Entry{{
			Type: transcript.EntryMessage,
			ID:   "tool",
			Message: &transcript.AgentMessage{
				Role:       transcript.RoleToolResult,
				ToolName:   "read_file",
				ToolCallID: "call-1",
				Content:    []transcript.ContentBlock{{Type: transcript.ContentText, Text: strings.Repeat("中", 500)}},
				Timestamp:  1,
			},
		}},
	}

	serialized := serializeCompactionPlan(plan, 256)
	if !strings.Contains(serialized, "[Previous checkpoint]\nold checkpoint") {
		t.Fatalf("previous checkpoint missing: %s", serialized)
	}
	if !strings.Contains(serialized, "...[truncated for compaction]") {
		t.Fatalf("expected tool result truncation: %s", serialized)
	}
}

func TestSerializeCompactionPlanPreservesAttachmentMeaningAndGlobalLimit(t *testing.T) {
	t.Parallel()
	plan := Plan{ToSummarize: []transcript.Entry{{
		Type: transcript.EntryMessage,
		ID:   "user",
		Message: &transcript.AgentMessage{
			Role: transcript.RoleUser,
			Content: []transcript.ContentBlock{
				{Type: transcript.ContentText, Text: strings.Repeat("old ", 200)},
				{Type: transcript.ContentImage, Name: "diagram.png", MIMEType: "image/png", SizeBytes: 42},
				{Type: transcript.ContentFile, Name: "notes.txt", MIMEType: "text/plain", SizeBytes: 12, ExtractedText: "important file fact"},
			},
			Timestamp: 1,
		},
	}}}

	serialized := serializeCompactionPlanWithLimit(plan, 256, 600)
	if utf8.RuneCountInString(serialized) > 600 {
		t.Fatalf("serialized rune count = %d", utf8.RuneCountInString(serialized))
	}
	for _, expected := range []string{"diagram.png", "notes.txt", "important file fact"} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("attachment context %q missing: %s", expected, serialized)
		}
	}
}

func TestNormalizeSummaryResultRequiresStableCheckpointStructure(t *testing.T) {
	t.Parallel()

	valid := `## Goal
G
## Constraints & Preferences
C
## Progress
### Done
D
### In Progress
I
### Blocked
B
## Key Decisions
K
## Next Steps
N
## Critical Context
X`
	if _, err := normalizeSummaryResult(valid); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
	if _, err := normalizeSummaryResult("## Goal\nonly goal"); err == nil {
		t.Fatal("expected incomplete checkpoint to be rejected")
	}

	fenced := "```markdown\n" + valid + "\n```"
	got, err := normalizeSummaryResult(fenced)
	if err != nil {
		t.Fatalf("fenced valid checkpoint rejected: %v", err)
	}
	if got != valid {
		t.Fatalf("fenced checkpoint normalization = %q, want original checkpoint", got)
	}

	if _, err := normalizeSummaryResult("说明\n" + valid); err == nil {
		t.Fatal("expected checkpoint preamble to be rejected")
	}
}

func TestSummarizeToolArgumentsRecursivelyOmitsSensitiveFields(t *testing.T) {
	t.Parallel()

	arguments := []byte(`{
		"path":"internal/contextengine/engine.go",
		"content":"large-or-sensitive-body",
		"Authorization":"Bearer secret-value",
		"nested":{"api-key":"sk-secret","query":"keep-query"},
		"items":[{"refresh.token":"refresh-secret","name":"visible"}]
	}`)

	got := summarizeToolArguments(arguments, 4096)
	for _, secret := range []string{"large-or-sensitive-body", "secret-value", "sk-secret", "refresh-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sensitive value %q leaked into compaction arguments: %s", secret, got)
		}
	}
	for _, expected := range []string{"internal/contextengine/engine.go", "keep-query", "visible", "[omitted]"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected %q in sanitized compaction arguments: %s", expected, got)
		}
	}
}
