package contextengine

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// Plan 是一次 Compaction 的纯规划结果。
//
// Plan 不调用模型、不写磁盘，因此可以通过大量单元测试覆盖边界条件。真正的 Compactor
// 只负责把 ToSummarize 序列化给摘要模型并在结果返回后提交 CompactionEntry。
type Plan struct {
	// ParentLeafID 是 Prepare 阶段观察到的当前 Leaf。摘要生成后 Commit 必须确认该 Leaf
	// 未变化，否则旧摘要不能挂到后来新增的消息之后。
	ParentLeafID string

	PreviousSummary string

	ToSummarize []transcript.Entry

	Retained []transcript.Entry

	FirstKeptEntryID string

	SplitTurn bool

	ReadFiles []string

	ModifiedFiles []string
}

// planCompaction 根据 KeepRecent Token 预算选择安全切点。
func planCompaction(
	document transcript.Document,
	keepRecentTokens int,
	estimator Estimator,
	policies ...ReasoningReplayPolicy,
) (Plan, error) {
	policy := ReasoningReplayAuto
	if len(policies) > 0 {
		policy = policies[0]
	}
	return planCompactionForWindow(document, keepRecentTokens, 0, estimator, policy)
}

func planCompactionForWindow(
	document transcript.Document,
	keepRecentTokens int,
	contextWindow int,
	estimator Estimator,
	policy ReasoningReplayPolicy,
) (Plan, error) {
	if keepRecentTokens <= 0 {
		return Plan{}, errors.New("KeepRecentTokens 必须大于 0")
	}
	if estimator == nil {
		return Plan{}, errors.New("Token Estimator 不能为空")
	}

	branch := document.ActiveBranch
	if len(branch) < 2 {
		return Plan{}, ErrNothingToCompact
	}

	baseIndex := 0
	previousSummary := ""
	latestIndex, latest := latestCompaction(branch)
	repairBoundaryIndex := -1
	if latest != nil {
		previousSummary = strings.TrimSpace(latest.Summary)
		index := findEntryIndex(branch, latest.FirstKeptEntryID)
		if index < 0 {
			return Plan{}, fmt.Errorf("上一次 Compaction firstKeptEntryId %s 不在当前分支", latest.FirstKeptEntryID)
		}
		baseIndex = index
		if latest.Details != nil && latest.Details.Degraded {
			repairBoundaryIndex = index
			baseIndex = findEntryIndex(branch, latest.Details.SourceFirstEntryID)
			if baseIndex < 0 || baseIndex >= repairBoundaryIndex {
				return Plan{}, fmt.Errorf("应急检查点 %s 的来源范围无效", latest.ID)
			}
			previousSummary = ""
			for i := latestIndex - 1; i >= 0; i-- {
				prior := branch[i]
				if prior.Type == transcript.EntryCompaction && (prior.Details == nil || !prior.Details.Degraded) {
					previousSummary = strings.TrimSpace(prior.Summary)
					break
				}
			}
		}
	}

	messageIndices := make([]int, 0, len(branch)-baseIndex)
	messageTokens := make(map[int]int)
	projected := make([]*schema.Message, 0, len(branch)-baseIndex)
	entryIDs := make([]string, 0, len(branch)-baseIndex)
	latestUserIndex := latestUserMessageIndex(branch, baseIndex)
	for index := baseIndex; index < len(branch); index++ {
		entry := branch[index]
		if entry.Type != transcript.EntryMessage || entry.Message == nil {
			continue
		}
		decoded, err := transcript.DecodeMessage(entry.Message)
		if err != nil {
			return Plan{}, fmt.Errorf("估算 Message Entry %s 失败: %w", entry.ID, err)
		}
		messageIndices = append(messageIndices, index)
		projected = append(projected, normalizeLegacyContextToolGuidance(applyReasoningReplayPolicy(decoded.Message, reasoningPolicyForIndex(policy, index, latestUserIndex))))
		entryIDs = append(entryIDs, entry.ID)
	}
	if contextWindow > 0 {
		limitWindowToolResults(projected, entryIDs, ToolResultWindowChars(contextWindow))
	}
	for pos, cost := range projectedMessageCosts(estimator, projected) {
		messageTokens[messageIndices[pos]] = cost
	}
	if len(messageIndices) < 2 {
		return Plan{}, ErrNothingToCompact
	}

	// 从最新消息向前累计，找到满足 KeepRecent 的最早候选消息。
	retainedTokens := 0
	candidatePos := len(messageIndices) - 1
	for pos := len(messageIndices) - 1; pos >= 0; pos-- {
		retainedTokens += messageTokens[messageIndices[pos]]
		candidatePos = pos
		if retainedTokens >= keepRecentTokens {
			break
		}
	}
	candidateIndex := messageIndices[candidatePos]
	if candidateIndex <= baseIndex && repairBoundaryIndex > baseIndex {
		for pos, index := range messageIndices {
			if index == repairBoundaryIndex {
				candidatePos, candidateIndex = pos, index
				break
			}
		}
	}
	if candidateIndex <= baseIndex {
		return Plan{}, ErrNothingToCompact
	}

	// 优先回退到完整 User Turn 边界。允许最多 25% 的 recent overshoot；超过则说明
	// 当前 User Turn 本身过大，进入 SplitTurn，避免一个长工具循环让压缩永久无法发生。
	firstKeptIndex := candidateIndex
	splitTurn := true
	for pos := candidatePos; pos >= 0; pos-- {
		idx := messageIndices[pos]
		if idx < baseIndex {
			break
		}
		entry := branch[idx]
		if entry.Message == nil || entry.Message.Role != transcript.RoleUser {
			continue
		}
		userRetained := tokensFromMessagePosition(messageIndices, messageTokens, pos)
		if idx > baseIndex && userRetained <= keepRecentTokens+keepRecentTokens/4 {
			firstKeptIndex = idx
			splitTurn = false
		}
		break
	}

	// SplitTurn 时绝不能从 ToolResult 开始。向前找到产生该 ToolResult 的 Assistant
	// ToolCall；这样 retained 区域始终包含完整 tool_call -> tool_result transaction。
	if splitTurn {
		adjusted, err := adjustSplitBoundaryForToolTransaction(branch, messageIndices, firstKeptIndex)
		if err != nil {
			return Plan{}, err
		}
		firstKeptIndex = adjusted
	}
	if firstKeptIndex <= baseIndex {
		return Plan{}, ErrNothingToCompact
	}

	toSummarize := make([]transcript.Entry, 0, firstKeptIndex-baseIndex)
	for index := baseIndex; index < firstKeptIndex; index++ {
		if branch[index].Type == transcript.EntryMessage && branch[index].Message != nil {
			toSummarize = append(toSummarize, branch[index])
		}
	}
	if len(toSummarize) == 0 {
		return Plan{}, ErrNothingToCompact
	}

	retained := make([]transcript.Entry, 0, len(branch)-firstKeptIndex)
	for index := firstKeptIndex; index < len(branch); index++ {
		if branch[index].Type == transcript.EntryMessage && branch[index].Message != nil {
			retained = append(retained, branch[index])
		}
	}
	if len(retained) == 0 {
		return Plan{}, ErrNothingToCompact
	}

	readFiles, modifiedFiles := extractArtifactPaths(toSummarize)
	if latest != nil && latest.Details != nil {
		readFiles = sortedUniqueStrings(append(readFiles, latest.Details.ReadFiles...))
		modifiedFiles = sortedUniqueStrings(append(modifiedFiles, latest.Details.ModifiedFiles...))
	}
	return Plan{
		ParentLeafID:     document.LeafID,
		PreviousSummary:  previousSummary,
		ToSummarize:      toSummarize,
		Retained:         retained,
		FirstKeptEntryID: branch[firstKeptIndex].ID,
		SplitTurn:        splitTurn,
		ReadFiles:        readFiles,
		ModifiedFiles:    modifiedFiles,
	}, nil
}

