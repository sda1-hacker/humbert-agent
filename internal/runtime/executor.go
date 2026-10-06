package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// DeltaEmitter 把 Assistant Streaming Delta 交给 RuntimeService。
//
// EventType 只使用 assistant.delta / assistant.reasoning.delta。Delta 是瞬时 UI 数据，
// 不能直接写 Session JSONL。
type DeltaEmitter func(eventType EventType, delta string)

const defaultAgentMaxIterations = 200

// Executor 执行不可变 Runtime Snapshot。
type Executor struct{}

// NewExecutor 创建 Runtime Executor。
func NewExecutor() *Executor {
	return &Executor{}
}

// Execute 从当前 Snapshot 启动一次新的 Eino Run。
//
// CheckpointStore 必须由 RuntimeService 在整个 ActiveRun 生命周期内持有；当 Tool Permission
// 返回 Ask 时，Eino 会在返回 interrupt event 前把完整执行状态保存到 snapshot.RunID 对应的
// checkpoint。Executor 本身不决定审批策略，只负责识别并把 root-cause interrupt 安全上抛。
func (e *Executor) Execute(ctx context.Context, snapshot *Snapshot, checkpointStore adk.CheckPointStore, emit DeltaEmitter) (ExecutionResult, error) {
	if err := validateExecutionInput(ctx, snapshot, checkpointStore); err != nil {
		return ExecutionResult{}, err
	}
	if emit == nil {
		emit = func(EventType, string) {}
	}

	runner, err := buildRunner(ctx, snapshot, checkpointStore)
	if err != nil {
		return ExecutionResult{}, err
	}
	events := runner.Run(ctx, snapshot.Messages, adk.WithCheckPointID(snapshot.RunID))
	return e.consumeEvents(ctx, snapshot, events, emit)
}

// Resume 从已有 Eino Checkpoint 定向恢复一个 Approval Interrupt。
//
// interruptID 来自 Eino root-cause InterruptCtx.ID；resumeJSON 由 ApprovalManager 创建，只包含
// Approved 布尔值。恢复后 Tool 自己会从 StatefulInterrupt 保存的 state 读取第一次调用参数，
// 因此 Runtime 不需要、也不应该持有 raw Tool Arguments。
func (e *Executor) Resume(
	ctx context.Context,
	snapshot *Snapshot,
	checkpointStore adk.CheckPointStore,
	interruptID string,
	resumeJSON string,
	emit DeltaEmitter,
) (ExecutionResult, error) {
	if err := validateExecutionInput(ctx, snapshot, checkpointStore); err != nil {
		return ExecutionResult{}, err
	}
	interruptID = strings.TrimSpace(interruptID)
	if interruptID == "" {
		return ExecutionResult{}, errors.New("恢复 Agent Turn 失败: InterruptID 不能为空")
	}
	if emit == nil {
		emit = func(EventType, string) {}
	}

	runner, err := buildRunner(ctx, snapshot, checkpointStore)
	if err != nil {
		return ExecutionResult{}, err
	}
	events, err := runner.ResumeWithParams(ctx, snapshot.RunID, &adk.ResumeParams{Targets: map[string]any{interruptID: resumeJSON}})
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("恢复 Eino Agent Checkpoint 失败: %w", err)
	}
	return e.consumeEvents(ctx, snapshot, events, emit)
}

func validateExecutionInput(ctx context.Context, snapshot *Snapshot, checkpointStore adk.CheckPointStore) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if snapshot == nil {
		return errors.New("Runtime Snapshot 不能为空")
	}
	if snapshot.Model == nil {
		return errors.New("Runtime Snapshot Model 不能为空")
	}
	if snapshot.SessionWriter == nil {
		return errors.New("Runtime Snapshot SessionWriter 不能为空")
	}
	if checkpointStore == nil {
		return errors.New("Approval CheckpointStore 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Agent Turn 在执行前已取消: %w", err)
	}
	return nil
}

func buildRunner(ctx context.Context, snapshot *Snapshot, checkpointStore adk.CheckPointStore) (*adk.Runner, error) {
	agent, err := buildChatModelAgent(ctx, snapshot, "")
	if err != nil {
		return nil, fmt.Errorf("创建 Eino ChatModelAgent 失败: %w", err)
	}

	return adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true, CheckPointStore: checkpointStore}), nil
}

