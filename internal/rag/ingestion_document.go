package rag

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// resolveIngestSource 统一托管上传和重试的原件解析，不改变版本预留顺序。
func (s *Service) resolveIngestSource(ctx context.Context, req IngestRequest, retry bool) (document.Source, *SourceFile, error) {
	var saved *SourceFile
	var err error
	if !retry && req.StoredSource != nil {
		if s.sources == nil {
			return document.Source{}, nil, errors.New("rag: 托管原件存储未配置")
		}
		file := *req.StoredSource
		req.Source, err = s.sources.Resolve(ctx, file)
		if err != nil {
			return document.Source{}, nil, err
		}
		saved = &file
	}
	if retry {
		if s.documents == nil || s.sources == nil || req.DocumentID == "" {
			return document.Source{}, nil, errors.New("rag: 重试需要文档 ID、状态存储和托管原件")
		}
		if _, err := s.documents.RecoverDocuments(ctx, req.CollectionID); err != nil {
			return document.Source{}, nil, err
		}
		record, err := s.documents.GetDocument(ctx, req.CollectionID, req.DocumentID)
		if err != nil {
			return document.Source{}, nil, err
		}
		if !CanRetryDocument(record) {
			return document.Source{}, nil, errors.New("rag: 只能重试已失败或已取消的导入")
		}
		file := record.SourceFile
		if pending, ok := record.Metadata["rag_pending_source"]; ok {
			// 缺失或损坏的新原件不能回退为旧版本原件，否则重试会导入错误内容。
			file = SourceFile{}
			data, err := json.Marshal(pending)
			if err != nil {
				return document.Source{}, nil, err
			}
			if err := json.Unmarshal(data, &file); err != nil {
				return document.Source{}, nil, err
			}
		}
		req.Source, err = s.sources.Resolve(ctx, file)
		if err != nil {
			return document.Source{}, nil, err
		}
		saved = &file
	}
	return req.Source, saved, nil
}

// ingestDocument 处理单份解析结果；空文档返回 nil，由批次入口统一判断。
func (s *Service) ingestDocument(ctx context.Context, req IngestRequest, sourceDoc *schema.Document, file SourceFile, sourceURI string, attempt int64, index int, advance func(string) error) (*IngestDocumentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if sourceDoc == nil {
		return nil, nil
	}
	copy := *sourceDoc
	copy.ID = strings.TrimSpace(copy.ID)
	if req.DocumentID != "" {
		copy.ID = req.DocumentID
	}
	sourceDoc = &copy
	if file.Path != "" {
		// Loader 读取的是内容摘要命名的副本，展示名和来源仍使用用户提交的原件信息。
		sourceDoc.MetaData = retrieval.CloneMetadata(sourceDoc.MetaData)
		sourceDoc.MetaData[MetaSourceURI], sourceDoc.MetaData[MetaFileName] = file.URI, file.Name
		sourceDoc.MetaData[MetaFileExt], sourceDoc.MetaData[MetaFileSize] = file.Type, file.Size
		sourceDoc.MetaData["rag_source_content_hash"] = file.Hash
		savedTitle := strings.TrimSuffix(filepath.Base(sourceURI), filepath.Ext(sourceURI))
		if title, _ := sourceDoc.MetaData[MetaTitle].(string); title == "" || title == savedTitle {
			sourceDoc.MetaData[MetaTitle] = strings.TrimSuffix(file.Name, filepath.Ext(file.Name))
		}
	}

	documentID := strings.TrimSpace(sourceDoc.ID)
	if documentID == "" {
		return nil, fmt.Errorf("%w: source document index %d", ErrMissingDocumentID, index)
	}

	markdown := chunker.NormalizeLineEndings(sourceDoc.Content)
	if strings.TrimSpace(markdown) == "" {
		return nil, nil
	}
	if len(markdown) > s.config.Ingest.MaxTextBytes {
		return nil, fmt.Errorf("%w: document text exceeds %d bytes", ErrIngestLimit, s.config.Ingest.MaxTextBytes)
	}

	prepared, err := s.splitIngestDocument(ctx, req, sourceDoc, markdown)
	if err != nil {
		return nil, err
	}
	batch, diag := prepared.batch, prepared.diagnostics
	layout, processConfig := prepared.layout, prepared.process
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateIngestionBatch(batch); err != nil {
		return nil, err
	}
	if err := s.fitInputs(ctx, &batch, layout); err != nil {
		return nil, err
	}
	if assets, assetErr := imageAssets(batch.Metadata); assetErr != nil {
		return nil, assetErr
	} else if len(assets) > 0 {
		if err := advance(StageImages); err != nil {
			return nil, err
		}
		if err := s.enrichImages(ctx, &batch, req, attempt); err != nil {
			return nil, err
		}
	}

	batch.Attempt = attempt
	batch.Source = file
	if attempt > 0 {
		qualifyChunkIDs(&batch)
	}
	batch.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(batch.Markdown)))
	batch.ProcessConfig, err = json.Marshal(processConfig)
	if err != nil {
		return nil, fmt.Errorf("encode effective process config: %w", err)
	}
	if err := ValidateIngestionBatch(batch); err != nil {
		return nil, err
	}
	if err := advance(StageIndex); err != nil {
		return nil, err
	}
	ids, warnings, err := s.storeBatch(ctx, batch)
	if err != nil {
		return nil, fmt.Errorf("store document %q: %w", documentID, err)
	}

	if parseWarnings, ok := sourceDoc.MetaData[MetaParserWarnings].([]string); ok {
		warnings = append(parseWarnings, warnings...)
	}
	images, _ := imageAssets(batch.Metadata)
	warnings = append(warnings, imageWarnings(images)...)
	item := IngestDocumentResult{
		Attempt:     attempt,
		DocumentID:  documentID,
		ParentCount: len(batch.Parents),
		ChildCount:  len(batch.Children),
		ChunkIDs:    ids,
		Diagnostics: diag,
		Warnings:    warnings,
		Images:      images,
	}

	return &item, nil
}

