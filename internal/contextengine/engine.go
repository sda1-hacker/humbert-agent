package contextengine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// SessionRepository 是 ContextEngine 与 Session Domain 的最小边界。
//
// ContextEngine 读取当前窗口并提交 CompactionEntry，不需要知道
// agents/<id>/sessions/<id> 的磁盘路径或 Session config.json 格式。
type SessionRepository interface {
	LoadContextTranscript(ctx context.Context, sessionID string) (transcript.Document, error)
	LoadTranscript(ctx context.Context, sessionID string) (transcript.Document, error)

	AppendCompaction(
		ctx context.Context,
		sessionID string,
		input transcript.AppendCompactionInput,
	) (transcript.Entry, error)
}

// Engine 负责把 Transcript 投影成模型上下文，并执行持久化 Compaction。
//
// Engine 不创建 Agent、不解析 Provider Credential、不负责 Tool 执行。RuntimeResolver
// 在同一 Turn 快照中解析 Model/Tools 后，把模型预算和 Tool Token estimate 传入本模块。
type Engine struct {
	config config.ContextConfig

	sessions SessionRepository

	estimator Estimator

	logger *logging.Logger
}

// NewEngine 创建 ContextEngine。
func NewEngine(
	cfg config.ContextConfig,
	sessions SessionRepository,
	estimator Estimator,
	logger *logging.Logger,
) (*Engine, error) {
	if sessions == nil {
		return nil, errors.New("ContextEngine SessionRepository 不能为空")
	}
	if estimator == nil {
		return nil, errors.New("ContextEngine Token Estimator 不能为空")
	}
	if logger == nil {
		return nil, errors.New("ContextEngine Logger 不能为空")
	}
	return &Engine{
		config:    cfg,
		sessions:  sessions,
		estimator: estimator,
		logger:    logger,
	}, nil
}