// consumeEvents 统一消费新 Run 与 Resume 的 Eino Event Stream。
//
// Assistant/ToolResult 的持久化语义与原 Runtime 保持一致；interrupt event 是“暂停”而不是
// error/complete，因此返回 ExecutionResult.Interrupted，交给 Service 保留 active session。
func (e *Executor) consumeEvents(ctx context.Context, snapshot *Snapshot, events *adk.AsyncIterator[*adk.AgentEvent], emit DeltaEmitter) (ExecutionResult, error) {
	result := ExecutionResult{}

	for {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("Agent Turn 已取消: %w", err)
		}

		event, ok := events.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return result, fmt.Errorf("Eino Agent 执行失败: %w", event.Err)
		}

		if event.Output != nil && event.Output.MessageOutput != nil {
			output := event.Output.MessageOutput
			switch output.Role {
			case schema.Assistant:
				message, materializeErr := materializeAssistantOutput(ctx, output, emit)

				if materializeErr == nil && assistantBlockedByFinishReason(message) {
					materializeErr = fmt.Errorf("%w: finish_reason=%s", ErrProviderContentBlocked, message.ResponseMeta.FinishReason)
				}

				forcedFinishReason := ""
				if materializeErr != nil {
					materializeErr = classifyProviderError(materializeErr)
					switch {
					case errors.Is(materializeErr, ErrProviderContentBlocked):
						message = nil
					case errors.Is(materializeErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
						forcedFinishReason = "aborted"
						message = partialAssistantForPersistence(message)
					default:
						forcedFinishReason = "error"
						message = partialAssistantForPersistence(message)
					}
				}

				if message != nil {
					stored, persistErr := persistAssistantMessage(context.WithoutCancel(ctx), snapshot, message, forcedFinishReason)
					if persistErr != nil {
						if materializeErr != nil {
							return result, errors.Join(materializeErr, fmt.Errorf("持久化 AssistantMessage 失败: %w", persistErr))
						}
						return result, fmt.Errorf("持久化 AssistantMessage 失败: %w", persistErr)
					}

					if len(message.ToolCalls) == 0 {
						result.Content = assistantText(message)
						result.MessageID = stored.EntryID
					}
				}
				if materializeErr != nil {
					return result, materializeErr
				}

			case schema.Tool:
				message, err := materializeMessageOutput(ctx, output)
				if err != nil {
					return result, fmt.Errorf("消费 Tool %q 输出失败: %w", output.ToolName, err)
				}
				if message != nil {
					if _, err := persistCompletedTool(ctx, snapshot, message); err != nil {
						return result, err
					}
				}

			default:
				if _, err := materializeMessageOutput(ctx, output); err != nil {
					return result, fmt.Errorf("消费 Agent Event 输出失败: %w", err)
				}
			}
		}

		if event.Action != nil && event.Action.Interrupted != nil {
			interrupted, err := extractApprovalInterrupt(event.Action.Interrupted)
			if err != nil {
				return result, err
			}
			result.Interrupted = interrupted
			return result, nil
		}
	}

	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("Agent Turn 已取消: %w", err)
	}
	return result, nil
}

func extractApprovalInterrupt(interrupt *adk.InterruptInfo) (*InterruptedExecution, error) {
	if interrupt == nil || len(interrupt.InterruptContexts) == 0 {
		return nil, errors.New("Eino 返回 Interrupt，但缺少 InterruptContexts")
	}

	var selected *adk.InterruptCtx
	for _, candidate := range interrupt.InterruptContexts {
		if candidate != nil && candidate.IsRootCause {
			selected = candidate
			break
		}
	}
	if selected == nil {
		for _, candidate := range interrupt.InterruptContexts {
			if candidate != nil {
				selected = candidate
				break
			}
		}
	}
	if selected == nil || strings.TrimSpace(selected.ID) == "" {
		return nil, errors.New("Eino Interrupt 缺少有效 root-cause ID")
	}

	info, err := approval.DecodeInterruptInfo(selected.Info)
	if err != nil {
		return nil, fmt.Errorf("当前 Runtime 只支持 Tool Approval Interrupt: %w", err)
	}
	return &InterruptedExecution{InterruptID: selected.ID, Info: info}, nil
}

const recoverableToolErrorPrefix = "[Humbert Tool Error]\n"

func formatRecoverableToolError(err error) string {
	return recoverableToolErrorPrefix +
		"本次工具调用失败，但 Agent Turn 可以继续。\n" +
		"错误: " + logging.SafeErrorText(err, 4096) + "\n" +
		"请根据错误调整参数或选择替代能力；不要假设该工具已经成功执行。"
}

