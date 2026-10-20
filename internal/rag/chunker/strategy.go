package chunker

import (
	"context"
	"errors"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

type StrategyTier string

// ErrChunkLimit 在生成中止损，避免先创建整篇文档的所有分块再检查上限。
var ErrChunkLimit = errors.New("chunker: chunk limit exceeded")

const (
	TierHeading   StrategyTier = "heading"
	TierHeuristic StrategyTier = "heuristic"
	TierLegacy    StrategyTier = "legacy"
)

// TierRejection 保存策略未通过质量检查的原因。
type TierRejection struct {
	Tier   StrategyTier `json:"tier"`
	Reason string       `json:"reason"`
}

// Diagnostics 记录最终策略、回退及文档画像，供导入预览和参数调试使用。
type Diagnostics struct {
	SelectedTier StrategyTier    `json:"selected_tier"`
	TierChain    []StrategyTier  `json:"tier_chain"`
	Rejected     []TierRejection `json:"rejected"`
	Profile      *DocProfile     `json:"profile,omitempty"`
}

func SelectStrategy(p *DocProfile) []StrategyTier {
	chain := []StrategyTier{}
	if p != nil {
		if p.MdHeadingTotal >= 3 && p.HeadingDensity() > 0.005 {
			chain = append(chain, TierHeading)
		}
		if p.HeuristicMarkerTotal() >= 5 || p.FormFeedCount > 0 || p.GermanChapterCount+p.EnglishChapterCount+p.ChineseChapterCount > 0 {
			chain = append(chain, TierHeuristic)
		}
	}
	return append(chain, TierLegacy)
}

// ensureDefaults 统一大小、重叠和近似 Token 目标；实际模型输入由导入阶段再次检查。
func ensureDefaults(cfg ragconfig.Splitter) ragconfig.Splitter {
	cfg = cfg.Effective()
	if cfg.TokenLimit > 0 {
		lang := LangMixed
		if len(cfg.Languages) > 0 {
			lang = cfg.Languages[0]
		}
		if budget := CharsForTokenLimit(cfg.TokenLimit, lang); budget > 0 {
			cfg.ChunkSize = min(cfg.ChunkSize, budget)
		}
	}
	cfg.ChunkOverlap = min(cfg.ChunkOverlap, cfg.ChunkSize/2)
	return cfg
}

func Split(text string, cfg ragconfig.Splitter) []Chunk {
	chunks, _, _ := SplitContext(context.Background(), text, cfg)
	return chunks
}

func SplitWithDiagnostics(text string, cfg ragconfig.Splitter) ([]Chunk, *Diagnostics) {
	chunks, diag, _ := SplitContext(context.Background(), text, cfg)
	return chunks, diag
}

// SplitContext 在当前调用内解析和切分；取消不会留下继续运行的后台 goroutine。
func SplitContext(ctx context.Context, text string, cfg ragconfig.Splitter) ([]Chunk, *Diagnostics, error) {
	layout, err := NewLayout(ctx, text)
	if err != nil {
		return nil, nil, err
	}
	return layout.split(ctx, 0, len(layout.source), cfg)
}

// Split 允许导入链路复用同一份 AST；maxChunks=0 仅供原有不设限的低层调用。
func (l *Layout) Split(ctx context.Context, cfg ragconfig.Splitter, maxChunks int) ([]Chunk, *Diagnostics, error) {
	return l.split(ctx, 0, len(l.source), cfg, maxChunks)
}

func chunkLimit(limits []int) int {
	if len(limits) > 0 {
		return limits[0]
	}
	return 0
}

func (l *Layout) split(ctx context.Context, start, end int, cfg ragconfig.Splitter, limits ...int) ([]Chunk, *Diagnostics, error) {
	limit := chunkLimit(limits)
	cfg = ensureDefaults(cfg)
	p := l.profile(ctx, start, end)
	diag := &Diagnostics{SelectedTier: TierLegacy, Profile: p}
	switch cfg.Strategy {
	case ragconfig.StrategyHeading:
		diag.TierChain = []StrategyTier{TierHeading, TierLegacy}
	case ragconfig.StrategyHeuristic:
		diag.TierChain = []StrategyTier{TierHeuristic, TierLegacy}
	case ragconfig.StrategyAuto:
		diag.TierChain = SelectStrategy(p)
	default:
		diag.TierChain = []StrategyTier{TierLegacy}
	}
	var chunks []Chunk
	for _, tier := range diag.TierChain {
		if err := ctx.Err(); err != nil {
			return nil, diag, err
		}
		switch tier {
		case TierHeading:
			chunks = l.splitHeadings(ctx, start, end, cfg, p, limit)
		case TierHeuristic:
			chunks = l.splitRange(ctx, start, end, cfg, l.heuristicBoundaries(ctx, start, end, cfg.Languages), limit)
		default:
			chunks = l.splitRange(ctx, start, end, cfg, nil, limit)
		}
		if limit > 0 && len(chunks) > limit {
			if tier == TierLegacy {
				return nil, diag, ErrChunkLimit
			}
			diag.Rejected = append(diag.Rejected, TierRejection{tier, ErrChunkLimit.Error()})
			continue
		}
		validation := ValidateChunks(chunks, end-start, cfg.ChunkSize)
		if validation.OK || tier == TierLegacy {
			diag.SelectedTier = tier
			break
		}
		diag.Rejected = append(diag.Rejected, TierRejection{tier, validation.Reason})
	}
	if cfg.TokenLimit > 0 {
		lang := tokenLanguage(string(l.source[start:end]), cfg)
		var fitted []Chunk
		for _, part := range chunks {
			var pieces []Chunk
			err := l.Fit(ctx, part, func(c Chunk) (bool, error) {
				return ApproxTokenCount(c.EmbeddingContent(), lang) <= cfg.TokenLimit, nil
			}, func(c Chunk) error {
				if limit > 0 && len(fitted)+len(pieces) >= limit {
					return ErrChunkLimit
				}
				pieces = append(pieces, c)
				return nil
			})
			if errors.Is(err, ErrStructureBudget) {
				if limit > 0 && len(fitted) >= limit {
					return nil, diag, ErrChunkLimit
				}
				fitted = append(fitted, part) // 软目标不破坏结构；硬预算不足时导入明确失败。
			} else if err != nil {
				return nil, diag, err
			} else {
				fitted = append(fitted, pieces...)
			}
		}
		chunks = fitted
	}
	for i := range chunks {
		chunks[i].Seq = i
	}
	return chunks, diag, ctx.Err()
}

func tokenLanguage(text string, cfg ragconfig.Splitter) string {
	if len(cfg.Languages) > 0 {
		return cfg.Languages[0]
	}
	return DetectLanguage(text)
}
