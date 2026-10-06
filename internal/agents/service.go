package agents

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	maxAgentNameLength         = 100
	maxInstructionLength       = 64 * 1024
	agentCreateRollbackTimeout = 3 * time.Second
)

// ServiceOption 注入可选领域依赖。正式应用提供全部依赖，纯 Profile 测试可省略目录和能力 Catalog。
type ServiceOption func(*Service)

// SkillSelectionValidator 只校验 Agent 引用，不暴露技能扫描、安装或 Eino 实现。
type SkillSelectionValidator interface {
	NormalizeAndValidateSelection(context.Context, []string) ([]string, error)
}

// MCPSelectionValidator 只校验 Server/raw Tool 选择，连接和执行仍由 MCP 模块拥有。
type MCPSelectionValidator interface {
	NormalizeAndValidateSelection(context.Context, []humbertmcp.ToolSelection) ([]humbertmcp.ToolSelection, error)
}

// WithWorkspaceManager 注入工作区生命周期依赖；正式 Bootstrap 必须提供。
func WithWorkspaceManager(manager *workspace.Manager) ServiceOption {
	return func(s *Service) { s.workspaces = manager }
}

// WithSkillCatalog 注入技能引用校验器，避免 Agent 直接依赖技能安装器。
func WithSkillCatalog(catalog SkillSelectionValidator) ServiceOption {
	return func(s *Service) { s.skills = catalog }
}

// WithMCPCatalog 注入 MCP 引用校验器，避免 Agent 直接管理 MCP 连接。
func WithMCPCatalog(catalog MCPSelectionValidator) ServiceOption {
	return func(s *Service) { s.mcp = catalog }
}

// Service 拥有 Agent Profile 的保存规则和目录生命周期。字段校验在 validation.go，
// 持久化在 Store，模型/技能/MCP/目录实现由各领域拥有；本服务不运行 Eino Agent。
type Service struct {
	store      *Store
	models     *models.Registry
	workspaces *workspace.Manager
	skills     SkillSelectionValidator
	mcp        MCPSelectionValidator
	logger     *logging.Logger

	// 删除持有写锁，Session 创建通过 WithActiveAgent 持有读锁。
	// 这样删除快照之后不能再创建新的依赖资源，避免遗留孤儿 Session。
	lifecycleMu sync.RWMutex
}

// NewService 组装 Profile 服务。Option 不提供服务查找，只设置调用方明确给出的依赖。
func NewService(store *Store, modelRegistry *models.Registry, logger *logging.Logger, options ...ServiceOption) *Service {
	service := &Service{store: store, models: modelRegistry, logger: logger}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// List 批量读取 Profile，再一次性投影模型展示名称。
func (s *Service) List(ctx context.Context) ([]AgentInfo, error) {
	values, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	return s.enrichModelDisplayNames(ctx, values)
}

// Get 返回当前可用 Profile；删除中的 Agent 由 Store 拒绝读取。
func (s *Service) Get(ctx context.Context, id string) (AgentInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AgentInfo{}, errors.New("Agent ID 不能为空")
	}
	value, err := s.store.Get(ctx, id)
	if err != nil {
		return AgentInfo{}, err
	}
	values, err := s.enrichModelDisplayNames(ctx, []AgentInfo{value})
	if err != nil {
		return AgentInfo{}, err
	}
	return values[0], nil
}

// WithActiveAgent 在“读取 Agent → 创建依赖资源”的整个回调期间持有生命周期读锁。
// 调用方不能拆开这两步，否则删除可能在中间完成，留下没有所属 Agent 的资源。
func (s *Service) WithActiveAgent(ctx context.Context, id string, fn func(AgentInfo) error) error {
	if fn == nil {
		return errors.New("Agent 生命周期回调不能为空")
	}
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	value, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	return fn(value)
}

