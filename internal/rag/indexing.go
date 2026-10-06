package rag

import (
	"fmt"
	"strconv"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

// qualifyChunkIDs 让不同版本的分块拥有独立 ID。
// 新版本在外部索引中写入失败时，不会覆盖仍可检索的旧版本。
func qualifyChunkIDs(batch *IngestionBatch) {
	parents := make(map[string]string, len(batch.Parents))
	for i := range batch.Parents {
		parent := &batch.Parents[i]
		id := fmt.Sprintf("%s#v%d#parent-%06d", batch.DocumentID, batch.Attempt, parent.ChunkIndex)
		parents[parent.ID], parent.ID = id, id
	}
	for i := range batch.Children {
		child := &batch.Children[i]
		child.ID = fmt.Sprintf("%s#v%d#chunk-%06d", batch.DocumentID, batch.Attempt, child.ChunkIndex)
		if child.ParentChunkID != "" {
			child.ParentChunkID = parents[child.ParentChunkID]
			child.Metadata[retrieval.MetaParentChunkID] = child.ParentChunkID
		}
	}
}

// validateStoredIDs 检查索引器返回的 ID 与全部子块一一对应，拒绝改名或重复。
func validateStoredIDs(chunks []ChunkRecord, ids []string) error {
	if len(chunks) != len(ids) {
		return fmt.Errorf("rag: indexer returned %d ids for %d chunks", len(ids), len(chunks))
	}
	expected := make(map[string]bool, len(chunks))
	for _, chunk := range chunks {
		expected[chunk.ID] = true
	}
	for _, id := range ids {
		if !expected[id] {
			return fmt.Errorf("rag: indexer must preserve chunk ids for document publication")
		}
		delete(expected, id)
	}
	return nil
}

// IngestionOptions 补充 Eino Store 没有定义的“完整原文和父块”信息。
// 它通过官方实现专属 Option 传递，不会成为外部索引需要序列化的元数据。
type IngestionOptions struct{ Batch *IngestionBatch }

// WithIngestionBatch 通过 Eino 实现专属选项传递完整文档批次。
func WithIngestionBatch(batch IngestionBatch) indexer.Option {
	return indexer.WrapImplSpecificOptFn(func(o *IngestionOptions) { o.Batch = &batch })
}

// IndexDocuments 只把子块交给索引组件，所有后端接收相同的检索文本。
// Content 是标题、面包屑和正文组成的检索表示；原文单独保存供引用和评测使用。
func IndexDocuments(batch IngestionBatch, builder searchcontent.Builder) []*schema.Document {
	docs := make([]*schema.Document, 0, len(batch.Children))
	for _, c := range batch.Children {
		m := retrieval.CloneMetadata(c.Metadata)
		m[retrieval.MetaCollectionID], m[retrieval.MetaDocumentID] = batch.CollectionID, batch.DocumentID
		m[retrieval.MetaRevision] = strconv.FormatInt(batch.Attempt, 10)
		m[retrieval.MetaRawContent], m[retrieval.MetaContextHeader] = c.Content, c.ContextHeader
		m[retrieval.MetaChunkIndex], m[retrieval.MetaChunkStart], m[retrieval.MetaChunkEnd] = c.ChunkIndex, c.StartRune, c.EndRune
		m[retrieval.MetaParentChunkID] = c.ParentChunkID
		if _, ok := m["title"]; !ok {
			if _, ok := m["_title"]; !ok {
				m["_title"] = batch.Title
			}
		}
		d := &schema.Document{ID: c.ID, Content: c.Content, MetaData: m}
		d.Content = builder.Build(d)
		docs = append(docs, d)
	}
	return docs
}
