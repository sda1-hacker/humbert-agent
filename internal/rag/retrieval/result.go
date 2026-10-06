package retrieval

import "strconv"

// MatchType 表示一个 Chunk 是通过哪一路 Retriever 命中的。
type MatchType string

const (
	MatchVector  MatchType = "vector"
	MatchKeyword MatchType = "keyword"
	MatchHybrid  MatchType = "hybrid"
)

// SearchResult 保存召回子块、分数和最终上下文；原始证据与父块内容分别保留，便于引用和评测。
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

	// Score 是当前阶段的排序分数，召回融合后或精排后含义不同。
	Score float64 `json:"score"`

	// BaseScore 保留进入精排前的召回分数，供诊断和评分融合使用。
	BaseScore float64 `json:"base_score,omitempty"`

	// ModelScore 是精排模型给出的相关性分数。
	ModelScore float64 `json:"model_score,omitempty"`

	// SourceWeight 是可选的来源可信度权重，默认不提供额外加分。
	SourceWeight float64 `json:"source_weight,omitempty"`

	Reranked bool `json:"reranked,omitempty"`

	VectorScore  float64 `json:"vector_score,omitempty"`
	KeywordScore float64 `json:"keyword_score,omitempty"`

	VectorRank  int `json:"vector_rank,omitempty"`
	KeywordRank int `json:"keyword_rank,omitempty"`

	MatchType MatchType `json:"match_type"`

	// ContextChunkID 是最终上下文对应的分块 ID，父块扩展时与命中子块 ID 不同。
	ContextChunkID string `json:"context_chunk_id,omitempty"`

	// ContextContent 保存最终上下文文本，原始命中正文仍保存在 Content 中。
	ContextContent      string `json:"context_content,omitempty"`
	ContextStartRune    int    `json:"context_start_rune"`
	ContextEndRune      int    `json:"context_end_rune"`
	ContextSourceHeader string `json:"context_source_header,omitempty"`

	// Evidence 保留父块分组前的全部子块范围，用于引用和评测。
	Evidence []HitEvidence `json:"evidence,omitempty"`

	Metadata map[string]any `json:"metadata,omitempty"`
}

// HitEvidence 原始命中子块正文、分数与范围，父块分组后仍保留引用和评测依据。
type HitEvidence struct {
	ChunkID   string  `json:"chunk_id"`
	StartRune int     `json:"start_rune"`
	EndRune   int     `json:"end_rune"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}

// EffectiveContent 优先返回扩展后的上下文，没有上下文时返回子块正文。
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

// IdentityKey 结合知识库、文档、版本和分块 ID 去重；仅有分块 ID 的独立结果仍可使用。
func (r SearchResult) IdentityKey() string {
	if r.CollectionID == "" && r.DocumentID == "" && r.DocumentRevision == 0 {
		return r.ChunkID
	}
	return r.CollectionID + "\x00" + r.DocumentID + "\x00" + strconv.FormatInt(r.DocumentRevision, 10) + "\x00" + r.ChunkID
}
