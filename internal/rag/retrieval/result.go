package retrieval

import "strconv"

// MatchType 表示一个 Chunk 是通过哪一路 Retriever 命中的。
type MatchType string

const (
	MatchVector  MatchType = "vector"
	MatchKeyword MatchType = "keyword"
	MatchHybrid  MatchType = "hybrid"
)

// SearchResult 是整个 RAG Retrieval Core 的统一结果模型。
//
// 生命周期:
//
//	Dense / BM25
//	    ↓
//	RRF
//	    ↓
//	Reranker
//	    ↓
//	Parent Context Expansion
//	    ↓
//	SearchResult
//
// 一个非常重要的原则:
//
//	Content
//
// 永远表示“真正被检索命中的 Chunk”。
//
// 即使后面扩展出了 Parent：
//
//	ContextContent
//
// 也绝对不覆盖 Content。
//
// 这样：
//
//	Citation
//	Debug
//	Rerank
//	Retrieval Evaluation
//
// 始终知道究竟是哪个小块真正被检索命中。
type SearchResult struct {
	CollectionID string `json:"collection_id,omitempty"`
	ChunkID      string `json:"chunk_id"`

	DocumentID       string `json:"document_id"`
	DocumentRevision int64  `json:"document_revision"`

	ParentChunkID string `json:"parent_chunk_id,omitempty"`

	ChunkIndex int `json:"chunk_index"`

	Content string `json:"content"`

	ContextHeader string `json:"context_header,omitempty"`

	StartRune int `json:"start_rune"`
	EndRune   int `json:"end_rune"`

	// Score 是“当前 Pipeline 阶段最终用于排序”的分数。
	//
	// RRF 后：
	//
	//     Score = RRF Score
	//
	// Rerank 后：
	//
	//     Score = Composite Score
	//
	// 原始召回分会另外保存在 BaseScore。
	Score float64 `json:"score"`

	// BaseScore 是进入 Reranker 前的 Retrieval Score。
	//
	// 对 Hybrid：
	//
	//     normalized RRF
	//
	// 对 Vector-only：
	//
	//     cosine similarity
	//
	// 对 Keyword-only：
	//
	//     normalized BM25
	BaseScore float64 `json:"base_score,omitempty"`

	// ModelScore 是 Rerank Model 给出的 relevance score。
	ModelScore float64 `json:"model_score,omitempty"`

	// SourceWeight 是可选来源权重。
	//
	// 当前普通 Document 默认0。
	//
	// 未来如果加入：
	//
	//     FAQ priority
	//     curated source
	//     trusted source
	//
	// 可以由上层填入 [0,1] 的权重。
	SourceWeight float64 `json:"source_weight,omitempty"`

	Reranked bool `json:"reranked,omitempty"`

	VectorScore  float64 `json:"vector_score,omitempty"`
	KeywordScore float64 `json:"keyword_score,omitempty"`

	VectorRank  int `json:"vector_rank,omitempty"`
	KeywordRank int `json:"keyword_rank,omitempty"`

	MatchType MatchType `json:"match_type"`

	// ContextChunkID 是最终给 LLM 阅读的上下文 Chunk。
	//
	// 普通模式：
	//
	//     ContextChunkID == ChunkID
	//
	// Parent-Child：
	//
	//     ContextChunkID == ParentChunkID
	ContextChunkID string `json:"context_chunk_id,omitempty"`

	// ContextContent 是最终推荐给 LLM 的上下文文本。
	//
	// 普通 Chunk：
	//
	//     ContextContent = Content
	//
	// Parent-Child：
	//
	//     ContextContent = Parent.Content
	ContextContent      string `json:"context_content,omitempty"`
	ContextStartRune    int    `json:"context_start_rune"`
	ContextEndRune      int    `json:"context_end_rune"`
	ContextSourceHeader string `json:"context_source_header,omitempty"`

	// Evidence survives parent grouping, retaining the child ranges used for
	// citations and evaluation rather than discarding all but one child.
	Evidence []HitEvidence `json:"evidence,omitempty"`

	Metadata map[string]any `json:"metadata,omitempty"`
}

type HitEvidence struct {
	ChunkID   string  `json:"chunk_id"`
	StartRune int     `json:"start_rune"`
	EndRune   int     `json:"end_rune"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}

// EffectiveContent 返回最终应该交给 LLM 阅读的文本。
//
// 如果 Pipeline 已经做 Parent Expansion：
//
//	ContextContent
//
// 优先。
//
// 否则直接使用命中的 Child Content。
func (r SearchResult) EffectiveContent() string {
	if r.ContextContent != "" {
		return r.ContextContent
	}

	return r.Content
}

// EffectiveChunkID 返回 EffectiveContent 对应的 Chunk ID。
func (r SearchResult) EffectiveChunkID() string {
	if r.ContextChunkID != "" {
		return r.ContextChunkID
	}

	return r.ChunkID
}

// IdentityKey prevents cross-collection/document collisions when results are
// merged. Legacy standalone results with only a ChunkID retain their key.
func (r SearchResult) IdentityKey() string {
	if r.CollectionID == "" && r.DocumentID == "" && r.DocumentRevision == 0 {
		return r.ChunkID
	}
	return r.CollectionID + "\x00" + r.DocumentID + "\x00" + strconv.FormatInt(r.DocumentRevision, 10) + "\x00" + r.ChunkID
}
