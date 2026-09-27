package runtime

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/models"
)

type countingBudgetModel struct {
	calls         atomic.Int64
	chunks        []*schema.Message
	responseUsage *schema.TokenUsage
}

func (m *countingBudgetModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	m.calls.Add(1)
	message := schema.AssistantMessage("short summary", nil)
	message.ResponseMeta = &schema.ResponseMeta{Usage: m.responseUsage}
	return message, nil
}
func (m *countingBudgetModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.calls.Add(1)
	return schema.StreamReaderFromArray(m.chunks), nil
}
func (m *countingBudgetModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

func TestSummaryCallsShareTaskBudget(t *testing.T) {
	model := &countingBudgetModel{}
	snapshot := &Snapshot{Model: model}
	if err := configureExecutionLimits(snapshot, ExecutionLimits{MaxModelCalls: 1}); err != nil {
		t.Fatal(err)
	}
	handler, err := contextengine.NewMidRunCompactor(contextengine.ContextMiddlewareConfig{
		SessionID: "test", Model: trackModel(model, snapshot), Estimator: contextengine.NewApproxEstimator(),
		CompactionContextWindow: 32768, CompactionMaxOutputTokens: 1024,
		Budget: contextengine.Budget{ContextWindow: 32768, ThresholdTokens: 6000, SoftThresholdTokens: 1000, TargetRecentTokens: 1, CheckpointBudgetTokens: 512},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		schema.UserMessage(strings.Repeat("历史需求", 1000)), schema.AssistantMessage(strings.Repeat("已经完成工作", 1000), nil), schema.UserMessage("继续"),
	}}
	_, state, err = handler.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("summary calls=%d", model.calls.Load())
	}
	if _, err := snapshot.Model.Generate(context.Background(), state.Messages); !errors.Is(err, ErrExecutionLimitExceeded) {
		t.Fatalf("main model bypassed summary budget: %v", err)
	}
	if model.calls.Load() != 1 || snapshot.limitState.modelCalls.Load() != 1 {
		t.Fatal("rejected call was executed or double counted")
	}
}

func TestChildModelAndToolsInheritParentBudget(t *testing.T) {
	model := &countingBudgetModel{}
	parent := &Snapshot{Model: model}
	if err := configureExecutionLimits(parent, ExecutionLimits{MaxModelCalls: 2, MaxToolCalls: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Model.Generate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	endpoint := buildToolLifecycleMiddleware(parent)(func(ctx context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		// 与 BuildChildAgent 一样，从父工具调用上下文继承共享预算。
		child := &Snapshot{limitState: limitStateFromContext(ctx)}
		if child.limitState != parent.limitState {
			t.Fatal("child received another budget")
		}
		bound, err := trackModel(model, child).WithTools(nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bound.Generate(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := bound.Generate(ctx, nil); !errors.Is(err, ErrExecutionLimitExceeded) {
			t.Fatalf("child model bypassed limit: %v", err)
		}
		called := false
		_, err = buildToolLifecycleMiddleware(child)(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
			called = true
			return &compose.ToolOutput{}, nil
		})(ctx, &compose.ToolInput{Name: "child_tool"})
		if called || !errors.Is(err, ErrExecutionLimitExceeded) {
			t.Fatalf("child tool bypassed limit: %v", err)
		}
		return &compose.ToolOutput{}, nil
	})
	if _, err := endpoint(context.Background(), &compose.ToolInput{Name: "run_agent"}); err != nil {
		t.Fatal(err)
	}
	if model.calls.Load() != 2 {
		t.Fatalf("model calls=%d", model.calls.Load())
	}
}

func TestStreamUsageCountsCumulativeTokensOnce(t *testing.T) {
	message := func(input, output int) *schema.Message {
		return &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: input, CompletionTokens: output, TotalTokens: input + output}}}
	}
	model := &countingBudgetModel{chunks: []*schema.Message{message(10, 0), message(10, 5), message(10, 5), message(10, 10)}}
	snapshot := &Snapshot{Model: model}
	if err := configureExecutionLimits(snapshot, ExecutionLimits{MaxTotalTokens: 20}); err != nil {
		t.Fatal(err)
	}
	stream, err := snapshot.Model.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Eino 会把同一个流分给模型状态与事件消费者；复制不能重复统计 Usage。
	for _, copy := range stream.Copy(2) {
		for {
			_, err := copy.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		copy.Close()
	}
	if got := snapshot.limitState.totalTokens.Load(); got != 20 {
		t.Fatalf("tokens=%d want 20", got)
	}
	if _, err := snapshot.Model.Generate(context.Background(), nil); !errors.Is(err, ErrExecutionLimitExceeded) {
		t.Fatalf("token limit not enforced: %v", err)
	}
}

func TestConcurrentModelsCannotOverbookTaskBudget(t *testing.T) {
	model := &countingBudgetModel{}
	snapshot := &Snapshot{Model: model}
	if err := configureExecutionLimits(snapshot, ExecutionLimits{MaxModelCalls: 3}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, err := snapshot.Model.Generate(context.Background(), nil)
			if err != nil && !errors.Is(err, ErrExecutionLimitExceeded) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if model.calls.Load() != 3 || snapshot.limitState.modelCalls.Load() != 3 {
		t.Fatalf("calls=%d budget=%d", model.calls.Load(), snapshot.limitState.modelCalls.Load())
	}
}

func TestToolAuxiliaryModelUsesSharedBudget(t *testing.T) {
	model := &countingBudgetModel{responseUsage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}
	parent := &Snapshot{Model: model}
	if err := configureExecutionLimits(parent, ExecutionLimits{MaxModelCalls: 1}); err != nil {
		t.Fatal(err)
	}
	endpoint := buildToolLifecycleMiddleware(parent)(func(ctx context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		auxiliary := TrackAuxiliaryModel(ctx, models.RuntimeSnapshot{Instance: model, ModelConfigID: "image"}, modelRoleImage)
		if _, err := auxiliary.Generate(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := parent.Model.Generate(ctx, nil); !errors.Is(err, ErrExecutionLimitExceeded) {
			t.Fatalf("auxiliary did not consume parent budget: %v", err)
		}
		return &compose.ToolOutput{}, nil
	})
	if _, err := endpoint(context.Background(), &compose.ToolInput{Name: "browser"}); err != nil {
		t.Fatal(err)
	}
	if parent.limitState.totalTokens.Load() != 15 {
		t.Fatalf("auxiliary usage=%d", parent.limitState.totalTokens.Load())
	}
	if model.calls.Load() != 1 {
		t.Fatalf("calls=%d", model.calls.Load())
	}
}
