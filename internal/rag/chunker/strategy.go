package chunker

// StrategyTier 表示真正执行分块算法的“内部层级”。
//
// 注意它和 SplitterConfig.Strategy 不是完全相同的概念。
//
// SplitterConfig.Strategy 是用户配置：
//
//	auto
//	heading
//	heuristic
//	recursive
//	legacy
//
// StrategyTier 是解析配置之后真正执行的算法：
//
//	heading
//	heuristic
//	legacy
//
// 例如：
//
//	StrategyRecursive
//
// 当前只是：
//
//	TierLegacy
//
// 的公开别名。
type StrategyTier string

const (
	TierHeading   StrategyTier = "heading"
	TierHeuristic StrategyTier = "heuristic"
	TierLegacy    StrategyTier = "legacy"
)

// TierRejection 表示某一个 Strategy Tier 为什么被 Validator 拒绝。
//
// 例如：
//
//	Heading
//	  ↓
//	产生大量 10～20 字碎片
//	  ↓
//	Validator Reject
//
// 最终记录：
//
//	Tier   = "heading"
//	Reason = "too many tiny chunks"
//
// 后面的 Chunk Preview 页面可以直接把这些信息展示出来。
type TierRejection struct {
	Tier   StrategyTier `json:"tier"`
	Reason string       `json:"reason"`
}

// Diagnostics 保存一次自适应分块过程的完整诊断信息。
//
// 正常生产入库路径调用：
//
//	Split()
//
// 不需要创建 Diagnostics，减少额外对象分配。
//
// 调试、Preview、学习时调用：
//
//	SplitWithDiagnostics()
//
// 就可以看到：
//
//	最终用了哪一个 Tier？
//	原本准备尝试哪些 Tier？
//	哪些 Tier 被拒绝？
//	为什么被拒绝？
//	Auto 是根据什么文档画像做出的决定？
type Diagnostics struct {
	// SelectedTier 是最终返回结果所对应的 Tier。
	SelectedTier StrategyTier `json:"selected_tier"`

	// TierChain 是本次准备依次尝试的 Tier。
	//
	// 例如：
	//
	//     [heading, heuristic, legacy]
	TierChain []StrategyTier `json:"tier_chain"`

	// Rejected 保存被 Validator 拒绝的 Tier。
	Rejected []TierRejection `json:"rejected"`

	// Profile 只有 Auto Strategy 才存在。
	//
	// 如果用户明确指定：
	//
	//     StrategyHeading
	//
	// 就没必要先扫描整篇文档做 Profiler，
	// 因此 Profile 为 nil。
	Profile *DocProfile `json:"profile,omitempty"`
}

// SelectStrategy 根据 DocProfile 生成 Auto Strategy 的候选链。
//
// 这个函数只负责：
//
//	Profile -> []StrategyTier
//
// 它不负责真正运行任何 Splitter。
//
// 这让职责非常清晰：
//
//	ProfileDocument()
//	    ↓
//	得到“事实”
//
//	SelectStrategy()
//	    ↓
//	根据事实选择算法候选
//
//	runTier()
//	    ↓
//	真正执行算法
func SelectStrategy(profile *DocProfile) []StrategyTier {
	if profile == nil {
		return []StrategyTier{TierLegacy}
	}

	chain := make([]StrategyTier, 0, 3)

	// ---------------------------------------------------------------------
	// Tier 1：Heading
	//
	// 当前 WeKnora 同时要求：
	//
	//     Markdown Heading 总数 >= 3
	//
	//     HeadingDensity > 0.005
	//
	//     DominantHeadingLevel > 0
	//
	// 为什么不能只看“有没有 #”？
	//
	// 因为文档可能只有：
	//
	//     # 文档标题
	//
	// 一个 Heading。
	//
	// 它不足以构成稳定的章节骨架，
	// 使用 Heading Splitter 并没有实际价值。
	// ---------------------------------------------------------------------

	if profile.MdHeadingTotal >= 3 &&
		profile.HeadingDensity() > 0.005 &&
		profile.DominantHeadingLevel() > 0 {

		chain = append(chain, TierHeading)
	}

	// ---------------------------------------------------------------------
	// Tier 2：Heuristic
	//
	// 满足以下任意条件即可进入候选：
	//
	// 1. Heuristic Marker 总数 >= 5
	//
	// 2. 存在 FormFeed
	//
	// 3. 存在任意语言 Chapter Marker
	//
	// 第 2、3 条为什么不要求 >=5？
	//
	// 因为：
	//
	//     \f
	//     第一章
	//     Chapter 1
	//
	// 本身就是非常强的文档结构信号。
	// ---------------------------------------------------------------------

	chapterCount :=
		profile.GermanChapterCount +
			profile.EnglishChapterCount +
			profile.ChineseChapterCount

	if profile.HeuristicMarkerTotal() >= 5 ||
		profile.FormFeedCount > 0 ||
		chapterCount > 0 {

		chain = append(chain, TierHeuristic)
	}

	// ---------------------------------------------------------------------
	// Tier 3：Legacy
	//
	// 无论前面检测到什么，
	// Legacy 永远是最后一道安全网。
	//
	// 因此 Auto 永远至少返回：
	//
	//     [legacy]
	// ---------------------------------------------------------------------

	chain = append(chain, TierLegacy)

	return chain
}

