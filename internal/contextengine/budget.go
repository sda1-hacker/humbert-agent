package contextengine

import (
	"errors"
	"fmt"
	"math"

	"github.com/sda1-hacker/humbert-agent/internal/config"
)

// CalculateBudget 根据模型能力与 Viper ContextConfig 计算本次运行预算。
//
// 大窗口模型使用 max(最小 reserve, context*largeRatio, maxOutputTokens)；小窗口模型
// 不套固定 16k，而使用 max(context*smallRatio, maxOutputTokens)，避免 16k 模型被固定
// reserve 吃掉全部输入空间。KeepRecent 使用 clamp(context*ratio, min, max)。
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

	keepRecent := int(math.Ceil(float64(contextWindow) * cfg.KeepRecentRatio))
	keepRecent = maxInt(keepRecent, cfg.KeepRecentMinTokens)
	keepRecent = minInt(keepRecent, cfg.KeepRecentMaxTokens)

	// Recent Tail 本身必须小于可用阈值，否则 Planner 永远无法产生有效压缩切点。
	threshold := contextWindow - reserve
	if keepRecent >= threshold {
		keepRecent = maxInt(256, threshold/2)
	}

	return Budget{
		ContextWindow:    contextWindow,
		MaxOutputTokens:  maxOutputTokens,
		ReserveTokens:    reserve,
		ThresholdTokens:  threshold,
		KeepRecentTokens: keepRecent,
	}, nil
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
