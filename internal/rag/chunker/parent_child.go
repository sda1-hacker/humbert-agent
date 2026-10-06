package chunker

import "strings"

// SplitParentChild 先切父块，再切用于检索的子块；完全重复的单一父块不保存。
func SplitParentChild(text string, parentCfg, childCfg SplitterConfig) ParentChildResult {
	result, _ := splitParentChild(text, parentCfg, childCfg, false)
	return result
}

// SplitParentChildWithDiagnostics 返回父子分块和父块策略诊断，子块仍独立选择策略。
func SplitParentChildWithDiagnostics(
	text string,
	parentCfg, childCfg SplitterConfig,
) (ParentChildResult, *Diagnostics) {
	return splitParentChild(text, parentCfg, childCfg, true)
}

// splitParentChild 共用父子切分流程，并将子块范围映射回整篇原文。
func splitParentChild(
	text string,
	parentCfg, childCfg SplitterConfig,
	withDiagnostics bool,
) (ParentChildResult, *Diagnostics) {
	if text == "" {
		if withDiagnostics {
			return ParentChildResult{}, &Diagnostics{SelectedTier: TierLegacy}
		}

		return ParentChildResult{}, nil
	}

	// 父子配置都应用默认值，子块还按近似 token 预算收紧大小。
	parentCfg = ensureDefaults(parentCfg)
	childCfg = ensureDefaults(childCfg)

	var (
		parents []Chunk
		diag    *Diagnostics
	)

	// -------------------------------------------------------------------------
	// 第一步：切 Parent。
	// -------------------------------------------------------------------------

	if withDiagnostics {
		parents, diag = SplitWithDiagnostics(text, parentCfg)
	} else {
		parents = Split(text, parentCfg)
	}

	if len(parents) == 0 {
		return ParentChildResult{}, diag
	}

	// 只有产生不同子块的父块才保存，避免重复持久化完全相同的内容。
	var newParents []Chunk

	// children 是最终真正用于检索的小块。
	var children []ChildChunk

	// Child.Seq 必须在整个文档范围内连续，
	// 而不是每个 Parent 都从0重新开始。
	childSeq := 0
	source := []rune(text)

	for _, parent := range parents {
		// 子块继续使用自己的策略，可根据父块局部结构选择更细的标题路径。

		// 正文可能含补充表头，子块切分必须使用原文范围，才能保持局部坐标正确。
		body := string(source[parent.Start:parent.End])
		subs := Split(body, childCfg)
		prefix := ""
		if strings.HasSuffix(parent.Content, body) {
			prefix = strings.TrimSuffix(parent.Content, body)
		}
		for i := range subs {
			if prefix != "" && !headerAlreadyPresent(prefix, subs[i].Content, "") &&
				!headerColumnMismatch(prefix, subs[i].Content) {
				subs[i].Content = prefix + subs[i].Content
			}
		}

		// -1 表示这个 Child 没有必要关联一个单独保存的 Parent。
		parentIndex := -1

		// 多个子块或单个不同子块需要保留父块；完全相同的单一子块不保存父块。

		if len(subs) > 1 || (len(subs) == 1 && subs[0].Content != parent.Content) {
			parentIndex = len(newParents)
			newParents = append(newParents, parent)
		}

		for _, sub := range subs {
			// 子块局部坐标加上父块原文起点，转换为整篇文档的字符范围。

			sub.Start += parent.Start
			sub.End += parent.Start

			// 合并父块外层标题和子块局部标题，去掉连接处重复行。

			sub.ContextHeader = mergeBreadcrumbs(
				parent.ContextHeader,
				sub.ContextHeader,
			)

			// 补充父块表头与标题路径后，再检查完整子块的近似预算。
			for _, part := range enforceTokenTarget(text, []Chunk{sub}, childCfg) {
				part.Seq = childSeq
				children = append(children, ChildChunk{Chunk: part, ParentIndex: parentIndex})
				childSeq++
			}
		}
	}

	return ParentChildResult{
		Parents:  newParents,
		Children: children,
	}, diag
}

// mergeBreadcrumbs 合并父子标题路径，并去除连接处重复的标题行。
func mergeBreadcrumbs(parent, child string) string {
	if parent == "" {
		return child
	}

	if child == "" {
		return parent
	}

	parentLines := strings.Split(parent, "\n")
	childLines := strings.Split(child, "\n")

	if len(parentLines) > 0 &&
		len(childLines) > 0 &&
		strings.TrimSpace(parentLines[len(parentLines)-1]) ==
			strings.TrimSpace(childLines[0]) {

		childLines = childLines[1:]
	}

	// Child 原本只有一行，
	// 并且这一行正好和 Parent 最后一行重复。
	//
	// 删除后已经没有新内容，
	// 直接返回 Parent。
	if len(childLines) == 0 {
		return parent
	}

	return parent + "\n" + strings.Join(childLines, "\n")
}
