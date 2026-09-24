package services

import (
	"context"
	"fmt"
	"github.com/sda1-hacker/humbert-agent/internal/workspaceview"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/searchindex"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const workspaceServiceTimeout = 15 * time.Second

// WorkspaceOverviewDTO 是工作区页面顶部总览的 Desktop DTO。
//
// 后端领域模型继续使用 time.Time / workspace.Mode；Wails 边界把它们转换成稳定字符串，
// 避免 Vue 需要理解 Go 时间类型或内部枚举实现。
type WorkspaceOverviewDTO struct {
	AgentID        string `json:"agentID"`
	AgentName      string `json:"agentName"`
	Mode           string `json:"mode"`
	RootDir        string `json:"rootDir"`
	FileCount      int    `json:"fileCount"`
	DirectoryCount int    `json:"directoryCount"`
	TotalBytes     int64  `json:"totalBytes"`
	StatsTruncated bool   `json:"statsTruncated"`
	ScannedAt      string `json:"scannedAt"`
}

// WorkspaceEntryDTO 是文件树中一个直接子项。
type WorkspaceEntryDTO struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	Hidden     bool   `json:"hidden"`
}

// WorkspaceDirectoryDTO 是文件树一次懒加载请求的结果。
type WorkspaceDirectoryDTO struct {
	Path      string              `json:"path"`
	Entries   []WorkspaceEntryDTO `json:"entries"`
	Truncated bool                `json:"truncated"`
}

