package contextengine

import (
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/config"
)

func testContextConfig() config.ContextConfig {
	return config.ContextConfig{
		AutoCompaction:       true,
		SmallWindowThreshold: 64 * 1024,
		MinReserveTokens:     16 * 1024,
		LargeReserveRatio:    0.10,
		SmallReserveRatio:    0.20,
		KeepRecentRatio:      0.20,
		KeepRecentMinTokens:  8 * 1024,
		KeepRecentMaxTokens:  32 * 1024,
		SerializerMaxChars:   4000,
		MemoryTurnInterval:   10,
		MemoryTokenInterval:  12 * 1024,
		OperationTimeoutMS:   120000,
	}
}

func TestCalculateBudgetSmallAndLargeWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		contextWindow  int
		maxOutput      int
		wantReserve    int
		wantThreshold  int
		wantKeepRecent int
	}{
		{name: "16k", contextWindow: 16 * 1024, maxOutput: 4 * 1024, wantReserve: 4 * 1024, wantThreshold: 12 * 1024, wantKeepRecent: 8 * 1024},
		{name: "32k", contextWindow: 32 * 1024, maxOutput: 8 * 1024, wantReserve: 8 * 1024, wantThreshold: 24 * 1024, wantKeepRecent: 8 * 1024},
		{name: "128k", contextWindow: 128 * 1024, maxOutput: 8 * 1024, wantReserve: 16 * 1024, wantThreshold: 112 * 1024, wantKeepRecent: 26215},
		{name: "1m", contextWindow: 1024 * 1024, maxOutput: 32 * 1024, wantReserve: 104858, wantThreshold: 943718, wantKeepRecent: 32 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget, err := CalculateBudget(testContextConfig(), tt.contextWindow, tt.maxOutput)
			if err != nil {
				t.Fatalf("CalculateBudget() error = %v", err)
			}
			if budget.ReserveTokens != tt.wantReserve {
				t.Fatalf("ReserveTokens = %d, want %d", budget.ReserveTokens, tt.wantReserve)
			}
			if budget.ThresholdTokens != tt.wantThreshold {
				t.Fatalf("ThresholdTokens = %d, want %d", budget.ThresholdTokens, tt.wantThreshold)
			}
			if budget.KeepRecentTokens != tt.wantKeepRecent {
				t.Fatalf("KeepRecentTokens = %d, want %d", budget.KeepRecentTokens, tt.wantKeepRecent)
			}
		})
	}
}

func TestCalculateBudgetRejectsInvalidOutputLimit(t *testing.T) {
	t.Parallel()

	if _, err := CalculateBudget(testContextConfig(), 4096, 4096); err == nil {
		t.Fatal("expected error when MaxOutputTokens reaches ContextWindow")
	}
}
