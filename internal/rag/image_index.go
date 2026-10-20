package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// enrichImages 在正文切分后处理图片。识别文本形成独立块，不混进普通正文或覆盖原文件。
// 发布仍使用原子版本事务：图片任务全部收尾后才发布；取消/索引失败保留上一版内容。
func (s *Service) enrichImages(ctx context.Context, batch *IngestionBatch, req IngestRequest, attempt int64) error {
	assets, err := imageAssets(batch.Metadata)
	if err != nil {
		return err
	}
	if s.images == nil {
		return errors.New("rag: 文档包含图片，请在 Dependencies.Images 配置图片处理器")
	}
	if err := prepareImageOwners(ctx, batch, assets); err != nil {
		return err
	}
	progress := func(ctx context.Context, states []ImageAsset) error {
		if s.imageStates == nil || attempt == 0 {
			return ctx.Err()
		}
		return s.imageStates.TrackImages(ctx, batch.CollectionID, batch.DocumentID, attempt, states)
	}
	results, err := s.images.ProcessImages(ctx, assets, progress)
	if err != nil {
		return err
	}
	if len(results) != len(assets) {
		return errors.New("rag: 图片处理结果数量不完整")
	}
	for i, result := range results {
		assets[i] = result.Asset
		for _, action := range []struct{ kind, text string }{{ChunkTypeImageCaption, result.Caption}, {ChunkTypeImageOCR, result.OCR}} {
			if strings.TrimSpace(action.text) == "" {
				continue
			}
			if err := s.appendImageText(ctx, batch, result.Asset, action.kind, action.text, req); err != nil {
				return err
			}
		}
	}
	batch.Metadata[MetaImageAssets] = assets
	// 普通正文块也携带自己引用的图片；检索到正文时仍能追溯图片和识别状态。
	for i := range batch.Children {
		child := &batch.Children[i]
		if child.ChunkType != ChunkTypeText {
			continue
		}
		var related []ImageAsset
		for _, asset := range assets {
			if imageBelongsToChunk(asset, *child) {
				related = append(related, asset)
			}
		}
		if len(related) > 0 {
			child.Metadata[MetaImageInfo] = related
		}
	}
	if len(batch.Children) == 0 {
		return errors.New("rag: 文档没有可检索文字；图片原件和处理状态已保留，请配置视觉识别后重试")
	}
	return ctx.Err()
}

// prepareImageOwners 将纯图片占位块保留为不入索引的父块，避免文件名污染向量/BM25 召回。
// 含正文的图片关联正文子块；识别块随后引用这个身份，无需额外的图片表或自建队列。
func prepareImageOwners(ctx context.Context, batch *IngestionBatch, assets []ImageAsset) error {
	parents := make(map[string]ChunkRecord, len(batch.Parents))
	for _, parent := range batch.Parents {
		parents[parent.ID] = parent
	}
	kept := make([]ChunkRecord, 0, len(batch.Children))
	for _, child := range batch.Children {
		text, err := documenttext.HasIndexableText(ctx, child.Content)
		if err != nil {
			return err
		}
		owner := child
		if !text {
			if parent, ok := parents[child.ParentChunkID]; ok {
				owner = parent
			} else {
				owner.ChunkType = ChunkTypeParentText
				owner.ChunkIndex = len(batch.Parents)
				owner.ID = fmt.Sprintf("%s#parent-%06d", batch.DocumentID, owner.ChunkIndex)
				owner.ParentChunkID = ""
				owner.Metadata = retrieval.CloneMetadata(owner.Metadata)
				owner.Metadata[retrieval.MetaChunkType] = ChunkTypeParentText
				owner.Metadata[retrieval.MetaChunkIndex] = owner.ChunkIndex
				delete(owner.Metadata, retrieval.MetaParentChunkID)
				batch.Parents = append(batch.Parents, owner)
			}
		} else {
			child.ChunkIndex = len(kept)
			child.ID = fmt.Sprintf("%s#chunk-%06d", batch.DocumentID, child.ChunkIndex)
			child.Metadata[retrieval.MetaChunkIndex] = child.ChunkIndex
			owner = child
			kept = append(kept, child)
		}
		for i := range assets {
			if assets[i].OwnerChunkID == "" && imageBelongsToChunk(assets[i], child) {
				assets[i].OwnerChunkID = owner.ID
			}
		}
	}
	batch.Children = kept
	for _, asset := range assets {
		if asset.OwnerChunkID == "" {
			return fmt.Errorf("rag: 图片 %s 缺少正文所属块", asset.Path)
		}
	}
	return nil
}

