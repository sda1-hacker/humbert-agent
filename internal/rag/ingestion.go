package rag

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// Ingest 完成解析、切分与索引。业务调用不能覆盖索引范围或 Embedding 模型。
func (s *Service) Ingest(ctx context.Context, req IngestRequest) (IngestResponse, error) {
	return s.ingest(ctx, req, false)
}

// RetryDocument 用托管原件重新执行失败/取消的导入，沿用稳定 ID 并预留新版本。
// req 只提供范围与切分参数，忽略 Source；不会自动续传中间块或反复调用模型。
func (s *Service) RetryDocument(ctx context.Context, req IngestRequest) (IngestResponse, error) {
	return s.ingest(ctx, req, true)
}

func (s *Service) ingest(ctx context.Context, req IngestRequest, retry bool) (response IngestResponse, err error) {
	if s.loader == nil {
		return IngestResponse{}, ErrMissingLoader
	}

	if s.indexer == nil {
		return IngestResponse{}, ErrMissingIndexer
	}

	req.CollectionID = strings.TrimSpace(req.CollectionID)
	if req.CollectionID == "" {
		return IngestResponse{}, ErrMissingCollectionID
	}

	if err := ctx.Err(); err != nil {
		return IngestResponse{}, err
	}
	if err := req.Splitter.Validate(); err != nil {
		return IngestResponse{}, err
	}
	if req.ParentChunkSize < 0 || req.ChildChunkSize < 0 {
		return IngestResponse{}, errors.New("rag: negative parent/child size")
	}
	req.DocumentID = strings.TrimSpace(req.DocumentID)
	ctx, finish, err := s.beginIngest(ctx, req)
	if err != nil {
		return IngestResponse{}, err
	}
	defer finish()
	source, saved, err := s.resolveIngestSource(ctx, req, retry)
	if err != nil {
		return IngestResponse{}, err
	}
	req.Source = source
	var attempt int64
	if s.lifecycle != nil {
		if req.DocumentID == "" {
			return IngestResponse{}, errors.New("rag: stable DocumentID is required for versioned ingestion")
		}
		var err error
		attempt, err = s.lifecycle.ReserveDocument(ctx, req.CollectionID, req.DocumentID)
		if err != nil {
			return IngestResponse{}, err
		}
		if attempt <= 0 {
			return IngestResponse{}, errors.New("rag: document lifecycle returned invalid version")
		}
	}
	file := SourceFile{URI: source.URI, Name: filepath.Base(source.URI), Type: strings.TrimPrefix(strings.ToLower(filepath.Ext(source.URI)), ".")}
	if saved != nil {
		file = *saved
	}
	stage := StageSource
	defer func() {
		if recover() != nil {
			// 在业务任务边界收尾，不向持久化或前端泄露 panic 参数、正文或调用栈。
			err = errors.New("rag: 导入组件发生内部异常")
		}
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), context.Cause(ctx), err)
		}
		var staged *IngestionError
		if errors.As(err, &staged) {
			stage = staged.Stage
		} else {
			err = &IngestionError{Stage: stage, Err: err}
		}
		if s.documents == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), ragconfig.CleanupTimeout)
		defer cancel()
		status, code := DocumentFailed, "processing_failed"
		if errors.Is(err, context.Canceled) {
			status, code = DocumentCancelled, "cancelled"
			if errors.Is(err, ErrServiceClosed) {
				code = "service_closed"
			}
		} else if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		// 状态清理独立于已取消请求；数据库再次校验版本，防止迟到错误覆盖新任务。
		failure := DocumentFailure{Status: status, Stage: stage, Code: code, Message: logging.SafeErrorText(err, 2048)}
		if stateErr := s.documents.FailDocument(cleanup, req.CollectionID, req.DocumentID, attempt, failure); stateErr != nil {
			err = errors.Join(err, fmt.Errorf("保存导入状态失败: %w", stateErr))
		}
	}()
	if s.documents != nil {
		if err := s.documents.StartDocument(ctx, req.CollectionID, req.DocumentID, attempt, file); err != nil {
			return IngestResponse{}, err
		}
	}
	advance := func(next string) error {
		stage = next
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.documents != nil {
			return s.documents.AdvanceDocument(ctx, req.CollectionID, req.DocumentID, attempt, next)
		}
		return nil
	}
	if s.sources != nil && saved == nil {
		source, file, err = s.sources.Save(ctx, source)
		if err != nil {
			return IngestResponse{}, err
		}
		if err := s.documents.StartDocument(ctx, req.CollectionID, req.DocumentID, attempt, file); err != nil {
			return IngestResponse{}, err
		}
	}

	if err := advance(StageParse); err != nil {
		return IngestResponse{}, err
	}
	sourceDocs, err := s.loader.Load(ctx, source)
	if err != nil {
		return IngestResponse{}, fmt.Errorf("load source document: %w", err)
	}
	if req.DocumentID != "" && len(sourceDocs) > 1 {
		return IngestResponse{}, errors.New("rag: one stable DocumentID cannot identify multiple source documents")
	}

	if err := advance(StageChunk); err != nil {
		return IngestResponse{}, err
	}

	response = IngestResponse{Documents: make([]IngestDocumentResult, 0, len(sourceDocs))}

	for index, sourceDoc := range sourceDocs {
		item, err := s.ingestDocument(ctx, req, sourceDoc, file, source.URI, attempt, index, advance)
		if err != nil {
			return IngestResponse{}, err
		}
		if item == nil {
			continue
		}
		response.Documents = append(response.Documents, *item)
		response.ParentCount += item.ParentCount
		response.ChildCount += item.ChildCount
	}

	if len(response.Documents) == 0 {
		return IngestResponse{}, &IngestionError{Stage: StageParse, Err: ErrEmptyDocument}
	}
	return response, nil
}
