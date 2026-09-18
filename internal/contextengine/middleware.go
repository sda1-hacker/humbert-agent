package contextengine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

// MidRunCompactor 在 Eino ReAct Loop 的每次 ChatModel 调用前检查当前内存 State。
//
// 重要边界：Assistant/ToolResult 的持久化由 Runtime Executor 消费 Eino Event 完成，
// Middleware 与 Event Consumer 并非同一个同步写事务。因此这里发生的压缩只修改 Eino
// state.Messages，不直接写 CompactionEntry，避免摘要先于对应 ToolResult 落盘。Turn 完成
// 后 RuntimeService 会基于完整 Transcript 再执行一次持久化 CompactIfNeeded，使临时
// checkpoint 最终收敛为 JSONL 中的稳定 CompactionEntry。
type MidRunCompactor struct {
	*adk.BaseChatModelAgentMiddleware

	config ContextMiddlewareConfig
}

// ContextMiddlewareConfig 是 MidRunCompactor 的不可变运行快照。
type ContextMiddlewareConfig struct {
	SessionID string

	Instruction string

	Model einomodel.BaseChatModel

	CompactionContextWindow   int
	CompactionMaxOutputTokens int

	Budget Budget

	ToolTokenEstimate int

	SerializerMaxChars int

	OperationTimeout time.Duration

	Estimator Estimator

	Logger *logging.Logger
}

// NewMidRunCompactor 创建一个仅属于当前 Turn 的 Eino Middleware。
func NewMidRunCompactor(config ContextMiddlewareConfig) (*MidRunCompactor, error) {
	if strings.TrimSpace(config.SessionID) == "" {
		return nil, errors.New("MidRunCompactor SessionID 不能为空")
	}
	if config.Model == nil {
		return nil, errors.New("MidRunCompactor Model 不能为空")
	}
	if config.Estimator == nil {
		return nil, errors.New("MidRunCompactor Estimator 不能为空")
	}
	if config.Logger == nil {
		return nil, errors.New("MidRunCompactor Logger 不能为空")
	}
	return &MidRunCompactor{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		config:                       config,
	}, nil
}

// BeforeModelRewriteState 在 Eino 每一次模型调用前执行 Context 预算保护。
//
// Eino v0.9.19 已推荐使用 interface-based ChatModelAgentMiddleware Handler；该 Hook 返回的
// state 会持久到当前 Run 后续迭代，因此比旧 BeforeChatModel closure 更适合做 ReAct loop
// 中的 Context 重写。这里仅修改 Eino 内存 State，不直接写 Session JSONL；稳定 Compaction
// 仍由 Turn 完成后的持久化维护阶段提交，避免 ToolResult 尚未落盘时破坏 Transcript 顺序。
func (m *MidRunCompactor) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	_ *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if err := m.beforeChatModel(ctx, state); err != nil {
		return ctx, state, err
	}
	return ctx, state, nil
}

// 编译期断言保证 Eino 升级后 Handler 接口发生变化时能够尽早暴露兼容性问题。
var _ adk.ChatModelAgentMiddleware = (*MidRunCompactor)(nil)

