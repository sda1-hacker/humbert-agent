package chunker

import "strings"

// absoluteMaxChunkSize 是任何最终 Chunk 的防御性绝对上限。
//
// 当前与 WeKnora 对齐：
//
//	7500 rune
//
// ChunkSize 是：
//
//	“目标大小”
//
// 例如：
//
//	512
//
// 但某些 Protected 内容可能天然超过 512。
//
// absoluteMaxChunkSize 则是：
//
//	“绝对不能无限超过的兜底上限”。
const absoluteMaxChunkSize = 7500

// mergeUnits 将上一阶段产生的 splitUnit
// 合并成最终 Chunk。
//
// 这是 Legacy Splitter 最核心的第二阶段。
//
// 它需要同时协调：
//
//	ChunkSize
//	ChunkOverlap
//	Table Header
//	Absolute Max
//	Source Position
//
// 这也是为什么 Merge 比 Split 本身更加复杂。
func mergeUnits(units []splitUnit, chunkSize int, chunkOverlap int) []Chunk {

	if len(units) == 0 {
		return nil
	}

	// Header Tracker 用于追踪 Markdown Table Header。
	headerTracker := newHeaderTracker()

	chunks := make([]Chunk, 0)

	// current 保存当前正在构建的 Chunk 所包含的 Unit。
	var current []splitUnit

	// curLen 保存 current 所有文本的 rune 总长度。
	curLen := 0

	for _, unit := range units {
		unitLen := RuneLen(unit.text)

		// =============================================================
		// 1. 单个 Unit 自己就超过 absoluteMaxChunkSize。
		// =============================================================
		//
		// 理论上上一阶段 buildUnitsWithProtection
		// 已经会处理超大的 Protected Span。
		//
		// 但这里仍然做第二层防御。
		//
		// 防御性编程原则：
		//
		// Merge 不相信上游一定完美。
		// =============================================================

		if unitLen >
			absoluteMaxChunkSize {

			// 当前已经积累一些内容：
			//
			// 先输出。
			if len(current) > 0 {
				chunks = append(
					chunks,
					buildChunk(current, len(chunks)),
				)
				current = nil
				curLen = 0
			}

			// 即使 Unit 超大，
			// HeaderTracker 也应该看到它，
			// 否则状态可能丢失。
			headerTracker.update(unit.text)

			runes := []rune(unit.text)

			offset := 0

			for offset <
				len(runes) {
				end := offset + absoluteMaxChunkSize

				if end > len(runes) {
					end = len(runes)
				} else {
					// -------------------------------------------------
					// 尽量不要机械地正好切在第 7500 rune。
					//
					// 往前最多 200 rune 寻找：
					//
					//	换行
					//	空格
					// -------------------------------------------------

					for i := end - 1; i > offset && i > end-200; i-- {
						if runes[i] == '\n' || runes[i] == ' ' {
							end = i + 1
							break
						}
					}
				}

				chunkText := string(runes[offset:end])

				chunks = append(
					chunks,
					Chunk{
						Content: chunkText,
						Seq:     len(chunks),
						Start:   unit.start + offset,
						End:     unit.start + end,
					},
				)
				offset = end
			}
			continue
		}

		// =============================================================
		// 2. 更新 Table Header 状态。
		// =============================================================

		headerTracker.update(unit.text)

		// -------------------------------------------------------------
		// 如果 HeaderTracker 发现：
		//
		// 当前 Unit 开始了一张新表，
		//
		// 那么旧 current 不应该和新表混合。
		//
		// 先 flush。
		// -------------------------------------------------------------

		if headerTracker.headerEndedThisUnit && len(current) > 0 {
			chunks = append(
				chunks,
				buildChunk(current, len(chunks)),
			)
			current = nil
			curLen = 0
		}

		// 当前 Active Header。
		headers := headerTracker.getHeaders()

		headersLen := RuneLen(headers)

		// -------------------------------------------------------------
		// Header 自己就比 ChunkSize 还大：
		//
		// 不补。
		//
		// 否则可能为了补一个 Header，
		// 每个 Chunk 都超预算。
		// -------------------------------------------------------------

		if headersLen > chunkSize {
			headers = ""
			headersLen = 0
		}

		// =============================================================
		// 3. 判断当前 Unit 是否还能放进 current。
		// =============================================================
		//
		// 为什么公式是：
		//
		//	curLen + unitLen + headersLen
		//
		// 而不是：
		//
		//	curLen + unitLen
		//
		// 因为如果这里发生切 Chunk，
		// 下一 Chunk 可能需要 prepend Table Header。
		//
		// 所以需要提前为 Header 预留空间。
		// =============================================================

		if curLen+unitLen+headersLen > chunkSize &&
			len(current) > 0 {
			// ---------------------------------------------------------
			// 3.1 输出当前 Chunk。
			// ---------------------------------------------------------

			chunks = append(
				chunks,
				buildChunk(current, len(chunks)),
			)

			// ---------------------------------------------------------
			// 3.2 从刚刚输出的 Chunk 尾部计算 Semantic Overlap。
			// ---------------------------------------------------------

			current, curLen = computeOverlap(current, chunkOverlap, chunkSize, unitLen)

			// ---------------------------------------------------------
			// 3.3 如果当前仍在 Table 中，
			// 新 Chunk 需要考虑补 Table Header。
			// ---------------------------------------------------------

			if headers != "" && headersLen+unitLen <= chunkSize {

				// -----------------------------------------------------
				// overlap 自己也占空间。
				//
				// 如果：
				//
				//	Header
				//	+
				//	Overlap
				//	+
				//	NextUnit
				//
				// 超过 ChunkSize，
				//
				// 就从 overlap 开头继续删除，
				// 给 Header 和 NextUnit 腾空间。
				// -----------------------------------------------------

				for len(current) > 0 && curLen+unitLen+headersLen > chunkSize {
					curLen -= RuneLen(current[0].text)
					current = current[1:]
				}

				// -----------------------------------------------------
				// 防止重复补 Header。
				//
				// overlap 或 next unit 自己已经包含 Header，
				// 就没有必要再插入。
				// -----------------------------------------------------

				overlapText := unitsText(current)

				if !headerAlreadyPresent(headers, overlapText, unit.text) &&
					!headerColumnMismatch(headers, unit.text) {

					// -------------------------------------------------
					// 这是一个 Synthetic Unit。
					//
					// text 是生成的 Header，
					//
					// 但：
					//
					//	start == end
					//
					// 表明它没有占用新的 source range。
					// -------------------------------------------------

					startPosition := unit.start

					if len(current) > 0 {
						startPosition = current[0].start
					}

					headerUnit := splitUnit{
						text:  headers,
						start: startPosition,
						end:   startPosition,
					}

					current = append([]splitUnit{headerUnit}, current...)

					curLen += headersLen
				}
			}
		}

		// =============================================================
		// 4. 再检查 Absolute Max。
		// =============================================================
		//
		// 即使正常 ChunkSize 逻辑因为特殊 Unit 没有触发，
		// 绝对不能让最终 Chunk 无限长。
		// =============================================================

		if curLen+unitLen > absoluteMaxChunkSize {

			if len(current) > 0 {
				chunks = append(
					chunks,
					buildChunk(current, len(chunks)),
				)
				current = nil
				curLen = 0
			}
		}

		// =============================================================
		// 5. 把当前 Unit 加进 current。
		// =============================================================

		current = append(current, unit)

		curLen += unitLen
	}

	// =============================================================
	// 6. 循环结束，把剩余 current 输出。
	// =============================================================

	if len(current) > 0 {
		chunks = append(chunks,
			buildChunk(current, len(chunks)),
		)
	}

	return chunks
}

