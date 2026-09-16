package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/projects"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// ProjectDTO 是 Desktop 的项目聚合视图。
// Project 自己拥有 Name/Workspace；Agent 子域拥有 Instruction/Model/Skills/Security。
// 为了保持现有 Vue 组件简单，这里把两者投影成一个只读 DTO，但写操作在后端仍拆成窄命令。
type ProjectDTO struct {
	ID                     string             `json:"id"`
	AgentID                string             `json:"agentID"`
	Name                   string             `json:"name"`
	Instruction            string             `json:"instruction"`
	ModelID                string             `json:"modelID"`
	ModelDisplayName       string             `json:"modelDisplayName"`
	ModelRoles             AgentModelRolesDTO `json:"modelRoles"`
	EnabledSkills          []string           `json:"enabledSkills"`
	EnabledBuiltinTools    []string           `json:"enabledBuiltinTools"`
	BuiltinToolsConfigured bool               `json:"builtinToolsConfigured"`
	AvailableBuiltinTools  []BuiltinToolDTO   `json:"availableBuiltinTools"`
	Sandbox                SandboxPolicyDTO   `json:"sandbox"`
	SandboxStatus          SandboxStatusDTO   `json:"sandboxStatus"`
	WorkspaceMode          string             `json:"workspaceMode"`
	WorkspacePath          string             `json:"workspacePath"`
	WorkspaceDisplayPath   string             `json:"workspaceDisplayPath"`
	CreatedAt              string             `json:"createdAt"`
	UpdatedAt              string             `json:"updatedAt"`
}

type CreateProjectRequest struct {
	Name                   string             `json:"name"`
	Instruction            string             `json:"instruction"`
	ModelID                string             `json:"modelID"`
	ModelRoles             AgentModelRolesDTO `json:"modelRoles"`
	EnabledSkills          []string           `json:"enabledSkills"`
	WorkspaceMode          string             `json:"workspaceMode"`
	WorkspacePath          string             `json:"workspacePath"`
	BuiltinToolsConfigured bool               `json:"builtinToolsConfigured"`
	EnabledBuiltinTools    []string           `json:"enabledBuiltinTools"`
	Sandbox                SandboxPolicyDTO   `json:"sandbox"`
}

type UpdateProjectRequest struct {
	Name                string             `json:"name"`
	Instruction         string             `json:"instruction"`
	ModelID             string             `json:"modelID"`
	ModelRoles          AgentModelRolesDTO `json:"modelRoles"`
	EnabledSkills       []string           `json:"enabledSkills"`
	WorkspaceMode       string             `json:"workspaceMode"`
	WorkspacePath       string             `json:"workspacePath"`
	EnabledBuiltinTools []string           `json:"enabledBuiltinTools"`
	Sandbox             SandboxPolicyDTO   `json:"sandbox"`
}

type ProjectService struct {
	core   *coreapp.Application
	agents *AgentService
}

func NewProjectService(core *coreapp.Application) *ProjectService {
	return &ProjectService{core: core, agents: NewAgentService(core)}
}
func (s *ProjectService) ServiceName() string { return "ProjectService" }

func (s *ProjectService) ListProjects() ([]ProjectDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	values, err := s.core.Projects().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 Project 列表失败: %w", err)
	}
	result := make([]ProjectDTO, 0, len(values))
	for _, value := range values {
		dto, err := s.toDTO(ctx, value)
		if err != nil {
			return nil, err
		}
		result = append(result, dto)
	}
	return result, nil
}
func (s *ProjectService) GetProject(id string) (ProjectDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.core.Projects().Get(ctx, id)
	if err != nil {
		return ProjectDTO{}, fmt.Errorf("读取 Project 失败: %w", err)
	}
	return s.toDTO(ctx, value)
}

