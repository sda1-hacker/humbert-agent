package chunker

import "regexp"

// 递归切分
// splitBySeparators 按照 separators 的优先级递归切割文本。
//
// -----------------------------------------------------------------------------
// 这是整个 Legacy Splitter 最重要的算法之一。
//
// 假设：
//
//	separators = [
//	    "\n\n",
//	    "\n",
//	    "。",
//	]
//
//	ChunkSize = 512
//
// 算法不是：
//
//	“随便找一个 separator 切”
//
// 而是：
//
//	第一步：
//	优先使用 \n\n
//
//	↓
//
//	如果某个子块仍然 > 512
//
//	↓
//
//	只对这个过大的子块使用 \n
//
//	↓
//
//	如果还 > 512
//
//	↓
//
//	再只对它使用 。
//
// 这就叫：
//
//	Recursive Separator Priority
//
// -----------------------------------------------------------------------------
// 举例：
//
// 原文：
//
//	Paragraph A
//
//	Line B
//	Line C
//	Line D
//
// 首先按：
//
//	\n\n
//
// 得到：
//
//	Paragraph A
//
// 和：
//
//	Line B
//	Line C
//	Line D
//
// 如果第二块仍然过大，
//
// 只对第二块继续按：
//
//	\n
//
// 切。
// -----------------------------------------------------------------------------
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

	// ---------------------------------------------------------------------
	// 如果当前文本已经在 ChunkSize 以内，
	// 就没有必要继续向下切。
	//
	// chunkSize == 0 是特殊语义：
	//
	//     不启用 size guard。
	//
	// 后面的某些内部调用可能只想根据 separator 拆，
	// 不关心长度。
	// ---------------------------------------------------------------------
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

		// regexp.QuoteMeta 很重要。
		//
		// separator 可能包含：
		//
		//     .
		//     |
		//     ?
		//
		// 这些在正则里有特殊含义。
		//
		// QuoteMeta 可以确保它们被当成普通文本。
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

		// -----------------------------------------------------------------
		// 把 separator 重新放回结果。
		//
		// 为什么？
		//
		// 我们整个 Chunker 一个非常重要的原则：
		//
		//     所有 Unit 拼回去
		//     必须等于原始文本
		//
		// 即：
		//
		//     strings.Join(...) 不是目标，
		//
		// 而是：
		//
		//     unit1.text +
		//     unit2.text +
		//     ...
		//
		// 必须能够无损恢复原文。
		//
		// 所以换行、句号等 separator 本身不能丢。
		// -----------------------------------------------------------------
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

		// -----------------------------------------------------------------
		// 核心：
		//
		// 当前 separator 已经使用过了。
		//
		// 如果某个 piece 仍然过大，
		// 只能继续使用“优先级更低”的 separator。
		// -----------------------------------------------------------------
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

		// -----------------------------------------------------------------
		// 一旦当前优先级 separator 能够真正切开，
		// 这一层工作就完成。
		//
		// 不需要再把整个 text 用后面的 separator 重切一次。
		//
		// 后面的 separator 只负责：
		//
		//     当前结果中仍然过大的局部。
		// -----------------------------------------------------------------

		return result
	}

	// 所有 separator 都不存在。
	return []string{text}
}
