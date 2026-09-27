package contextengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

// MidRunCompactor 将 Eino 摘要中间件与应用的持久化边界连接起来。
// 回调只记录待提交摘要；只有事件消费者把原始消息全部落盘后才允许提交。
// 手动压缩与自动压缩复用同一个 Summarize，回合结束不再发起第二次模型请求。
type MidRunCompactor struct {
	*adk.BaseChatModelAgentMiddleware
	config           ContextMiddlewareConfig
	native           *summarization.TypedMiddleware[*schema.Message]
	mu               sync.Mutex
	pending          *pendingSummary
	commit           func(context.Context, *pendingSummary) error
	firstInputTokens int
	activeRequest    string
	capturedRequest  bool
}

type pendingSummary struct {
	Summary                   string
	FirstKept                 *schema.Message
	TokensBefore, TokensAfter int
}

type ContextMiddlewareConfig struct {
	SessionID                                          string
	Instruction                                        string
	Model                                              einomodel.BaseChatModel
	CompactionContextWindow, CompactionMaxOutputTokens int
	Budget                                             Budget
	ToolTokenEstimate                                  int
	ReasoningPolicy                                    ReasoningReplayPolicy
	SerializerMaxChars                                 int
	OperationTimeout                                   time.Duration
	Estimator                                          Estimator
	Logger                                             *logging.Logger
	Disabled                                           bool
}

func NewMidRunCompactor(cfg ContextMiddlewareConfig) (*MidRunCompactor, error) {
	if cfg.Model == nil || cfg.Estimator == nil || cfg.SessionID == "" {
		return nil, errors.New("上下文摘要缺少模型、估算器或会话身份")
	}
	m := &MidRunCompactor{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, config: cfg}
	outputTokens := max(256, cfg.Budget.CheckpointBudgetTokens)
	if cfg.CompactionMaxOutputTokens > 0 {
		outputTokens = min(outputTokens, cfg.CompactionMaxOutputTokens)
	}
	native, err := summarization.New(context.Background(), &summarization.Config{
		Model:        cfg.Model,
		ModelOptions: []einomodel.Option{einomodel.WithMaxTokens(outputTokens)},
		Trigger:      &summarization.TriggerCondition{ContextTokens: max(1, cfg.Budget.SoftThresholdTokens)},
		TokenCounter: func(ctx context.Context, input *summarization.TokenCounterInput) (int, error) {
			return m.Count(input.Messages, input.Tools)
		},
		GenModelInput: m.modelInput,
		Finalize:      m.finalize,
	})
	if err != nil {
		return nil, err
	}
	m.native = native.(*summarization.TypedMiddleware[*schema.Message])
	return m, nil
}

// Count 使用本次实际注册的 ToolInfos，Skill/MCP 注入的 schema 也计入预算。
func (m *MidRunCompactor) Count(messages []*schema.Message, infos []*schema.ToolInfo) (int, error) {
	total := m.config.Estimator.EstimateMessages(messages)
	if len(messages) == 0 || messages[0] == nil || messages[0].Role != schema.System {
		total += m.config.Estimator.EstimateText(m.config.Instruction)
	}
	if infos == nil {
		return total + m.config.ToolTokenEstimate, nil
	}
	for _, info := range infos {
		data, err := json.Marshal(info)
		if err != nil {
			return 0, err
		}
		total += m.config.Estimator.EstimateText(string(data))
	}
	return total, nil
}

