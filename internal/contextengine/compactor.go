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

	before, err := e.Build(ctx, BuildRequest{
		SessionID:         request.SessionID,
		Instruction:       request.Instruction,
		ContextWindow:     request.ContextWindow,
		MaxOutputTokens:   request.MaxOutputTokens,
		ToolTokenEstimate: request.ToolTokenEstimate,
		ReasoningPolicy:   ReasoningReplayAuto,
	})
	if err != nil {
		return CompactResult{}, err
	}
	if !request.Force && !before.Usage.NeedsCompaction {
		return CompactResult{Compacted: false, Before: before.Usage, After: before.Usage}, nil
	}

	document, err := e.sessions.LoadTranscript(ctx, request.SessionID)
	if err != nil {
		return CompactResult{}, fmt.Errorf("读取待压缩 Session 失败: %w", err)
	}
	plan, err := planCompaction(document, budget.KeepRecentTokens, e.estimator)
	if err != nil {
		return CompactResult{}, err
	}

	serialized := serializeCompactionPlan(plan, e.config.SerializerMaxChars)
	if serialized == "" {
		return CompactResult{}, ErrNothingToCompact
	}

	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(e.config.OperationTimeoutMS)*time.Millisecond)
	defer cancel()

	response, err := request.Model.Generate(operationCtx, []*schema.Message{
		schema.SystemMessage(compactionSystemPrompt),
		schema.UserMessage(serialized),
	})
	if err != nil {
		return CompactResult{}, fmt.Errorf("调用模型生成 Compaction Checkpoint 失败: %w", err)
	}
	if response == nil {
		return CompactResult{}, errors.New("压缩模型返回空 Message")
	}
	summary, err := normalizeSummaryResult(messageVisibleText(response))
	if err != nil {
		return CompactResult{}, err
	}

	// TokensAfter 是提交前的估算：summary + retained + instruction + tools。真正提交后再
	// Build 一次得到 After Usage；持久化该值主要用于历史诊断，不作为下一次阈值事实源。
	retainedMessages, err := decodeEntries(plan.Retained, ReasoningReplayAuto)
	if err != nil {
		return CompactResult{}, err
	}
	// 使用压缩前 Snapshot 已经拆分好的 System/Tool/Memory 基础占用，而不是仅重新估算
	// request.Instruction。这样 TokensAfter 在存在 Session Memory 时不会漏算 Key Facts，
	// 同时与 Context Usage Breakdown 保持同一套分类口径。
	tokensAfterEstimate := before.Usage.SystemTokens +
		before.Usage.ToolTokens +
		before.Usage.MemoryTokens +
		e.estimator.EstimateMessage(schema.UserMessage(compactionCheckpointPrefix+summary)) +
		e.estimator.EstimateMessages(retainedMessages)

	entry, err := e.sessions.AppendCompaction(operationCtx, request.SessionID, transcript.AppendCompactionInput{
		ExpectedLeafID:   plan.ParentLeafID,
		Summary:          summary,
		FirstKeptEntryID: plan.FirstKeptEntryID,
		TokensBefore:     before.Usage.UsedTokens,
		TokensAfter:      tokensAfterEstimate,
		Details: transcript.CompactionDetails{
			Reason:        string(request.Reason),
			SplitTurn:     plan.SplitTurn,
			ReadFiles:     append([]string(nil), plan.ReadFiles...),
			ModifiedFiles: append([]string(nil), plan.ModifiedFiles...),
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
		ReasoningPolicy:   ReasoningReplayAuto,
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
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		decoded, err := transcript.DecodeMessage(entry.Message)
		if err != nil {
			return nil, fmt.Errorf("恢复 Compaction Entry %s 失败: %w", entry.ID, err)
		}
		result = append(result, applyReasoningReplayPolicy(decoded.Message, policy))
	}
	return result, nil
}
