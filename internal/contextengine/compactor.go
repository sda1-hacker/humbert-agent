package contextengine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// Compact 在当前 ActiveBranch 上执行一次持久化压缩。
//
// 流程固定为 Prepare -> Generate -> Commit：摘要模型调用发生在 Session 文件锁之外；
// Commit 时 TranscriptStore 再次验证 FirstKeptEntryID 仍在当前 ActiveBranch。如果用户在
// 摘要期间发生 Retry/Fork，提交会返回 transcript.ErrCompactionStale，不会把旧摘要接到
// 新分支。
func (e *Engine) Compact(ctx context.Context, request CompactRequest) (CompactResult, error) {
	if ctx == nil {
		return CompactResult{}, errors.New("context.Context 不能为空")
	}
	if request.Model == nil {
		return CompactResult{}, errors.New("Compaction Model 不能为空")
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return CompactResult{}, errors.New("Session ID 不能为空")
	}
	if request.Reason == "" {
		request.Reason = CompactionReasonThreshold
	}

	startedAt := time.Now()
	budget, err := CalculateBudget(e.config, request.ContextWindow, request.MaxOutputTokens)
	if err != nil {
		return CompactResult{}, fmt.Errorf("计算 Compaction Budget 失败: %w", err)
	}

	document, err := e.sessions.LoadTranscript(ctx, request.SessionID)
	if err != nil {
		return CompactResult{}, fmt.Errorf("读取待压缩 Session 失败: %w", err)
	}
	before, err := e.buildFromDocument(ctx, BuildRequest{
		SessionID:         request.SessionID,
		Instruction:       request.Instruction,
		ContextWindow:     request.ContextWindow,
		MaxOutputTokens:   request.MaxOutputTokens,
		ToolTokenEstimate: request.ToolTokenEstimate,
		ReasoningPolicy:   request.ReasoningPolicy,
	}, budget, document)
	if err != nil {
		return CompactResult{}, err
	}
	shouldCompact := before.Usage.NeedsCompaction
	if request.UseSoftLimit {
		shouldCompact = before.Usage.NeedsSoftCompaction
	}
	if !request.Force && !shouldCompact {
		return CompactResult{Compacted: false, Before: before.Usage, After: before.Usage}, nil
	}

	// 如果固定上下文本身已经吃掉硬安全线，继续压缩历史不会解决问题。这里立即返回
	// 带分类占用的错误，让 UI/日志能够明确提示“工具定义或系统指令过大”，而不是重复
	// 调用压缩模型三次以后才得到泛化错误。
	if before.Budget.FixedTokens >= before.Budget.ThresholdTokens-512 {
		return CompactResult{}, &FixedContextBudgetError{
			ContextWindow:       before.Budget.ContextWindow,
			ThresholdTokens:     before.Budget.ThresholdTokens,
			SystemTokens:        before.Usage.SystemTokens,
			ToolTokens:          before.Usage.ToolTokens,
			MemoryTokens:        before.Usage.MemoryTokens,
			ReferenceTokens:     before.Usage.ReferenceTokens,
			HistoryBudgetTokens: before.Budget.HistoryBudgetTokens,
		}
	}

	targetRecent := before.Budget.TargetRecentTokens
	if targetRecent <= 0 {
		// 固定上下文已经吃掉绝大多数窗口时仍尽量保留最后一个安全消息；若连这样都无法
		// 降到阈值，后续 tokensAfterEstimate 会返回明确的固定开销诊断。
		targetRecent = 1
	}
	plan, err := planCompaction(document, targetRecent, e.estimator, request.ReasoningPolicy)
	if err != nil {
		return CompactResult{}, err
	}

	toSummarize, err := decodeEntries(plan.ToSummarize, ReasoningReplayOmit)
	if err != nil {
		return CompactResult{}, err
	}
	// 压缩器必须与正常投影共享“中断工具调用结果未知”的恢复语义，不能把只有 ToolCall
	// 没有 ToolResult 的崩溃历史总结成已经执行成功。
	toSummarize = closeInterruptedToolCalls(toSummarize)

	compactionWindow := request.CompactionContextWindow
	if compactionWindow <= 0 {
		compactionWindow = request.ContextWindow
	}
	compactionOutput := request.CompactionMaxOutputTokens
	if compactionOutput <= 0 {
		compactionOutput = request.MaxOutputTokens
	}
	generator := CheckpointGenerator{
		Model:            request.Model,
		Estimator:        e.estimator,
		ContextWindow:    compactionWindow,
		MaxOutputTokens:  compactionOutput,
		OperationTimeout: time.Duration(e.config.OperationTimeoutMS) * time.Millisecond,
		ArgumentMaxRunes: e.config.SerializerMaxChars,
	}
	summary, err := generator.Generate(ctx, CheckpointInput{
		PreviousCheckpoint: plan.PreviousSummary,
		Messages:           toSummarize,
	})
	if err != nil {
		// 远程/本地压缩模型不可用时，优先让会话继续，而不是把已经完成的用户 Turn
		// 变成失败。应急检查点不会冒充完整摘要，并且 durable metadata 会保留本次
		// 来源范围，模型后续可以通过 session_history 精确追回旧细节。
		e.logger.Warn(
			ctx,
			"生成 Context Checkpoint 失败，改用本地应急检查点",
			"operation", "context.compaction.fallback",
			"session_id", request.SessionID,
			"reason", string(request.Reason),
			"error", err.Error(),
		)
		summary = localFallbackCheckpoint(plan.PreviousSummary, toSummarize, e.config.SerializerMaxChars)
		if strings.TrimSpace(summary) == "" {
			return CompactResult{}, fmt.Errorf("生成 Compaction Checkpoint 失败且本地兜底为空: %w", err)
		}
	}

	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(e.config.OperationTimeoutMS)*time.Millisecond)
	defer cancel()

	// TokensAfter 是提交前的估算：summary + retained + instruction + tools。真正提交后再
	// Build 一次得到 After Usage；持久化该值主要用于历史诊断，不作为下一次阈值事实源。
	retainedMessages, err := decodeEntries(plan.Retained, request.ReasoningPolicy)
	if err != nil {
		return CompactResult{}, err
	}
	// 使用压缩前 Snapshot 已经拆分好的 System/Tool/Memory/Reference 基础占用，而不是仅重新估算
	// request.Instruction。这样 TokensAfter 在存在 Session Memory 时不会漏算 Key Facts，
	// 同时与 Context Usage Breakdown 保持同一套分类口径。
	tokensAfterEstimate := before.Usage.SystemTokens +
		before.Usage.ToolTokens +
		before.Usage.MemoryTokens +
		before.Usage.ReferenceTokens +
		e.estimator.EstimateMessage(schema.UserMessage(compactionCheckpointPrefix+summary)) +
		e.estimator.EstimateMessages(retainedMessages)
	if tokensAfterEstimate >= before.Usage.UsedTokens {
		return CompactResult{}, fmt.Errorf(
			"%w: 压缩没有降低 Context 占用: before=%d after_estimate=%d",
			ErrContextBudgetExceeded,
			before.Usage.UsedTokens,
			tokensAfterEstimate,
		)
	}

	entry, err := e.sessions.AppendCompaction(operationCtx, request.SessionID, transcript.AppendCompactionInput{
		ExpectedLeafID:   plan.ParentLeafID,
		Summary:          summary,
		FirstKeptEntryID: plan.FirstKeptEntryID,
		TokensBefore:     before.Usage.UsedTokens,
		TokensAfter:      tokensAfterEstimate,
		Details: transcript.CompactionDetails{
			Reason:             string(request.Reason),
			SplitTurn:          plan.SplitTurn,
			ReadFiles:          append([]string(nil), plan.ReadFiles...),
			ModifiedFiles:      append([]string(nil), plan.ModifiedFiles...),
			WindowGeneration:   before.Window.Generation + 1,
			SourceFirstEntryID: firstPlanEntryID(plan.ToSummarize),
			SourceLastEntryID:  lastPlanEntryID(plan.ToSummarize),
			SourceEntryCount:   len(plan.ToSummarize),
		},
	})
	if err != nil {
		return CompactResult{}, fmt.Errorf("提交 CompactionEntry 失败: %w", err)
	}

	after, err := e.Build(operationCtx, BuildRequest{
		SessionID:         request.SessionID,
		Instruction:       request.Instruction,
		ContextWindow:     request.ContextWindow,
		MaxOutputTokens:   request.MaxOutputTokens,
		ToolTokenEstimate: request.ToolTokenEstimate,
		ReasoningPolicy:   request.ReasoningPolicy,
	})
	if err != nil {
		return CompactResult{}, fmt.Errorf("重建压缩后 Context 失败: %w", err)
	}

	e.logger.Info(
		ctx,
		"Context Compaction 已完成",
		"operation", "context.compaction.complete",
		"session_id", request.SessionID,
		"compaction_id", entry.ID,
		"reason", string(request.Reason),
		"split_turn", plan.SplitTurn,
		"tokens_before", before.Usage.UsedTokens,
		"tokens_after", after.Usage.UsedTokens,
		logging.Duration(startedAt),
	)

	return CompactResult{
		Compacted:        true,
		CompactionID:     entry.ID,
		FirstKeptEntryID: plan.FirstKeptEntryID,
		Before:           before.Usage,
		After:            after.Usage,
	}, nil
}

func decodeEntries(entries []transcript.Entry, policy ReasoningReplayPolicy) ([]*schema.Message, error) {
	result := make([]*schema.Message, 0, len(entries))
	latestUserIndex := latestUserMessageIndex(entries, 0)
	for index, entry := range entries {
		if entry.Message == nil {
			continue
		}
		decoded, err := transcript.DecodeMessage(entry.Message)
		if err != nil {
			return nil, fmt.Errorf("恢复 Compaction Entry %s 失败: %w", entry.ID, err)
		}
		result = append(result, applyReasoningReplayPolicy(decoded.Message, reasoningPolicyForIndex(policy, index, latestUserIndex)))
	}
	return result, nil
}

func firstPlanEntryID(entries []transcript.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[0].ID
}

func lastPlanEntryID(entries []transcript.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].ID
}
