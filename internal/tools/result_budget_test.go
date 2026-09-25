package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type resultBudgetArchiver struct{ originals map[string]string }

func (a *resultBudgetArchiver) Archive(_ context.Context, _, _, content string) (string, error) {
	if a.originals == nil {
		a.originals = make(map[string]string)
	}
	id := "artifact-" + string(rune('0'+len(a.originals)))
	a.originals[id] = content
	return id, nil
}
func TestAggregateToolResultBudgetArchivesCompleteOverflow(t *testing.T) {
	ctx := context.Background()
	archive := &resultBudgetArchiver{}
	budget := NewResultBudget(10)
	tool := &guardedInvokableTool{descriptor: Descriptor{Name: "read_file"}, scope: Scope{SessionID: "session", ToolResultMaxChars: 100, ToolResultBudget: budget}, archiver: archive}
	first, err := tool.protectLargeResult(ctx, "123456")
	if err != nil || first != "123456" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := tool.protectLargeResult(ctx, "abcdef")
	if err != nil {
		t.Fatal(err)
	}
	var pointer struct {
		ArtifactID string `json:"artifact_id"`
		Head       string `json:"head"`
		Tail       string `json:"tail"`
	}
	if json.Unmarshal([]byte(second), &pointer) != nil || pointer.ArtifactID == "" {
		t.Fatalf("missing archive pointer: %q", second)
	}
	if archive.originals[pointer.ArtifactID] != "abcdef" {
		t.Fatal("archived result was not complete")
	}
	if len([]rune(pointer.Head+pointer.Tail)) > 2 {
		t.Fatalf("preview exceeded shared budget: %#v", pointer)
	}
	third, err := tool.protectLargeResult(ctx, strings.Repeat("z", 20))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(third), &pointer) != nil || len([]rune(pointer.Head+pointer.Tail)) > 1 {
		t.Fatalf("archive preview consumed reserved recovery budget: %q", third)
	}
	used, limit := budget.Usage()
	if used >= limit {
		t.Fatalf("archive previews left no recovery budget: usage=%d/%d", used, limit)
	}
}

func TestResultBudgetSeedsRetainedWindowResults(t *testing.T) {
	budget := NewResultBudget(10)
	budget.SeedUsed(8)
	if budget.reserveFull(3) {
		t.Fatal("new result exceeded remaining window budget")
	}
	if !budget.reserveFull(2) {
		t.Fatal("remaining window budget was lost")
	}
	used, _ := budget.Usage()
	if used != 10 {
		t.Fatalf("used=%d", used)
	}
}

func TestContextResourceResultNeverCreatesAnotherArtifact(t *testing.T) {
	archive := &resultBudgetArchiver{}
	budget := NewResultBudget(10)
	tool := &guardedInvokableTool{
		descriptor: Descriptor{Name: "context_resource"},
		scope:      Scope{SessionID: "session", ToolResultMaxChars: 20, ToolResultBudget: budget},
		archiver:   archive,
	}
	result, err := tool.protectLargeResult(context.Background(), "123456")
	if err != nil || result != "123456" {
		t.Fatalf("first=%q err=%v", result, err)
	}
	result, err = tool.protectLargeResult(context.Background(), "abcdef")
	if err != nil || !strings.Contains(result, "budget_exhausted") {
		t.Fatalf("second=%q err=%v", result, err)
	}
	if len(archive.originals) != 0 {
		t.Fatalf("context_resource recursively archived %d results", len(archive.originals))
	}
}