func isRecoverableToolErrorResult(content string) bool {
	return strings.HasPrefix(content, recoverableToolErrorPrefix)
}

// materializeAssistantOutput 发送实时正文/思考增量，返回本步骤的合并消息。
// 读取失败时保留已经生成的文本，是否允许写入由 consumeEvents 的取消/审核规则决定。
func materializeAssistantOutput(ctx context.Context, output *adk.MessageVariant, emit DeltaEmitter) (*schema.Message, error) {
	message, err := materializeOutput(ctx, output, func(chunk *schema.Message) { emitAssistantDeltas(chunk, emit) })
	if err != nil {
		return message, fmt.Errorf("消费 Assistant 输出失败: %w", classifyProviderError(err))
	}
	return message, nil
}

// materializeMessageOutput 只返回完整消息，失败时丢弃未完成 ToolResult。
// Assistant 的局部文本可以用于失败恢复，半截工具输出不能被误记为已完成的副作用。
func materializeMessageOutput(ctx context.Context, output *adk.MessageVariant) (*schema.Message, error) {
	message, err := materializeOutput(ctx, output, nil)
	if err != nil {
		return nil, err
	}
	return message, nil
}

// materializeOutput 是消息流唯一的读取与关闭入口，不决定消息是否持久化。
// 非流式和流式输出都经过 visit；回调仅用于实时 UI，不把 delta 当作 JSONL 事实。
// 返回局部消息与原始错误，让上层明确选择保留 Assistant 或拒绝不完整 ToolResult。
func materializeOutput(ctx context.Context, output *adk.MessageVariant, visit func(*schema.Message)) (*schema.Message, error) {
	if output == nil {
		return nil, nil
	}
	if !output.IsStreaming {
		if output.Message != nil && visit != nil {
			visit(output.Message)
		}
		return output.Message, nil
	}
	stream := output.MessageStream
	if stream == nil {
		return nil, errors.New("Streaming 输出缺少 MessageStream")
	}
	defer stream.Close()
	chunks := make([]*schema.Message, 0, 16)
	for {
		if err := ctx.Err(); err != nil {
			return concatMessagesBestEffort(chunks), fmt.Errorf("消费消息流被取消: %w", err)
		}
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return concatMessagesBestEffort(chunks), fmt.Errorf("读取消息流失败: %w", err)
		}
		if message != nil {
			chunks = append(chunks, message)
			if visit != nil {
				visit(message)
			}
		}
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("合并消息流失败: %w", err)
	}
	return merged, nil
}

// emitAssistantDeltas 只负责实时 UI。
func emitAssistantDeltas(message *schema.Message, emit DeltaEmitter) {
	if message == nil || emit == nil {
		return
	}
	if reasoning := assistantReasoning(message); reasoning != "" {
		emit(EventAssistantReasoningDelta, reasoning)
	}
	if text := assistantText(message); text != "" {
		emit(EventAssistantDelta, text)
	}
}

// concatMessagesBestEffort 在 Streaming 被取消/中断时尽量保留已经生成的用户可见内容。
//
// 如果 schema.ConcatMessages 因半截 ToolCall Arguments 失败，fallback 只保留安全的
// Text/Reasoning，不自行猜测尚未完整的 ToolCall。
func concatMessagesBestEffort(chunks []*schema.Message) *schema.Message {
	if len(chunks) == 0 {
		return nil
	}
	merged, err := schema.ConcatMessages(chunks)
	if err == nil {
		return merged
	}

	fallback := &schema.Message{Role: schema.Assistant}
	var content strings.Builder
	var reasoning strings.Builder
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		content.WriteString(assistantText(chunk))
		reasoning.WriteString(assistantReasoning(chunk))
	}
	fallback.Content = content.String()
	fallback.ReasoningContent = reasoning.String()
	if fallback.Content == "" && fallback.ReasoningContent == "" {
		return nil
	}
	return fallback
}

// assistantBlockedByFinishReason 判断 Provider 是否通过 FinishReason 表示输出被审核拦截。
//
// 不同 OpenAI-Compatible 实现会返回 content_filter、safety 或 blocked。这里只处理
// 明确的安全终态，不把普通 stop/length/tool_calls 误认为异常。
func assistantBlockedByFinishReason(message *schema.Message) bool {
	if message == nil || message.ResponseMeta == nil {
		return false
	}

	reason := strings.ToLower(strings.TrimSpace(message.ResponseMeta.FinishReason))
	switch reason {
	case "content_filter", "content-filter", "safety", "blocked", "moderation":
		return true
	default:
		return false
	}
}