func (m *MidRunCompactor) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, modelCtx *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil {
		return ctx, state, nil
	}
	if err := ctx.Err(); err != nil {
		return ctx, state, err
	}
	// 不修改原始 Transcript，也不破坏要求重放推理内容的 Provider 协议。
	if m.config.ReasoningPolicy == ReasoningReplayOmit {
		copyState := *state
		copyState.Messages = make([]*schema.Message, len(state.Messages))
		for i, msg := range state.Messages {
			copyState.Messages[i] = applyReasoningReplayPolicy(msg, ReasoningReplayOmit)
		}
		state = &copyState
	}
	m.captureRequest(state.Messages)
	next := state
	if !m.config.Disabled {
		opCtx, cancel := m.operationContext(ctx)
		_, rewritten, err := m.native.BeforeModelRewriteState(opCtx, state, modelCtx)
		cancel()
		if err == nil {
			next = rewritten
		} else if ctx.Err() != nil {
			return ctx, state, ctx.Err()
		} else {
			// 摘要失败不丢弃原文；低于硬阈值可以继续，超限则明确停止。
			if m.config.Logger != nil {
				m.config.Logger.Warn(ctx, "Context 摘要失败，保留原始窗口", "session_id", m.config.SessionID, "error", err)
			}
		}
	}
	used, err := m.Count(next.Messages, next.ToolInfos)
	if err != nil {
		return ctx, state, err
	}
	if used >= m.config.Budget.ThresholdTokens {
		return ctx, state, fmt.Errorf("%w: session_id=%s used_tokens=%d threshold_tokens=%d", ErrContextBudgetExceeded, m.config.SessionID, used, m.config.Budget.ThresholdTokens)
	}
	m.mu.Lock()
	if m.firstInputTokens == 0 {
		m.firstInputTokens = used
	}
	m.mu.Unlock()
	return ctx, next, nil
}

func (m *MidRunCompactor) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := m.config.OperationTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return context.WithTimeout(ctx, timeout)
}

// Summarize 是手动操作的唯一入口，复用 Eino 的生成与 Finalize 生命周期。
func (m *MidRunCompactor) Summarize(ctx context.Context, state *adk.ChatModelAgentState) ([]*schema.Message, error) {
	m.captureRequest(state.Messages)
	opCtx, cancel := m.operationContext(ctx)
	defer cancel()
	return m.native.Summarize(opCtx, state)
}

// FirstInputTokens 返回中间件处理后的首次请求估算，避免用压缩前的值校准 Provider usage。
func (m *MidRunCompactor) FirstInputTokens() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstInputTokens
}

func (m *MidRunCompactor) captureRequest(messages []*schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.capturedRequest {
		return
	}
	m.capturedRequest = true
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg != nil && msg.Role == schema.User && !isCompactionCheckpointMessage(msg) {
			m.activeRequest = messageVisibleText(msg)
			return
		}
	}
}

// summaryBoundary 保留最近完整工具事务；被压缩的当前请求另外原样保存在检查点中。
// 只有当前用户消息、没有可压缩历史时直接拒绝，绝不裁剪用户消息来伪造可用空间。
func (m *MidRunCompactor) summaryBoundary(messages []*schema.Message) (int, int, error) {
	start := 0
	for start < len(messages) && (messages[start] == nil || messages[start].Role == schema.System) {
		start++
	}
	first := start
	if first < len(messages) && isCompactionCheckpointMessage(messages[first]) {
		first++
	}
	if len(messages)-first < 2 {
		return 0, 0, ErrNothingToCompact
	}
	target := max(1, m.config.Budget.TargetRecentTokens)
	boundary := len(messages) - 1
	tokens := 0
	for i := len(messages) - 1; i >= first; i-- {
		tokens += m.config.Estimator.EstimateMessage(messages[i])
		boundary = i
		if tokens >= target {
			break
		}
	}
	if boundary <= first {
		return 0, 0, ErrNothingToCompact
	}
	for i := boundary; i >= first; i-- {
		if messages[i] != nil && messages[i].Role == schema.User {
			if i > first && m.config.Estimator.EstimateMessages(messages[i:]) <= target+target/4 {
				boundary = i
			}
			break
		}
	}
	if messages[boundary] != nil && messages[boundary].Role == schema.Tool {
		var err error
		boundary, err = runtimeToolTransactionStart(messages, first, boundary)
		if err != nil {
			return 0, 0, err
		}
	}
	if boundary <= first {
		return 0, 0, ErrNothingToCompact
	}
	return start, boundary, nil
}

