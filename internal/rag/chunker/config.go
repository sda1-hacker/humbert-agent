package chunker

import "slices"

// 分块策略名称
const (
	// StrategyAuto 表示自动模式。
	StrategyAuto = "auto"

	// StrategyHeading 强制优先使用 Markdown Heading 分块。
	StrategyHeading = "heading"

	// StrategyHeuristic 强制优先使用启发式章节识别。
	// 这一类没有标准 Markdown Heading 的文档。
	StrategyHeuristic = "heuristic"

	// StrategyRecursive 表示递归文本切分。
	// 实际是 legacy splitter 的一个公开别名。
	StrategyRecursive = "recursive"

	// StrategyLegacy 表示传统递归 SplitText。
	// 它也是整个策略链最后的兜底方案。
	StrategyLegacy = "legacy"
)

// 默认分块参数
const (
	// DefaultChunkSize 是默认目标 Chunk 大小。
	DefaultChunkSize = 512

	// DefaultChunkOverlap 是默认最大重叠大小。
	// 最多允许保留 80 个 rune。而不是 80个 byte
	DefaultChunkOverlap = 80

	// DefaultParentChunkSize 是 Parent-Child 模式下默认 Parent 大小。
	// Parent 更大，主要用于回答时恢复较完整上下文。
	DefaultParentChunkSize = 4096

	// DefaultChildChunkSize 是 Parent-Child 模式下默认 Child 大小。
	// Child 更小，主要用于：Embedding、BM25、精准召回
	DefaultChildChunkSize = 384
)

// SplitterConfig 定义整个 Chunker 的分块配置。
// Eino 层最终也只需要把外部配置转换成这个结构。
type SplitterConfig struct {
	// 块的大小
	// 默认值 512 这里是目标大小，不是绝对大小
	// 后面可能有 protected span、表格、代码等特殊结构，可能允许一个 Chunk 的大小超过 ChunkSize
	ChunkSize int

	// 相邻 Chunk 最大重叠的字符数
	// 0 关闭重叠；DefaultConfig() 提供 80 的默认值。
	ChunkOverlap int

	// 递归切分的分隔符优先级
	// \n\n  \n  。
	// 如果某一块仍然太大，再对那一块递归使用下一个分隔符。
	Separators []string

	// 分块策略
	Strategy string

	// 分块的近似 Token 目标；最终模型输入由 embeddinginput.Budget 校验。
	TokenLimit int

	// 语言提示。 []string{"zh"}、[]string{"en"}
	// 用于启发式切分，Token 预估，如果为空，后面由 Profiler 自动检测。
	Languages []string
}

// DefaultSeparators 返回默认递归分隔符。
func DefaultSeparators() []string {
	return []string{
		"\n\n",
		"\n",
		"。",
	}
}

// DefaultConfig 返回一份默认 SplitterConfig。
//
// 注意这里返回的 Separators 是一份新的 slice，
// 避免不同配置实例共享底层数组。
func DefaultConfig() SplitterConfig {
	return SplitterConfig{
		ChunkSize:    DefaultChunkSize,
		ChunkOverlap: DefaultChunkOverlap,
		Separators:   DefaultSeparators(),
	}
}

// NormalizeSplitterConfig 对调用者传进来的配置应用基础默认值。
func NormalizeSplitterConfig(cfg SplitterConfig) SplitterConfig {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = DefaultChunkSize
	}

	if cfg.ChunkOverlap < 0 {
		cfg.ChunkOverlap = 0
	}

	if len(cfg.Separators) == 0 {
		cfg.Separators = DefaultSeparators()
	}

	return cfg
}

// DeriveParentChildConfigs 根据一份基础配置，生成 Parent 和 Child 配置。
// Parent 大窗口、负责完整的上下文、不使用 TokenLimit
// Child 小窗口、负责Embedding / Retrieval、使用 TokenLimit
func DeriveParentChildConfigs(
	base SplitterConfig,
	parentSize int,
	childSize int) (parent SplitterConfig, child SplitterConfig) {
	if parentSize <= 0 {
		parentSize = DefaultParentChunkSize
	}

	if childSize <= 0 {
		childSize = DefaultChildChunkSize
	}

	parent = base.Clone()
	parent.ChunkSize = parentSize
	// 父块用于补充上下文，不受子块的模型输入预算约束。
	parent.TokenLimit = 0
	child = base.Clone()
	child.ChunkSize = childSize
	child.ChunkOverlap = childSize / 5
	if base.ChunkOverlap == 0 {
		child.ChunkOverlap = 0
	}

	return parent, child
}

// Clone 复制配置中的切片，避免父子配置或 Eino 调用选项共享可变数组。
func (c SplitterConfig) Clone() SplitterConfig {
	c.Separators = slices.Clone(c.Separators)
	c.Languages = slices.Clone(c.Languages)
	return c
}