// ensureDefaults 是真正进入 Strategy Pipeline 之前的运行时配置归一化。
//
// 我们第一阶段已经有：
//
//	NormalizeSplitterConfig()
//
// 两者非常相似，但职责不同。
//
// NormalizeSplitterConfig：
//
//	负责基础配置默认值。
//
// ensureDefaults：
//
//	除了基础默认值，还会处理运行时约束：
//
//	1. TokenLimit -> 字符预算
//	2. Overlap 最大不能超过 ChunkSize / 2
//
// Split() 使用的是 ensureDefaults()。
func ensureDefaults(cfg SplitterConfig) SplitterConfig {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = DefaultChunkSize
	}

	if cfg.ChunkOverlap <= 0 {
		cfg.ChunkOverlap = DefaultChunkOverlap
	}

	if len(cfg.Separators) == 0 {
		cfg.Separators = DefaultSeparators()
	}

	// ---------------------------------------------------------------------
	// TokenLimit
	//
	// 例如：
	//
	//     ChunkSize = 1000
	//     TokenLimit = 256
	//     Language = zh
	//
	// 中文字符预算：
	//
	//     256 * 1.7 * 0.9
	//     ≈ 391
	//
	// 所以最终：
	//
	//     ChunkSize = 391
	//
	// 注意：
	//
	// TokenLimit 只能把 ChunkSize 变小，
	// 不能反过来把用户配置的 ChunkSize 变大。
	// ---------------------------------------------------------------------

	if cfg.TokenLimit > 0 {
		lang := LangMixed

		if len(cfg.Languages) > 0 {
			lang = cfg.Languages[0]
		}

		charBudget := CharsForTokenLimit(cfg.TokenLimit, lang)

		if charBudget > 0 && (cfg.ChunkSize == 0 || charBudget < cfg.ChunkSize) {
			cfg.ChunkSize = charBudget
		}
	}

	// ---------------------------------------------------------------------
	// Overlap 防御。
	//
	// 假设：
	//
	//     ChunkSize = 500
	//     Overlap   = 450
	//
	// 那么相邻两个 Chunk 几乎完全一样：
	//
	//     Chunk1:
	//     0 ---------------- 500
	//
	//     Chunk2:
	//       50 ---------------- 550
	//
	// 会制造大量重复向量和重复 BM25 文档。
	//
	// 所以最大限制为：
	//
	//     ChunkSize / 2
	// ---------------------------------------------------------------------

	if cfg.ChunkSize > 0 && cfg.ChunkOverlap > cfg.ChunkSize/2 {
		cfg.ChunkOverlap = cfg.ChunkSize / 2
	}

	return cfg
}

// resolveChainWithProfile 根据用户配置决定真正的 Tier Chain。
//
// 返回：
//
//	chain
//	profile
//
// profile 只有 Auto Strategy 才会返回。
//
// -----------------------------------------------------------------------------
// 显式策略：
//
//	heading
//	    -> heading -> legacy
//
//	heuristic
//	    -> heuristic -> legacy
//
//	recursive
//	    -> legacy
//
//	legacy
//	    -> legacy
//
//	""
//	    -> legacy
//
// Auto：
//
//	ProfileDocument()
//	    ↓
//	SelectStrategy()
//
// -----------------------------------------------------------------------------
// 特别注意空 Strategy。
//
// 很多人直觉上会认为：
//
//	"" == auto
//
// 但当前 WeKnora 实际代码为了兼容旧数据库配置：
//
//	"" == legacy
//
// 如果我们想启用 Auto，必须明确设置：
//
//	Strategy: StrategyAuto
func resolveChainWithProfile(text string, cfg SplitterConfig) ([]StrategyTier, *DocProfile) {
	switch cfg.Strategy {
	case StrategyHeading:
		return []StrategyTier{TierHeading, TierLegacy}, nil

	case StrategyHeuristic:
		return []StrategyTier{TierHeuristic, TierLegacy}, nil

	case StrategyRecursive:
		// recursive 只是 legacy 的公开别名。
		return []StrategyTier{TierLegacy}, nil

	case StrategyLegacy, "":
		// 空值保持向后兼容：
		//
		//     "" == legacy
		return []StrategyTier{TierLegacy}, nil

	case StrategyAuto:
		fallthrough

	default:
		// 未知 Strategy 当前也按 Auto 处理。
		//
		// 这是当前 WeKnora 的实际 switch 行为。
		profile := ProfileDocument(text)

		return SelectStrategy(profile), profile
	}
}

