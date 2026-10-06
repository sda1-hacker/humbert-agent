package retrieval

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strconv"

	"github.com/cloudwego/eino/schema"
)

// 这些元数据是 RAG 的文档语义，与具体的向量库和 BM25 库无关。
const (
	MetaSourceDocumentID    = "rag_source_document_id"
	MetaSourceDocumentIndex = "rag_source_document_index"
	MetaChunkType           = "rag_chunk_type"
	MetaCollectionID        = "rag_collection_id"
	MetaDocumentID          = "rag_document_id"
	MetaRevision            = "rag_document_revision"
	MetaRawContent          = "rag_raw_content"
	MetaParentChunkID       = "rag_parent_chunk_id"
	MetaChunkIndex          = "rag_chunk_index"
	MetaChunkStart          = "rag_chunk_start"
	MetaChunkEnd            = "rag_chunk_end"
	MetaContextHeader       = "rag_context_header"
	MetaMatchType           = "rag_match_type"
	MetaVectorScore         = "rag_vector_score"
	MetaKeywordScore        = "rag_keyword_score"
	MetaVectorRank          = "rag_vector_rank"
	MetaKeywordRank         = "rag_keyword_rank"
)

// Documents 把内部的引用信息写入 Eino 文档；相关性分数使用官方 WithScore。
func Documents(results []SearchResult) []*schema.Document {
	docs := make([]*schema.Document, 0, len(results))
	for _, r := range results {
		m := CloneMetadata(r.Metadata)
		m[MetaCollectionID], m[MetaDocumentID] = r.CollectionID, r.DocumentID
		// bigint 使用字符串，经过 JSON 或其他数据库时不会丢失精度。
		m[MetaRevision] = strconv.FormatInt(r.DocumentRevision, 10)
		m[MetaRawContent], m[MetaParentChunkID] = r.Content, r.ParentChunkID
		m[MetaChunkIndex], m[MetaChunkStart], m[MetaChunkEnd] = r.ChunkIndex, r.StartRune, r.EndRune
		m[MetaContextHeader], m[MetaMatchType] = r.ContextHeader, string(r.MatchType)
		m[MetaVectorScore], m[MetaKeywordScore] = r.VectorScore, r.KeywordScore
		m[MetaVectorRank], m[MetaKeywordRank] = r.VectorRank, r.KeywordRank
		docs = append(docs, (&schema.Document{ID: r.ChunkID, Content: r.Content, MetaData: m}).WithScore(r.Score))
	}
	return docs
}

// Results 接收任意 Eino Retriever 的输出。没有 RAG 元数据时仍可进行基本检索；
// 原文、父子关系和版本信息只有后端保留了这些元数据才可以恢复。
func Results(docs []*schema.Document) ([]SearchResult, error) {
	results := make([]SearchResult, 0, len(docs))
	for i, d := range docs {
		if d == nil || d.ID == "" {
			return nil, fmt.Errorf("rag: invalid retrieved document at %d", i)
		}
		if math.IsNaN(d.Score()) || math.IsInf(d.Score(), 0) {
			return nil, fmt.Errorf("rag: invalid score for %q", d.ID)
		}
		m := d.MetaData
		revision, err := Revision(m)
		if err != nil {
			return nil, err
		}
		content := d.Content
		if raw, ok := m[MetaRawContent].(string); ok {
			content = raw
		}
		r := SearchResult{ChunkID: d.ID, Content: content, Score: d.Score(), BaseScore: d.Score(), Metadata: m,
			CollectionID: text(m, MetaCollectionID), DocumentID: text(m, MetaDocumentID), DocumentRevision: revision,
			ParentChunkID: text(m, MetaParentChunkID), ContextHeader: text(m, MetaContextHeader),
			MatchType: MatchType(text(m, MetaMatchType)), VectorScore: number(m, MetaVectorScore), KeywordScore: number(m, MetaKeywordScore)}
		for _, field := range []struct {
			key    string
			target *int
		}{{MetaChunkIndex, &r.ChunkIndex}, {MetaChunkStart, &r.StartRune}, {MetaChunkEnd, &r.EndRune}, {MetaVectorRank, &r.VectorRank}, {MetaKeywordRank, &r.KeywordRank}} {
			value := number(m, field.key)
			// 位置信息不能因强制类型转换而被截断。
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value >= float64(int(^uint(0)>>1)) {
				return nil, fmt.Errorf("rag: invalid metadata %s", field.key)
			}
			*field.target = int(value)
		}
		results = append(results, r)
	}
	return results, nil
}

// Revision 不接受 float64，防止大版本号在 JSON 反序列化后悄悄损失精度。
func Revision(m map[string]any) (int64, error) {
	var value int64
	var err error
	switch v := m[MetaRevision].(type) {
	case nil:
		return 0, nil
	case string:
		value, err = strconv.ParseInt(v, 10, 64)
	case int64:
		value = v
	case int:
		value = int64(v)
	case json.Number:
		value, err = v.Int64()
	default:
		err = fmt.Errorf("revision must be an integer or decimal string")
	}
	if err != nil || value < 0 {
		return 0, fmt.Errorf("rag: invalid document revision %v", m[MetaRevision])
	}
	return value, nil
}

// text 读取字符串元数据，缺失或类型不符时返回空字符串。
func text(m map[string]any, k string) string { v, _ := m[k].(string); return v }

// number 读取数值元数据；缺失视为零，类型错误返回非数供上层拒绝。
func number(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, err := v.Float64()
		if err == nil {
			return f
		}
	}
	if m[k] != nil {
		return math.NaN()
	}
	return 0
}

// CloneMetadata 返回可写的顶层副本，避免不同分块或检索组件相互修改元数据。
// 嵌套值仍按只读数据共享，不进行递归复制。
func CloneMetadata(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src)+8)
	maps.Copy(dst, src)
	return dst
}
