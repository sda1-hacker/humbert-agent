package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecutionLimitStateCountsAttemptedToolCalls(t *testing.T) {
	state := &executionLimitState{maxToolCalls: 2}
	if err := state.beforeToolCall(); err != nil {
		t.Fatal(err)
	}
	if err := state.beforeToolCall(); err != nil {
		t.Fatal(err)
	}
	if err := state.beforeToolCall(); !errors.Is(err, ErrExecutionLimitExceeded) {
		t.Fatalf("error=%v want ErrExecutionLimitExceeded", err)
	}
}

func TestConfigureExecutionLimitsInstallsModelCounter(t *testing.T) {
	model := &countingBudgetModel{}
	snapshot := &Snapshot{Model: model}
	err := configureExecutionLimits(snapshot, ExecutionLimits{
		MaxDuration: time.Minute, MaxModelCalls: 2, MaxToolCalls: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.limitState == nil {
		t.Fatal("budget state missing")
	}
	for range 2 {
		if _, err := snapshot.Model.Generate(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := snapshot.Model.Generate(context.Background(), nil); !errors.Is(err, ErrExecutionLimitExceeded) {
		t.Fatalf("error=%v want ErrExecutionLimitExceeded", err)
	}
	if model.calls.Load() != 2 {
		t.Fatalf("actual calls=%d", model.calls.Load())
	}

}

func TestConfigureExecutionLimitsRejectsNegativeValues(t *testing.T) {
	err := configureExecutionLimits(&Snapshot{}, ExecutionLimits{MaxToolCalls: -1})
	if err == nil {
		t.Fatal("expected negative execution limit to fail")
	}
}

func TestExecutionLimitStopsAfterReportedTokenBudget(t *testing.T) {
	state := &executionLimitState{maxTotalTokens: 1000}
	if err := state.beforeModelCall(); err != nil {
		t.Fatal(err)
	}
	state.addTokens(600)
	if err := state.beforeToolCall(); err != nil {
		t.Fatal(err)
	}
	state.addTokens(400)
	if err := state.beforeToolCall(); !errors.Is(err, ErrExecutionLimitExceeded) {
		t.Fatalf("tool error=%v", err)
	}
	if err := state.beforeModelCall(); !errors.Is(err, ErrExecutionLimitExceeded) {
		t.Fatalf("model error=%v", err)
	}
}