func (m *MidRunCompactor) beforeChatModel(ctx context.Context, state *adk.ChatModelAgentState) error {
	if ctx == nil {
		return errors.New("MidRun Compaction context.Context 不能为空")
	}
	if state == nil || len(state.Messages) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("MidRun Compaction 被取消: %w", err)
	}

	instructionTokens := m.config.Estimator.EstimateText(m.config.Instruction)
	// Eino 的 State 在某些执行路径已经包含 SystemMessage。此时不再重复计算 instruction。
	if state.Messages[0] != nil && state.Messages[0].Role == schema.System {
		instructionTokens = 0
	}
	used := instructionTokens + m.config.ToolTokenEstimate + m.config.Estimator.EstimateMessages(state.Messages)
	if used < m.config.Budget.ThresholdTokens {
		return nil
	}

	startedAt := time.Now()
	const maxCompactionPasses = 3
	current := state.Messages
	currentUsed := used
	passes := 0
	for currentUsed >= m.config.Budget.ThresholdTokens && passes < maxCompactionPasses {
		compacted, err := m.compactMessages(ctx, current)
		if err != nil {
			if errors.Is(err, ErrNothingToCompact) {
				return fmt.Errorf(
					"%w: session_id=%s used_tokens=%d threshold_tokens=%d",
					ErrContextBudgetExceeded,
					m.config.SessionID,
					currentUsed,
					m.config.Budget.ThresholdTokens,
				)
			}
			return fmt.Errorf("执行同 Turn 上下文压缩失败: %w", err)
		}
		if len(compacted) == 0 {
			return fmt.Errorf(
				"%w: session_id=%s used_tokens=%d threshold_tokens=%d",
				ErrContextBudgetExceeded,
				m.config.SessionID,
				currentUsed,
				m.config.Budget.ThresholdTokens,
			)
		}
		nextUsed := instructionTokens + m.config.ToolTokenEstimate + m.config.Estimator.EstimateMessages(compacted)
		// 不能用“消息条数是否减少”判断压缩有效性：一个 checkpoint 可能比若干很短消息
		// 更长。真正需要保证的是 Token 预算单调下降，否则继续循环只会重复调用模型。
		if nextUsed >= currentUsed {
			return fmt.Errorf(
				"%w: session_id=%s used_tokens=%d compacted_tokens=%d threshold_tokens=%d",
				ErrContextBudgetExceeded,
				m.config.SessionID,
				currentUsed,
				nextUsed,
				m.config.Budget.ThresholdTokens,
			)
		}
		current = compacted
		passes++
		currentUsed = nextUsed
	}
	if currentUsed >= m.config.Budget.ThresholdTokens {
		return fmt.Errorf(
			"%w: session_id=%s used_tokens=%d threshold_tokens=%d attempts=%d",
			ErrContextBudgetExceeded,
			m.config.SessionID,
			currentUsed,
			m.config.Budget.ThresholdTokens,
			passes,
		)
	}
	state.Messages = current

	m.config.Logger.Info(
		ctx,
		"同 Turn Context 已执行内存压缩",
		"operation", "context.compaction.mid_run",
		"session_id", m.config.SessionID,
		"tokens_before", used,
		"tokens_after", currentUsed,
		"compaction_passes", passes,
		"messages_after", len(current),
		logging.Duration(startedAt),
	)
	return nil
}

