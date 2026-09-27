package runtime

import (
	"errors"
	"fmt"
	"sync/atomic"
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
	if s == nil {
		return nil
	}
	return reserveCall(&s.modelCalls, s.maxModelCalls, "模型")
}

func (s *executionLimitState) beforeToolCall() error {
	if err := s.checkTokens(); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	return reserveCall(&s.toolCalls, s.maxToolCalls, "工具")
}

// 并发子 Agent 在调用前原子预留次数；被拒绝的调用不增加已执行次数。
func reserveCall(counter *atomic.Int64, limit int64, kind string) error {
	for {
		current := counter.Load()
		if limit > 0 && current >= limit {
			return fmt.Errorf("%w: %s调用次数达到 %d", ErrExecutionLimitExceeded, kind, limit)
		}
		if counter.CompareAndSwap(current, current+1) {
			return nil
		}
	}
}

func (s *executionLimitState) checkTokens() error {
	// Token 按 Provider 已报告用量累计，达到上限后禁止后续调用；不是调用前精确配额。
	// 当前响应或已经在途的并发响应仍可能跨过阈值，未报告的用量不能伪造为精确统计。
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
	snapshot.limitState = state
	snapshot.Model = trackModel(snapshot.Model, snapshot)
	return nil
}
