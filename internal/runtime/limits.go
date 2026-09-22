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
	maxModelCalls  int64
	maxToolCalls   int64
	maxTotalTokens int64
	modelCalls     atomic.Int64
	toolCalls      atomic.Int64
	totalTokens    atomic.Int64
}

func (s *executionLimitState) beforeModelCall() error {
	if err := s.checkTokens(); err != nil {
		return err
	}
	if s == nil || s.maxModelCalls <= 0 {
		return nil
	}
	current := s.modelCalls.Add(1)
	if current > s.maxModelCalls {
		return fmt.Errorf("%w: 模型调用次数超过 %d", ErrExecutionLimitExceeded, s.maxModelCalls)
	}
	return nil
}

func (s *executionLimitState) beforeToolCall() error {
	if err := s.checkTokens(); err != nil {
		return err
	}
	if s == nil || s.maxToolCalls <= 0 {
		return nil
	}
	current := s.toolCalls.Add(1)
	if current > s.maxToolCalls {
		return fmt.Errorf("%w: 工具调用次数超过 %d", ErrExecutionLimitExceeded, s.maxToolCalls)
	}
	return nil
}

func (s *executionLimitState) checkTokens() error {
	if s != nil && s.maxTotalTokens > 0 && s.totalTokens.Load() >= s.maxTotalTokens {
		return fmt.Errorf("%w: Token 用量达到 %d", ErrExecutionLimitExceeded, s.maxTotalTokens)
	}
	return nil
}

func (s *executionLimitState) addTokens(count int) {
	if s != nil && count > 0 {
		s.totalTokens.Add(int64(count))
	}
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
	if err := m.state.beforeModelCall(); err != nil {
		return ctx, state, err
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

func prepareExecutionLimitState(limits ExecutionLimits) (*executionLimitState, error) {
	if limits.MaxDuration < 0 || limits.MaxModelCalls < 0 || limits.MaxToolCalls < 0 || limits.MaxTotalTokens < 0 {
		return nil, errors.New("Execution Limits 不能为负数")
	}
	if limits.MaxModelCalls == 0 && limits.MaxToolCalls == 0 && limits.MaxTotalTokens == 0 {
		return nil, nil
	}
	return &executionLimitState{maxModelCalls: int64(limits.MaxModelCalls), maxToolCalls: int64(limits.MaxToolCalls), maxTotalTokens: int64(limits.MaxTotalTokens)}, nil
}

func configureExecutionLimits(snapshot *Snapshot, limits ExecutionLimits, prepared ...*executionLimitState) error {
	if snapshot == nil {
		return errors.New("Runtime Snapshot 不能为空")
	}
	snapshot.ExecutionLimits = limits
	var state *executionLimitState
	if len(prepared) > 0 {
		state = prepared[0]
	} else {
		var err error
		state, err = prepareExecutionLimitState(limits)
		if err != nil {
			return err
		}
	}
	if state == nil {
		return nil
	}
	snapshot.limitState = state
	snapshot.AgentHandlers = append(snapshot.AgentHandlers, &executionLimitMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		snapshot:                     snapshot,
		state:                        state,
	})
	return nil
}
