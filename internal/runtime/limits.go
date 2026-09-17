package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/adk"
)

var ErrExecutionLimitExceeded = errors.New("任务运行已达到执行上限")

type executionLimitState struct {
	maxModelCalls int64
	maxToolCalls  int64
	modelCalls    atomic.Int64
	toolCalls     atomic.Int64
}

func (s *executionLimitState) beforeToolCall() error {
	if s == nil || s.maxToolCalls <= 0 {
		return nil
	}
	current := s.toolCalls.Add(1)
	if current > s.maxToolCalls {
		return fmt.Errorf("%w: 工具调用次数超过 %d", ErrExecutionLimitExceeded, s.maxToolCalls)
	}
	return nil
}

type executionLimitMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	snapshot *Snapshot
	state    *executionLimitState
}

func (m *executionLimitMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, modelContext *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if err := ctx.Err(); err != nil {
		return ctx, state, err
	}
	if m.state != nil && m.state.maxModelCalls > 0 {
		current := m.state.modelCalls.Add(1)
		if current > m.state.maxModelCalls {
			return ctx, state, fmt.Errorf("%w: 模型调用次数超过 %d", ErrExecutionLimitExceeded, m.state.maxModelCalls)
		}
	}
	if m.snapshot != nil {
		reportToolLifecycleEvent(ctx, m.snapshot, Event{
			Type:       EventModelStarted,
			OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return ctx, state, nil
}

var _ adk.ChatModelAgentMiddleware = (*executionLimitMiddleware)(nil)

func configureExecutionLimits(snapshot *Snapshot, limits ExecutionLimits) error {
	if snapshot == nil {
		return errors.New("Runtime Snapshot 不能为空")
	}
	if limits.MaxDuration < 0 || limits.MaxModelCalls < 0 || limits.MaxToolCalls < 0 {
		return errors.New("Execution Limits 不能为负数")
	}
	snapshot.ExecutionLimits = limits
	if limits.MaxModelCalls == 0 && limits.MaxToolCalls == 0 {
		return nil
	}
	state := &executionLimitState{maxModelCalls: int64(limits.MaxModelCalls), maxToolCalls: int64(limits.MaxToolCalls)}
	snapshot.limitState = state
	snapshot.AgentHandlers = append(snapshot.AgentHandlers, &executionLimitMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		snapshot:                     snapshot,
		state:                        state,
	})
	return nil
}