type ingestProcessConfig struct {
	Version        string `json:"version"`
	MaxInputTokens int    `json:"max_input_tokens"`
	MaxBatchTokens int    `json:"max_batch_tokens"`
	// Splitter 普通切分或父子切分使用的基础参数。
	Splitter       ragconfig.Splitter  `json:"splitter"`
	ParentChild    bool                `json:"parent_child"`
	ParentSplitter *ragconfig.Splitter `json:"parent_splitter,omitempty"`
	ChildSplitter  *ragconfig.Splitter `json:"child_splitter,omitempty"`
}

type preparedIngestion struct {
	batch       IngestionBatch
	layout      *chunker.Layout
	diagnostics *chunker.Diagnostics
	process     ingestProcessConfig
}

// splitIngestDocument 保留普通/父子两种策略，统一块数错误和处理配置。
func (s *Service) splitIngestDocument(ctx context.Context, req IngestRequest, sourceDoc *schema.Document, markdown string) (*preparedIngestion, error) {
	baseCfg := req.Splitter.Effective()
	var (
		batch    IngestionBatch
		diag     *chunker.Diagnostics
		splitErr error
	)
	layout, layoutErr := chunker.NewLayout(ctx, markdown)
	if layoutErr != nil {
		return nil, layoutErr
	}
	processConfig := ingestProcessConfig{Version: "humbert-rag-v10-structures", Splitter: baseCfg, ParentChild: req.ParentChild,
		MaxInputTokens: s.config.InputBudget.Effective().MaxInputTokens, MaxBatchTokens: s.config.InputBudget.Effective().MaxBatchTokens}

	if req.ParentChild {
		parentCfg, childCfg := baseCfg.ForParentChild(req.ParentChunkSize, req.ChildChunkSize)
		processConfig.ParentSplitter, processConfig.ChildSplitter = &parentCfg, &childCfg

		parentChild, diagnostics, err := layout.SplitParentChild(ctx, parentCfg, childCfg, s.config.Ingest.MaxChunks)
		diag, splitErr = diagnostics, err

		if err == nil {
			batch = buildParentChildBatch(req.CollectionID, sourceDoc, markdown, parentChild)
		}
	} else {
		chunks, diagnostics, err := layout.Split(ctx, baseCfg, s.config.Ingest.MaxChunks)
		diag, splitErr = diagnostics, err

		if err == nil {
			batch = buildFlatBatch(req.CollectionID, sourceDoc, markdown, chunks)
		}
	}

	if errors.Is(splitErr, chunker.ErrChunkLimit) {
		return nil, fmt.Errorf("%w: more than %d chunks", ErrIngestLimit, s.config.Ingest.MaxChunks)
	}
	if splitErr != nil {
		return nil, splitErr
	}
	return &preparedIngestion{batch: batch, layout: layout, diagnostics: diag, process: processConfig}, nil
}