// Build 从当前 ActiveBranch 构造下一次模型调用的 Context Snapshot。
func (e *Engine) Build(ctx context.Context, request BuildRequest) (Snapshot, error) {
	started := time.Now()
	if ctx == nil {
		return Snapshot{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("构建 Context 被取消: %w", err)
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return Snapshot{}, errors.New("Session ID 不能为空")
	}

	budget, err := CalculateBudget(e.config, request.ContextWindow, request.MaxOutputTokens)
	if err != nil {
		return Snapshot{}, fmt.Errorf("计算 Context Budget 失败: %w", err)
	}
	loadStarted := time.Now()
	document, err := e.sessions.LoadContextTranscript(ctx, request.SessionID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("读取 Session Transcript 失败: %w", err)
	}
	loadDuration := time.Since(loadStarted)
	snapshot, err := e.buildFromDocument(ctx, request, budget, document)
	if err == nil {
		e.logger.Info(ctx, "Context 构建指标",
			"operation", "context.build", "session_id", request.SessionID,
			"cache_hit", document.ReadStats.CacheHit,
			"index_rebuilt", document.ReadStats.IndexRebuilt,
			"bytes_read", document.ReadStats.BytesRead,
			"load_ms", loadDuration.Milliseconds(), "build_ms", time.Since(started).Milliseconds(),
			"estimated_input_tokens", snapshot.Usage.UsedTokens)
	}
	return snapshot, err
}

// buildFromDocument 使用调用方已经读取并验证过的 Transcript 构建 Snapshot。
// Compact 使用按需读取的当前窗口；修复应急检查点时才读取更早来源。
// 提交时 AppendCompaction 仍会独立验证 ExpectedLeaf。
func (e *Engine) buildFromDocument(
	ctx context.Context,
	request BuildRequest,
	budget Budget,
	document transcript.Document,
) (Snapshot, error) {
	projection, err := projectActiveBranch(document, request.ReasoningPolicy)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateProjectedToolTransactions(projection.Messages); err != nil {
		return Snapshot{}, err
	}

	baseInstruction := strings.TrimSpace(request.Instruction)
	breakdown := usageBreakdown{
		SystemTokens:  e.estimator.EstimateText(baseInstruction),
		ToolTokens:    maxInt(request.ToolTokenEstimate, 0),
		MessageTokens: e.estimator.EstimateMessages(projection.RecentMessages),
	}
	if projection.Checkpoint != nil {
		breakdown.CheckpointTokens = e.estimator.EstimateMessage(projection.Checkpoint)
	}

	// System/Tool 都不能靠压缩旧对话释放，因此必须先从硬阈值中扣除，再决定
	// 本轮真正能够保留多少 Recent History。
	budget = ResolveBudgetForFixedContext(
		budget,
		breakdown.SystemTokens+breakdown.ToolTokens,
		breakdown.CheckpointTokens,
	)

	messages := projection.Messages
	usage := usageFromBudget(budget, breakdown, projection.LatestCompactionID)
	assembly := assemblyFromProjection(projection)

	return Snapshot{
		Instruction: baseInstruction,
		Messages:    messages,
		Budget:      budget,
		Window:      projection.Window,
		Usage:       usage,
		Assembly:    assembly,
	}, nil
}

func assemblyFromProjection(projection projectionResult) Assembly {
	assembly := Assembly{
		VisibleMessageCount: len(projection.Messages),
		RecentMessageCount:  len(projection.RecentMessages),
		CheckpointInjected:  projection.Checkpoint != nil,
		LatestCompactionID:  projection.LatestCompactionID,
		Window:              projection.Window,
	}

	for _, message := range projection.RecentMessages {
		if message == nil {
			continue
		}
		switch message.Role {
		case schema.User:
			assembly.UserMessageCount++
		case schema.Assistant:
			assembly.AssistantMessageCount++
			assembly.ToolCallCount += len(message.ToolCalls)
		case schema.Tool:
			assembly.ToolResultCount++
		}
	}
	return assembly
}

// usageBreakdown 是 Usage 各来源 Token 的内部构建结构。
//
// 使用单独结构而不是在 usageFromBudget 中重新解析 Message，可以保证“Context 是什么”与
// “Context 为什么占这些 Token”来自同一次投影，后续增加 Skill/MCP 分类时也不会依赖 UI
// 侧猜测。所有字段在进入 Usage 前都会归一化为非负值。
type usageBreakdown struct {
	SystemTokens     int
	ToolTokens       int
	CheckpointTokens int
	MessageTokens    int
}

func usageFromBudget(budget Budget, breakdown usageBreakdown, latestCompactionID string) Usage {
	breakdown.SystemTokens = maxInt(breakdown.SystemTokens, 0)
	breakdown.ToolTokens = maxInt(breakdown.ToolTokens, 0)
	breakdown.CheckpointTokens = maxInt(breakdown.CheckpointTokens, 0)
	breakdown.MessageTokens = maxInt(breakdown.MessageTokens, 0)

	used := breakdown.SystemTokens +
		breakdown.ToolTokens +
		breakdown.CheckpointTokens +
		breakdown.MessageTokens
	percent := 0.0
	if budget.ContextWindow > 0 {
		percent = float64(used) * 100 / float64(budget.ContextWindow)
	}
	return Usage{
		ContextWindow:          budget.ContextWindow,
		UsedTokens:             used,
		SystemTokens:           breakdown.SystemTokens,
		ToolTokens:             breakdown.ToolTokens,
		CheckpointTokens:       breakdown.CheckpointTokens,
		MessageTokens:          breakdown.MessageTokens,
		ReserveTokens:          budget.ReserveTokens,
		ThresholdTokens:        budget.ThresholdTokens,
		KeepRecentTokens:       budget.KeepRecentTokens,
		PreferredRecentTokens:  budget.PreferredRecentTokens,
		TargetRecentTokens:     budget.TargetRecentTokens,
		FixedTokens:            budget.FixedTokens,
		HistoryBudgetTokens:    budget.HistoryBudgetTokens,
		CheckpointBudgetTokens: budget.CheckpointBudgetTokens,
		SoftThresholdTokens:    budget.SoftThresholdTokens,
		Percent:                percent,
		NeedsCompaction:        used >= budget.ThresholdTokens,
		NeedsSoftCompaction:    used >= budget.SoftThresholdTokens,
		LatestCompactionID:     latestCompactionID,
		UpdatedAt:              time.Now().UTC(),
	}
}

// Config 返回 Engine 启动时冻结的 Context 策略副本。
func (e *Engine) Config() config.ContextConfig {
	return e.config
}

// EstimateTools 计算当前 Tool Snapshot 的 schema token 估算值。
//
// Tool Definitions 会和每次模型请求一起占用 Context Window，因此必须与 Transcript
// Messages 一起纳入预算。估算只读取 Tool.Info，不执行工具，也不记录参数或 Credential。
func (e *Engine) EstimateTools(ctx context.Context, tools []einotool.BaseTool) (int, error) {
	return e.estimator.EstimateTools(ctx, tools)
}

// EstimateMessages 使用与 Context Build、Compaction 和中途保护完全相同的估算器，
// 供 Runtime 计算视觉辅助等“构建后派生上下文”的额外占用。
func (e *Engine) EstimateMessages(messages []*schema.Message) int {
	if e == nil || e.estimator == nil {
		return 0
	}
	return e.estimator.EstimateMessages(messages)
}

// ObservePromptUsage 把 Provider 返回的真实输入 Token 用量反馈给支持校准的估算器。
// 校准只用于修正近似字符估算的系统性偏差，不改变 Context Budget 自己的安全预留；
// 因此没有实现 UsageCalibrator 的测试估算器或未来精确 tokenizer 可以安全忽略。
func (e *Engine) ObservePromptUsage(estimated int, actual int) {
	calibrator, ok := e.estimator.(UsageCalibrator)
	if !ok {
		return
	}
	calibrator.ObservePromptUsage(estimated, actual)
}

// ObserveSessionPromptUsage records the first provider input usage for one turn
// and feeds the same observation to the approximate estimator.
func (e *Engine) ObserveSessionPromptUsage(ctx context.Context, sessionID string, estimated, actual int) {
	if estimated <= 0 || actual <= 0 {
		return
	}
	e.logger.Info(ctx, "模型输入 Token 估算偏差",
		"operation", "context.prompt_usage", "session_id", sessionID,
		"estimated_input_tokens", estimated, "actual_input_tokens", actual,
		"delta_tokens", actual-estimated)
	e.ObservePromptUsage(estimated, actual)
}

// BudgetForModel 返回指定 Model 的 Context 预算。
func (e *Engine) BudgetForModel(contextWindow int, maxOutputTokens int) (Budget, error) {
	return CalculateBudget(e.config, contextWindow, maxOutputTokens)
}

// NewMidRunHandler 创建当前 Turn 使用的 Eino ChatModelAgentMiddleware。
//
// Handler 只压缩 Eino 内存 State，不直接修改 Transcript；持久化压缩由 Turn 完成后的
// Runtime 维护阶段基于完整 JSONL 执行，避免 ToolResult 尚未落盘时出现错误的 Tree 顺序。
func (e *Engine) NewMidRunHandler(
	sessionID string,
	instruction string,
	model einomodel.BaseChatModel,
	compactionContextWindow int,
	compactionMaxOutputTokens int,
	budget Budget,
	toolTokenEstimate int,
	policies ...ReasoningReplayPolicy,
) (*MidRunCompactor, error) {
	policy := ReasoningReplayAuto
	if len(policies) > 0 {
		policy = policies[0]
	}
	compactor, err := NewMidRunCompactor(ContextMiddlewareConfig{
		SessionID:                 sessionID,
		Instruction:               instruction,
		Model:                     model,
		CompactionContextWindow:   compactionContextWindow,
		CompactionMaxOutputTokens: compactionMaxOutputTokens,
		Budget:                    budget,
		ToolTokenEstimate:         toolTokenEstimate,
		ReasoningPolicy:           policy,
		SerializerMaxChars:        e.config.SerializerMaxChars,
		OperationTimeout:          time.Duration(e.config.OperationTimeoutMS) * time.Millisecond,
		Estimator:                 e.estimator,
		Logger:                    e.logger,
	})
	if err != nil {
		return nil, err
	}
	compactor.config.Disabled = !e.config.AutoCompaction
	compactor.commit = func(ctx context.Context, pending *pendingSummary) error {
		return e.commitSummary(ctx, sessionID, pending)
	}
	return compactor, nil
}