// Split 是 Chunker 面向正常生产路径的主要入口。
//
// 后面我们的 Eino Transformer 最终调用的就应该是：
//
//	Split()
//
// 而不是直接调用：
//
//	SplitText()
//
// 因为 SplitText 只是 Tier 3 Legacy 实现。
//
// Split() 才包含：
//
//	默认值
//	TokenLimit
//	Auto Strategy
//	Validator
//	Fallback
func Split(text string, cfg SplitterConfig) []Chunk {
	if text == "" {
		return nil
	}

	cfg = ensureDefaults(cfg)

	chain, profile := resolveChainWithProfile(text, cfg)
	totalChars := RuneLen(text)

	var lastOut []Chunk

	for i, tier := range chain {
		out := runTier(tier, text, cfg, profile)
		validation := ValidateChunks(out, totalChars, cfg.ChunkSize)

		if validation.OK {
			return out
		}

		// -------------------------------------------------------------
		// Legacy 是整个 Strategy Chain 的最终安全网。
		//
		// 如果连 Legacy 都没有通过 Validator，
		// 我们仍然保留它。
		//
		// Validator 的含义是：
		//
		//     “值得继续使用这个 Tier 吗？”
		//
		// 而不是：
		//
		//     “这个结果绝对禁止返回。”
		//
		// 因此最终宁愿返回一个不够理想的 Legacy 结果，
		// 也不要直接返回空结果。
		// -------------------------------------------------------------

		if tier == TierLegacy && i == len(chain)-1 {
			lastOut = out
		}
	}

	if lastOut != nil {
		return lastOut
	}

	// 理论上正常 Chain 一定包含 Legacy，
	// 这里仅作为最后一道防御。
	return SplitText(text, cfg)
}

// SplitWithDiagnostics 与 Split 的分块结果语义相同，
// 但额外返回完整诊断信息。
//
// 它适合：
//
//	CLI Preview
//	Debug API
//	管理后台分块预览
//
// 不建议在高频生产入库热路径中无条件使用，
// 因为会创建额外的 Diagnostics / Rejection 对象。
func SplitWithDiagnostics(text string, cfg SplitterConfig) ([]Chunk, *Diagnostics) {
	// 即使输入为空，也让 SelectedTier 有一个有效值，
	// 避免调试 UI 显示空字符串。
	diag := &Diagnostics{
		SelectedTier: TierLegacy,
	}

	if text == "" {
		return nil, diag
	}

	cfg = ensureDefaults(cfg)

	chain, profile := resolveChainWithProfile(text, cfg)

	diag.TierChain = chain
	diag.Profile = profile

	totalChars := RuneLen(text)

	var lastOut []Chunk
	var lastTier StrategyTier

	for i, tier := range chain {
		out := runTier(tier, text, cfg, profile)
		validation := ValidateChunks(out, totalChars, cfg.ChunkSize)

		if validation.OK {
			diag.SelectedTier = tier
			return out, diag
		}

		diag.Rejected = append(diag.Rejected, TierRejection{
			Tier:   tier,
			Reason: validation.Reason,
		})

		if tier == TierLegacy && i == len(chain)-1 {
			lastOut = out
			lastTier = tier
		}
	}

	if lastOut != nil {
		diag.SelectedTier = lastTier
		return lastOut, diag
	}

	// Defensive fallback。
	return SplitText(text, cfg), diag
}

// runTier 真正把 StrategyTier 分发到具体 Splitter。
//
// Heading / Heuristic 的真实实现我们后面才写。
//
// Legacy 已经完成：
//
//	SplitText()
func runTier(tier StrategyTier, text string, cfg SplitterConfig, profile *DocProfile) []Chunk {
	switch tier {
	case TierHeading:
		return splitByHeadings(text, cfg, profile)

	case TierHeuristic:
		return splitByHeuristics(text, cfg, profile)

	case TierLegacy:
		return SplitText(text, cfg)

	default:
		// 防御未来增加新的 Tier，
		// 即使忘记在这里实现，也仍然退回 Legacy。
		return SplitText(text, cfg)
	}
}

// -----------------------------------------------------------------------------
// Heading / Heuristic 注册点
// -----------------------------------------------------------------------------
//
// 这两个变量看起来有点特殊。
//
// 为什么不直接：
//
//     func splitByHeadings(...) {}
//
// 因为当前 WeKnora 使用的是一种轻量注册方式：
//
// strategy.go 先提供默认实现：
//
//     splitByHeadings = Legacy
//
// 后面的：
//
//     heading_splitter.go
//
// 在 init() 中把它替换成真正 Heading Splitter。
//
// Heuristic 同理。
//
// 这样 strategy.go 不需要直接依赖具体实现文件内部细节。
//
// 当前阶段 Heading / Heuristic 还没实现，
// 所以它们暂时退回 Legacy。
//
// 这意味着当前阶段：
//
//     Strategy Resolver 已经能够正确选择 Tier Chain，
//
// 但真正的：
//
//     Heading Algorithm
//     Heuristic Algorithm
//
// 要在后续阶段完成。
//
// 等 heading_splitter.go 写好后会出现类似：
//
//     func init() {
//         splitByHeadings = splitByHeadingImpl
//     }
//
// 然后这里就不需要再修改。
// -----------------------------------------------------------------------------

var splitByHeadings = func(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
	return SplitText(text, cfg)
}

var splitByHeuristics = func(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
	return SplitText(text, cfg)
}