// 同一图片文件可能在多页出现；只按文件名关联会把后面页的图片误挂到第一页。
func imageBelongsToChunk(asset ImageAsset, child ChunkRecord) bool {
	ref := "images/" + asset.Path
	if !strings.Contains(child.Content, "]("+ref+")") && !strings.Contains(child.Content, `src="`+ref+`"`) && !strings.Contains(child.Content, "src='"+ref+"'") {
		return false
	}
	source := asset.Source
	if source.Page == 0 && source.Slide == 0 && source.Block == 0 && source.Sheet == "" {
		return true
	}
	for _, loc := range retrieval.Locators(child.Metadata) {
		if (source.Page == 0 || (loc.Page <= source.Page && source.Page <= max(loc.Page, loc.PageEnd))) &&
			(source.Slide == 0 || source.Slide == loc.Slide) &&
			(source.Block == 0 || source.Block == loc.Block) &&
			(source.Sheet == "" || source.Sheet == loc.Sheet) {
			return true
		}
	}
	return false
}

// appendImageText 把识别结果附加到仅用于范围校验/内容摘要的临时文本末尾，独立构建 image_* 块。
// Start/End 属于转换后的逻辑文本；定位明确标为 image_ocr/image_caption，绝不伪造 PDF 字形坐标。
func (s *Service) appendImageText(ctx context.Context, batch *IngestionBatch, asset ImageAsset, kind, text string, req IngestRequest) error {
	text = chunker.NormalizeLineEndings(text)
	if len(text) > s.config.Ingest.MaxTextBytes-len(batch.Markdown)-2 {
		return fmt.Errorf("%w: image text exceeds document budget", ErrIngestLimit)
	}
	start := utf8.RuneCountInString(batch.Markdown) + 2
	batch.Markdown += "\n\n" + text
	locator := asset.Source
	locator.Image = "images/" + asset.Path
	locator.Mapping = kind
	locator.Start, locator.End = start, start+utf8.RuneCountInString(text)
	block := documenttext.SourceBlock{Start: locator.Start, End: locator.End, Locator: locator}
	batch.SourceBlocks = append(batch.SourceBlocks, block)
	source := &schema.Document{ID: batch.DocumentID, MetaData: retrieval.CloneMetadata(batch.Metadata)}
	source.MetaData[documenttext.MetaSourceBlocks] = []documenttext.SourceBlock{block}
	layout, err := chunker.NewLayout(ctx, text)
	if err != nil {
		return err
	}
	cfg := req.Splitter.Effective()
	if req.ParentChild {
		_, cfg = cfg.ForParentChild(req.ParentChunkSize, req.ChildChunkSize)
	}
	remaining := s.config.Ingest.MaxChunks - len(batch.Children)
	if remaining <= 0 {
		return fmt.Errorf("%w: more than %d chunks", ErrIngestLimit, s.config.Ingest.MaxChunks)
	}
	chunks, _, err := layout.Split(ctx, cfg, remaining)
	if err != nil {
		if errors.Is(err, chunker.ErrChunkLimit) {
			return fmt.Errorf("%w: image chunks exceed document budget", ErrIngestLimit)
		}
		return err
	}
	makeRecord := func(chunk chunker.Chunk) ChunkRecord {
		localStart, localEnd := chunk.Start, chunk.End
		chunk.Start += start
		chunk.End += start
		chunk.Seq = len(batch.Children)
		record := buildChunkRecord(source, chunk, fmt.Sprintf("%s#chunk-%06d", batch.DocumentID, chunk.Seq), kind, asset.OwnerChunkID)
		loc := locator
		loc.Start, loc.End = chunk.Start, chunk.End
		loc.Quote = layout.Quote(localStart, localEnd)
		record.Metadata[documenttext.MetaSourceLocators] = []documenttext.SourceLocator{loc}
		record.Metadata[MetaImageInfo] = []ImageAsset{asset}
		return record
	}
	budget := s.config.InputBudget.Effective()
	limit := min(budget.MaxInputTokens, budget.MaxBatchTokens)
	for _, chunk := range chunks {
		err := layout.Fit(ctx, chunk, func(c chunker.Chunk) (bool, error) {
			count := budget.CountTokens(indexDocument(*batch, makeRecord(c), s.config.InputBuilder).Content)
			if count < 0 {
				return false, errors.New("rag: tokenizer returned a negative count")
			}
			return count <= limit, nil
		}, func(c chunker.Chunk) error {
			if len(batch.Children) >= s.config.Ingest.MaxChunks {
				return ErrIngestLimit
			}
			batch.Children = append(batch.Children, makeRecord(c))
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func imageWarnings(images []ImageAsset) []string {
	var warnings []string
	for i, img := range images {
		switch img.Status {
		case "failed", "partial_failure":
			warnings = append(warnings, fmt.Sprintf("图片 %d（页码 %d）处理状态 %s；已保留成功结果，可重试失败动作。", i+1, img.Page, img.Status))
		case "not_requested":
			warnings = append(warnings, fmt.Sprintf("图片 %d 已保存，未开启图片识别。", i+1))
		}
	}
	return warnings
}

func hasFailedImages(metadata map[string]any) bool {
	images, err := imageAssets(metadata)
	if err != nil {
		return false
	}
	for _, img := range images {
		if img.Status == "failed" || img.Status == "partial_failure" || img.Status == "cancelled" {
			return true
		}
	}
	return false
}

// ReadDocumentImage 先检查图片属于当前文档，再委托资产存储读取；调用者不能传入任意本地路径。
func (s *Service) ReadDocumentImage(ctx context.Context, collectionID, documentID, path string) ([]byte, string, error) {
	ctx, done, err := s.beginRequest(ctx, 0)
	if err != nil {
		return nil, "", err
	}
	defer done()
	if s.images == nil || s.documents == nil {
		return nil, "", errors.New("rag: image reader/document tracking is not configured")
	}
	collectionID, documentID, err = documentScope(ctx, collectionID, documentID)
	if err != nil {
		return nil, "", err
	}
	record, err := s.documents.GetDocument(ctx, collectionID, documentID)
	if err != nil {
		return nil, "", err
	}
	assets, err := imageAssets(record.Metadata)
	if pending := record.Metadata["rag_pending_images"]; pending != nil {
		pendingAssets, parseErr := imageAssets(map[string]any{MetaImageAssets: pending})
		if parseErr != nil {
			return nil, "", parseErr
		}
		assets = append(assets, pendingAssets...)
	}
	if err != nil {
		return nil, "", err
	}
	for _, asset := range assets {
		if asset.Path == path {
			data, err := s.images.ReadImage(ctx, asset)
			return data, asset.MIME, err
		}
	}
	return nil, "", errors.New("rag: image does not belong to the document")
}

// 图片所属块也必须使用版本化 ID，不能在元数据里留下上一轮或临时身份。
func qualifyImageMetadata(metadata map[string]any, ids map[string]string) {
	for _, key := range []string{MetaImageAssets, MetaImageInfo} {
		if metadata[key] == nil {
			continue
		}
		assets, err := imageAssets(map[string]any{MetaImageAssets: metadata[key]})
		if err != nil {
			continue
		}
		for i := range assets {
			if id, ok := ids[assets[i].OwnerChunkID]; ok {
				assets[i].OwnerChunkID = id
			}
		}
		metadata[key] = assets
	}
}
