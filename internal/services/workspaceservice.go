package services

import (
	"context"
	"fmt"
	"github.com/sda1-hacker/humbert-agent/internal/workspaceview"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
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
	core *coreapp.Application
}

func NewWorkspaceService(core *coreapp.Application) *WorkspaceService {
	return &WorkspaceService{core: core}
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
