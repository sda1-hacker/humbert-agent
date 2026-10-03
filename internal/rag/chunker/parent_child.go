package chunker

import "strings"

// SplitParentChild 使用父子分块模式切分整篇文档。
//
// Parent-Child RAG 的核心思想是：
//
//	Child 小
//	    → 更容易精准检索
//
//	Parent 大
//	    → 检索命中 Child 后，给 LLM 提供更完整上下文
//
// 典型配置:
//
//	ParentChunkSize = 4096
//	ChildChunkSize  = 384
//
// 整体流程:
//
//	Document
//	    ↓
//	Split(parentCfg)
//	    ↓
//	Parent 0
//	Parent 1
//	Parent 2
//	    ↓
//	每个 Parent.Content 再调用 Split(childCfg)
//	    ↓
//	Child
//	    ↓
//	把 Child 的局部 Start/End 转换回整篇文档坐标
//	    ↓
//	合并 Parent / Child Breadcrumb
//	    ↓
//	ParentChildResult
//
// 注意:
//
// Parent 和 Child 都复用完整 Split()，而不是直接调用 SplitText()。
//
// 因此:
//
//	parentCfg.Strategy = auto
//
// 时 Parent 可以走:
//
//	Heading / Heuristic / Legacy
//
// 同理:
//
//	childCfg.Strategy = auto
//
// 时每一个 Parent 内部还可以重新根据自己的局部结构选择策略。
//
// 例如整篇文档:
//
//	# Chapter 1
//
//	## Installation
//	### Linux
//	...
//
// Parent 可能以 H1 为大结构；
// Child 再处理 Parent 内容时，又可以识别 H2/H3，
// 从而得到更细粒度的 ContextHeader。
func SplitParentChild(text string, parentCfg, childCfg SplitterConfig) ParentChildResult {
	result, _ := splitParentChild(text, parentCfg, childCfg, false)
	return result
}

// SplitParentChildWithDiagnostics 是 SplitParentChild 的调试版本。
//
// ParentChildResult 与 SplitParentChild 完全相同，
// 额外返回的 Diagnostics 描述的是：
//
//	“整篇文档切 Parent 时”
//
// 使用了什么 Strategy。
//
// 例如:
//
//	TierChain:
//	    heading → heuristic → legacy
//
//	SelectedTier:
//	    heading
//
// 注意:
//
// 当前 Diagnostics 不记录每一个 Parent 的 Child Strategy Trace。
//
// 原因是 Parent 可能很多，如果把每个 Child Split 的完整 Profile /
// Reject Trace 都保存下来，会让调试结构快速膨胀。
//
// 如果未来我们的 Demo 需要一个非常详细的 Chunk Preview，
// 可以再单独设计:
//
//	ParentDiagnostics[]
//
// 但当前保持 WeKnora 的简单模型。
func SplitParentChildWithDiagnostics(
	text string,
	parentCfg, childCfg SplitterConfig,
) (ParentChildResult, *Diagnostics) {
	return splitParentChild(text, parentCfg, childCfg, true)
}

