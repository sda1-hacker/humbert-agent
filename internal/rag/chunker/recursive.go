package chunker

import (
	"context"
	"strings"
)

// splitBySeparators 按字面分隔符递归拆分，分隔符作为独立单元保留，不使用正则重复扫描。
func splitBySeparators(text string, separators []string, chunkSize int) []string {
	return splitBySeparatorsContext(context.Background(), text, separators, chunkSize)
}

func splitBySeparatorsContext(ctx context.Context, text string, separators []string, chunkSize int) []string {
	if ctx.Err() != nil {
		return nil
	}
	if text == "" || len(separators) == 0 || (chunkSize > 0 && RuneLen(text) <= chunkSize) {
		return []string{text}
	}

	for index, separator := range separators {
		if separator == "" {
			continue
		}

		var pieces []string
		for remaining := text; ; {
			if ctx.Err() != nil {
				return nil
			}
			part, rest, found := strings.Cut(remaining, separator)
			if part != "" {
				pieces = append(pieces, part)
			}

			if !found {
				break
			}
			pieces = append(pieces, separator)
			remaining = rest
		}
		if len(pieces) <= 1 {
			continue
		}

		// 只有过大的局部继续使用下一层分隔符，所有单元拼接后仍等于原文。
		remainingSeparators := separators[index+1:]
		result := make([]string, 0, len(pieces))

		for _, piece := range pieces {
			if ctx.Err() != nil {
				return nil
			}
			if chunkSize > 0 && RuneLen(piece) > chunkSize && len(remainingSeparators) > 0 {
				result = append(result, splitBySeparatorsContext(ctx, piece, remainingSeparators, chunkSize)...)
			} else {
				result = append(result, piece)
			}
		}
		return result
	}

	return []string{text}
}
