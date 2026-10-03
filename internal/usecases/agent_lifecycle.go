package usecases

import (
	"context"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
)

// AgentLifecycle 收拢 Agent/会话删除的跨领域协调。
// 具体删除、调度与并发规则仍使用已有领域服务，所有入口共用同一流程。
type AgentLifecycle struct {
	agents      *agents.Service
	sessions    *sessions.Service
	runtime     *runtime.Service
	tasks       *tasks.Manager
	permissions *permission.Engine
}

func NewAgentLifecycle(agents *agents.Service, sessions *sessions.Service, runtime *runtime.Service, tasks *tasks.Manager, permissions *permission.Engine) *AgentLifecycle {
	return &AgentLifecycle{agents: agents, sessions: sessions, runtime: runtime, tasks: tasks, permissions: permissions}
}

// DeleteAgent 保留原有顺序：暂停任务派发，在 Runtime 删除门闩内执行可恢复删除，
// 然后清理会话元数据和进程内授权。Custom Workspace 的所有权由 Agent 服务处理。
func (s *AgentLifecycle) DeleteAgent(ctx context.Context, agentID string) error {
	release := s.tasks.SuspendAgent(agentID)
	defer release()
	sessionIDs, err := s.runtime.DeleteAgent(ctx, agentID, func(ctx context.Context) error {
		return s.agents.Delete(ctx, agentID)
	})
	if err != nil {
		return err
	}
	if err := s.sessions.PurgeAgentMetadata(ctx, agentID); err != nil {
		return err
	}
	s.ClearSessionRules(sessionIDs)
	return nil
}

// DeleteSession 复用 Task Manager 的调度临界区，兼容普通会话和 TaskRun 会话。
func (s *AgentLifecycle) DeleteSession(ctx context.Context, sessionID string) error {
	if _, err := s.tasks.DeleteConversation(ctx, sessionID); err != nil {
		return err
	}
	s.ClearSessionRules([]string{sessionID})
	return nil
}

// ClearSessionRules 在会话已经删除后释放其临时授权，可供任务历史清理共用。
func (s *AgentLifecycle) ClearSessionRules(sessionIDs []string) {
	if s.permissions == nil {
		return
	}
	for _, id := range sessionIDs {
		s.permissions.ClearSessionRules(id)
	}
}
