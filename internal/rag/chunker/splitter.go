package chunker

// SplitText 先识别保护区域，再递归切分普通文本，最后合并为带原文范围的块。
func SplitText(
	text string,
	cfg SplitterConfig,
) []Chunk {

	if text == "" {
		return nil
	}

	// 未提供正块大小时使用默认大小。

	chunkSize :=
		cfg.ChunkSize

	if chunkSize <= 0 {
		chunkSize =
			DefaultChunkSize
	}

	// 负重叠归零，显式零重叠仍有效；此底层入口不应用策略层的其他运行默认值。

	chunkOverlap :=
		cfg.ChunkOverlap

	if chunkOverlap < 0 {
		chunkOverlap = 0
	}

	// 底层直接使用分隔符；业务入口或 DefaultConfig 负责提供默认分隔符。

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

	// 普通区域递归拆分，保护区域保持原子，再统一记录字符坐标。

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
