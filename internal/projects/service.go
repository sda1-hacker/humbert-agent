package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const maxProjectNameLength = 100

type Service struct {
	store      *Store
	agents     *agents.Service
	workspaces *workspace.Manager
	logger     *logging.Logger
}

func NewService(store *Store, agentService *agents.Service, workspaceManager *workspace.Manager, logger *logging.Logger) *Service {
	return &Service{store: store, agents: agentService, workspaces: workspaceManager, logger: logger}
}

func (s *Service) List(ctx context.Context) ([]Project, error)         { return s.store.List(ctx) }
func (s *Service) Get(ctx context.Context, id string) (Project, error) { return s.store.Get(ctx, id) }

func (s *Service) Create(ctx context.Context, input CreateInput) (Project, error) {
	if s.store == nil || s.agents == nil || s.workspaces == nil {
		return Project{}, errors.New("ProjectService 尚未正确初始化")
	}
	id := strings.TrimSpace(input.ID)
	agentID := strings.TrimSpace(input.AgentID)
	if id == "" || agentID == "" {
		return Project{}, errors.New("Project ID/AgentID 不能为空")
	}
	if _, err := s.agents.Get(ctx, agentID); err != nil {
		return Project{}, fmt.Errorf("Project 引用的 Agent 无效: %w", err)
	}
	name, err := normalizeName(input.Name)
	if err != nil {
		return Project{}, err
	}
	resolvedWorkspace, err := s.workspaces.Resolve(ctx, id, input.WorkspaceMode, input.WorkspacePath)
	if err != nil {
		return Project{}, fmt.Errorf("准备 Project Workspace 失败: %w", err)
	}
	now := time.Now().UTC()
	value := Project{
		ID:            id,
		Name:          name,
		AgentID:       agentID,
		WorkspaceMode: resolvedWorkspace.Mode,
		WorkspacePath: resolvedWorkspace.ConfiguredPath,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.store.Create(ctx, value); err != nil {
		return Project{}, err
	}
	if s.logger != nil {
		s.logger.Info(ctx, "Project 已创建", "operation", "project.create", "project_id", id, "agent_id", agentID)
	}
	return value, nil
}

func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (Project, error) {
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return Project{}, err
	}
	name, err := normalizeName(input.Name)
	if err != nil {
		return Project{}, err
	}
	resolvedWorkspace, err := s.workspaces.Resolve(ctx, current.ID, input.WorkspaceMode, input.WorkspacePath)
	if err != nil {
		return Project{}, fmt.Errorf("准备 Project Workspace 失败: %w", err)
	}
	current.Name = name
	current.WorkspaceMode = resolvedWorkspace.Mode
	current.WorkspacePath = resolvedWorkspace.ConfiguredPath
	current.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, current); err != nil {
		return Project{}, err
	}
	return current, nil
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.store.Delete(ctx, id) }

// EnsureLegacyAgentProjects 为升级前 Agent 建立一对一 Project。旧 Workspace 字段仅在这里读取。
func (s *Service) EnsureLegacyAgentProjects(ctx context.Context) error {
	values, err := s.agents.List(ctx)
	if err != nil {
		return err
	}
	for _, info := range values {
		agent := info.Agent
		if _, err := s.store.Get(ctx, agent.ID); err == nil {
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		mode := agent.WorkspaceMode
		if mode == "" {
			mode = workspace.ModeManaged
		}
		if _, err := s.Create(ctx, CreateInput{ID: agent.ID, Name: agent.Name, AgentID: agent.ID, WorkspaceMode: mode, WorkspacePath: agent.WorkspacePath}); err != nil && !errors.Is(err, ErrAlreadyExists) {
			return fmt.Errorf("迁移 Agent %s 为 Project 失败: %w", agent.ID, err)
		}
	}
	return nil
}

func normalizeName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("Project 名称不能为空")
	}
	if len([]rune(value)) > maxProjectNameLength {
		return "", fmt.Errorf("Project 名称不能超过 %d 个字符", maxProjectNameLength)
	}
	return value, nil
}