// CreateProject 以当前产品的一对一默认关系创建 Project + Agent。
// 领域对象已经分离，未来允许一个 Agent 服务多个 Project 时无需改 Session/Runtime 协议。
func (s *ProjectService) CreateProject(request CreateProjectRequest) (ProjectDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	// Agent 的 Workspace 字段只保留给旧数据迁移；新 Project 从创建开始就是 Workspace 的
	// 唯一权威来源，因此新 Agent 的 legacy workspace 固定为 managed/empty。
	agent, err := s.agents.CreateAgent(CreateAgentRequest{
		Name: request.Name, Instruction: request.Instruction, ModelID: request.ModelID, ModelRoles: request.ModelRoles, EnabledSkills: request.EnabledSkills,
		WorkspaceMode: string(workspace.ModeManaged), WorkspacePath: "",
		BuiltinToolsConfigured: request.BuiltinToolsConfigured, EnabledBuiltinTools: request.EnabledBuiltinTools, Sandbox: request.Sandbox,
	})
	if err != nil {
		return ProjectDTO{}, err
	}
	project, err := s.core.Projects().Create(ctx, projects.CreateInput{ID: agent.ID, Name: request.Name, AgentID: agent.ID, WorkspaceMode: workspace.Mode(request.WorkspaceMode), WorkspacePath: request.WorkspacePath})
	if err != nil {
		rollbackErr := s.core.Agents().Delete(context.WithoutCancel(ctx), agent.ID)
		if rollbackErr != nil {
			return ProjectDTO{}, errors.Join(fmt.Errorf("创建 Project 失败: %w", err), fmt.Errorf("回滚 Agent 失败: %w", rollbackErr))
		}
		return ProjectDTO{}, fmt.Errorf("创建 Project 失败: %w", err)
	}
	return s.toDTO(ctx, project)
}

// UpdateProject 将一次“项目设置保存”拆成明确领域命令；不再调用全量 UpdateAgent。
func (s *ProjectService) UpdateProject(id string, request UpdateProjectRequest) (ProjectDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	project, err := s.core.Projects().Get(ctx, id)
	if err != nil {
		return ProjectDTO{}, err
	}
	previousProject := project
	previousAgent, err := s.core.Agents().Get(ctx, project.AgentID)
	if err != nil {
		return ProjectDTO{}, err
	}

	// Workspace 先做 Resolve/Update；后续 Agent 写失败时会尽力补偿恢复。
	project, err = s.core.Projects().Update(ctx, id, projects.UpdateInput{Name: request.Name, WorkspaceMode: workspace.Mode(request.WorkspaceMode), WorkspacePath: request.WorkspacePath})
	if err != nil {
		return ProjectDTO{}, fmt.Errorf("更新 Project Workspace 失败: %w", err)
	}
	rollback := func(cause error) (ProjectDTO, error) {
		rollbackCtx := context.WithoutCancel(ctx)
		_, projectRollbackErr := s.core.Projects().Update(rollbackCtx, id, projects.UpdateInput{Name: previousProject.Name, WorkspaceMode: previousProject.WorkspaceMode, WorkspacePath: previousProject.WorkspacePath})
		_, profileRollbackErr := s.core.Agents().UpdateProfile(rollbackCtx, project.AgentID, previousAgent.Agent.Name, previousAgent.Agent.Instruction)
		_, modelRollbackErr := s.core.Agents().SetModel(rollbackCtx, project.AgentID, previousAgent.Agent.ModelID)
		_, modelRolesRollbackErr := s.core.Agents().SetModelRoles(rollbackCtx, project.AgentID, previousAgent.Agent.ModelRoles)
		_, skillsRollbackErr := s.core.Agents().SetSkills(rollbackCtx, project.AgentID, previousAgent.Agent.EnabledSkills)
		_, securityRollbackErr := s.core.Agents().UpdateSecurity(rollbackCtx, project.AgentID, previousAgent.Agent.EnabledBuiltinTools, previousAgent.Agent.Sandbox)
		return ProjectDTO{}, errors.Join(cause, projectRollbackErr, profileRollbackErr, modelRollbackErr, modelRolesRollbackErr, skillsRollbackErr, securityRollbackErr)
	}
	if _, err = s.core.Agents().UpdateProfile(ctx, project.AgentID, request.Name, request.Instruction); err != nil {
		return rollback(fmt.Errorf("更新 Agent Profile 失败: %w", err))
	}
	if _, err = s.core.Agents().SetModel(ctx, project.AgentID, request.ModelID); err != nil {
		return rollback(fmt.Errorf("更新 Agent Model 失败: %w", err))
	}
	if _, err = s.core.Agents().SetModelRoles(ctx, project.AgentID, modelRolesFromDTO(request.ModelRoles)); err != nil {
		return rollback(fmt.Errorf("更新 Agent Model Roles 失败: %w", err))
	}
	if _, err = s.core.Agents().SetSkills(ctx, project.AgentID, request.EnabledSkills); err != nil {
		return rollback(fmt.Errorf("更新 Agent Skills 失败: %w", err))
	}
	if err = s.agents.validateBuiltinToolNames(request.EnabledBuiltinTools); err != nil {
		return rollback(err)
	}
	if _, err = s.core.Agents().UpdateSecurity(ctx, project.AgentID, request.EnabledBuiltinTools, sandboxPolicyFromDTO(request.Sandbox)); err != nil {
		return rollback(fmt.Errorf("更新 Agent Security 失败: %w", err))
	}
	return s.toDTO(ctx, project)
}

