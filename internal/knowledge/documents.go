package knowledge

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

type Document struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Size      int64     `json:"size"`
	Revision  int64     `json:"revision"`
	Status    string    `json:"status"`
	Stage     string    `json:"stage"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Retryable bool      `json:"retryable"`
}

type DocumentPage struct {
	Documents []Document `json:"documents"`
	HasMore   bool       `json:"has_more"`
}

func (m *Manager) collection(id string) (Collection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.IndexFunc(m.state.Collections, func(c Collection) bool { return c.ID == id })
	if i < 0 {
		return Collection{}, errors.New("知识库不存在")
	}
	return clone(m.state.Collections[i]), nil
}

func (m *Manager) Documents(ctx context.Context, collection string, offset int) (DocumentPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := m.collection(collection); err != nil {
		return DocumentPage{}, err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return DocumentPage{}, err
	}
	defer release()
	// 只按截止时间恢复遗留状态，不抢占仍在运行的任务。
	if _, err := e.RecoverDocuments(ctx, collection); err != nil {
		return DocumentPage{}, err
	}
	rows, err := e.store.ListDocuments(ctx, collection, 51, offset)
	if err != nil {
		return DocumentPage{}, err
	}
	page := DocumentPage{Documents: []Document{}, HasMore: len(rows) > 50}
	for _, r := range rows[:min(len(rows), 50)] {
		page.Documents = append(page.Documents, Document{r.ID, r.Title, r.Name, r.SourceFile.Type, r.Size, r.Revision, r.ParseStatus, r.ProcessingStage, logging.SafeErrorText(errors.New(r.ErrorMessage), 1200), r.UpdatedAt, rag.CanRetryDocument(r)})
	}
	return page, nil
}

func (m *Manager) Delete(ctx context.Context, collection, documentID string) error {
	if _, err := m.collection(collection); err != nil {
		return err
	}
	if err := m.Cancel(ctx, collection, documentID); err != nil {
		return err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return err
	}
	defer release()
	if err := e.DeleteDocument(ctx, collection, documentID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := clone(m.state)
	next.Jobs = slices.DeleteFunc(next.Jobs, func(j jobRecord) bool { return j.CollectionID == collection && j.DocumentID == documentID })
	if err := m.persist(ctx, next); err != nil {
		return err
	}
	m.state = next
	return nil
}

// PreviewDocument 展示已发布的分块摘要，正文按页读取到 UI，不返回整篇转换文本。
func (m *Manager) PreviewDocument(ctx context.Context, collection, documentID string, offset int) ([]Source, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if offset < 0 {
		return nil, errors.New("无效的分块页码")
	}
	c, err := m.collection(collection)
	if err != nil {
		return nil, err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return nil, err
	}
	defer release()
	r, err := e.GetDocument(ctx, collection, documentID)
	if err != nil {
		return nil, err
	}
	chunks, err := e.store.PageChunks(ctx, collection, documentID, offset)
	if err != nil {
		return nil, err
	}
	result := []Source{}
	for _, chunk := range chunks {
		hit := retrieval.SearchResult{CollectionID: collection, DocumentID: documentID, DocumentRevision: r.Revision, ChunkID: chunk.ID, Content: chunk.Content, ContextHeader: chunk.ContextHeader, SourceLocators: retrieval.Locators(chunk.Metadata)}
		source, err := e.source(ctx, c, hit, 1800)
		if err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, nil
}

func (m *Manager) DocumentName(ctx context.Context, collection, documentID string) (string, error) {
	if _, err := m.collection(collection); err != nil {
		return "", err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return "", err
	}
	defer release()
	r, err := e.GetDocument(ctx, collection, documentID)
	return filepath.Base(r.Name), err
}

// DownloadOriginal 的目标路径只来自桌面保存对话框；工具不暴露文件写入能力。
func (m *Manager) DownloadOriginal(ctx context.Context, collection, documentID, destination string) error {
	if _, err := m.collection(collection); err != nil {
		return err
	}
	e, ctx, release, err := m.acquire(ctx, 0, false)
	if err != nil {
		return err
	}
	defer release()
	r, err := e.GetDocument(ctx, collection, documentID)
	if err != nil {
		return err
	}
	if _, err := e.sources.Resolve(ctx, r.SourceFile); err != nil {
		return err
	}
	input, err := e.sources.Open(r.Path)
	if err != nil {
		return err
	}
	defer input.Close()
	// 使用临时文件完成复制再替换，失败不留下半份下载。
	output, err := os.CreateTemp(filepath.Dir(destination), ".humbert-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if _, err = io.Copy(output, input); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return err
	}
	if err = output.Close(); err != nil {
		return err
	}
	return atomicfile.Replace(output.Name(), destination)
}
