package contextengine

import (
	"errors"
	"fmt"
	"math"

	"github.com/sda1-hacker/humbert-agent/internal/config"
)

// CalculateBudget 根据模型能力与 ContextConfig 计算“基础预算”。此时尚不知道当前 Turn
// 的 System/Tool/Memory 固定开销，因此 PreferredRecentTokens 只代表期望值；Engine.Build
// 会再调用 ResolveBudgetForFixedContext 得到真正的 TargetRecentTokens。
func CalculateBudget(cfg config.ContextConfig, contextWindow int, maxOutputTokens int) (Budget, error) {
	if contextWindow <= 0 {
		return Budget{}, errors.New("Context Window 必须大于 0")
	}
	if maxOutputTokens <= 0 || maxOutputTokens >= contextWindow {
		return Budget{}, errors.New("Max Output Tokens 必须大于 0 且小于 Context Window")
	}

	var reserve int
	if contextWindow < cfg.SmallWindowThreshold {
		reserve = int(math.Ceil(float64(contextWindow) * cfg.SmallReserveRatio))
		reserve = maxInt(reserve, maxOutputTokens)
	} else {
		reserve = int(math.Ceil(float64(contextWindow) * cfg.LargeReserveRatio))
		reserve = maxInt(reserve, cfg.MinReserveTokens)
		reserve = maxInt(reserve, maxOutputTokens)
	}
	if reserve >= contextWindow {
		return Budget{}, fmt.Errorf("Context Reserve %d 已达到或超过 Context Window %d", reserve, contextWindow)
	}

	preferredRecent := int(math.Ceil(float64(contextWindow) * cfg.KeepRecentRatio))
	preferredRecent = maxInt(preferredRecent, cfg.KeepRecentMinTokens)
	preferredRecent = minInt(preferredRecent, cfg.KeepRecentMaxTokens)

	hard := contextWindow - reserve
	if preferredRecent >= hard {
		preferredRecent = maxInt(256, hard/2)
	}

	checkpointBudget := int(math.Ceil(float64(contextWindow) * 0.04))
	checkpointBudget = maxInt(checkpointBudget, 2048)
	checkpointBudget = minInt(checkpointBudget, 8192)
	checkpointBudget = minInt(checkpointBudget, maxInt(256, hard/3))

	soft := int(math.Floor(float64(hard) * 0.85))
	if soft <= 0 || soft >= hard {
		soft = maxInt(1, hard-1)
	}

	return Budget{
		ContextWindow:          contextWindow,
		MaxOutputTokens:        maxOutputTokens,
		ReserveTokens:          reserve,
		ThresholdTokens:        hard,
		SoftThresholdTokens:    soft,
		KeepRecentTokens:       preferredRecent,
		PreferredRecentTokens:  preferredRecent,
		TargetRecentTokens:     preferredRecent,
		CheckpointBudgetTokens: checkpointBudget,
		HistoryBudgetTokens:    hard,
	}, nil
}

// ResolveBudgetForFixedContext 把本轮不可压缩的固定开销纳入预算，计算真正可用于历史的容量。
// checkpointTokens 是当前已有检查点的实际占用，只用于防止软阈值低于当前基础占用；新的
// 压缩规划仍按 CheckpointBudgetTokens 为下一检查点预留空间。
func ResolveBudgetForFixedContext(base Budget, fixedTokens int, checkpointTokens int) Budget {
	resolved := base
	fixedTokens = maxInt(fixedTokens, 0)
	checkpointTokens = maxInt(checkpointTokens, 0)
	resolved.FixedTokens = fixedTokens
	resolved.HistoryBudgetTokens = maxInt(0, resolved.ThresholdTokens-fixedTokens)

	availableRecent := resolved.HistoryBudgetTokens - resolved.CheckpointBudgetTokens
	if availableRecent < 0 {
		availableRecent = 0
	}
	target := minInt(resolved.PreferredRecentTokens, availableRecent)
	if target > 0 && target < 256 {
		target = minInt(256, resolved.HistoryBudgetTokens)
	}
	resolved.TargetRecentTokens = maxInt(target, 0)
	resolved.KeepRecentTokens = resolved.TargetRecentTokens

	// 软阈值必须至少覆盖当前固定上下文和已有检查点，否则一个新 Session 会永远处于
	// “需要提前压缩”状态。剩余可压缩空间按 80% 使用后触发主动维护。
	compressibleCapacity := maxInt(0, resolved.ThresholdTokens-fixedTokens)
	soft := fixedTokens + int(math.Floor(float64(compressibleCapacity)*0.80))
	minimum := fixedTokens + checkpointTokens
	if soft < minimum {
		soft = minimum
	}
	if soft >= resolved.ThresholdTokens {
		soft = maxInt(0, resolved.ThresholdTokens-1)
	}
	resolved.SoftThresholdTokens = soft
	return resolved
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