func (s *ProjectService) DeleteProject(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	project, err := s.core.Projects().Get(ctx, id)
	if err != nil {
		return err
	}
	sessions, err := s.core.Sessions().List(ctx, project.ID)
	if err != nil {
		return err
	}
	if len(sessions) > 0 {
		return fmt.Errorf("当前 Project 仍有 %d 个 Session，请先删除会话", len(sessions))
	}
	if err := s.core.Projects().Delete(ctx, id); err != nil {
		return fmt.Errorf("删除 Project 失败: %w", err)
	}

	// Project 与 Agent 已经是两个领域对象。未来允许多个 Project 共用一个 Agent 时，
	// 删除 Project 不能顺带删除仍被其它 Project 引用的 Agent。
	projectsLeft, err := s.core.Projects().List(ctx)
	if err != nil {
		_, rollbackErr := s.core.Projects().Create(context.WithoutCancel(ctx), projects.CreateInput{
			ID: project.ID, Name: project.Name, AgentID: project.AgentID, WorkspaceMode: project.WorkspaceMode, WorkspacePath: project.WorkspacePath,
		})
		return errors.Join(fmt.Errorf("检查 Agent Project 引用失败: %w", err), rollbackErr)
	}
	for _, candidate := range projectsLeft {
		if candidate.AgentID == project.AgentID {
			return nil
		}
	}

	if err := s.core.Agents().Delete(ctx, project.AgentID); err != nil {
		_, rollbackErr := s.core.Projects().Create(context.WithoutCancel(ctx), projects.CreateInput{
			ID: project.ID, Name: project.Name, AgentID: project.AgentID, WorkspaceMode: project.WorkspaceMode, WorkspacePath: project.WorkspacePath,
		})
		return errors.Join(fmt.Errorf("删除 Project 后清理未引用 Agent 失败: %w", err), rollbackErr)
	}
	return nil
}

func (s *ProjectService) SelectWorkspaceDirectory(currentPath string) (string, error) {
	return selectDirectory("选择 Project Workspace", currentPath)
}

func (s *ProjectService) toDTO(ctx context.Context, project projects.Project) (ProjectDTO, error) {
	agent, err := s.core.Agents().Get(ctx, project.AgentID)
	if err != nil {
		return ProjectDTO{}, fmt.Errorf("读取 Project Agent 失败: %w", err)
	}
	agentDTO, err := s.agents.toDTO(agent)
	if err != nil {
		return ProjectDTO{}, err
	}
	display := strings.TrimSpace(project.WorkspacePath)
	if project.WorkspaceMode == workspace.ModeManaged {
		display, err = s.core.Workspaces().ManagedPath(project.ID)
		if err != nil {
			return ProjectDTO{}, err
		}
	}
	updated := project.UpdatedAt
	if agent.Agent.UpdatedAt.After(updated) {
		updated = agent.Agent.UpdatedAt
	}
	created := project.CreatedAt
	if created.IsZero() {
		created = agent.Agent.CreatedAt
	}
	return ProjectDTO{ID: project.ID, AgentID: project.AgentID, Name: project.Name, Instruction: agentDTO.Instruction, ModelID: agentDTO.ModelID, ModelDisplayName: agentDTO.ModelDisplayName, ModelRoles: agentDTO.ModelRoles, EnabledSkills: agentDTO.EnabledSkills, EnabledBuiltinTools: agentDTO.EnabledBuiltinTools, BuiltinToolsConfigured: agentDTO.BuiltinToolsConfigured, AvailableBuiltinTools: agentDTO.AvailableBuiltinTools, Sandbox: agentDTO.Sandbox, SandboxStatus: agentDTO.SandboxStatus, WorkspaceMode: string(project.WorkspaceMode), WorkspacePath: project.WorkspacePath, WorkspaceDisplayPath: display, CreatedAt: created.UTC().Format(time.RFC3339Nano), UpdatedAt: updated.UTC().Format(time.RFC3339Nano)}, nil
}