// buildChunk 将若干 splitUnit 合成为最终 Chunk。
func buildChunk(units []splitUnit, seq int) Chunk {

	var builder strings.Builder
	for _, unit := range units {
		builder.WriteString(unit.text)
	}

	return Chunk{
		Content: builder.String(),
		Seq:     seq,
		Start:   units[0].start,
		End:     units[len(units)-1].end,
	}
}

// headerAlreadyPresent 判断 Active Header 是否已经存在于：
//
//	Overlap
//
// 或：
//
//	Next Unit
//
// 防止重复补：
//
//	| 城市 | 标准 |
//	| --- | --- |
//	| 城市 | 标准 |
//	| --- | --- |
func headerAlreadyPresent(headers string, overlapText string, unitText string) bool {

	// 最快路径：
	//
	// 完整 Header 已经存在。
	if strings.Contains(overlapText, headers) ||
		strings.Contains(unitText, headers) {
		return true
	}

	// -------------------------------------------------------------
	// 有时候完整 Header 不一致，
	// 但是列名行已经存在。
	//
	// 例如 Header：
	//
	//	| 城市 | 标准 |
	//	| --- | --- |
	//
	// 如果 overlap 里已经有：
	//
	//	| 城市 | 标准 |
	//
	// 也不应该重复添加。
	// -------------------------------------------------------------

	columnRow := headerColumnRow(headers)

	if columnRow == "" {
		return false
	}

	return strings.Contains(overlapText, columnRow) ||
		strings.Contains(unitText, columnRow)
}

// headerColumnRow 从 Header 中找到真正的“列名行”。
//
// 会跳过：
//
//	空行
//	separator
//	只有 pipe 的空表头
func headerColumnRow(header string) string {

	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "---") {
			continue
		}

		onlyPipes := true

		for _, r := range line {

			if r != '|' && r != ' ' && r != '\t' {
				onlyPipes = false
				break
			}
		}

		if !onlyPipes {
			return line
		}
	}

	return ""
}
