package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
)

// DeltaEmitter 把 Assistant Streaming Delta 交给 RuntimeService。
//
// EventType 只使用 assistant.delta / assistant.reasoning.delta。Delta 是瞬时 UI 数据，
// 不能直接写 Session JSONL。
type DeltaEmitter func(eventType EventType, delta string)

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
func (e *Executor) Execute(
	ctx context.Context,
	snapshot *Snapshot,
	checkpointStore adk.CheckPointStore,
	emit DeltaEmitter,
) (ExecutionResult, error) {
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
	events, err := runner.ResumeWithParams(
		ctx,
		snapshot.RunID,
		&adk.ResumeParams{Targets: map[string]any{interruptID: resumeJSON}},
	)
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

func buildRunner(
	ctx context.Context,
	snapshot *Snapshot,
	checkpointStore adk.CheckPointStore,
) (*adk.Runner, error) {
	agent, err := adk.NewChatModelAgent(
		ctx,
		&adk.ChatModelAgentConfig{
			Name:        snapshot.AgentName,
			Instruction: snapshot.Instruction,
			Model:       snapshot.Model,
			Handlers:    append([]adk.ChatModelAgentMiddleware(nil), snapshot.AgentHandlers...),
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{
					Tools:               snapshot.Tools,
					ExecuteSequentially: true,
					ToolCallMiddlewares: []compose.ToolMiddleware{
						{Invokable: buildToolLifecycleMiddleware(snapshot)},
					},
				},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("创建 Eino ChatModelAgent 失败: %w", err)
	}

	return adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
		CheckPointStore: checkpointStore,
	}), nil
}

// consumeEvents 统一消费新 Run 与 Resume 的 Eino Event Stream。
//
// Assistant/ToolResult 的持久化语义与原 Runtime 保持一致；interrupt event 是“暂停”而不是
// error/complete，因此返回 ExecutionResult.Interrupted，交给 Service 保留 active session。
func (e *Executor) consumeEvents(
	ctx context.Context,
	snapshot *Snapshot,
	events *adk.AsyncIterator[*adk.AgentEvent],
	emit DeltaEmitter,
) (ExecutionResult, error) {
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
					materializeErr = fmt.Errorf(
						"%w: finish_reason=%s",
						ErrProviderContentBlocked,
						message.ResponseMeta.FinishReason,
					)
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
					stored, persistErr := persistAssistantMessage(
						context.WithoutCancel(ctx), snapshot, message, forcedFinishReason,
					)
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

// buildToolLifecycleMiddleware 负责实时 Tool 生命周期，并把可恢复的 Tool 失败转换成标准
// ToolResult 交回模型。只有 Interrupt 与整个 Turn 的 context 取消继续向 Eino 上抛。
//
// 这种语义让 web_fetch 网络超时、远程服务失败、模型参数错误等局部能力故障不会直接
// 杀死 Agent Turn；模型仍能读取失败原因并选择 install_skill、web_search 或其它替代策略。
func buildToolLifecycleMiddleware(snapshot *Snapshot) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			if input == nil {
				return next(ctx, input)
			}
			if snapshot != nil && snapshot.limitState != nil {
				if err := snapshot.limitState.beforeToolCall(); err != nil {
					return nil, err
				}
			}

			startedAt := time.Now()
			// Tool Arguments 属于不可信且可能包含密钥/文件正文的模型生成数据。实时事件只用于
			// UI Trace，不参与真正 Tool 执行，因此在离开 Runtime 边界前统一脱敏并限制长度；
			// checkpoint 与 guarded Tool 仍持有原始参数，审批恢复不会使用这里的展示字符串。
			reportToolLifecycleEvent(ctx, snapshot, Event{
				Type:          EventToolStarted,
				ToolCallID:    input.CallID,
				ToolName:      input.Name,
				ToolArguments: logging.RedactText(input.Arguments, 4096),
				OccurredAt:    startedAt.UTC().Format(time.RFC3339Nano),
			})

			output, err := next(ctx, input)
			duration := time.Since(startedAt).Milliseconds()
			if err != nil {
				// Eino Interrupt 是正常的 Human-in-the-loop 暂停信号，不是 Tool 失败。
				// 必须原样上抛，让 Runner 保存 checkpoint 并由 RuntimeService 发布审批事件。
				var interruptSignal *adk.InterruptSignal
				if errors.As(err, &interruptSignal) {
					return nil, err
				}

				// 整个 Turn 已经被取消/超时属于 Runtime 终止条件，不能伪装成普通 ToolResult。
				// 反过来，web_fetch 自己的 HTTP Client timeout、远程安装失败、模型参数错误等
				// 都只是“某个能力本次调用失败”，应该把错误交还给模型，让 ReAct 循环有机会
				// 调整策略，而不是因为一个外部网站超时直接终止整次聊天。
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, err
				}
				if errors.Is(err, ErrExecutionLimitExceeded) {
					return nil, err
				}

				reportToolLifecycleEvent(context.WithoutCancel(ctx), snapshot, Event{
					Type:       EventToolFailed,
					ToolCallID: input.CallID,
					ToolName:   input.Name,
					DurationMS: duration,
					Error:      logging.SafeErrorText(err, 2048),
					OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
				})

				// ToolOutput 以成功的 Eino transport 结果返回，但正文明确标记失败。这样 Eino
				// 会生成标准 Tool Message 并继续下一轮模型推理；持久化层通过固定前缀把它
				// 标记成 IsError=true，不会把失败误记为成功。
				return &compose.ToolOutput{Result: formatRecoverableToolError(err)}, nil
			}

			reportToolLifecycleEvent(ctx, snapshot, Event{
				Type:       EventToolCompleted,
				ToolCallID: input.CallID,
				ToolName:   input.Name,
				DurationMS: duration,
				OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
			})
			return output, nil
		}
	}
}

