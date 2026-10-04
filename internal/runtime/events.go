package runtime

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"

	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
)

// 本文件负责运行状态的 UI 投影及错误展示。实时事件不写入 Transcript；
// 展示投影应复制可变切片，避免前端查询或观察者影响已经冻结的 Turn。
func (s *Service) deltaEmitter(snapshot *Snapshot) DeltaEmitter {
	return func(eventType EventType, delta string) {
		if delta == "" {
			return
		}
		s.publishEvent(Event{
			Type:          eventType,
			RequestID:     snapshot.RequestID,
			RunID:         snapshot.RunID,
			SessionID:     snapshot.SessionID,
			AgentID:       snapshot.AgentID,
			ModelID:       snapshot.ModelID,
			ModelRevision: snapshot.ModelRevision,
			ToolRevision:  snapshot.ToolRevision,
			Delta:         delta,
			OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
}

// finishRun 是所有终态的唯一出口。完成必要收尾和释放 Session 后才通知订阅者。
// Executor 已保存 partial/error 消息，这里只投影事件，不再次写 Transcript。
func (s *Service) finishRun(active *activeRun, result ExecutionResult, runErr error) {
	active.finishOnce.Do(func() {
		s.cleanupRun(active)
		snapshot := active.snapshot
		eventType, operation, message := EventTurnCompleted, "runtime.turn.complete", "Agent Turn 已完成"
		if errors.Is(runErr, context.Canceled) {
			eventType, operation, message = EventTurnCancelled, "runtime.turn.cancelled", "Agent Turn 已取消"
		} else if runErr != nil {
			eventType, operation, message = EventTurnFailed, "runtime.turn.failed", "Agent Turn 执行失败"
		}
		s.publishEvent(Event{
			Type: eventType, RequestID: active.RequestID, RunID: active.RunID,
			SessionID: active.SessionID, AgentID: snapshot.AgentID,
			ModelID: snapshot.ModelID, ModelRevision: snapshot.ModelRevision,
			ToolRevision: snapshot.ToolRevision, MessageID: result.MessageID,
			Error:      runtimeUserVisibleError(runErr),
			ToolCalls:  toolCallCount(snapshot.limitState),
			OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
		args := []any{
			"operation", operation, "request_id", active.RequestID, "run_id", active.RunID,
			"session_id", active.SessionID, "agent_id", snapshot.AgentID,
			"message_id", result.MessageID, logging.Duration(active.startedAt),
		}
		if runErr == nil {
			s.logger.Info(context.Background(), message, args...)
		} else {
			args = append(args, "error", runErr)
			if eventType == EventTurnCancelled {
				s.logger.Warn(context.Background(), message, args...)
			} else {
				s.logger.Error(context.Background(), message, args...)
			}
		}
	})
}

func toolCallCount(state *executionLimitState) int {
	if state == nil {
		return 0
	}
	return int(state.toolCalls.Load())
}

// runtimeUserVisibleError 把底层 Provider/Eino/HTTP 错误转换成适合直接展示给用户的文本。
//
// 结构化日志仍然记录原始 error，便于开发排查；Runtime Event 不直接把第三方 SDK 的
// 实现细节、长错误链或潜在响应片段暴露到 UI。
func runtimeUserVisibleError(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, context.Canceled) {
		return "本次生成已取消。"
	}

	if errors.Is(err, contextengine.ErrContextBudgetExceeded) {
		return "当前对话上下文已达到模型安全预算，并且没有更多历史可以安全压缩。请手动精简当前请求、增大该模型的 Context Window，或新建会话继续。"
	}

	lower := strings.ToLower(err.Error())
	if errors.Is(err, adk.ErrExceedMaxIterations) || strings.Contains(lower, "exceeds max iterations") {
		return "本次任务已达到 Agent 迭代次数上限，已完成的操作和对话记录仍然保留。可以发送“继续”接着处理；如经常触发，可调高 runtime.max_iterations。"
	}
	if errors.Is(err, ErrExecutionLimitExceeded) {
		return "本次任务已达到配置的模型、工具或 Token 使用上限，已完成的操作仍然保留。请调整任务预算后继续。"
	}

	if strings.Contains(lower, "free quota exhausted") ||
		strings.Contains(lower, "free tier quota") {
		return "模型服务的免费额度已用尽。请到服务商控制台充值，或关闭“仅使用免费额度”后重试。"
	}
	if strings.Contains(lower, "quota exhausted") ||
		strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "exceeded your current quota") ||
		strings.Contains(lower, "insufficient balance") ||
		strings.Contains(lower, "insufficient credit") ||
		strings.Contains(lower, "balance insufficient") ||
		strings.Contains(lower, "余额不足") ||
		strings.Contains(lower, "额度不足") {
		return "模型服务的账户余额或调用额度不足。请检查服务商账户、充值或切换模型后重试。"
	}
	if strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "status code: 429") ||
		strings.Contains(lower, "status: 429") {
		return "模型服务当前请求过于频繁。请稍后重试，或检查服务商的速率限制。"
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(lower, "client.timeout") ||
		strings.Contains(lower, "context deadline exceeded") {
		return "模型服务在等待响应时超时。本次已经生成的内容已尽量保留，可以直接重试。"
	}

	if strings.Contains(lower, "inappropriat") ||
		strings.Contains(lower, "output data may contain") ||
		strings.Contains(lower, "content filter") ||
		strings.Contains(lower, "content_filter") ||
		strings.Contains(lower, "safety policy") ||
		strings.Contains(lower, "sensitive content") {
		return "模型服务因内容安全策略中止了本次生成。此前已经成功生成的内容已尽量保留。"
	}

	if strings.Contains(lower, "status code: 401") ||
		strings.Contains(lower, "status: 401") ||
		strings.Contains(lower, "status code: 403") ||
		strings.Contains(lower, "status: 403") {
		return "模型服务拒绝了本次请求。请检查 API Key、账户权限和所选模型的访问资格。"
	}

	return "模型生成过程中发生错误。本次已经生成的内容已尽量保留，请稍后重试。"
}

func (s *Service) activeRunStatus(sessionID string) *ActiveRunStatus {
	s.mu.Lock()
	requestID, exists := s.activeBySession[sessionID]
	if !exists {
		s.mu.Unlock()
		return nil
	}
	active, exists := s.activeByRequest[requestID]
	if !exists || active == nil || active.snapshot == nil {
		// reserveSession 与 activeByRequest 注册之间存在很短的初始化窗口。该窗口由
		// StartTurn 调用方自己的 starting 状态表达，Overview 不伪造一个未冻结 Runtime。
		s.mu.Unlock()
		return nil
	}
	status := &ActiveRunStatus{
		RequestID:         active.RequestID,
		RunID:             active.RunID,
		SessionID:         active.SessionID,
		Phase:             active.phase,
		StartedAt:         active.startedAt.UTC().Format(time.RFC3339Nano),
		WaitingApprovalID: active.waitingApprovalID,
		Runtime:           cloneRuntimeManifest(active.snapshot.Manifest),
	}
	waitingApprovalID := active.waitingApprovalID
	s.mu.Unlock()

	if waitingApprovalID != "" && s.approvals != nil {
		if request, ok := s.approvals.Get(waitingApprovalID); ok {
			status.Approval = &request
		}
	}
	return status
}

func cloneRuntimeManifest(value RuntimeManifest) RuntimeManifest {
	value.Extensions = append([]CapabilitySummary(nil), value.Extensions...)
	for i := range value.Extensions {
		value.Extensions[i].ToolNames = append([]string(nil), value.Extensions[i].ToolNames...)
	}

	clone := value
	clone.BuiltinToolNames = append([]string(nil), value.BuiltinToolNames...)
	clone.SkillNames = append([]string(nil), value.SkillNames...)
	clone.MCPServers = append([]humbertmcp.RuntimeServerSnapshot(nil), value.MCPServers...)
	clone.MCPUnavailable = append([]humbertmcp.RuntimeServerFailure(nil), value.MCPUnavailable...)
	clone.MCPTools = append([]humbertmcp.RuntimeToolSnapshot(nil), value.MCPTools...)
	clone.MCPToolNames = append([]string(nil), value.MCPToolNames...)
	clone.ExposedToolNames = append([]string(nil), value.ExposedToolNames...)
	return clone
}

// publishEvent 发布 Runtime 瞬时 UI Event。
//
// Event delivery failure 不改变 Agent 执行结果。例如用户恰好关闭窗口，Session JSONL
// 仍应由 Executor 正常完成写入。
func (s *Service) publishEvent(event Event) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.events.Publish(ctx, TopicEvent, event); err != nil {
		s.logger.Warn(
			context.Background(),
			"发布 Runtime Event 失败",
			"operation", "runtime.event.publish",
			"event_type", string(event.Type),
			"request_id", event.RequestID,
			"run_id", event.RunID,
			"session_id", event.SessionID,
			"error", err,
		)
	}
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		s.logger.Warn(context.Background(), "Runtime Event 订阅处理较慢", "operation", "runtime.event.slow_delivery", "event_type", string(event.Type), "session_id", event.SessionID, "duration_ms", elapsed.Milliseconds())
	}
}
