package contextengine

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitTextByEstimatedTokensPreservesAllHistoryInOrder(t *testing.T) {
	t.Parallel()

	original := strings.Repeat("前", 31) + strings.Repeat("middle-", 20) + strings.Repeat("后", 29)
	remaining := original
	var rebuilt strings.Builder
	for remaining != "" {
		chunk, rest := splitTextByEstimatedTokens(remaining, 40, plannerEstimator{})
		if chunk == "" {
			t.Fatal("split returned empty chunk")
		}
		if !utf8.ValidString(chunk) || !utf8.ValidString(rest) {
			t.Fatal("split produced invalid UTF-8")
		}
		rebuilt.WriteString(chunk)
		remaining = rest
	}
	// splitTextByEstimatedTokens trims only segment boundaries. Use content without boundary spaces
	// so exact equality verifies that no middle section was silently omitted.
	if rebuilt.String() != original {
		t.Fatalf("rebuilt history differs: got %q want %q", rebuilt.String(), original)
	}
}

func TestTruncateTextOnlyTruncatesSingleLocalField(t *testing.T) {
	t.Parallel()

	got := truncateText(strings.Repeat("中", 20), 8)
	if !strings.HasPrefix(got, strings.Repeat("中", 8)) {
		t.Fatalf("unexpected prefix: %q", got)
	}
	if !strings.Contains(got, "truncated for local context field") {
		t.Fatalf("missing local truncation marker: %q", got)
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
