package workspaceview

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	// maxOverviewFiles 是工作区总览为了统计文件数量和最近修改文件，最多遍历的文件数。
	// 文件树本身仍然可以继续按目录展开；这个上限只保护总览扫描，避免大仓库首屏卡住。
	maxOverviewFiles = 5000

	// recentFileLimit 控制工作区首页“最近修改”列表长度。
	recentFileLimit = 12
)

// Service 是“工作区与产物”领域的只读查询服务。
//
// 它不负责写文件，也不拥有新的 Workspace 生命周期。当前 Agent 的 Workspace 仍然由
// agents.Service + workspace.Manager 解析；完整会话历史仍然由 sessions.Service 保存。
// 本服务只是把这些已经存在的事实投影成桌面 UI 可以安全消费的查询模型。
type Service struct {
	agents     *agents.Service
	sessions   *sessions.Service
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
	RecentFiles    []RecentFile
	ScannedAt      time.Time
}

// RecentFile 是工作区最近修改文件列表的一项。
//
// 它依据文件系统真实 mtime 排序，不声称“这个文件一定由 Agent 修改”。
// Agent 可验证的文件产物来源由 Artifact 单独表示。
type RecentFile struct {
	Path       string
	Size       int64
	ModifiedAt time.Time
}

// Artifact 是 Humbert 可以从成功工具事务中证明来源的文件产物/文件操作。
//
// 与 RecentFile 不同，Artifact 必须带 Session/Tool 来源。run_command 之类无法可靠知道
// 具体改了哪个文件的工具不会被强行归因，避免 UI 给用户造成错误的审计结论。
type Artifact struct {
	Path         string
	Operation    string
	Available    bool
	Size         int64
	ModifiedAt   time.Time
	SessionID    string
	SessionTitle string
	EntryID      string
	ToolCallID   string
	ToolName     string
	OccurredAt   time.Time
}

// NewService 创建工作区只读查询服务。
func NewService(
	agentService *agents.Service,
	sessionService *sessions.Service,
	workspaceManager *workspace.Manager,
) (*Service, error) {
	if agentService == nil {
		return nil, errors.New("WorkspaceView AgentService 不能为空")
	}
	if sessionService == nil {
		return nil, errors.New("WorkspaceView SessionService 不能为空")
	}
	if workspaceManager == nil {
		return nil, errors.New("WorkspaceView WorkspaceManager 不能为空")
	}
	return &Service{
		agents:     agentService,
		sessions:   sessionService,
		workspaces: workspaceManager,
	}, nil
}

// Overview 返回当前 Agent Workspace 的总览。
//
// 扫描只读取文件元数据，不读取文件正文。常见依赖/构建目录不会进入总览统计，
// 但用户仍然可以在文件树中手工展开这些目录。
func (s *Service) Overview(ctx context.Context, agentID string) (Overview, error) {
	resolved, info, err := s.resolve(ctx, agentID)
	if err != nil {
		return Overview{}, err
	}

	result := Overview{
		AgentID:     info.Agent.ID,
		AgentName:   info.Agent.Name,
		Mode:        resolved.Mode,
		RootDir:     resolved.RootDir,
		RecentFiles: []RecentFile{},
		ScannedAt:   time.Now().UTC(),
	}

	recent := make([]RecentFile, 0, recentFileLimit*2)
	visitedFiles := 0
	err = filepath.WalkDir(resolved.RootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// 工作区可能同时被外部程序修改。单个文件瞬间消失时不应该让整个页面失败。
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == resolved.RootDir {
			return nil
		}
		relative, relErr := filepath.Rel(resolved.RootDir, path)
		if relErr != nil {
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
			// 达到保护上限后立即结束整棵树遍历，而不是只停止计数却继续访问后面的目录。
			return fs.SkipAll
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		visitedFiles++
		result.FileCount++
		result.TotalBytes += info.Size()
		recent = append(recent, RecentFile{
			Path:       filepath.ToSlash(relative),
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC(),
		})
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return Overview{}, fmt.Errorf("扫描 Workspace 总览失败: %w", err)
	}

	sort.SliceStable(recent, func(i, j int) bool {
		if recent[i].ModifiedAt.Equal(recent[j].ModifiedAt) {
			return recent[i].Path < recent[j].Path
		}
		return recent[i].ModifiedAt.After(recent[j].ModifiedAt)
	})
	if len(recent) > recentFileLimit {
		recent = recent[:recentFileLimit]
	}
	result.RecentFiles = recent
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

// Artifacts 返回当前 Workspace 可以从 Session 成功工具事务中证明来源的最近文件产物。
func (s *Service) Artifacts(ctx context.Context, agentID string, limit int) ([]Artifact, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	resolved, _, err := s.resolve(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.scanArtifacts(ctx, resolved, limit)
}

// resolve 每次查询都重新读取 Agent Profile 并解析 Workspace。
//
// 这和 Runtime“每个 User Turn 冻结一次 Workspace Snapshot”的原则并不冲突：
// UI 是低频控制面查询，用户修改 Agent Workspace 后应该立即看到新目录；正在运行的旧 Turn
// 仍然继续使用它自己已经冻结的 Snapshot。
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
