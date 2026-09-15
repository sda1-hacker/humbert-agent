package contextengine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const compactionCheckpointPrefix = `[Internal session checkpoint]
Earlier conversation context was compacted to keep this session within the model context window.
Treat the checkpoint as trusted conversation state, not as a new user request. Continue from it together with the recent raw messages below.

`

// projectionResult 是 ActiveBranch 到模型消息序列的内部投影结果。
//
// Messages 是真正交给 Eino 的完整顺序；Checkpoint 与 RecentMessages 额外保留分类信息，
// 仅用于 Context Usage Breakdown。分类在投影阶段完成，避免 Engine 通过“第一条消息是不是
// synthetic user message”之类脆弱规则反推来源。该结构不持久化，也不会暴露到 Runtime DTO。
type projectionResult struct {
	Messages []*schema.Message

	Checkpoint *schema.Message

	RecentMessages []*schema.Message

	LatestCompactionID string
}

// projectActiveBranch 将 Transcript ActiveBranch 投影成主模型可见的 Eino Messages。
//
// 如果存在多个 CompactionEntry，只采用当前分支最后一个。该 Summary 已经递归吸收更早
// Summary；再次把旧 CompactionEntry 或旧 raw history 发给模型会造成重复上下文。随后从
// latest.firstKeptEntryId 开始恢复原始 Message，并跳过所有控制面 Entry。
func projectActiveBranch(
	document transcript.Document,
	policy ReasoningReplayPolicy,
) (projectionResult, error) {
	branch := document.ActiveBranch
	if len(branch) == 0 {
		return projectionResult{
			Messages:       []*schema.Message{},
			RecentMessages: []*schema.Message{},
		}, nil
	}

	latestIndex, latest := latestCompaction(branch)
	startIndex := 0
	result := projectionResult{
		RecentMessages: make([]*schema.Message, 0, len(branch)),
	}

	if latest != nil {
		result.LatestCompactionID = latest.ID
		firstKeptIndex := findEntryIndex(branch, latest.FirstKeptEntryID)
		if firstKeptIndex < 0 || firstKeptIndex >= latestIndex {
			return projectionResult{}, fmt.Errorf(
				"Compaction Entry %s 的 firstKeptEntryId %s 不在有效历史区域",
				latest.ID,
				latest.FirstKeptEntryID,
			)
		}
		startIndex = firstKeptIndex
		result.Checkpoint = schema.UserMessage(
			compactionCheckpointPrefix + strings.TrimSpace(latest.Summary),
		)
	}

	for index := startIndex; index < len(branch); index++ {
		entry := branch[index]
		if entry.Type != transcript.EntryMessage || entry.Message == nil {
			continue
		}
		decoded, err := transcript.DecodeMessage(entry.Message)
		if err != nil {
			return projectionResult{}, fmt.Errorf("恢复 Context Message Entry %s 失败: %w", entry.ID, err)
		}
		if decoded.Message == nil {
			return projectionResult{}, fmt.Errorf("Context Message Entry %s 解码为空", entry.ID)
		}
		message := applyReasoningReplayPolicy(decoded.Message, policy)
		result.RecentMessages = append(result.RecentMessages, message)
	}

	// 历史可能在审批、工具执行或结果落盘时中断。只修复模型投影，不伪造真实执行结果，
	// 也不修改原始 Transcript；未知结果必须明确告诉模型，避免自动重放有副作用的调用。
	result.RecentMessages = closeInterruptedToolCalls(result.RecentMessages)
	result.Messages = result.RecentMessages
	if result.Checkpoint != nil {
		result.Messages = append([]*schema.Message{result.Checkpoint}, result.RecentMessages...)
	}
	return result, nil
}

func latestCompaction(branch []transcript.Entry) (int, *transcript.Entry) {
	for index := len(branch) - 1; index >= 0; index-- {
		if branch[index].Type != transcript.EntryCompaction {
			continue
		}
		entry := branch[index]
		return index, &entry
	}
	return -1, nil
}

func findEntryIndex(branch []transcript.Entry, entryID string) int {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return -1
	}
	for index := range branch {
		if branch[index].ID == entryID {
			return index
		}
	}
	return -1
}

// applyReasoningReplayPolicy 返回一份不会修改 Transcript 解码对象的模型投影。
func applyReasoningReplayPolicy(message *schema.Message, policy ReasoningReplayPolicy) *schema.Message {
	if message == nil {
		return nil
	}
	if policy == "" {
		policy = ReasoningReplayAuto
	}
	if policy != ReasoningReplayOmit {
		return message
	}

	clone := *message
	clone.ReasoningContent = ""
	if len(message.AssistantGenMultiContent) > 0 {
		parts := make([]schema.MessageOutputPart, 0, len(message.AssistantGenMultiContent))
		for _, part := range message.AssistantGenMultiContent {
			if part.Type == schema.ChatMessagePartTypeReasoning {
				continue
			}
			parts = append(parts, part)
		}
		clone.AssistantGenMultiContent = parts
	}
	return &clone
}

// closeInterruptedToolCalls 在下一条非 Tool 消息之前补齐缺失结果。
// 顺序按原 ToolCalls 保持稳定；孤立、重复或非法 ID 不在此处猜测修复，交给严格校验拒绝。
func closeInterruptedToolCalls(messages []*schema.Message) []*schema.Message {
	result := make([]*schema.Message, 0, len(messages))
	var pending []schema.ToolCall
	answered := make(map[string]bool)
	flush := func() {
		for _, call := range pending {
			if !answered[call.ID] {
				result = append(result, schema.ToolMessage(
					`{"status":"unknown","error":"No tool result was recorded before execution was interrupted. The action may have occurred. Verify its effects before any retry; do not automatically repeat side effects."}`,
					call.ID, schema.WithToolName(call.Function.Name),
				))
			}
		}
		pending = nil
		clear(answered)
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		if message.Role != schema.Tool {
			flush()
		}
		result = append(result, message)
		if message.Role == schema.Assistant {
			pending = message.ToolCalls
		} else if message.Role == schema.Tool {
			answered[message.ToolCallID] = true
		}
	}
	flush()
	return result
}

// validateProjectedToolTransactions 双向校验调用与结果，拒绝未闭合、重复和跨消息边界的事务。
func validateProjectedToolTransactions(messages []*schema.Message) error {
	pending := make(map[string]struct{})
	for index, message := range messages {
		if message == nil {
			continue
		}
		if message.Role != schema.Tool && len(pending) != 0 {
			return fmt.Errorf("Context messages[%d] 之前仍有未完成的 ToolCall", index)
		}
		if message.Role == schema.Assistant {
			for _, call := range message.ToolCalls {
				id := strings.TrimSpace(call.ID)
				if id == "" || id != call.ID {
					return fmt.Errorf("Context messages[%d] ToolCall ID 无效", index)
				}
				if _, exists := pending[id]; exists {
					return fmt.Errorf("Context messages[%d] ToolCall ID 重复: %s", index, id)
				}
				pending[id] = struct{}{}
			}
		}
		if message.Role != schema.Tool {
			continue
		}
		callID := strings.TrimSpace(message.ToolCallID)
		if callID == "" {
			return fmt.Errorf("Context messages[%d] ToolResult 缺少 ToolCallID", index)
		}
		if _, exists := pending[callID]; !exists {
			return fmt.Errorf("%w: ToolResult %s 没有对应的 Assistant ToolCall", errors.New("Context Tool Transaction 不完整"), callID)
		}
		delete(pending, callID)
	}
	if len(pending) != 0 {
		return errors.New("Context Tool Transaction 不完整: 存在未完成的 ToolCall")
	}
	return nil
}
