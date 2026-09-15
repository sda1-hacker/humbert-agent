package contextengine

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

func TestMidRunHandlerKeepsStateWhenBelowThreshold(t *testing.T) {
	t.Parallel()

	handler := &MidRunCompactor{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		config: ContextMiddlewareConfig{
			SessionID:   "session-handler",
			Instruction: "system",
			Budget: Budget{
				ContextWindow:    4096,
				ThresholdTokens:  3000,
				KeepRecentTokens: 1000,
			},
			Estimator: plannerEstimator{},
			Logger:    logging.NewBootstrap(),
		},
	}
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("hello"),
	}}

	returnedCtx, returnedState, err := handler.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{})
	if err != nil {
		t.Fatalf("BeforeModelRewriteState() error = %v", err)
	}
	if returnedCtx == nil || returnedState != state {
		t.Fatal("handler must preserve context/state identity when compaction is unnecessary")
	}
	if len(returnedState.Messages) != 2 || returnedState.Messages[1].Content != "hello" {
		t.Fatalf("unexpected state mutation: %#v", returnedState.Messages)
	}
}
