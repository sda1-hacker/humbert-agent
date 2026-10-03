package searchindex

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// Service 管理会话与工作区搜索的查询、索引刷新和数据库生命周期。
// 两个 SQLite 文件都是可重建投影；消息和文件仍分别由 Session 与 Workspace 拥有。
// 将资源放在核心服务中，桌面、工具或其他入口就能复用查询，不必自己启动后台刷新。
type Service struct {
	agents        *agents.Service
	sessions      *sessions.Service
	workspaces    *workspace.Manager
	conversations *Controller
	documents     *Controller
}

// NewService 仅构造索引控制器，首次查询时才打开数据库并异步刷新。
func NewService(cacheDir string, agents *agents.Service, sessions *sessions.Service, workspaces *workspace.Manager) *Service {
	return &Service{
		agents: agents, sessions: sessions, workspaces: workspaces,
		conversations: NewController(filepath.Join(cacheDir, "conversation-search.sqlite")),
		documents:     NewController(filepath.Join(cacheDir, "document-search.sqlite")),
	}
}

// SearchSessions 立即返回已提交的索引视图，Updating 供调用方决定是否继续轮询。
func (s *Service) SearchSessions(ctx context.Context, query string) ([]Result, Status, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []Result{}, Status{}, nil
	}
	if utf8.RuneCountInString(query) > 200 {
		return nil, Status{}, fmt.Errorf("搜索词不能超过 200 字")
	}
	index, status, release, err := s.conversations.Acquire("sessions", func(ctx context.Context, index *Index) error {
		return RefreshSessions(ctx, index, s.agents, s.sessions)
	})
	if err != nil {
		return nil, Status{}, err
	}
	defer release()
	results, err := index.Search(ctx, query, 50)
	return results, status, err
}

// SearchDocuments 每次按 Agent 当前配置解析工作区，避免切换工作区后混入旧目录结果。
func (s *Service) SearchDocuments(ctx context.Context, agentID, query string) ([]DocumentResult, Status, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []DocumentResult{}, Status{}, nil
	}
	if utf8.RuneCountInString(query) > 200 {
		return nil, Status{}, fmt.Errorf("搜索词不能超过 200 字")
	}
	info, err := s.agents.Get(ctx, agentID)
	if err != nil {
		return nil, Status{}, err
	}
	resolved, err := s.workspaces.Resolve(ctx, info.Agent.ID, info.Agent.WorkspaceMode, info.Agent.WorkspacePath)
	if err != nil {
		return nil, Status{}, err
	}
	index, status, release, err := s.documents.Acquire(agentID+"\x00"+resolved.RootDir, func(ctx context.Context, index *Index) error {
		return RefreshDocuments(ctx, index, s.workspaces, agentID, resolved)
	})
	if err != nil {
		return nil, Status{}, err
	}
	defer release()
	results, err := index.SearchDocuments(ctx, agentID, resolved.RootDir, query, 50)
	return results, status, err
}

// Close 先取消并等待索引刷新和查询退出，再释放两个数据库。
// 应用必须在关闭 Session/Workspace 之前调用它；Controller 本身保证重复关闭安全。
func (s *Service) Close() error {
	return errors.Join(s.conversations.Close(), s.documents.Close())
}