// Create 先校验字段和引用，再校验目录、保存 Profile、准备工作区。
// Validate 不创建托管目录；Resolve 在 Profile 成功落盘后准备目录，失败时补偿删除 Profile。
// 两步都有明确目的，归一化阶段不再额外访问工作区。
func (s *Service) Create(ctx context.Context, input CreateInput) (AgentInfo, error) {
	id := uuid.NewString()
	normalized, err := normalizeInput(input.Name, input.Avatar, input.Instruction, input.ModelID, input.WorkspaceMode, input.WorkspacePath)
	if err != nil {
		return AgentInfo{}, err
	}
	skills, err := s.normalizeEnabledSkills(ctx, input.EnabledSkills)
	if err != nil {
		return AgentInfo{}, err
	}
	mcpTools, err := s.normalizeEnabledMCPTools(ctx, input.EnabledMCPTools)
	if err != nil {
		return AgentInfo{}, err
	}
	builtinTools, err := normalizeBuiltinToolSelection(input.EnabledBuiltinTools)
	if err != nil {
		return AgentInfo{}, err
	}
	policy, err := normalizeSandboxPolicy(input.Sandbox)
	if err != nil {
		return AgentInfo{}, err
	}
	if err := s.ensureModelUsable(ctx, normalized.ModelID); err != nil {
		return AgentInfo{}, err
	}
	roles, err := s.normalizeAndValidateModelRoles(ctx, input.ModelRoles)
	if err != nil {
		return AgentInfo{}, err
	}
	if s.workspaces != nil {
		if err := s.workspaces.Validate(ctx, id, normalized.WorkspaceMode, normalized.WorkspacePath); err != nil {
			return AgentInfo{}, fmt.Errorf("Workspace 配置无效: %w", err)
		}
	}
	now := time.Now().UTC()
	value := Agent{
		ID: id, Name: normalized.Name, Avatar: normalized.Avatar,
		SubagentEnabled: input.SubagentEnabled, Instruction: normalized.Instruction,
		ModelID: normalized.ModelID, ModelRoles: roles,
		EnabledSkills: append([]string(nil), skills...), EnabledMCPTools: cloneMCPSelections(mcpTools),
		EnabledBuiltinTools: cloneStringsPreserveNil(builtinTools), Sandbox: policy,
		WorkspaceMode: normalized.WorkspaceMode, WorkspacePath: normalized.WorkspacePath,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.Create(ctx, value); err != nil {
		return AgentInfo{}, err
	}
	if s.workspaces != nil {
		if _, err := s.workspaces.Resolve(ctx, id, value.WorkspaceMode, value.WorkspacePath); err != nil {
			workspaceErr := fmt.Errorf("准备 Agent Workspace 失败: %w", err)
			// 原请求可能已取消，回滚使用独立且有上限的 Context，确保补偿仍能完成。
			if rollbackErr := s.rollbackCreatedAgent(id); rollbackErr != nil {
				s.logger.Error(context.Background(), "Agent 创建失败且补偿删除失败",
					"operation", "agent.create.rollback", "agent_id", id,
					"workspace_error", workspaceErr, "rollback_error", rollbackErr)
				return AgentInfo{}, errors.Join(workspaceErr, fmt.Errorf("回滚 Agent Profile 失败: %w", rollbackErr))
			}
			return AgentInfo{}, workspaceErr
		}
	}
	s.logProfileChange(ctx, "agent.create", "Agent 已创建", value)
	return s.Get(ctx, id)
}

// Update 保存完整表单，只影响之后的 Turn，不移动或删除原工作区文件。
// 可选字段的 nil 表示没有提交，非 nil 的空值表示明确清空。必须在 Store.Mutate 的锁内
// 保留未提交字段，不能拿调用前读取的旧 Profile 覆盖同时保存的模型、工具或安全配置。
func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (AgentInfo, error) {
	existing, err := s.store.Get(ctx, id)
	if err != nil {
		return AgentInfo{}, err
	}
	normalized, err := normalizeInput(input.Name, input.Avatar, input.Instruction, input.ModelID, input.WorkspaceMode, input.WorkspacePath)
	if err != nil {
		return AgentInfo{}, err
	}
	skills, err := s.normalizeEnabledSkills(ctx, input.EnabledSkills)
	if err != nil {
		return AgentInfo{}, err
	}
	var mcpTools []humbertmcp.ToolSelection
	if input.EnabledMCPTools != nil {
		mcpTools, err = s.normalizeEnabledMCPTools(ctx, *input.EnabledMCPTools)
		if err != nil {
			return AgentInfo{}, err
		}
	}
	var builtinTools []string
	if input.EnabledBuiltinTools != nil {
		builtinTools, err = normalizeBuiltinToolSelection(*input.EnabledBuiltinTools)
		if err != nil {
			return AgentInfo{}, err
		}
	}
	var policy sandbox.AgentPolicy
	if input.Sandbox != nil {
		policy, err = normalizeSandboxPolicy(*input.Sandbox)
		if err != nil {
			return AgentInfo{}, err
		}
	}
	var roles ModelRoles
	if input.ModelRoles != nil {
		roles, err = s.normalizeAndValidateModelRoles(ctx, *input.ModelRoles)
		if err != nil {
			return AgentInfo{}, err
		}
	}
	if err := s.ensureModelUsable(ctx, normalized.ModelID); err != nil {
		return AgentInfo{}, err
	}
	// 先准备新目录，再保存新引用。目录无法访问时保持原 Profile，避免配置半更新。
	if s.workspaces != nil {
		if _, err := s.workspaces.Resolve(ctx, existing.Agent.ID, normalized.WorkspaceMode, normalized.WorkspacePath); err != nil {
			return AgentInfo{}, fmt.Errorf("准备新的 Agent Workspace 失败: %w", err)
		}
	}
	updated, err := s.store.Mutate(ctx, id, func(current *Agent) error {
		current.Name, current.Avatar, current.Instruction = normalized.Name, normalized.Avatar, normalized.Instruction
		current.ModelID = normalized.ModelID
		current.EnabledSkills = append([]string(nil), skills...)
		current.WorkspaceMode, current.WorkspacePath = normalized.WorkspaceMode, normalized.WorkspacePath
		if input.SubagentEnabled != nil {
			current.SubagentEnabled = *input.SubagentEnabled
		}
		if input.ModelRoles != nil {
			current.ModelRoles = roles
		}
		if input.EnabledMCPTools != nil {
			current.EnabledMCPTools = cloneMCPSelections(mcpTools)
		}
		if input.EnabledBuiltinTools != nil {
			current.EnabledBuiltinTools = cloneStringsPreserveNil(builtinTools)
		}
		if input.Sandbox != nil {
			current.Sandbox = policy
		}
		current.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		return AgentInfo{}, err
	}
	s.logProfileChange(ctx, "agent.update", "Agent Profile 已更新", updated)
	return s.Get(ctx, id)
}

// logProfileChange 统一完整配置保存的审计字段；不记录指令、头像或任何凭证正文。
func (s *Service) logProfileChange(ctx context.Context, operation, message string, value Agent) {
	s.logger.Info(ctx, message, "operation", operation, "agent_id", value.ID, "model_id", value.ModelID,
		"skill_count", len(value.EnabledSkills), "mcp_server_selection_count", len(value.EnabledMCPTools),
		"builtin_tool_count", len(value.EnabledBuiltinTools), "sandbox_profile", string(value.Sandbox.Profile),
		"workspace_mode", string(value.WorkspaceMode))
}

// mutateProfile 是局部配置命令共同的原子写入口。patch 只修改命令所属字段，其余字段
// 来自锁内最新 Profile；保存后重新投影 AgentInfo，让调用方收到完整、最新的界面数据。
func (s *Service) mutateProfile(ctx context.Context, id string, patch func(*Agent) error) (AgentInfo, error) {
	id = strings.TrimSpace(id)
	if _, err := s.store.Mutate(ctx, id, patch); err != nil {
		return AgentInfo{}, err
	}
	return s.Get(ctx, id)
}

// UpdateProfile 只保存身份和指令，复用与 Create/Update 相同的字符、字节和头像检查。
func (s *Service) UpdateProfile(ctx context.Context, id, name, profileAvatar, instruction string) (AgentInfo, error) {
	profile, err := normalizeProfile(name, profileAvatar, instruction)
	if err != nil {
		return AgentInfo{}, err
	}
	return s.mutateProfile(ctx, id, func(a *Agent) error {
		a.Name, a.Avatar, a.Instruction = profile.Name, profile.Avatar, profile.Instruction
		return nil
	})
}

// SetModel 仅替换主聊天模型；当前 Turn 继续使用启动时冻结的模型。
func (s *Service) SetModel(ctx context.Context, id, modelID string) (AgentInfo, error) {
	modelID = strings.TrimSpace(modelID)
	if err := s.ensureModelUsable(ctx, modelID); err != nil {
		return AgentInfo{}, err
	}
	return s.mutateProfile(ctx, id, func(a *Agent) error { a.ModelID = modelID; return nil })
}

// SetModelRoles 仅替换辅助模型角色，留空由 Runtime 执行回退策略。
func (s *Service) SetModelRoles(ctx context.Context, id string, roles ModelRoles) (AgentInfo, error) {
	normalized, err := s.normalizeAndValidateModelRoles(ctx, roles)
	if err != nil {
		return AgentInfo{}, err
	}
	return s.mutateProfile(ctx, id, func(a *Agent) error { a.ModelRoles = normalized; return nil })
}

// SetSkills 明确替换技能选择，空集合表示禁用，不影响内置工具或 MCP 绑定。
func (s *Service) SetSkills(ctx context.Context, id string, names []string) (AgentInfo, error) {
	normalized, err := s.normalizeEnabledSkills(ctx, names)
	if err != nil {
		return AgentInfo{}, err
	}
	return s.mutateProfile(ctx, id, func(a *Agent) error { a.EnabledSkills = append([]string{}, normalized...); return nil })
}

// UpdateSecurity 同时保存工具选择与沙箱策略，保证安全配置作为一个整体原子替换。
func (s *Service) UpdateSecurity(ctx context.Context, id string, names []string, policy sandbox.AgentPolicy) (AgentInfo, error) {
	tools, err := normalizeBuiltinToolSelection(names)
	if err != nil {
		return AgentInfo{}, err
	}
	normalized, err := normalizeSandboxPolicy(policy)
	if err != nil {
		return AgentInfo{}, err
	}
	return s.mutateProfile(ctx, id, func(a *Agent) error {
		a.EnabledBuiltinTools, a.Sandbox = append([]string{}, tools...), normalized
		return nil
	})
}

// Delete 通过持久化状态机删除完整 Agent Aggregate。
//
// Agent 内部目录（Profile、Session sidecar等）属于 Humbert 自有数据；删除 Agent
// 时一并删除。Managed Workspace 同样属于 Humbert 管理范围，会同步清理。Custom Workspace
// 是用户自己的外部目录，只解除引用，绝不会递归删除。任一步失败都会保留 deleting 标记，
// 后续重试或应用重启可以继续完成清理。
func (s *Service) Delete(ctx context.Context, id string) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	state, err := s.store.BeginDelete(ctx, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	return s.resumeDeletion(ctx, state)
}

// RecoverDeletions 继续上次进程未完成的 Agent 删除。
//
// 返回聚合错误供 Bootstrap 记录；失败的 Agent 仍保持隐藏和可重试，不应因此阻塞应用启动。
func (s *Service) RecoverDeletions(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	states, recoveryErr := s.store.ListDeleting(ctx)
	for _, state := range states {
		if err := s.resumeDeletion(ctx, state); err != nil {
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("恢复删除 Agent %s 失败: %w", state.AgentID, err))
		}
	}
	return recoveryErr
}

func (s *Service) resumeDeletion(ctx context.Context, state DeletionState) error {
	mode := state.WorkspaceMode
	if mode == "" {
		mode = workspace.ModeManaged
	}
	if s.workspaces != nil && mode == workspace.ModeManaged {
		if err := s.workspaces.DeleteManaged(ctx, state.AgentID); err != nil {
			return fmt.Errorf("删除 Agent Managed Workspace 失败: %w", err)
		}
	}
	if err := s.store.DeleteMarked(ctx, state.AgentID); err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.Info(ctx, "Agent 已删除", "operation", "agent.delete", "agent_id", state.AgentID,
			"workspace_mode", string(mode))
	}
	return nil
}

