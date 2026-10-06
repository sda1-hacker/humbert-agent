package chunker

import "strings"

// absoluteMaxChunkSize 是保护区域等异常长内容的防御上限；目标块大小仍由配置决定。
const absoluteMaxChunkSize = 7500

// mergeUnits 按目标大小合并单元，并处理表头补充、重叠和原文范围。
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

		// 合并阶段再次限制异常长单元，避免依赖上游完全正确。

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
					// 在硬边界前 200 字符内优先寻找换行或空格。

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

		// 新表开始前输出当前块，避免混合两张表的表头。

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

		// 表头自身超过目标大小时不补充，避免每块都被表头挤满。

		if headersLen > chunkSize {
			headers = ""
			headersLen = 0
		}

		// 预算为可能补充的表头预留空间。

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

				// 重叠部分需要为表头与下一单元让出空间。

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

					// 补充的表头不占新的原文范围，因此起止位置相同。

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

// headerAlreadyPresent 判断正文或重叠部分是否已经包含表头，避免重复补充。
func headerAlreadyPresent(headers string, overlapText string, unitText string) bool {

	// 最快路径：
	//
	// 完整 Header 已经存在。
	if strings.Contains(overlapText, headers) ||
		strings.Contains(unitText, headers) {
		return true
	}

	// 即使完整表头不同，已有相同列名行时也不重复添加。

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
