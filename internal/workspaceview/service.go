package workspaceview

import (
	"context"
	"errors"
	"fmt"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
)

const (
	// maxOverviewFiles 是工作区总览统计最多遍历的普通文件数。
	//
	// 工作区页面真正的核心能力是“浏览文件”，文件树本身使用按目录懒加载，
	// 不受这个数字限制。这里设置保护上限只是为了避免顶部文件数/目录数/大小统计
	// 在大型仓库中阻塞首屏。
	maxOverviewFiles = 5000
)

// Service 是桌面工作区的只读查询服务。
//
// 这一层只负责三个事实：
//  1. 当前 Agent 实际绑定哪个 Workspace；
//  2. Workspace 里现在有哪些文件；
//  3. 指定文件当前可以如何安全预览。
//
// 它不再扫描 Session 去构造“产物历史”或“最近修改记录”。文件操作历史属于一次
// 对话的执行结果，应该在聊天 Turn 中展示；工作区页面只关心当前文件系统状态。
type Service struct {
	agents     *agents.Service
	workspaces *workspace.Manager
}

// Overview 描述工作区页面顶部需要的轻量总览。
type Overview struct {
	AgentID        string
	AgentName      string
	Mode           workspace.Mode
	RootDir        string
	FileCount      int
	DirectoryCount int
	TotalBytes     int64
	StatsTruncated bool
	ScannedAt      time.Time
}

// NewService 创建工作区只读查询服务。
//
// 注意这里不再依赖 SessionService：工作区页面已经不承担“从 session.jsonl 推断产物”的
// 职责，这也避免每次打开工作区都重新扫描历史会话。
func NewService(
	agentService *agents.Service,
	workspaceManager *workspace.Manager,
) (*Service, error) {
	if agentService == nil {
		return nil, errors.New("WorkspaceView AgentService 不能为空")
	}
	if workspaceManager == nil {
		return nil, errors.New("WorkspaceView WorkspaceManager 不能为空")
	}
	return &Service{
		agents:     agentService,
		workspaces: workspaceManager,
	}, nil
}

// Overview 返回当前 Agent Workspace 的总览。
//
// 扫描只读取文件元数据，不读取文件正文。常见依赖/构建目录不会进入总览统计，
// 但用户仍然可以在左侧文件树中手工展开这些目录。
func (s *Service) Overview(ctx context.Context, agentID string) (Overview, error) {
	resolved, info, err := s.resolve(ctx, agentID)
	if err != nil {
		return Overview{}, err
	}

	result := Overview{
		AgentID:   info.Agent.ID,
		AgentName: info.Agent.Name,
		Mode:      resolved.Mode,
		RootDir:   resolved.RootDir,
		ScannedAt: time.Now().UTC(),
	}

	visitedFiles := 0
	err = filepath.WalkDir(resolved.RootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// 工作区可能同时被 IDE、终端或其它程序修改。单个路径瞬间消失时，
			// 总览统计应该继续，而不是让整个工作区页面失败。
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == resolved.RootDir {
			return nil
		}
		if entry.IsDir() {
			if shouldSkipOverviewDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			result.DirectoryCount++
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if visitedFiles >= maxOverviewFiles {
			result.StatsTruncated = true
			return fs.SkipAll
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		visitedFiles++
		result.FileCount++
		result.TotalBytes += info.Size()
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return Overview{}, fmt.Errorf("扫描 Workspace 总览失败: %w", err)
	}
	return result, nil
}

// ListDirectory 只读取当前 Agent Workspace 中的一层目录。
func (s *Service) ListDirectory(ctx context.Context, agentID, relativePath string) (workspace.DirectoryListing, error) {
	resolved, _, err := s.resolve(ctx, agentID)
	if err != nil {
		return workspace.DirectoryListing{}, err
	}
	return s.workspaces.ListDirectory(ctx, resolved, relativePath, workspace.DefaultBrowseLimit)
}

// PreviewFile 返回当前 Agent Workspace 中一个文件的受限预览。
func (s *Service) PreviewFile(ctx context.Context, agentID, relativePath string) (workspace.FilePreview, error) {
	resolved, _, err := s.resolve(ctx, agentID)
	if err != nil {
		return workspace.FilePreview{}, err
	}
	return s.workspaces.PreviewFile(ctx, resolved, relativePath)
}

// resolve 每次查询都重新读取 Agent Profile 并解析 Workspace。
//
// UI 中的“Agent / 项目切换”只是选择查询哪个 Agent 的 Workspace，不会修改 Agent
// 配置，也不会影响已经运行中的 Turn。正在运行的 Turn 继续使用自己冻结的 Runtime
// Snapshot；工作区页面则立即反映最新配置。
func (s *Service) resolve(ctx context.Context, agentID string) (workspace.Workspace, agents.AgentInfo, error) {
	if ctx == nil {
		return workspace.Workspace{}, agents.AgentInfo{}, errors.New("context.Context 不能为空")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return workspace.Workspace{}, agents.AgentInfo{}, errors.New("Agent ID 不能为空")
	}
	info, err := s.agents.Get(ctx, agentID)
	if err != nil {
		return workspace.Workspace{}, agents.AgentInfo{}, err
	}
	resolved, err := s.workspaces.Resolve(ctx, info.Agent.ID, info.Agent.WorkspaceMode, info.Agent.WorkspacePath)
	if err != nil {
		return workspace.Workspace{}, agents.AgentInfo{}, fmt.Errorf("解析 Agent Workspace 失败: %w", err)
	}
	return resolved, info, nil
}

func shouldSkipOverviewDirectory(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".idea", ".vscode", "node_modules", "vendor", "dist", "build", ".next", ".cache":
		return true
	default:
		return false
	}
}
