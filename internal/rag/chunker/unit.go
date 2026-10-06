package chunker

// 把 普通区域 +Protected Span 组合成 []splitUnit

// 表示单个 Protected Span 能保持为一个原子 Unit 的最大 rune 数量。
// 为什么 Protected Span 还要有上限？
// 因为现实世界里的文档并不可靠。如果说 OCR 把很大一块内容识别 Markdown Table
// 或者文档中真的存在 30000 字代码块
// 如果我们坚持 Protected Span 不可切分
// 那么后续的 Embedding API、Reranker、LLM 可能都无法正常运行，可能会超出上下文
// 所以 Protected 的意思是“尽量不要切分”，而不是“绝对不能切分”
const maxProtectedUnitSize = 7500

// 表示强制拆分超大 Protected Span 时，会在目标位置前最多回看多少 rune。尝试寻找更自然的换行，空格。
const protectedSplitLookback = 200

// splitUnit 保存切分中间单元；范围使用原文字符坐标，合成表头的起止位置相同。
type splitUnit struct {
	// text 是这个 Unit 的真实文本。
	text string

	// start / end 是原始文档中的 rune offset。
	// 使用：
	//     [start, end)
	// 左闭右开区间。
	start int
	end   int
}

// buildUnitsWithProtection 递归切分普通区域，保留保护区域，并转换成字符坐标。
func buildUnitsWithProtection(
	text string,
	protected []span,
	separators []string,
	chunkSize int,
) []splitUnit {

	if text == "" {
		return nil
	}

	units := make([]splitUnit, 0)

	// bytePos：
	// 当前已经消费到原始 string 的哪个 byte。
	// 用于：
	//     text[bytePos:p.start]
	// 这种字符串切片。
	bytePos := 0

	// runePos：
	// 当前已经消费到原文第几个 Unicode rune。
	// 用于：
	//     splitUnit.start/end
	runePos := 0

	// ---------------------------------------------------------------------
	// 依次处理每一个 Protected Span。
	// protectedSpans() 已保证：
	//     1. 按位置排序
	//     2. 不重叠
	// ---------------------------------------------------------------------

	for _, protectedSpan := range protected {
		// A. Protected Span 之前存在普通文本。
		if protectedSpan.start > bytePos {
			plainText := text[bytePos:protectedSpan.start]

			// 普通文本可以递归切。
			parts := splitBySeparators(plainText, separators, chunkSize)

			// plainText 在原文中的 rune 起点。
			runeOffset := runePos

			for _, part := range parts {

				partRuneLen := RuneLen(part)

				units = append(
					units,
					splitUnit{
						text:  part,
						start: runeOffset,
						end:   runeOffset + partRuneLen,
					},
				)

				// 下一个 part 紧接着当前 part。
				runeOffset += partRuneLen
			}

			// 整段普通文本消费完以后，
			// 文档级 runePos 向前移动。
			runePos += RuneLen(plainText)
		}

		// B. 处理 Protected Span。
		protectedText := text[protectedSpan.start:protectedSpan.end]
		protectedRuneLen := RuneLen(protectedText)

		// 正常情况：
		// Protected 内容不超过 7500 rune，
		// 整体作为一个原子 Unit。
		if protectedRuneLen <=
			maxProtectedUnitSize {

			units = append(
				units,
				splitUnit{
					text:  protectedText,
					start: runePos,
					end:   runePos + protectedRuneLen,
				},
			)

		} else {
			// 异常长的保护区域仍须拆分，避免无界块大小。
			runes := []rune(protectedText)
			offset := 0
			for offset < len(runes) {
				chunkEnd := offset + maxProtectedUnitSize

				// 最后一块不足 7500，
				// 直接取到末尾。
				if chunkEnd > len(runes) {
					chunkEnd = len(runes)
				} else {
					// 硬拆分前最多回看 200 字符，优先在换行或空格处切开。
					minSearch := chunkEnd - protectedSplitLookback

					if minSearch < offset {
						minSearch = offset
					}

					for i := chunkEnd - 1; i > offset && i > minSearch; i-- {
						if runes[i] == '\n' || runes[i] == ' ' {
							// 把找到的 newline / space
							// 留在当前 Unit 尾部。
							chunkEnd = i + 1
							break
						}
					}
				}

				chunkText := string(runes[offset:chunkEnd])

				chunkRuneLen := chunkEnd - offset

				units = append(
					units,
					splitUnit{
						text:  chunkText,
						start: runePos + offset,
						end:   runePos + offset + chunkRuneLen,
					},
				)
				offset = chunkEnd
			}
		}

		// 整个 Protected Span 已消费。
		runePos += protectedRuneLen

		// bytePos 必须使用 byte offset。
		bytePos = protectedSpan.end
	}

	// ---------------------------------------------------------------------
	// 最后一个 Protected Span 后面可能还有普通文本。
	// 例如：
	//     ```code```
	//     最后一段正文
	// 这里处理“最后一段正文”。
	// ---------------------------------------------------------------------
	if bytePos < len(text) {

		remaining := text[bytePos:]

		parts := splitBySeparators(remaining, separators, chunkSize)

		runeOffset := runePos

		for _, part := range parts {

			partRuneLen := RuneLen(part)

			units = append(
				units,
				splitUnit{
					text:  part,
					start: runeOffset,
					end:   runeOffset + partRuneLen,
				},
			)
			runeOffset += partRuneLen
		}
	}

	return units
}
