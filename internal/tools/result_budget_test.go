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

func TestGeneralResultsCannotConsumeRecoveryReserve(t *testing.T) {
	budget := NewResultBudgetWithRecovery(10, 3)

	if !budget.reserveFull(7) {
		t.Fatal("general result should fill the general pool")
	}
	if budget.reserveFull(1) {
		t.Fatal("general result consumed protected recovery reserve")
	}
	if got := budget.GeneralRemaining(); got != 0 {
		t.Fatalf("general remaining=%d, want 0", got)
	}
	if got := budget.RecoveryRemaining(); got != 3 {
		t.Fatalf("recovery remaining=%d, want 3", got)
	}
}

func TestRecoveryCanBorrowUnusedGeneralCapacity(t *testing.T) {
	budget := NewResultBudgetWithRecovery(10, 3)

	if !budget.reserveRecovery(8) {
		t.Fatal("recovery should be able to use unused general capacity")
	}
	if got := budget.RecoveryRemaining(); got != 2 {
		t.Fatalf("recovery remaining=%d, want 2", got)
	}
	if budget.reserveFull(3) {
		t.Fatal("general result exceeded total budget after recovery borrowed capacity")
	}
	if !budget.reserveFull(2) {
		t.Fatal("remaining total/general capacity should still be usable")
	}
}

func TestSeedUsedDoesNotChargeHistoricalToolResultsToCurrentTurn(t *testing.T) {
	budget := NewResultBudgetWithRecovery(10, 3)
	budget.SeedUsed(10)

	if !budget.reserveFull(7) {
		t.Fatal("historical ToolResults must not consume the current turn general budget")
	}
	if !budget.reserveRecovery(3) {
		t.Fatal("historical ToolResults must not consume the current turn recovery budget")
	}
	used, total := budget.Usage()
	if used != 10 || total != 10 {
		t.Fatalf("usage=%d/%d, want 10/10", used, total)
	}
}

func TestAggregateToolResultBudgetArchivesOverflowWithoutConsumingRecoveryReserve(t *testing.T) {
	ctx := context.Background()
	archive := &resultBudgetArchiver{}
	budget := NewResultBudgetWithRecovery(20, 6)
	tool := &guardedInvokableTool{
		descriptor: Descriptor{Name: "read_file"},
		scope: Scope{
			SessionID:          "session",
			ToolResultMaxChars: 100,
			ToolResultBudget:   budget,
		},
		archiver: archive,
	}

	first, err := tool.protectLargeResult(ctx, "1234567890")
	if err != nil || first != "1234567890" {
		t.Fatalf("first=%q err=%v", first, err)
	}

	second, err := tool.protectLargeResult(ctx, "abcdefghij")
	if err != nil {
		t.Fatal(err)
	}
	var pointer struct {
		ArtifactID string `json:"artifact_id"`
		Head       string `json:"head"`
		Tail       string `json:"tail"`
	}
	if err := json.Unmarshal([]byte(second), &pointer); err != nil || pointer.ArtifactID == "" {
		t.Fatalf("missing archive pointer: %q err=%v", second, err)
	}
	if archive.originals[pointer.ArtifactID] != "abcdefghij" {
		t.Fatal("archived result was not complete")
	}
	if got := len([]rune(pointer.Head + pointer.Tail)); got != 4 {
		t.Fatalf("preview chars=%d, want 4", got)
	}

	if got := budget.GeneralRemaining(); got != 0 {
		t.Fatalf("general remaining=%d, want 0", got)
	}
	if got := budget.RecoveryRemaining(); got != 6 {
		t.Fatalf("archive preview consumed protected recovery reserve: remaining=%d", got)
	}
}

func TestContextResourceUsesRecoveryBudgetAndNeverCreatesAnotherArtifact(t *testing.T) {
	archive := &resultBudgetArchiver{}
	budget := NewResultBudgetWithRecovery(10, 4)

	// Exhaust the normal/general pool first.
	if !budget.reserveFull(6) {
		t.Fatal("failed to fill general pool")
	}

	tool := &guardedInvokableTool{
		descriptor: Descriptor{Name: "context_resource"},
		scope: Scope{
			SessionID:          "session",
			ToolResultMaxChars: 20,
			ToolResultBudget:   budget,
		},
		archiver: archive,
	}

	result, err := tool.protectLargeResult(context.Background(), "1234")
	if err != nil || result != "1234" {
		t.Fatalf("recovery result=%q err=%v", result, err)
	}

	result, err = tool.protectLargeResult(context.Background(), "x")
	if err != nil || !strings.Contains(result, "budget_exhausted") {
		t.Fatalf("exhausted result=%q err=%v", result, err)
	}
	if len(archive.originals) != 0 {
		t.Fatalf("context_resource recursively archived %d results", len(archive.originals))
	}
}

func TestDefaultRecoveryReserveKeepsMeaningfulCapacity(t *testing.T) {
	budget := NewResultBudget(32768)
	generalUsed, generalLimit := budget.GeneralUsage()
	if generalUsed != 0 {
		t.Fatalf("general used=%d, want 0", generalUsed)
	}
	if generalLimit != 24576 {
		t.Fatalf("general limit=%d, want 24576", generalLimit)
	}
	_, reserve := budget.RecoveryUsage()
	if reserve != 8192 {
		t.Fatalf("recovery reserve=%d, want 8192", reserve)
	}
}