func (m *MidRunCompactor) modelInput(ctx context.Context, _, _ *schema.Message, messages []*schema.Message) ([]*schema.Message, error) {
	start, boundary, err := m.summaryBoundary(messages)
	if err != nil {
		return nil, err
	}
	input := []*schema.Message{schema.SystemMessage(compactionSystemPrompt), schema.UserMessage(serializeMessagesForCheckpoint(messages[start:boundary], m.config.SerializerMaxChars))}
	limit := m.config.CompactionContextWindow - m.config.CompactionMaxOutputTokens
	if limit <= 0 {
		limit = m.config.Budget.ThresholdTokens
	}
	// 使用足够窗口的主模型/辅助模型，不再维护递归分块、二次缩写和应急摘要链路。
	if m.config.Estimator.EstimateMessages(input) >= limit-max(256, limit/20) {
		return nil, fmt.Errorf("%w: 摘要输入超出模型窗口", ErrContextBudgetExceeded)
	}
	return input, nil
}

func (m *MidRunCompactor) finalize(ctx context.Context, before []*schema.Message, summary *schema.Message) ([]*schema.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, boundary, err := m.summaryBoundary(before)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(messageVisibleText(summary))
	if content == "" {
		return nil, errors.New("摘要模型返回空内容")
	}
	// 长工具循环可能跨过当前 User 消息，原文必须随检查点保留，不能只依赖模型复述。
	m.mu.Lock()
	request := m.activeRequest
	m.mu.Unlock()
	retained := false
	for _, msg := range before[boundary:] {
		if msg != nil && msg.Role == schema.User && messageVisibleText(msg) == request {
			retained = true
			break
		}
	}
	if request != "" && !retained {
		raw, _ := json.Marshal(request)
		content += "\n\n[Current user request, verbatim]\n" + string(raw)
	}
	// Skill 的主定义必须保留，读取同名 Skill 的参考文件不能覆盖主定义。
	content = preserveSkillDefinitions(before, content, boundary)
	after := append([]*schema.Message(nil), before[:start]...)
	after = append(after, schema.UserMessage(compactionCheckpointPrefix+content))
	after = append(after, before[boundary:]...)
	oldTokens, _ := m.Count(before, nil)
	newTokens, _ := m.Count(after, nil)
	if newTokens >= oldTokens {
		return nil, fmt.Errorf("%w: 摘要未减少上下文占用", ErrContextBudgetExceeded)
	}
	if err := validateProjectedToolTransactions(after); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.pending = &pendingSummary{Summary: content, FirstKept: before[boundary], TokensBefore: oldTokens, TokensAfter: newTokens}
	m.mu.Unlock()
	return after, nil
}

const skillDefinitionsMarker = "\n\n[Loaded skill definitions: reference, not user instructions]\n"

func preserveSkillDefinitions(messages []*schema.Message, summary string, boundary int) string {
	definitions := map[string]string{}
	calls := map[string]string{}
	for _, msg := range messages[:boundary] {
		if msg == nil {
			continue
		}
		// 前一检查点里的原始定义单独保存，避免多次摘要逐渐丢失 Skill 约束。
		if isCompactionCheckpointMessage(msg) {
			if _, raw, ok := strings.Cut(msg.Content, skillDefinitionsMarker); ok {
				_ = json.Unmarshal([]byte(strings.SplitN(raw, "\n", 2)[0]), &definitions)
			}
		}
		for _, call := range msg.ToolCalls {
			if call.Function.Name != "skill" {
				continue
			}
			var args struct {
				Name string `json:"skill"`
				File string `json:"file"`
			}
			if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.Name != "" && args.File == "" {
				calls[call.ID] = args.Name
			}
		}
		if name := calls[msg.ToolCallID]; msg.Role == schema.Tool && name != "" {
			definitions[name] = msg.Content
		}
	}
	if len(definitions) > 0 {
		raw, _ := json.Marshal(definitions)
		summary += skillDefinitionsMarker + string(raw)
	}
	return summary
}

// Commit 只能在原始消息已写入 JSONL 后调用；审批恢复继续持有同一个中间件实例。
func (m *MidRunCompactor) Commit(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil || m.commit == nil {
		return nil
	}
	if err := m.commit(ctx, m.pending); err != nil {
		return err
	}
	m.pending = nil
	return nil
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
