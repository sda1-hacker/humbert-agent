package chunker

import (
	"context"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// SplitText 递归切分普通区域，保护表格与代码，再合并为带原文范围的块。
func SplitText(text string, cfg ragconfig.Splitter) []Chunk {
	return splitText(context.Background(), text, cfg)
}

func splitText(ctx context.Context, text string, cfg ragconfig.Splitter) []Chunk {
	if text == "" || ctx.Err() != nil {
		return nil
	}

	chunkSize := cfg.ChunkSize
	if chunkSize <= 0 {
		chunkSize = ragconfig.DefaultChunkSize
	}
	// 底层入口保留调用方分隔符；默认值由业务入口统一提供。
	units := buildUnitsWithProtection(ctx, text, protectedSpansContext(ctx, text), cfg.Separators, chunkSize)
	return mergeUnits(ctx, units, chunkSize, max(0, cfg.ChunkOverlap))
}
