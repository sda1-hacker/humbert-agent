package chunker

import (
	"context"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// SplitParentChild 先切父块，再切检索子块；完全相同的单一父块不重复保存。
func SplitParentChild(text string, parentCfg, childCfg ragconfig.Splitter) ParentChildResult {
	result, _, _ := SplitParentChildContext(context.Background(), text, parentCfg, childCfg)
	return result
}

func SplitParentChildWithDiagnostics(text string, parentCfg, childCfg ragconfig.Splitter) (ParentChildResult, *Diagnostics) {
	result, diag, _ := SplitParentChildContext(context.Background(), text, parentCfg, childCfg)
	return result, diag
}

// SplitParentChildContext 全程复用一份 AST 和全文坐标，子块自动继承正确的标题、表头及代码围栏。
func SplitParentChildContext(ctx context.Context, text string, parentCfg, childCfg ragconfig.Splitter) (ParentChildResult, *Diagnostics, error) {
	layout, err := NewLayout(ctx, text)
	if err != nil {
		return ParentChildResult{}, nil, err
	}
	return layout.SplitParentChild(ctx, parentCfg, childCfg, 0)
}

// SplitParentChild 共用布局与全局子块预算；每个父块不会重新获得完整的 MaxChunks 额度。
func (layout *Layout) SplitParentChild(ctx context.Context, parentCfg, childCfg ragconfig.Splitter, maxChunks int) (ParentChildResult, *Diagnostics, error) {
	parents, diag, err := layout.split(ctx, 0, len(layout.source), parentCfg, maxChunks)
	if err != nil {
		return ParentChildResult{}, diag, err
	}
	var result ParentChildResult
	for _, parent := range parents {
		remaining := 0
		if maxChunks > 0 {
			remaining = maxChunks - len(result.Children)
			if remaining <= 0 {
				return ParentChildResult{}, diag, ErrChunkLimit
			}
		}
		children, _, err := layout.split(ctx, parent.Start, parent.End, childCfg, remaining)
		if err != nil {
			return ParentChildResult{}, diag, err
		}
		index := -1
		if len(children) > 1 || (len(children) == 1 && children[0].Content != parent.Content) {
			index = len(result.Parents)
			parent.Seq = index
			result.Parents = append(result.Parents, parent)
		}
		for _, child := range children {
			child.Seq = len(result.Children)
			result.Children = append(result.Children, ChildChunk{Chunk: child, ParentIndex: index})
		}
	}
	return result, diag, ctx.Err()
}