// splitParentChild 是 Parent-Child 的内部统一实现。
//
// withDiagnostics:
//
//	false
//	    正常 ingestion hot path。
//
//	true
//	    Preview / Debug 路径。
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

	// Parent 和 Child 都先应用完整运行时默认值。
	//
	// Parent:
	//
	//     ChunkSize
	//     Overlap
	//     Separators
	//
	// Child:
	//
	// 除了以上内容，还可能通过 TokenLimit
	// 进一步缩小实际 ChunkSize。
	//
	// 例如:
	//
	//     child ChunkSize = 1024
	//     TokenLimit      = 200
	//     Language        = zh
	//
	// ensureDefaults 后 Child 实际字符预算
	// 可能远低于 1024。
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

	// newParents 与 parents 不一定相同。
	//
	// 为什么？
	//
	// 假设某个 Parent：
	//
	//     只有 200 rune
	//
	// Child Split 后：
	//
	//     还是完全相同的一个 200 rune Chunk
	//
	// 那么数据库里没必要同时保存：
	//
	//     Parent = 200 rune
	//     Child  = 同样 200 rune
	//
	// 这会制造完全冗余的数据。
	//
	// 因此只有“确实产生父子层级价值”的 Parent
	// 才进入 newParents。
	var newParents []Chunk

	// children 是最终真正用于检索的小块。
	var children []ChildChunk

	// Child.Seq 必须在整个文档范围内连续，
	// 而不是每个 Parent 都从0重新开始。
	childSeq := 0

	for _, parent := range parents {
		// ---------------------------------------------------------------------
		// 第二步：在当前 Parent 内再次执行完整 Split。
		//
		// 非常重要:
		//
		// childCfg.Strategy 不被强制修改。
		//
		// 如果是 auto：
		//
		//     每个 Parent 会根据自己的局部结构再选择一次 Strategy。
		//
		// 例如 Parent 内含：
		//
		//     ## Installation
		//     ### Linux
		//     ### Windows
		//
		// Child Split 就有机会得到更细的 Breadcrumb。
		// ---------------------------------------------------------------------

		subs := Split(parent.Content, childCfg)

		// -1 表示这个 Child 没有必要关联一个单独保存的 Parent。
		parentIndex := -1

		// ---------------------------------------------------------------------
		// 判断这个 Parent 有没有必要保存。
		//
		// 情况 A:
		//
		//     Parent
		//         ↓
		//     Child1
		//     Child2
		//
		// 有多个 Child：
		//
		//     Parent 有明显上下文扩展价值
		//     → 保存 Parent
		//
		// 情况 B:
		//
		//     Parent
		//         ↓
		//     只有一个 Child
		//
		// 但 Child.Content 与 Parent.Content 不一样：
		//
		//     → 仍然保存 Parent
		//
		// 情况 C:
		//
		//     Parent
		//         ↓
		//     唯一 Child
		//
		// 且：
		//
		//     Child.Content == Parent.Content
		//
		// 两者完全重复：
		//
		//     → 不保存 Parent
		//     → ParentIndex = -1
		// ---------------------------------------------------------------------

		if len(subs) > 1 || (len(subs) == 1 && subs[0].Content != parent.Content) {
			parentIndex = len(newParents)
			newParents = append(newParents, parent)
		}

		for _, sub := range subs {
			// -----------------------------------------------------------------
			// Split(parent.Content, childCfg)
			//
			// 得到的 sub.Start / sub.End 是：
			//
			//     “相对于 Parent.Content”
			//
			// 的局部坐标。
			//
			// 例如:
			//
			//     Parent.Start = 5000
			//
			// Child:
			//
			//     Start = 100
			//     End   = 400
			//
			// 那么整篇文档里的真实位置：
			//
			//     Start = 5100
			//     End   = 5400
			//
			// 所以必须重新加 parent.Start。
			// -----------------------------------------------------------------

			sub.Start += parent.Start
			sub.End += parent.Start

			// -----------------------------------------------------------------
			// Child 的 ContextHeader 需要同时保留：
			//
			//     Parent 的外层上下文
			//
			// 和：
			//
			//     Child 自己重新识别出的局部上下文
			//
			// 例如 Parent:
			//
			//     # 产品手册
			//     ## 安装
			//
			// Child:
			//
			//     ## 安装
			//     ### Linux
			//
			// merge 后:
			//
			//     # 产品手册
			//     ## 安装
			//     ### Linux
			//
			// 中间重复的：
			//
			//     ## 安装
			//
			// 会被 mergeBreadcrumbs 去掉。
			// -----------------------------------------------------------------

			sub.ContextHeader = mergeBreadcrumbs(
				parent.ContextHeader,
				sub.ContextHeader,
			)

			// Child Seq 使用整个文档级连续编号。
			sub.Seq = childSeq

			children = append(children, ChildChunk{
				Chunk:       sub,
				ParentIndex: parentIndex,
			})

			childSeq++
		}
	}

	return ParentChildResult{
		Parents:  newParents,
		Children: children,
	}, diag
}

// mergeBreadcrumbs 合并 Parent 和 Child 的 ContextHeader。
//
// 最常见的情况:
//
// Parent:
//
//	# 产品手册
//	## 安装
//
// Child:
//
//	## 安装
//	### Linux
//
// 如果直接拼接:
//
//	# 产品手册
//	## 安装
//	## 安装
//	### Linux
//
// 会重复。
//
// 当前规则:
//
// 如果：
//
//	Parent 最后一行
//
// 等于：
//
//	Child 第一行
//
// 则删除 Child 第一行，再拼接。
//
// 最终:
//
//	# 产品手册
//	## 安装
//	### Linux
//
// -----------------------------------------------------------------------------
// 为什么只处理“接缝处的一行重复”，
// 而不是做任意字符串去重？
//
// 因为这是一个结构非常明确的问题：
//
// Parent 的最后一个 Heading
// 通常正好就是 Child 输入文本顶部重新识别到的第一个 Heading。
//
// 如果做过度智能的任意去重，很容易误删合法的重复章节名。
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
