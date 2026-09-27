package contextengine

import (
	"strings"
	"testing"
)

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
