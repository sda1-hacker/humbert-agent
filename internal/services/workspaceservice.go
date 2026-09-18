package services

import (
	"context"
	"fmt"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
	"github.com/sda1-hacker/humbert-agent/internal/workspaceview"
)

const workspaceServiceTimeout = 15 * time.Second

// WorkspaceOverviewDTO 是工作区页面顶部总览的 Desktop DTO。
//
// 后端领域模型继续使用 time.Time / workspace.Mode；Wails 边界把它们转换成稳定字符串，
// 避免 Vue 需要理解 Go 时间类型或内部枚举实现。
type WorkspaceOverviewDTO struct {
	AgentID        string                   `json:"agentID"`
	AgentName      string                   `json:"agentName"`
	Mode           string                   `json:"mode"`
	RootDir        string                   `json:"rootDir"`
	FileCount      int                      `json:"fileCount"`
	DirectoryCount int                      `json:"directoryCount"`
	TotalBytes     int64                    `json:"totalBytes"`
	StatsTruncated bool                     `json:"statsTruncated"`
	RecentFiles    []WorkspaceRecentFileDTO `json:"recentFiles"`
	ScannedAt      string                   `json:"scannedAt"`
}

// WorkspaceRecentFileDTO 表示文件系统真实“最近修改”项，不等同于 Agent 可审计产物。
type WorkspaceRecentFileDTO struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
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

// WorkspaceArtifactDTO 是右侧“产物”列表的一项。
//
// SessionID/EntryID/ToolCallID 让产物来源可以追溯，而不是只展示一个无来源文件名。
type WorkspaceArtifactDTO struct {
	Path         string `json:"path"`
	Operation    string `json:"operation"`
	Available    bool   `json:"available"`
	Size         int64  `json:"size"`
	ModifiedAt   string `json:"modifiedAt,omitempty"`
	SessionID    string `json:"sessionID"`
	SessionTitle string `json:"sessionTitle"`
	EntryID      string `json:"entryID"`
	ToolCallID   string `json:"toolCallID"`
	ToolName     string `json:"toolName"`
	OccurredAt   string `json:"occurredAt"`
}

// WorkspaceService 是 Wails Desktop 的工作区只读适配器。
//
// 所有查询都要求 AgentID，并由 Core 的 WorkspaceView Service 重新解析当前 Agent Workspace。
// 前端不能提交绝对 Workspace Root 来读取任意目录。
type WorkspaceService struct {
	core *coreapp.Application
}

func NewWorkspaceService(core *coreapp.Application) *WorkspaceService {
	return &WorkspaceService{core: core}
}

func (s *WorkspaceService) ServiceName() string { return "WorkspaceService" }

// Overview 返回当前 Agent Workspace 总览。
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

// Artifacts 返回最近可证明来源的 Agent 文件产物/文件操作。
func (s *WorkspaceService) Artifacts(agentID string, limit int) ([]WorkspaceArtifactDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), workspaceServiceTimeout)
	defer cancel()
	values, err := s.core.WorkspaceView().Artifacts(ctx, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("读取工作区产物失败: %w", err)
	}
	result := make([]WorkspaceArtifactDTO, 0, len(values))
	for _, value := range values {
		result = append(result, workspaceArtifactDTO(value))
	}
	return result, nil
}

func workspaceOverviewDTO(value workspaceview.Overview) WorkspaceOverviewDTO {
	recent := make([]WorkspaceRecentFileDTO, 0, len(value.RecentFiles))
	for _, item := range value.RecentFiles {
		recent = append(recent, WorkspaceRecentFileDTO{
			Path:       item.Path,
			Size:       item.Size,
			ModifiedAt: formatRequiredTime(item.ModifiedAt),
		})
	}
	return WorkspaceOverviewDTO{
		AgentID:        value.AgentID,
		AgentName:      value.AgentName,
		Mode:           string(value.Mode),
		RootDir:        value.RootDir,
		FileCount:      value.FileCount,
		DirectoryCount: value.DirectoryCount,
		TotalBytes:     value.TotalBytes,
		StatsTruncated: value.StatsTruncated,
		RecentFiles:    recent,
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

func workspaceArtifactDTO(value workspaceview.Artifact) WorkspaceArtifactDTO {
	modifiedAt := ""
	if !value.ModifiedAt.IsZero() {
		modifiedAt = formatRequiredTime(value.ModifiedAt)
	}
	return WorkspaceArtifactDTO{
		Path:         value.Path,
		Operation:    value.Operation,
		Available:    value.Available,
		Size:         value.Size,
		ModifiedAt:   modifiedAt,
		SessionID:    value.SessionID,
		SessionTitle: value.SessionTitle,
		EntryID:      value.EntryID,
		ToolCallID:   value.ToolCallID,
		ToolName:     value.ToolName,
		OccurredAt:   formatRequiredTime(value.OccurredAt),
	}
}

func formatRequiredTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
