package chunker

import (
	"context"
	"strings"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// enforceTokenTarget 为无法继续切分的段落和保护块提供最终近似预算处理；索引器仍检查完整模型输入。
func enforceTokenTarget(ctx context.Context, text string, chunks []Chunk, cfg ragconfig.Splitter) []Chunk {
	if cfg.TokenLimit <= 0 {
		return chunks
	}
	return enforceTokenTargetOnRunes(ctx, []rune(text), chunks, cfg, tokenLanguage(text, cfg))
}

// tokenLanguage 优先采用显式语言，避免已知语言的文档仍被全文扫描。
func tokenLanguage(text string, cfg ragconfig.Splitter) string {
	if len(cfg.Languages) > 0 {
		return cfg.Languages[0]
	}
	return DetectLanguage(text)
}

// enforceTokenTargetOnRunes 复用全文字符数据；父子切分只创建一次，子块按自己的范围读取。
func enforceTokenTargetOnRunes(ctx context.Context, source []rune, chunks []Chunk, cfg ragconfig.Splitter, lang string) []Chunk {
	if cfg.TokenLimit <= 0 {
		return chunks
	}
	var out []Chunk
	for _, c := range chunks {
		if ctx.Err() != nil {
			return nil
		}
		if ApproxTokenCount(c.EmbeddingContent(), lang) <= cfg.TokenLimit {
			c.Seq = len(out)
			out = append(out, c)
			continue
		}
		if len(protectedSpansContext(ctx, c.Content)) > 0 {
			// TokenLimit 是软目标。结构块交给完整输入预算处理，不能为了近似字符比率破坏表格、代码或公式。
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
		// 标题自身可能耗尽预算；保留引用上下文，由导入阶段返回明确预算错误。
		if budget < 1 {
			c.Seq = len(out)
			out = append(out, c)
			continue
		}
		for start := c.Start; start < c.End; {
			if ctx.Err() != nil {
				return nil
			}
			end := min(start+budget, c.End)
			// 在硬切分边界附近优先选择句末或换行。
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
