package rag

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 文档模型的校验错误，与检索库实现无关。
var (
	ErrInvalidIngestionBatch = errors.New("rag: invalid ingestion batch")
	ErrUnknownParentChunk    = errors.New("rag: child references unknown parent chunk")
)

// ValidateIngestionBatch 在调用模型或存储前检查文档身份、分块范围和父子关系。
func ValidateIngestionBatch(batch IngestionBatch) error {
	if batch.Attempt < 0 {
		return fmt.Errorf("%w: negative attempt", ErrInvalidIngestionBatch)
	}
	if batch.ContentHash != "" && batch.ContentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(batch.Markdown))) {
		return fmt.Errorf("%w: content hash does not match Markdown", ErrInvalidIngestionBatch)
	}
	if len(batch.ProcessConfig) > 0 && !json.Valid(batch.ProcessConfig) {
		return fmt.Errorf("%w: invalid process config", ErrInvalidIngestionBatch)
	}
	if len(batch.ProcessConfig) > 0 {
		var config map[string]json.RawMessage
		if json.Unmarshal(batch.ProcessConfig, &config) != nil || config == nil {
			return fmt.Errorf("%w: process config must be an object", ErrInvalidIngestionBatch)
		}
	}
	if strings.TrimSpace(batch.CollectionID) == "" {
		return fmt.Errorf("%w: missing collection id", ErrInvalidIngestionBatch)
	}

	if strings.TrimSpace(batch.DocumentID) == "" {
		return fmt.Errorf("%w: missing document id", ErrInvalidIngestionBatch)
	}

	ids := make(map[string]struct{}, len(batch.Parents)+len(batch.Children))
	parentIDs := make(map[string]ChunkRecord, len(batch.Parents))
	indexes := make(map[string]map[int]bool)
	sourceRunes := utf8.RuneCountInString(batch.Markdown)

	validateRecord := func(record ChunkRecord) error {
		if strings.TrimSpace(record.ID) == "" {
			return fmt.Errorf("%w: empty chunk id", ErrInvalidIngestionBatch)
		}

		if record.DocumentID != batch.DocumentID {
			return fmt.Errorf(
				"%w: chunk %q belongs to document %q, batch document is %q",
				ErrInvalidIngestionBatch,
				record.ID,
				record.DocumentID,
				batch.DocumentID,
			)
		}

		if record.ChunkIndex < 0 {
			return fmt.Errorf("%w: chunk %q has negative index", ErrInvalidIngestionBatch, record.ID)
		}

		if strings.TrimSpace(record.Content) == "" || record.StartRune < 0 || record.EndRune <= record.StartRune || record.EndRune > sourceRunes {
			return fmt.Errorf(
				"%w: chunk %q has invalid rune range [%d,%d)",
				ErrInvalidIngestionBatch,
				record.ID,
				record.StartRune,
				record.EndRune,
			)
		}

		if _, exists := ids[record.ID]; exists {
			return fmt.Errorf("%w: duplicate chunk id %q", ErrInvalidIngestionBatch, record.ID)
		}

		ids[record.ID] = struct{}{}
		if indexes[record.ChunkType] == nil {
			indexes[record.ChunkType] = make(map[int]bool)
		}
		if indexes[record.ChunkType][record.ChunkIndex] {
			return fmt.Errorf("%w: duplicate %s chunk index %d", ErrInvalidIngestionBatch, record.ChunkType, record.ChunkIndex)
		}
		indexes[record.ChunkType][record.ChunkIndex] = true
		return nil
	}

	for _, parent := range batch.Parents {
		if parent.ChunkType != ChunkTypeParentText {
			return fmt.Errorf(
				"%w: parent %q has chunk type %q",
				ErrInvalidIngestionBatch,
				parent.ID,
				parent.ChunkType,
			)
		}

		if err := validateRecord(parent); err != nil {
			return err
		}

		if parent.ParentChunkID != "" {
			return fmt.Errorf("%w: nested parent chunks are unsupported", ErrInvalidIngestionBatch)
		}
		parentIDs[parent.ID] = parent
	}

	for _, child := range batch.Children {
		if child.ChunkType != ChunkTypeText {
			return fmt.Errorf(
				"%w: child %q has chunk type %q",
				ErrInvalidIngestionBatch,
				child.ID,
				child.ChunkType,
			)
		}

		if err := validateRecord(child); err != nil {
			return err
		}

		if child.ParentChunkID == "" {
			continue
		}

		if _, exists := parentIDs[child.ParentChunkID]; !exists {
			return fmt.Errorf(
				"%w: child %q -> %q",
				ErrUnknownParentChunk,
				child.ID,
				child.ParentChunkID,
			)
		}
		parent := parentIDs[child.ParentChunkID]
		if child.StartRune < parent.StartRune || child.EndRune > parent.EndRune {
			return fmt.Errorf("%w: child %q is outside parent source range", ErrInvalidIngestionBatch, child.ID)
		}
	}

	return nil
}
