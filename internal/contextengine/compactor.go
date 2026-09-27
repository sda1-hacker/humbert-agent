package contextengine

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// Compact 与自动压缩共用 Eino Summarize，只额外负责读取稳定窗口和提交事务。
func (e *Engine) Compact(ctx context.Context, request CompactRequest) (CompactResult, error) {
	before, err := e.Build(ctx, BuildRequest{SessionID: request.SessionID, Instruction: request.Instruction, ContextWindow: request.ContextWindow, MaxOutputTokens: request.MaxOutputTokens, ToolTokenEstimate: request.ToolTokenEstimate, ReasoningPolicy: request.ReasoningPolicy})
	if err != nil {
		return CompactResult{}, err
	}
	if !request.Force && !before.Usage.NeedsSoftCompaction {
		return CompactResult{Before: before.Usage, After: before.Usage}, nil
	}
	handler, err := e.NewMidRunHandler(request.SessionID, request.Instruction, request.Model, request.CompactionContextWindow, request.CompactionMaxOutputTokens, before.Budget, request.ToolTokenEstimate, request.ReasoningPolicy)
	if err != nil {
		return CompactResult{}, err
	}
	if _, err = handler.Summarize(ctx, &adk.ChatModelAgentState{Messages: before.Messages}); err != nil {
		return CompactResult{}, err
	}
	if err = handler.Commit(ctx); err != nil {
		return CompactResult{}, err
	}
	after, err := e.Build(ctx, BuildRequest{SessionID: request.SessionID, Instruction: request.Instruction, ContextWindow: request.ContextWindow, MaxOutputTokens: request.MaxOutputTokens, ToolTokenEstimate: request.ToolTokenEstimate, ReasoningPolicy: request.ReasoningPolicy})
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{Compacted: true, CompactionID: after.Window.CheckpointID, FirstKeptEntryID: after.Window.StartEntryID, Before: before.Usage, After: after.Usage}, nil
}

const entryIDKey = "humbert_entry_id"

// commitSummary 在当前分支中定位保留边界，并用 ExpectedLeaf 防止把摘要写入并发变化的分支。
// 初始消息携带 EntryID；本轮新生成的消息使用唯一 ToolCallID 定位，不比较可能重复的正文。
func (e *Engine) commitSummary(ctx context.Context, sessionID string, pending *pendingSummary) error {
	doc, err := e.sessions.LoadContextTranscript(ctx, sessionID)
	if err != nil {
		return err
	}
	kept := -1
	for i, entry := range doc.ActiveBranch {
		if entry.Message == nil {
			continue
		}
		if id, _ := pending.FirstKept.Extra[entryIDKey].(string); id != "" {
			if entry.ID == id {
				kept = i
				break
			}
			continue
		}
		decoded, err := transcript.DecodeMessage(entry.Message)
		if err != nil {
			return err
		}
		if sameMessageIdentity(pending.FirstKept, decoded.Message) {
			kept = i
			break
		}
	}
	if kept < 0 {
		return fmt.Errorf("%w: 摘要保留边界尚未落盘或已经离开当前分支", transcript.ErrCompactionStale)
	}
	first, last, count := "", "", 0
	for _, entry := range doc.ActiveBranch[:kept] {
		if entry.Message == nil {
			continue
		}
		if first == "" {
			first = entry.ID
		}
		last = entry.ID
		count++
	}
	if count == 0 {
		return ErrNothingToCompact
	}
	generation := 1
	if _, previous := latestCompaction(doc.ActiveBranch); previous != nil && previous.Details != nil {
		generation = previous.Details.WindowGeneration + 1
	}
	_, err = e.sessions.AppendCompaction(ctx, sessionID, transcript.AppendCompactionInput{
		ExpectedLeafID: doc.LeafID, Summary: pending.Summary, FirstKeptEntryID: doc.ActiveBranch[kept].ID,
		TokensBefore: pending.TokensBefore, TokensAfter: pending.TokensAfter,
		Details: transcript.CompactionDetails{Reason: "summarization", WindowGeneration: generation, SourceFirstEntryID: first, SourceLastEntryID: last, SourceEntryCount: count},
	})
	return err
}

func sameMessageIdentity(a, b *schema.Message) bool {
	if a == nil || b == nil || a.Role != b.Role {
		return false
	}
	if a.Role == schema.Tool {
		return a.ToolCallID != "" && a.ToolCallID == b.ToolCallID
	}
	if len(a.ToolCalls) == 0 || len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i, call := range a.ToolCalls {
		if call.ID == "" || call.ID != b.ToolCalls[i].ID {
			return false
		}
	}
	return true
}
