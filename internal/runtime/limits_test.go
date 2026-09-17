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
	snapshot := &Snapshot{}
	err := configureExecutionLimits(snapshot, ExecutionLimits{
		MaxDuration: time.Minute, MaxModelCalls: 2, MaxToolCalls: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.limitState == nil || len(snapshot.AgentHandlers) != 1 {
		t.Fatalf("limits were not installed: %+v", snapshot.ExecutionLimits)
	}
	middleware, ok := snapshot.AgentHandlers[0].(*executionLimitMiddleware)
	if !ok {
		t.Fatalf("handler type=%T", snapshot.AgentHandlers[0])
	}
	for index := 0; index < 2; index++ {
		if _, _, err := middleware.BeforeModelRewriteState(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := middleware.BeforeModelRewriteState(context.Background(), nil, nil); !errors.Is(err, ErrExecutionLimitExceeded) {
		t.Fatalf("error=%v want ErrExecutionLimitExceeded", err)
	}
}

func TestConfigureExecutionLimitsRejectsNegativeValues(t *testing.T) {
	err := configureExecutionLimits(&Snapshot{}, ExecutionLimits{MaxToolCalls: -1})
	if err == nil {
		t.Fatal("expected negative execution limit to fail")
	}
}
