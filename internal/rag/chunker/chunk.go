package chunker

import (
	"strings"
)

// Chunk 表示经过文档分块之后的一个文本块
// 是整个 RAG 核心的数据结构
// 无论使用 递归分块（Recursive Splitter）、标题分块（Heading Splitter）、启发式分块（Heuristic Splitter）、分子分块（Parent-Child Splitter）
// 都会产生统一的Chunk
type Chunk struct {
	// 是当前 Chunk 的文本内容
	Content string

	// 是当前 Chunk 的标题上下文。并不是Content的一部分，只是为了增强Embedding、BM25、Rerank时的语义
	ContextHeader string

	// 表示 Chunk 在整篇文档中的编号。可用于恢复原文顺序、邻居查找、连续 Chunk 合并、父子分块中的“子块”全局排序
	Seq int

	// 表示当前 Chunk 在“原始文档”中的开始位置。使用的是Unicode rune offset，而不是 Go string 的 byte offset。
	// 因为在 go 中 rune 和 byte 对于中文的offset不一样，所以统一使用 rune offset
	Start int

	// 表示当前 Chunk 在“原始文档”中的结束位置。也是使用Unicode rune offset。
	// 使用 [Start, End) 左闭右开区间。
	// 理想状态下，End - Start == len(Content)
	End int
}

// EmbeddingContent 返回用于检索的文本
// 不仅仅用于 Embedding 也可以用于 BM25、Rerank、调试检索文本
// 如果没有 ContextHeader 就直接返回 Content
// 如果有 ContextHeader 则返回 ContextHeader 和 Content
func (c Chunk) EmbeddingContent() string {
	body := strings.TrimSpace(c.Content)
	if c.ContextHeader == "" {
		return body
	}
	return c.ContextHeader + "\n\n" + body
}

// ChildChunk 表示父子分块下的子块
type ChildChunk struct {
	Chunk

	// 子块对应的父块索引
	ParentIndex int
}

// ParentChildResult 表示父子分块的最终结果
// Parents用于提供完整的上下文，Children用于 Embedding 的检索
type ParentChildResult struct {
	Parents  []Chunk
	Children []ChildChunk
}