func projectedMessageCosts(estimator Estimator, messages []*schema.Message) []int {
	if detailed, ok := estimator.(interface{ EstimateMessageCosts([]*schema.Message) []int }); ok {
		return detailed.EstimateMessageCosts(messages)
	}
	costs := make([]int, len(messages))
	for i, message := range messages {
		costs[i] = estimator.EstimateMessage(message)
	}
	return costs
}

func sortedUniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return setToSortedSlice(set)
}

func tokensFromMessagePosition(indices []int, tokens map[int]int, startPos int) int {
	total := 0
	for pos := startPos; pos < len(indices); pos++ {
		total += tokens[indices[pos]]
	}
	return total
}

func adjustSplitBoundaryForToolTransaction(
	branch []transcript.Entry,
	messageIndices []int,
	candidateIndex int,
) (int, error) {
	entry := branch[candidateIndex]
	if entry.Message == nil || entry.Message.Role != transcript.RoleToolResult {
		return candidateIndex, nil
	}
	callID := strings.TrimSpace(entry.Message.ToolCallID)
	if callID == "" {
		return 0, errors.New("ToolResult 缺少 ToolCallID，无法规划安全 Compaction 边界")
	}

	for pos := len(messageIndices) - 1; pos >= 0; pos-- {
		idx := messageIndices[pos]
		if idx >= candidateIndex {
			continue
		}
		message := branch[idx].Message
		if message == nil || message.Role != transcript.RoleAssistant {
			continue
		}
		for _, block := range message.Content {
			if block.Type == transcript.ContentToolCall && block.ID == callID {
				return idx, nil
			}
		}
	}
	return 0, fmt.Errorf("ToolResult %s 找不到对应 Assistant ToolCall", callID)
}

func extractArtifactPaths(entries []transcript.Entry) ([]string, []string) {
	readSet := make(map[string]struct{})
	modifiedSet := make(map[string]struct{})

	// 先建立 ToolResult 状态。只有明确记录了成功结果的调用才能被记为读过/修改过文件；
	// Permission Deny、用户拒绝、Tool Error 或崩溃后缺失结果都不能污染 Artifact 状态。
	results := make(map[string]bool)
	for _, entry := range entries {
		if entry.Message == nil || entry.Message.Role != transcript.RoleToolResult {
			continue
		}
		callID := strings.TrimSpace(entry.Message.ToolCallID)
		if callID != "" {
			results[callID] = transcript.ToolResultSucceeded(entry.Message)
		}
	}

	for _, entry := range entries {
		if entry.Message == nil || entry.Message.Role != transcript.RoleAssistant {
			continue
		}
		for _, block := range entry.Message.Content {
			if block.Type != transcript.ContentToolCall || len(block.Arguments) == 0 || !results[block.ID] {
				continue
			}
			read, modified := transcript.FileArtifactPaths(block.Name, block.Arguments)
			for _, path := range read {
				readSet[path] = struct{}{}
			}
			for _, path := range modified {
				modifiedSet[path] = struct{}{}
			}
		}
	}

	return setToSortedSlice(readSet), setToSortedSlice(modifiedSet)
}

func setToSortedSlice(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
