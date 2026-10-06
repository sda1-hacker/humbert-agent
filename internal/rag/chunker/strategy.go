package chunker

// StrategyTier 表示实际执行的分块算法；recursive 配置复用 legacy 算法。
type StrategyTier string

const (
	TierHeading   StrategyTier = "heading"
	TierHeuristic StrategyTier = "heuristic"
	TierLegacy    StrategyTier = "legacy"
)

// TierRejection 记录某种分块策略被质量校验拒绝的原因。
type TierRejection struct {
	Tier   StrategyTier `json:"tier"`
	Reason string       `json:"reason"`
}

// Diagnostics 记录最终策略、尝试顺序与拒绝原因，便于调整分块参数。
type Diagnostics struct {
	SelectedTier StrategyTier    `json:"selected_tier"`
	TierChain    []StrategyTier  `json:"tier_chain"`
	Rejected     []TierRejection `json:"rejected"`
	// Profile 仅在自动选择策略时生成。
	Profile *DocProfile `json:"profile,omitempty"`
}

// SelectStrategy 根据标题、章节和分页信号选择策略，递归切分始终作为最后的兜底。
func SelectStrategy(profile *DocProfile) []StrategyTier {
	if profile == nil {
		return []StrategyTier{TierLegacy}
	}
	chain := make([]StrategyTier, 0, 3)
	if profile.MdHeadingTotal >= 3 && profile.HeadingDensity() > 0.005 && profile.DominantHeadingLevel() > 0 {
		chain = append(chain, TierHeading)
	}
	chapterCount := profile.GermanChapterCount + profile.EnglishChapterCount + profile.ChineseChapterCount
	if profile.HeuristicMarkerTotal() >= 5 || profile.FormFeedCount > 0 || chapterCount > 0 {
		chain = append(chain, TierHeuristic)
	}
	return append(chain, TierLegacy)
}

// ensureDefaults 补齐基础配置，再根据近似 token 预算收紧块大小和重叠范围。
// 这里的预算用于切分；完整模型输入仍由索引器独立检查。
func ensureDefaults(cfg SplitterConfig) SplitterConfig {
	cfg = NormalizeSplitterConfig(cfg)
	if cfg.TokenLimit > 0 {
		lang := LangMixed
		if len(cfg.Languages) > 0 {
			lang = cfg.Languages[0]
		}
		if budget := CharsForTokenLimit(cfg.TokenLimit, lang); budget > 0 && budget < cfg.ChunkSize {
			cfg.ChunkSize = budget
		}
	}
	if cfg.ChunkOverlap > cfg.ChunkSize/2 {
		cfg.ChunkOverlap = cfg.ChunkSize / 2
	}
	return cfg
}

// resolveChainWithProfile 将显式策略或自动策略转换成实际执行顺序。
func resolveChainWithProfile(text string, cfg SplitterConfig) ([]StrategyTier, *DocProfile) {
	switch cfg.Strategy {
	case StrategyHeading:
		return []StrategyTier{TierHeading, TierLegacy}, nil
	case StrategyHeuristic:
		return []StrategyTier{TierHeuristic, TierLegacy}, nil
	case StrategyRecursive, StrategyLegacy, "":
		return []StrategyTier{TierLegacy}, nil
	default:
		profile := ProfileDocument(text)
		return SelectStrategy(profile), profile
	}
}

// Split 执行分块和最终 token 预算处理；空策略使用递归切分。
func Split(text string, cfg SplitterConfig) []Chunk {
	if text == "" {
		return nil
	}
	cfg = ensureDefaults(cfg)
	return enforceTokenTarget(text, splitConfigured(text, cfg, nil), cfg)
}

// SplitWithDiagnostics 与 Split 使用同一条执行路径，并额外返回策略诊断。
func SplitWithDiagnostics(text string, cfg SplitterConfig) ([]Chunk, *Diagnostics) {
	cfg = ensureDefaults(cfg)
	diag := &Diagnostics{SelectedTier: TierLegacy}
	return enforceTokenTarget(text, splitConfigured(text, cfg, diag), cfg), diag
}

// splitConfigured 依次尝试策略；最后一种策略即使质量不达标也保留结果，避免丢失原文。
// diag 为空时跳过诊断收集，普通分块与预览不再维护两套循环。
func splitConfigured(text string, cfg SplitterConfig, diag *Diagnostics) []Chunk {
	if text == "" {
		return nil
	}
	chain, profile := resolveChainWithProfile(text, cfg)
	if diag != nil {
		diag.TierChain, diag.Profile = chain, profile
	}
	totalChars := RuneLen(text)
	for i, tier := range chain {
		chunks := runTier(tier, text, cfg, profile)
		validation := ValidateChunks(chunks, totalChars, cfg.ChunkSize)
		if diag != nil && !validation.OK {
			diag.Rejected = append(diag.Rejected, TierRejection{Tier: tier, Reason: validation.Reason})
		}
		if validation.OK || (tier == TierLegacy && i == len(chain)-1 && chunks != nil) {
			if diag != nil {
				diag.SelectedTier = tier
			}
			return chunks
		}
	}
	return SplitText(text, cfg)
}

// runTier 直接调用对应算法，不再通过 init 注册可变函数。
func runTier(tier StrategyTier, text string, cfg SplitterConfig, profile *DocProfile) []Chunk {
	switch tier {
	case TierHeading:
		return splitByHeadings(text, cfg, profile)
	case TierHeuristic:
		return splitByHeuristics(text, cfg, profile)
	default:
		return SplitText(text, cfg)
	}
}