func (m *MidRunCompactor) compactMessages(ctx context.Context, messages []*schema.Message) ([]*schema.Message, error) {
	prefixEnd := 0
	for prefixEnd < len(messages) {
		message := messages[prefixEnd]
		if message == nil {
			prefixEnd++
			continue
		}
		if message.Role == schema.System || isSessionMemoryReferenceMessage(message) {
			prefixEnd++
			continue
		}
		break
	}

	previousCheckpoint := ""
	conversationStart := prefixEnd
	if conversationStart < len(messages) && isCompactionCheckpointMessage(messages[conversationStart]) {
		previousCheckpoint = strings.TrimSpace(strings.TrimPrefix(messageVisibleText(messages[conversationStart]), compactionCheckpointPrefix))
		conversationStart++
	}
	if len(messages)-conversationStart < 2 {
		return nil, ErrNothingToCompact
	}

	targetRecent := m.config.Budget.TargetRecentTokens
	if targetRecent <= 0 {
		targetRecent = 1
	}
	candidate := len(messages) - 1
	retainedTokens := 0
	for index := len(messages) - 1; index >= conversationStart; index-- {
		retainedTokens += m.config.Estimator.EstimateMessage(messages[index])
		candidate = index
		if retainedTokens >= targetRecent {
			break
		}
	}
	if candidate <= conversationStart {
		return nil, ErrNothingToCompact
	}

	// 优先完整 User Turn；若当前 Turn 太大则允许 split，但不能从 ToolResult 开始。
	boundary := candidate
	for index := candidate; index >= conversationStart; index-- {
		if messages[index] != nil && messages[index].Role == schema.User {
			if index > conversationStart {
				candidateTokens := m.config.Estimator.EstimateMessages(messages[index:])
				if candidateTokens <= targetRecent+targetRecent/4 {
					boundary = index
				}
			}
			break
		}
	}
	if boundary < len(messages) && messages[boundary] != nil && messages[boundary].Role == schema.Tool {
		adjusted, err := runtimeToolTransactionStart(messages, conversationStart, boundary)
		if err != nil {
			return nil, err
		}
		boundary = adjusted
	}
	if boundary <= conversationStart {
		return nil, ErrNothingToCompact
	}

	toSummarize := make([]*schema.Message, 0, boundary-conversationStart)
	for _, message := range messages[conversationStart:boundary] {
		toSummarize = append(toSummarize, applyReasoningReplayPolicy(message, ReasoningReplayOmit))
	}
	toSummarize = closeInterruptedToolCalls(toSummarize)
	compactionWindow := m.config.CompactionContextWindow
	if compactionWindow <= 0 {
		compactionWindow = m.config.Budget.ContextWindow
	}
	compactionOutput := m.config.CompactionMaxOutputTokens
	if compactionOutput <= 0 {
		compactionOutput = m.config.Budget.MaxOutputTokens
	}
	generator := CheckpointGenerator{
		Model:            m.config.Model,
		Estimator:        m.config.Estimator,
		ContextWindow:    compactionWindow,
		MaxOutputTokens:  compactionOutput,
		OperationTimeout: m.config.OperationTimeout,
		ArgumentMaxRunes: m.config.SerializerMaxChars,
	}
	summary, err := generator.Generate(ctx, CheckpointInput{
		PreviousCheckpoint: previousCheckpoint,
		Messages:           toSummarize,
	})
	if err != nil {
		m.config.Logger.Warn(
			ctx,
			"生成 MidRun Checkpoint 失败，改用本地应急检查点",
			"operation", "context.compaction.mid_run_fallback",
			"session_id", m.config.SessionID,
			"error", err.Error(),
		)
		summary = localFallbackCheckpoint(previousCheckpoint, toSummarize, m.config.SerializerMaxChars)
		if strings.TrimSpace(summary) == "" {
			return nil, fmt.Errorf("生成 MidRun Checkpoint 失败且本地兜底为空: %w", err)
		}
	}

	result := make([]*schema.Message, 0, prefixEnd+1+len(messages)-boundary)
	result = append(result, messages[:prefixEnd]...)
	result = append(result, schema.UserMessage(compactionCheckpointPrefix+summary+"\n\n[Internal continuation notice]\n当前任务仍在进行。请根据这个检查点和最近消息继续，不要因为上下文压缩而结束任务，也不必向用户解释压缩过程。"))
	result = append(result, messages[boundary:]...)
	if err := validateProjectedToolTransactions(result[prefixEnd:]); err != nil {
		return nil, err
	}
	return result, nil
}

func isCompactionCheckpointMessage(message *schema.Message) bool {
	return message != nil && message.Role == schema.User && strings.HasPrefix(strings.TrimSpace(messageVisibleText(message)), compactionCheckpointPrefix)
}

func runtimeToolTransactionStart(messages []*schema.Message, first int, toolIndex int) (int, error) {
	toolMessage := messages[toolIndex]
	if toolMessage == nil {
		return 0, errors.New("ToolResult Message 为空")
	}
	callID := strings.TrimSpace(toolMessage.ToolCallID)
	if callID == "" {
		return 0, errors.New("ToolResult 缺少 ToolCallID")
	}
	for index := toolIndex - 1; index >= first; index-- {
		message := messages[index]
		if message == nil || message.Role != schema.Assistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.ID == callID {
				return index, nil
			}
		}
	}
	return 0, fmt.Errorf("ToolResult %s 找不到对应 ToolCall", callID)
}

func messageVisibleText(message *schema.Message) string {
	if message == nil {
		return ""
	}
	if strings.TrimSpace(message.Content) != "" {
		return message.Content
	}
	var builder strings.Builder
	if message.Role == schema.User {
		for _, part := range message.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText {
				builder.WriteString(part.Text)
			}
		}
		return builder.String()
	}
	for _, part := range message.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}
