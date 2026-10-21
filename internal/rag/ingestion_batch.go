package rag

import (
	"fmt"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// newIngestionBatch 统一两种切分策略的文档身份与元数据，避免来源处理逻辑分叉。
// 全文位置表只留在本次导入内存中；落库时每个分块仅保存与自身范围相关的出处。
func newIngestionBatch(collectionID string, source *schema.Document, markdown string, childCount int) IngestionBatch {
	metadata := retrieval.CloneMetadata(source.MetaData)
	delete(metadata, documenttext.MetaSourceBlocks)
	blocks, _ := source.MetaData[documenttext.MetaSourceBlocks].([]documenttext.SourceBlock)
	return IngestionBatch{
		CollectionID: collectionID,
		DocumentID:   source.ID,
		Title:        documentTitle(source),
		Markdown:     markdown,
		Metadata:     metadata,
		SourceBlocks: blocks,
		Children:     make([]ChunkRecord, 0, childCount),
	}
}

// buildFlatBatch 将普通切分结果转换成持久化模型。
func buildFlatBatch(collectionID string, source *schema.Document, markdown string, chunks []chunker.Chunk) IngestionBatch {
	batch := newIngestionBatch(collectionID, source, markdown, len(chunks))
	for _, chunk := range chunks {
		chunkID := fmt.Sprintf("%s#chunk-%06d", source.ID, chunk.Seq)

		batch.Children = append(batch.Children, buildChunkRecord(source, chunk, chunkID, ChunkTypeText, ""))
	}

	return batch
}

// buildParentChildBatch 转换父子切分结果，保存父块身份与子块的关联关系。
func buildParentChildBatch(collectionID string, source *schema.Document, markdown string, result chunker.ParentChildResult) IngestionBatch {
	batch := newIngestionBatch(collectionID, source, markdown, len(result.Children))
	batch.Parents = make([]ChunkRecord, 0, len(result.Parents))

	parentIDs := make([]string, len(result.Parents))

	for i, parent := range result.Parents {
		parentID := fmt.Sprintf("%s#parent-%06d", source.ID, parent.Seq)
		parentIDs[i] = parentID

		batch.Parents = append(batch.Parents, buildChunkRecord(source, parent, parentID, ChunkTypeParentText, ""))
	}

	for _, child := range result.Children {
		parentID := ""

		if child.ParentIndex >= 0 && child.ParentIndex < len(parentIDs) {
			parentID = parentIDs[child.ParentIndex]
		}

		childID := fmt.Sprintf("%s#chunk-%06d", source.ID, child.Seq)

		batch.Children = append(batch.Children, buildChunkRecord(source, child.Chunk, childID, ChunkTypeText, parentID))
	}

	return batch
}

// buildChunkRecord 保存正文、原文坐标和来源元数据，不生成模型向量。
func buildChunkRecord(
	source *schema.Document,
	chunk chunker.Chunk,
	id string,
	chunkType string,
	parentChunkID string,
) ChunkRecord {
	metadata := retrieval.CloneMetadata(source.MetaData)
	delete(metadata, "rag_image_assets")
	delete(metadata, "rag_image_recognition")
	delete(metadata, "rag_parse_diagnostics")
	delete(metadata, "rag_parse_config")
	delete(metadata, MetaParserWarnings)
	documenttext.AttachLocators(metadata, chunk.Start, chunk.End)

	metadata[retrieval.MetaSourceDocumentID] = source.ID
	metadata[retrieval.MetaChunkIndex] = chunk.Seq
	metadata[retrieval.MetaChunkStart] = chunk.Start
	metadata[retrieval.MetaChunkEnd] = chunk.End
	metadata[retrieval.MetaContextHeader] = chunk.ContextHeader
	metadata[retrieval.MetaChunkType] = chunkType

	if parentChunkID != "" {
		metadata[retrieval.MetaParentChunkID] = parentChunkID
	}

	return ChunkRecord{
		ID:            id,
		DocumentID:    source.ID,
		ChunkType:     chunkType,
		ChunkIndex:    chunk.Seq,
		Content:       chunk.Content,
		ContextHeader: chunk.ContextHeader,
		StartRune:     chunk.Start,
		EndRune:       chunk.End,
		ParentChunkID: parentChunkID,
		Metadata:      metadata,
	}
}

// documentTitle 按统一标题优先级取值，缺失时使用文档 ID。
func documentTitle(doc *schema.Document) string {
	if doc == nil {
		return ""
	}

	if title := retrieval.Title(ragconfig.SearchText{}, doc); title != "" {
		return title
	}

	return doc.ID
}