// enrichModelDisplayNames 只做跨领域展示投影，一次列表只读取一次模型目录。
// 模型被删除或未指定模型时名称留空，仍允许用户打开 Agent 设置修复配置。
func (s *Service) enrichModelDisplayNames(ctx context.Context, values []AgentInfo) ([]AgentInfo, error) {
	needsModels := false
	for _, value := range values {
		if strings.TrimSpace(value.Agent.ModelID) != "" {
			needsModels = true
			break
		}
	}
	if !needsModels || s.models == nil {
		return values, nil
	}
	models, err := s.models.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 Agent Model 展示信息失败: %w", err)
	}
	names := make(map[string]string, len(models))
	for _, value := range models {
		names[value.Model.ID] = value.Model.DisplayName
	}
	for i := range values {
		values[i].ModelDisplayName = names[values[i].Agent.ModelID]
	}
	return values, nil
}

// cloneMCPSelections 深拷贝嵌套工具集合，避免调用方之后修改切片影响已准备的保存结果。
func cloneMCPSelections(values []humbertmcp.ToolSelection) []humbertmcp.ToolSelection {
	result := make([]humbertmcp.ToolSelection, len(values))
	for i, value := range values {
		result[i] = humbertmcp.ToolSelection{ServerID: value.ServerID, Tools: append([]string(nil), value.Tools...)}
	}
	return result
}