// partialAssistantForPersistence 确保异常中断时不会把半截 ToolCall 写入永久 Session。
func partialAssistantForPersistence(message *schema.Message) *schema.Message {
	if message == nil {
		return nil
	}
	copyValue := *message
	copyValue.ToolCalls = nil
	if copyValue.Content == "" && copyValue.ReasoningContent == "" && len(copyValue.AssistantGenMultiContent) == 0 {
		return nil
	}
	return &copyValue
}

// persistAssistantMessage 只补充 schema.Message 无法稳定携带的 Provider/Model 描述。
func persistAssistantMessage(ctx context.Context, snapshot *Snapshot, message *schema.Message, forcedFinishReason string) (sessions.Message, error) {
	if snapshot == nil || snapshot.SessionWriter == nil {
		return sessions.Message{}, errors.New("SessionWriter 不能为空")
	}
	if message == nil {
		return sessions.Message{}, errors.New("Assistant Message 不能为空")
	}
	if message.Role != schema.Assistant {
		return sessions.Message{}, fmt.Errorf("期望 Assistant Message，实际 role=%q", message.Role)
	}

	return snapshot.SessionWriter.AppendAssistantMessage(
		ctx,
		snapshot.SessionID,
		message,
		sessions.AssistantPersistence{
			API:                snapshot.ProviderAPI,
			Provider:           snapshot.ProviderID,
			Model:              snapshot.ModelName,
			ResponseModel:      extraString(message.Extra, "response_model", "responseModel", "model"),
			ResponseID:         extraString(message.Extra, "response_id", "responseId", "request_id"),
			ForcedFinishReason: forcedFinishReason,
		},
	)
}

// persistCompletedTool 持久化标准 Eino Tool Message。
func persistCompletedTool(ctx context.Context, snapshot *Snapshot, message *schema.Message) (sessions.Message, error) {
	if snapshot == nil || snapshot.SessionWriter == nil {
		return sessions.Message{}, errors.New("SessionWriter 不能为空")
	}
	if message == nil {
		return sessions.Message{}, errors.New("Tool Message 不能为空")
	}
	if message.Role != schema.Tool {
		return sessions.Message{}, fmt.Errorf("期望 Tool Message，实际 role=%q", message.Role)
	}
	if strings.TrimSpace(message.ToolCallID) == "" {
		return sessions.Message{}, errors.New("Eino Tool Message 缺少 ToolCallID")
	}
	if strings.TrimSpace(message.ToolName) == "" {
		return sessions.Message{}, errors.New("Eino Tool Message 缺少 ToolName")
	}

	// 工具已返回时，即使用户同时取消，也应尽力保存实际结果，避免下次只能推断为未知。
	// 独立收尾仍设上限，避免关闭过程无限等待。
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	stored, err := snapshot.SessionWriter.AppendToolResult(
		persistCtx,
		snapshot.SessionID,
		message,
		sessions.ToolResultPersistence{IsError: isRecoverableToolErrorResult(message.Content) || transcript.ToolResultRejected(message.Content)},
	)
	if err != nil {
		return sessions.Message{}, fmt.Errorf("持久化 ToolResultMessage 失败: %w", err)
	}
	return stored, nil
}

// assistantText 仅用于 Streaming UI 与最终 ExecutionResult，不参与磁盘协议转换。
func assistantText(message *schema.Message) string {
	if message == nil {
		return ""
	}
	if message.Content != "" {
		return message.Content
	}

	var builder strings.Builder
	for _, part := range message.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

// assistantReasoning 仅用于把 Provider Reasoning Streaming 到 UI。
func assistantReasoning(message *schema.Message) string {
	if message == nil {
		return ""
	}
	if message.ReasoningContent != "" {
		return message.ReasoningContent
	}

	var builder strings.Builder
	for _, part := range message.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeReasoning && part.Reasoning != nil {
			builder.WriteString(part.Reasoning.Text)
		}
	}
	return builder.String()
}

// extraString 从 Eino Message Extra 中读取非敏感字符串元数据。
func extraString(extra map[string]any, keys ...string) string {
	if extra == nil {
		return ""
	}
	for _, key := range keys {
		value, exists := extra[key]
		if !exists {
			continue
		}
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}
