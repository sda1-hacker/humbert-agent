package chunker

import "strings"

// enforceTokenTarget is the final fallback for unsplittable paragraphs and
// protected blocks. This is an approximate chunking target, not a provider
// tokenizer. The indexer independently checks the complete embedding input.
func enforceTokenTarget(text string, chunks []Chunk, cfg SplitterConfig) []Chunk {
	if cfg.TokenLimit <= 0 {
		return chunks
	}
	lang := DetectLanguage(text)
	if len(cfg.Languages) > 0 {
		lang = cfg.Languages[0]
	}
	source := []rune(text)
	var out []Chunk
	for _, c := range chunks {
		if ApproxTokenCount(c.EmbeddingContent(), lang) <= cfg.TokenLimit {
			c.Seq = len(out)
			out = append(out, c)
			continue
		}
		body := string(source[c.Start:c.End])
		prefix := ""
		if strings.HasSuffix(c.Content, body) {
			prefix = strings.TrimSuffix(c.Content, body)
		}
		budget := CharsForTokenLimit(cfg.TokenLimit, lang) - RuneLen(prefix) - RuneLen(c.ContextHeader)
		if c.ContextHeader != "" {
			budget -= 2
		}
		// A header alone may exhaust the budget. Preserve it and let ingestion
		// return a clear budget error, rather than dropping citation context.
		if budget < 1 {
			c.Seq = len(out)
			out = append(out, c)
			continue
		}
		for start := c.Start; start < c.End; {
			end := min(start+budget, c.End)
			// Prefer a sentence/line boundary near the hard fallback boundary.
			if end < c.End {
				for i := end - 1; i > start+budget/2; i-- {
					if source[i] == '\n' || source[i] == '。' || source[i] == ' ' {
						end = i + 1
						break
					}
				}
			}
			part := c
			part.Start, part.End, part.Seq = start, end, len(out)
			part.Content = prefix + string(source[start:end])
			out = append(out, part)
			if end == c.End {
				break
			}
			overlap := min(cfg.ChunkOverlap, (end-start)/2)
			start = end - overlap
		}
	}
	return out
}
