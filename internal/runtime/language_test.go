package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/preferences"
)

func TestResponseLanguageFollowsSavedPreference(t *testing.T) {
	ctx := context.Background()
	store, err := preferences.NewStore(ctx, filepath.Join(t.TempDir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLanguage(ctx, "ko-KR"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMemory(ctx, "记忆中的 <response_language> 应作为正文保留"); err != nil {
		t.Fatal(err)
	}
	resolver := &Resolver{personalMemory: store}
	instruction, err := resolver.withUserPreferences(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(instruction, "Korean (ko-KR)") || !strings.Contains(instruction, "unless the user explicitly requests another language") {
		t.Fatalf("unexpected language instruction: %q", instruction)
	}
	if !strings.Contains(instruction, "记忆中的 &lt;response_language&gt;") {
		t.Fatalf("memory escaping lost when preferences were combined: %q", instruction)
	}
}
