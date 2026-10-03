package chunker

// SplitText 是 Legacy Recursive Splitter 的公开入口。
//
// 到这里，我们终于把整个 Legacy Pipeline 串起来：
//
//	text
//	  ↓
//	protectedSpans
//	  ↓
//	buildUnitsWithProtection
//	  ↓
//	  ├─ Protected Atomic Unit
//	  └─ Recursive Split Unit
//	  ↓
//	mergeUnits
//	  ↓
//	  ├─ HeaderTracker
//	  ├─ Semantic Overlap
//	  ├─ ChunkSize
//	  └─ Absolute Max
//	  ↓
//	[]Chunk
//
// -----------------------------------------------------------------------------
// 注意：
//
// SplitText 是“底层 Legacy API”。
//
// 它和以后实现的：
//
//	Split()
//
// 不完全一样。
//
// Split() 会先执行：
//
//	ensureDefaults()
//
// 而当前 WeKnora 的 SplitText 本身只做最基础的防御。
func SplitText(
	text string,
	cfg SplitterConfig,
) []Chunk {

	if text == "" {
		return nil
	}

	// -------------------------------------------------------------
	// ChunkSize：
	//
	// <= 0
	//
	// 使用默认：
	//
	// 512
	// -------------------------------------------------------------

	chunkSize :=
		cfg.ChunkSize

	if chunkSize <= 0 {
		chunkSize =
			DefaultChunkSize
	}

	// -------------------------------------------------------------
	// ChunkOverlap：
	//
	// 这里特别注意：
	//
	// SplitText 的底层语义是：
	//
	//	负数 → 0
	//
	// 但：
	//
	//	0 本身允许存在
	//
	// 这和后面 Strategy 层：
	//
	//	ensureDefaults()
	//
	// 不一样。
	//
	// ensureDefaults() 会把 <=0 恢复为默认 80。
	//
	// 我们现在是完全对标当前 WeKnora SplitText，
	// 所以这里不能调用 NormalizeSplitterConfig。
	// -------------------------------------------------------------

	chunkOverlap :=
		cfg.ChunkOverlap

	if chunkOverlap < 0 {
		chunkOverlap = 0
	}

	// -------------------------------------------------------------
	// Separators：
	//
	// 当前底层 SplitText 直接使用 cfg.Separators。
	//
	// 正常生产路径后面会由：
	//
	//	NormalizeSplitterConfig
	//
	// 或：
	//
	//	ensureDefaults
	//
	// 保证它不为空。
	//
	// 因此我们的测试和调用应优先使用：
	//
	//	DefaultConfig()
	// -------------------------------------------------------------

	separators :=
		cfg.Separators

	// -------------------------------------------------------------
	// Step 1：
	//
	// 找 Protected Span。
	// -------------------------------------------------------------

	protected :=
		protectedSpans(
			text,
		)

	// -------------------------------------------------------------
	// Step 2：
	//
	// 普通区域 Recursive Split，
	// Protected 区域 Atomic。
	//
	// 最终全部转换成 rune-position splitUnit。
	// -------------------------------------------------------------

	units :=
		buildUnitsWithProtection(
			text,
			protected,
			separators,
			chunkSize,
		)

	// -------------------------------------------------------------
	// Step 3：
	//
	// Unit → 最终 Chunk。
	// -------------------------------------------------------------

	return mergeUnits(
		units,
		chunkSize,
		chunkOverlap,
	)
}
