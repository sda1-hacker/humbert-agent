package chunker

import "regexp"

// splitBySeparators 按分隔符优先级递归切分，完整保留分隔符以恢复原文。
func splitBySeparators(
	text string,
	separators []string,
	chunkSize int,
) []string {

	// 没有文本或者没有任何 separator，
	// 无法继续切。
	if text == "" || len(separators) == 0 {
		return []string{text}
	}

	// 文本已在目标大小以内时停止；零大小表示只按分隔符拆分。
	if chunkSize > 0 && RuneLen(text) <= chunkSize {
		return []string{text}
	}

	// ---------------------------------------------------------------------
	// 按 separator 优先级依次尝试。
	// ---------------------------------------------------------------------
	for index, separator := range separators {

		if separator == "" {
			continue
		}

		// 分隔符按字面文本匹配，必须转义正则特殊字符。
		re := regexp.MustCompile("(" + regexp.QuoteMeta(separator) + ")")

		// Split 本身会丢掉 separator，
		// 所以我们后面还要重新把 separator 放回来。
		splits := re.Split(text, -1)

		matches := re.FindAllString(text, -1)

		// 当前文本根本没有这个 separator，
		// 换下一个。
		if len(matches) == 0 {
			continue
		}

		// 把分隔符放回切分结果，确保所有单元拼接后无损恢复原文。
		pieces := make(
			[]string,
			0,
			len(splits)+len(matches),
		)

		for i, part := range splits {

			if part != "" {
				pieces = append(
					pieces,
					part,
				)
			}

			if i < len(matches) &&
				matches[i] != "" {

				pieces = append(
					pieces,
					matches[i],
				)
			}
		}

		// 极端情况下并没有真正切开，
		// 那么继续尝试下一个 separator。
		if len(pieces) <= 1 {
			continue
		}

		// 仅使用更低优先级分隔符继续拆分过大局部。
		remainingSeparators :=
			separators[index+1:]

		result := make(
			[]string,
			0,
			len(pieces),
		)

		for _, piece := range pieces {

			// -------------------------------------------------------------
			// 当前 piece 仍然太大，
			// 并且还有下一层 separator：
			//
			//     递归。
			// -------------------------------------------------------------
			if chunkSize > 0 && RuneLen(piece) > chunkSize && len(remainingSeparators) > 0 {
				subPieces :=
					splitBySeparators(piece, remainingSeparators, chunkSize)

				result = append(result, subPieces...)

				continue
			}

			// 不需要继续递归。
			result = append(result, piece)
		}

		// 当前分隔符成功切开后不重切全文，只递归处理过大片段。

		return result
	}

	// 所有 separator 都不存在。
	return []string{text}
}