func cloneStringsPreserveNil(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

// SetMCPToolsForAgent 原子替换 MCP 选择，不覆盖其他能力配置。
func (s *Service) SetMCPToolsForAgent(ctx context.Context, id string, values []humbertmcp.ToolSelection) (AgentInfo, error) {
	normalized, err := s.normalizeEnabledMCPTools(ctx, values)
	if err != nil {
		return AgentInfo{}, err
	}
	return s.mutateProfile(ctx, id, func(a *Agent) error { a.EnabledMCPTools = cloneMCPSelections(normalized); return nil })
}

// CountAgentsUsingMCPServer 返回引用指定 MCP Server 的 Agent 数量。
// mcp.Manager 在删除 Server 前通过接口调用本方法，避免产生 dangling server_id。
func (s *Service) CountAgentsUsingMCPServer(ctx context.Context, serverID string) (int, error) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return 0, errors.New("MCP Server ID 不能为空")
	}
	values, err := s.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("读取 Agent MCP 引用失败: %w", err)
	}
	count := 0
	for _, value := range values {
		for _, selection := range value.Agent.EnabledMCPTools {
			if selection.ServerID == serverID && len(selection.Tools) > 0 {
				count++
				break
			}
		}
	}
	return count, nil
}

// EnableSkillForAgent 在锁内合并选择，两个同时安装的 Skill 不会互相覆盖。
func (s *Service) EnableSkillForAgent(ctx context.Context, id, name string) (AgentInfo, error) {
	normalized, err := s.normalizeEnabledSkills(ctx, []string{name})
	if err != nil {
		return AgentInfo{}, err
	}
	if len(normalized) != 1 {
		return AgentInfo{}, errors.New("Skill 名称不能为空")
	}
	name = normalized[0]
	return s.mutateProfile(ctx, id, func(a *Agent) error {
		for _, enabled := range a.EnabledSkills {
			if enabled == name {
				return nil
			}
		}
		a.EnabledSkills = append(a.EnabledSkills, name)
		sort.Strings(a.EnabledSkills)
		return nil
	})
}

