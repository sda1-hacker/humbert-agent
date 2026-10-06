package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

func TestTerminalEventFollowsReservationRelease(t *testing.T) {
	for _, runErr := range []error{nil, errors.New("provider failed"), context.Canceled} {
		t.Run(fmt.Sprint(runErr), func(t *testing.T) {
			repo := &blockedMaintenanceRepository{entered: make(chan struct{})}
			logger := logging.NewBootstrap()
			engine, err := contextengine.NewEngine(config.ContextConfig{AutoCompaction: true, OperationTimeoutMS: 10000}, repo, contextengine.NewApproxEstimator(), logger)
			if err != nil {
				t.Fatal(err)
			}
			events := eventbus.New()
			service := NewService(&Resolver{contextEngine: engine}, nil, nil, events, logger, nil)
			defer service.rootCancel()
			ctx, cancel := context.WithCancel(service.rootCtx)
			active := &activeRun{
				ctx: ctx, cancel: cancel,
				phase: RunPhaseRunning, startedAt: time.Now(), checkpointStore: approval.NewCheckpointStore(),
				Snapshot: &Snapshot{RequestID: "request", RunID: "run", SessionID: "session", Model: unusedModel{}, ContextWindow: 8192, MaxOutputTokens: 1024},
			}
			service.activeByRequest["request"] = active
			service.activeBySession["session"] = "request"
			service.reservationAgents["session"] = "agent"
			terminals := 0
			unsubscribe, err := events.Subscribe(TopicEvent, func(_ context.Context, payload any) {
				event := payload.(Event)
				if event.Type == EventTurnCompleted || event.Type == EventTurnFailed || event.Type == EventTurnCancelled {
					terminals++
					if err := service.reserveAgentSession("session", "agent", "next-request"); err != nil {
						t.Errorf("terminal event observed while session still busy: %v", err)
					} else {
						service.releaseReservation("session", "next-request")
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer unsubscribe()
			service.finishRun(active, ExecutionResult{}, runErr)
			service.finishRun(active, ExecutionResult{}, runErr)
			if terminals != 1 {
				t.Fatalf("terminal events=%d want=1", terminals)
			}
		})
	}
}