// WorkspacePreviewDTO 是中间预览区使用的受限内容。
//
// 只有白名单图片会包含 DataURL；普通文本只包含受限 Content；其它二进制只返回元数据。
type WorkspacePreviewDTO struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	MIMEType   string `json:"mimeType"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
	Content    string `json:"content,omitempty"`
	DataURL    string `json:"dataURL,omitempty"`
	Truncated  bool   `json:"truncated"`
}

// WorkspaceService 是 Wails Desktop 的工作区只读适配器。
//
// 工作区页面只负责浏览当前文件系统。文件生成/修改/删除结果直接从对应聊天 Turn 的
// Tool Trace 展示，不再通过这个服务扫描 Session 历史。
//
// 所有查询都要求 AgentID，并由 Core 的 WorkspaceView Service 重新解析该 Agent 当前
// Workspace；前端不能提交绝对 Workspace Root 来读取任意目录。
type WorkspaceService struct {
	core       *coreapp.Application
	docMu      sync.Mutex
	docStateMu sync.Mutex
	docIndex   *searchindex.Index
	docCtx     context.Context
	docCancel  context.CancelFunc
	docWorkers sync.WaitGroup
	docReaders sync.WaitGroup
	docStates  map[string]documentRefreshState
}

type documentRefreshState struct {
	updating  bool
	checked   time.Time
	lastError string
}

type DocumentSearchDTO struct {
	Results  []searchindex.DocumentResult `json:"results"`
	Updating bool                         `json:"updating"`
	Error    string                       `json:"error,omitempty"`
}

func NewWorkspaceService(core *coreapp.Application) *WorkspaceService {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkspaceService{core: core, docCtx: ctx, docCancel: cancel, docStates: make(map[string]documentRefreshState)}
}

func (s *WorkspaceService) ServiceShutdown() error {
	s.docStateMu.Lock()
	s.docCancel()
	s.docStateMu.Unlock()
	s.docWorkers.Wait()
	s.docReaders.Wait()
	s.docStateMu.Lock()
	defer s.docStateMu.Unlock()
	if s.docIndex != nil {
		index := s.docIndex
		s.docIndex = nil
		return index.Close()
	}
	return nil
}

func (s *WorkspaceService) ServiceName() string { return "WorkspaceService" }

// Overview 返回指定 Agent Workspace 的总览。
func (s *WorkspaceService) Overview(agentID string) (WorkspaceOverviewDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), workspaceServiceTimeout)
	defer cancel()
	value, err := s.core.WorkspaceView().Overview(ctx, agentID)
	if err != nil {
		return WorkspaceOverviewDTO{}, fmt.Errorf("读取工作区总览失败: %w", err)
	}
	return workspaceOverviewDTO(value), nil
}

// ListDirectory 懒加载工作区中的一个目录。
func (s *WorkspaceService) ListDirectory(agentID, path string) (WorkspaceDirectoryDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), workspaceServiceTimeout)
	defer cancel()
	value, err := s.core.WorkspaceView().ListDirectory(ctx, agentID, path)
	if err != nil {
		return WorkspaceDirectoryDTO{}, fmt.Errorf("读取工作区目录失败: %w", err)
	}
	return workspaceDirectoryDTO(value), nil
}

// PreviewFile 返回安全受限的文件预览。
func (s *WorkspaceService) PreviewFile(agentID, path string) (WorkspacePreviewDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), workspaceServiceTimeout)
	defer cancel()
	value, err := s.core.WorkspaceView().PreviewFile(ctx, agentID, path)
	if err != nil {
		return WorkspacePreviewDTO{}, fmt.Errorf("预览工作区文件失败: %w", err)
	}
	return workspacePreviewDTO(value), nil
}

// SearchDocuments 立即检索已提交索引，后台同步当前工作区文件。工作区位置每次重新解析。
func (s *WorkspaceService) SearchDocuments(agentID, query string) (DocumentSearchDTO, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return DocumentSearchDTO{Results: []searchindex.DocumentResult{}}, nil
	}
	if len([]rune(query)) > 200 {
		return DocumentSearchDTO{}, fmt.Errorf("搜索词不能超过 200 字")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := s.core.Agents().Get(ctx, agentID)
	if err != nil {
		return DocumentSearchDTO{}, err
	}
	resolved, err := s.core.Workspaces().Resolve(ctx, info.Agent.ID, info.Agent.WorkspaceMode, info.Agent.WorkspacePath)
	if err != nil {
		return DocumentSearchDTO{}, err
	}
	s.docStateMu.Lock()
	if err := s.docCtx.Err(); err != nil {
		s.docStateMu.Unlock()
		return DocumentSearchDTO{}, err
	}
	if s.docIndex == nil {
		s.docIndex, err = searchindex.Open(filepath.Join(s.core.Config().Paths.CacheDir, "document-search.sqlite"))
		if err != nil {
			s.docStateMu.Unlock()
			return DocumentSearchDTO{}, err
		}
	}
	index := s.docIndex
	key := agentID + "\x00" + resolved.RootDir
	state := s.docStates[key]
	if s.docCtx.Err() == nil && !state.updating && time.Since(state.checked) > 10*time.Second {
		state.updating = true
		state.lastError = ""
		s.docStates[key] = state
		s.docWorkers.Add(1)
		go s.refreshDocumentIndex(index, agentID, resolved, key)
	}
	s.docReaders.Add(1)
	s.docStateMu.Unlock()
	defer s.docReaders.Done()
	results, err := index.SearchDocuments(ctx, agentID, resolved.RootDir, query, 50)
	if err != nil {
		return DocumentSearchDTO{}, err
	}
	return DocumentSearchDTO{Results: results, Updating: state.updating, Error: state.lastError}, nil
}

func (s *WorkspaceService) refreshDocumentIndex(index *searchindex.Index, agentID string, resolved workspace.Workspace, key string) {
	defer s.docWorkers.Done()
	ctx, cancel := context.WithTimeout(s.docCtx, 2*time.Minute)
	defer cancel()
	s.docMu.Lock()
	err := s.updateDocumentIndex(ctx, index, agentID, resolved)
	s.docMu.Unlock()
	s.docStateMu.Lock()
	state := s.docStates[key]
	state.updating, state.checked, state.lastError = false, time.Now(), ""
	if err != nil && s.docCtx.Err() == nil {
		state.lastError = err.Error()
	}
	s.docStates[key] = state
	s.docStateMu.Unlock()
}

func (s *WorkspaceService) updateDocumentIndex(ctx context.Context, index *searchindex.Index, agentID string, resolved workspace.Workspace) error {
	root, err := s.core.Workspaces().OpenRoot(ctx, resolved)
	if err != nil {
		return err
	}
	defer root.Close()
	seen := make(map[string]bool)
	files, docs := 0, 0
	truncated := false
	err = filepath.WalkDir(resolved.RootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == resolved.RootDir {
			return nil
		}
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".idea", ".vscode", "node_modules", "vendor", "dist", "build", ".next", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		files++
		if files > 5000 || docs >= 1000 {
			truncated = true
			return fs.SkipAll
		}
		if documenttext.MIMEForName(entry.Name()) == "" {
			return nil
		}
		docs++
		rel, err := filepath.Rel(resolved.RootDir, path)
		if err != nil {
			return nil
		}
		seen[rel] = true
		stat, err := entry.Info()
		if err != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > 12<<20 {
			return nil
		}
		current, err := index.DocumentCurrent(ctx, agentID, resolved.RootDir, rel, stat.Size(), stat.ModTime().UnixNano())
		if err != nil || current {
			return err
		}
		file, err := root.Open(rel)
		if err != nil {
			return nil
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (12<<20)+1))
		file.Close()
		if readErr != nil || len(data) > 12<<20 {
			return nil
		}
		plain, _, extractErr := documenttext.Extract(ctx, entry.Name(), documenttext.MIMEForName(entry.Name()), data)
		if extractErr != nil {
			// Unsupported or scanned documents have no searchable text until changed.
			plain = ""
		}
		return index.ReplaceDocument(ctx, agentID, resolved.RootDir, rel, stat.Size(), stat.ModTime().UnixNano(), plain)
	})
	if err != nil && err != fs.SkipAll {
		return err
	}
	if !truncated {
		if err := index.PruneDocuments(ctx, agentID, resolved.RootDir, seen); err != nil {
			return err
		}
	}
	return nil
}

func workspaceOverviewDTO(value workspaceview.Overview) WorkspaceOverviewDTO {
	return WorkspaceOverviewDTO{
		AgentID:        value.AgentID,
		AgentName:      value.AgentName,
		Mode:           string(value.Mode),
		RootDir:        value.RootDir,
		FileCount:      value.FileCount,
		DirectoryCount: value.DirectoryCount,
		TotalBytes:     value.TotalBytes,
		StatsTruncated: value.StatsTruncated,
		ScannedAt:      formatRequiredTime(value.ScannedAt),
	}
}

func workspaceDirectoryDTO(value workspace.DirectoryListing) WorkspaceDirectoryDTO {
	entries := make([]WorkspaceEntryDTO, 0, len(value.Entries))
	for _, entry := range value.Entries {
		modifiedAt := ""
		if !entry.ModifiedAt.IsZero() {
			modifiedAt = formatRequiredTime(entry.ModifiedAt)
		}
		entries = append(entries, WorkspaceEntryDTO{
			Name:       entry.Name,
			Path:       entry.Path,
			Type:       entry.Type,
			Size:       entry.Size,
			ModifiedAt: modifiedAt,
			Hidden:     entry.Hidden,
		})
	}
	return WorkspaceDirectoryDTO{Path: value.Path, Entries: entries, Truncated: value.Truncated}
}

func workspacePreviewDTO(value workspace.FilePreview) WorkspacePreviewDTO {
	return WorkspacePreviewDTO{
		Path:       value.Path,
		Name:       value.Name,
		Kind:       value.Kind,
		MIMEType:   value.MIMEType,
		Size:       value.Size,
		ModifiedAt: formatRequiredTime(value.ModifiedAt),
		Content:    value.Content,
		DataURL:    value.DataURL,
		Truncated:  value.Truncated,
	}
}

func formatRequiredTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