func reportToolLifecycleEvent(ctx context.Context, snapshot *Snapshot, event Event) {
	if snapshot == nil || snapshot.EventReporter == nil {
		return
	}

	event.RequestID = snapshot.RequestID
	event.RunID = snapshot.RunID
	event.SessionID = snapshot.SessionID
	event.AgentID = snapshot.AgentID
	event.ModelID = snapshot.ModelID
	event.ModelRevision = snapshot.ModelRevision
	event.ToolRevision = snapshot.ToolRevision
	snapshot.EventReporter.Report(ctx, event)
}

// materializeAssistantOutput 消费一个 Assistant MessageVariant。
//
// Streaming 时每个 Chunk 立即向 UI 发送 reasoning/text delta，但磁盘只保存 Step
// 结束后由 schema.ConcatMessages 合并得到的一条完整 schema.Message。
func materializeAssistantOutput(
	ctx context.Context,
	output *adk.MessageVariant,
	emit DeltaEmitter,
) (*schema.Message, error) {
	if output == nil {
		return nil, nil
	}

	if !output.IsStreaming {
		if output.Message == nil {
			return nil, nil
		}
		emitAssistantDeltas(output.Message, emit)
		return output.Message, nil
	}

	stream := output.MessageStream
	if stream == nil {
		return nil, errors.New("Assistant Event 标记为 Streaming，但 MessageStream 为空")
	}
	defer stream.Close()

	chunks := make([]*schema.Message, 0, 16)
	for {
		if err := ctx.Err(); err != nil {
			return concatMessagesBestEffort(chunks), fmt.Errorf("消费 Assistant Stream 被取消: %w", err)
		}

		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return concatMessagesBestEffort(chunks), fmt.Errorf(
				"读取 Assistant Stream 失败: %w",
				classifyProviderError(err),
			)
		}
		if message == nil {
			continue
		}

		chunks = append(chunks, message)
		emitAssistantDeltas(message, emit)
	}

	if len(chunks) == 0 {
		return nil, nil
	}

	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("合并 Assistant Stream Message 失败: %w", err)
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

// materializeMessageOutput 消费一个普通 MessageVariant 并合并 Streaming Chunk。
func materializeMessageOutput(ctx context.Context, output *adk.MessageVariant) (*schema.Message, error) {
	if output == nil {
		return nil, nil
	}
	if !output.IsStreaming {
		return output.Message, nil
	}

	stream := output.MessageStream
	if stream == nil {
		return nil, errors.New("Agent Event 标记为 Streaming，但 MessageStream 为空")
	}
	defer stream.Close()

	chunks := make([]*schema.Message, 0, 8)
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("消费 Agent Stream 被取消: %w", err)
		}

		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 Agent Stream 失败: %w", err)
		}
		if message != nil {
			chunks = append(chunks, message)
		}
	}

	if len(chunks) == 0 {
		return nil, nil
	}
	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("合并 Agent Stream Message 失败: %w", err)
	}
	return merged, nil
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
func persistAssistantMessage(
	ctx context.Context,
	snapshot *Snapshot,
	message *schema.Message,
	forcedFinishReason string,
) (sessions.Message, error) {
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
func persistCompletedTool(
	ctx context.Context,
	snapshot *Snapshot,
	message *schema.Message,
) (sessions.Message, error) {
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
		sessions.ToolResultPersistence{IsError: isRecoverableToolErrorResult(message.Content)},
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