// DisableSkillForAgent 不要求资源仍存在，允许移除已删除的 Skill 引用。
func (s *Service) DisableSkillForAgent(ctx context.Context, id, name string) (AgentInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AgentInfo{}, errors.New("Skill 名称不能为空")
	}
	return s.mutateProfile(ctx, id, func(a *Agent) error {
		next := make([]string, 0, len(a.EnabledSkills))
		for _, enabled := range a.EnabledSkills {
			if enabled != name {
				next = append(next, enabled)
			}
		}
		a.EnabledSkills = next
		return nil
	})
}

// rollbackCreatedAgent 不使用已经失败的创建请求 Context；补偿最多执行三秒。
// 复用正常删除状态机，使补偿中断后也能在下次启动继续恢复。
func (s *Service) rollbackCreatedAgent(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), agentCreateRollbackTimeout)
	defer cancel()
	state, err := s.store.BeginDelete(ctx, id)
	if err != nil {
		return err
	}
	return s.resumeDeletion(ctx, state)
}

// AgentsUsingSkill 查询持久化引用，供技能维护判断是否允许删除资源。
func (s *Service) AgentsUsingSkill(
	ctx context.Context,
	skillName string,
) ([]AgentInfo, error) {
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return nil, errors.New("Skill 名称不能为空")
	}
	values, err := s.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 Agent Skill 引用失败: %w", err)
	}
	result := make([]AgentInfo, 0)
	for _, value := range values {
		for _, enabled := range value.Agent.EnabledSkills {
			if enabled == skillName {
				result = append(result, value)
				break
			}
		}
	}
	return result, nil
}
