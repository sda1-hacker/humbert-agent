package agents

import (
	"context"
	"errors"
	"fmt"
	"runtime"
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
	maxAgentNameLength = 100

	maxInstructionLength = 64 * 1024

	agentCreateRollbackTimeout = 3 * time.Second
)

// ServiceOption 配置 AgentService 的可选依赖。
type ServiceOption func(
	service *Service,
)

// SkillSelectionValidator 是 Agent Domain 对 Skill Catalog 的最小依赖。
//
// Agent Service 只关心“这些名称是否能被当前 Agent 安全启用”，不需要知道 Skill 文件如何
// 扫描、安装或如何接入 Eino。skills.Manager 通过同签名方法实现该边界。
type SkillSelectionValidator interface {
	NormalizeAndValidateSelection(ctx context.Context, names []string) ([]string, error)
}

// MCPSelectionValidator 是 Agent Domain 对 MCP Server Catalog 的最小依赖。
//
// MCP Manager 负责确认 Server 引用存在并规范化 raw Tool 选择；Agent Service 不关心
// MCP transport/session/Eino Adapter 的实现。
type MCPSelectionValidator interface {
	NormalizeAndValidateSelection(ctx context.Context, values []humbertmcp.ToolSelection) ([]humbertmcp.ToolSelection, error)
}

// WithWorkspaceManager 为 AgentService 配置 WorkspaceManager.
//
// 正式 Application Bootstrap 必须传入。
// 保留 Option 形式主要为了不破坏部分只测试 Agent Store 的既有测试。
func WithWorkspaceManager(
	manager *workspace.Manager,
) ServiceOption {
	return func(
		service *Service,
	) {
		service.workspaces =
			manager
	}
}

// WithSkillCatalog 为 AgentService 注入 Skill 选择校验器。
//
// 正式 Application Bootstrap 必须提供。保留接口而不是直接依赖 skills.Manager，避免 Agent
// Profile 领域与 Skill 的文件系统实现形成反向耦合。
func WithSkillCatalog(catalog SkillSelectionValidator) ServiceOption {
	return func(service *Service) {
		service.skills = catalog
	}
}

// WithMCPCatalog 为 AgentService 注入 MCP Tool Selection 校验器。
func WithMCPCatalog(catalog MCPSelectionValidator) ServiceOption {
	return func(service *Service) {
		service.mcp = catalog
	}
}

// Service 实现 Agent Profile 的领域规则。
//
// AgentService 负责：
//
//   - Agent Profile CRUD；
//   - 默认 Model 校验；
//   - Workspace 配置校验；
//   - Managed Workspace 创建；
//   - Custom Workspace 可用性校验。
//
// 它不负责运行 Eino Agent。
type Service struct {
	store *Store

	models *models.Registry

	workspaces *workspace.Manager

	skills SkillSelectionValidator

	mcp MCPSelectionValidator

	logger *logging.Logger

	// lifecycleMu 只协调 Agent 删除与依赖 Agent 的新资源创建。删除持有写锁，
	// Session 创建通过 WithActiveAgent 持有读锁，避免在删除快照之后又落入新 Session。
	lifecycleMu sync.RWMutex
}

// NewService 创建 AgentService。
func NewService(
	store *Store,
	modelRegistry *models.Registry,
	logger *logging.Logger,
	options ...ServiceOption,
) *Service {
	service :=
		&Service{
			store: store,

			models: modelRegistry,

			logger: logger,
		}

	for _, option := range options {

		if option == nil {
			continue
		}

		option(service)
	}

	return service
}

// List 返回全部 Agent。
func (s *Service) List(
	ctx context.Context,
) ([]AgentInfo, error) {
	values, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}

	return s.enrichModelDisplayNames(ctx, values)
}

// Get 返回指定 Agent。
func (s *Service) Get(
	ctx context.Context,
	id string,
) (AgentInfo, error) {
	id =
		strings.TrimSpace(
			id,
		)

	if id == "" {
		return AgentInfo{},
			errors.New(
				"Agent ID 不能为空",
			)
	}

	value, err := s.store.Get(
		ctx,
		id,
	)
	if err != nil {
		return AgentInfo{}, err
	}

	values, err := s.enrichModelDisplayNames(ctx, []AgentInfo{value})
	if err != nil {
		return AgentInfo{}, err
	}

	return values[0], nil
}

// WithActiveAgent 在 Agent 保持 active 的整个回调期间持有生命周期读锁。
//
// Session 创建等跨领域操作必须通过该入口完成“读取 Agent -> 创建依赖资源”，否则
// 删除流程可能在两步之间推进，留下创建成功但立即失去所属 Agent 的孤儿数据。
func (s *Service) WithActiveAgent(
	ctx context.Context,
	id string,
	fn func(AgentInfo) error,
) error {
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

// Create 创建 Agent。
//
// Workspace 生命周期：
//
//	Validate
//	   ↓
//	创建文件 Profile
//	   ↓
//	Resolve Workspace
//	   ↓
//	成功返回
//
// Managed Workspace 会在 Resolve 阶段真正创建目录。
// Custom Workspace 则要求用户选择的目录已经存在并可访问。
//
// 如果 Workspace 最终准备失败，会补偿删除刚刚创建的 Agent Profile。
func (s *Service) Create(
	ctx context.Context,
	input CreateInput,
) (AgentInfo, error) {
	id :=
		uuid.NewString()

	normalized, err :=
		s.normalizeInput(
			id,
			input.Name,
			input.Instruction,
			input.ModelID,
			input.WorkspaceMode,
			input.WorkspacePath,
		)

	if err != nil {
		return AgentInfo{},
			err
	}

	normalized.EnabledSkills, err = s.normalizeEnabledSkills(ctx, input.EnabledSkills)
	if err != nil {
		return AgentInfo{}, err
	}
	enabledMCPTools, err := s.normalizeEnabledMCPTools(ctx, input.EnabledMCPTools)
	if err != nil {
		return AgentInfo{}, err
	}
	enabledBuiltinTools, err := normalizeBuiltinToolSelection(input.EnabledBuiltinTools)
	if err != nil {
		return AgentInfo{}, err
	}
	sandboxPolicy, err := normalizeSandboxPolicy(input.Sandbox)
	if err != nil {
		return AgentInfo{}, err
	}

	if err :=
		s.ensureModelUsable(
			ctx,
			normalized.ModelID,
		); err != nil {

		return AgentInfo{},
			err
	}

	modelRoles, err := s.normalizeAndValidateModelRoles(ctx, input.ModelRoles)
	if err != nil {
		return AgentInfo{}, err
	}

	if s.workspaces != nil {
		if err :=
			s.workspaces.Validate(
				ctx,
				id,
				normalized.WorkspaceMode,
				normalized.WorkspacePath,
			); err != nil {

			return AgentInfo{},
				fmt.Errorf(
					"Workspace 配置无效: %w",
					err,
				)
		}
	}

	now :=
		time.Now().UTC()

	value :=
		Agent{
			ID: id,

			Name: normalized.Name,

			Instruction: normalized.Instruction,

			ModelID: normalized.ModelID,

			ModelRoles: modelRoles,

			EnabledSkills: append([]string(nil), normalized.EnabledSkills...),

			EnabledMCPTools: cloneMCPSelections(enabledMCPTools),

			EnabledBuiltinTools: cloneStringsPreserveNil(enabledBuiltinTools),

			Sandbox: sandboxPolicy,

			WorkspaceMode: normalized.WorkspaceMode,

			WorkspacePath: normalized.WorkspacePath,

			CreatedAt: now,

			UpdatedAt: now,
		}

	if err :=
		s.store.Create(
			ctx,
			value,
		); err != nil {

		return AgentInfo{},
			err
	}

	if s.workspaces != nil {
		if _, err :=
			s.workspaces.Resolve(
				ctx,
				value.ID,
				value.WorkspaceMode,
				value.WorkspacePath,
			); err != nil {

			workspaceErr :=
				fmt.Errorf(
					"准备 Agent Workspace 失败: %w",
					err,
				)

			rollbackErr :=
				s.rollbackCreatedAgent(
					value.ID,
				)

			if rollbackErr != nil {
				s.logger.Error(
					context.Background(),
					"Agent 创建失败且补偿删除失败",
					"operation",
					"agent.create.rollback",
					"agent_id",
					value.ID,
					"workspace_error",
					workspaceErr,
					"rollback_error",
					rollbackErr,
				)

				return AgentInfo{},
					errors.Join(
						workspaceErr,
						fmt.Errorf(
							"回滚 Agent Profile 失败: %w",
							rollbackErr,
						),
					)
			}

			return AgentInfo{},
				workspaceErr
		}
	}

	s.logger.Info(
		ctx,
		"Agent 已创建",
		"operation",
		"agent.create",
		"agent_id",
		value.ID,
		"model_id",
		value.ModelID,
		"skill_count",
		len(value.EnabledSkills),
		"mcp_server_selection_count",
		len(value.EnabledMCPTools),
		"builtin_tool_count",
		len(value.EnabledBuiltinTools),
		"sandbox_profile",
		string(value.Sandbox.Profile),
		"workspace_mode",
		string(
			value.WorkspaceMode,
		),
	)

	return s.Get(
		ctx,
		value.ID,
	)
}

// Update 修改 Agent Profile。
//
// Workspace 修改不会移动任何文件。
//
// 例如：
//
//	/Workspaces/A
//	    ↓
//	/Workspaces/B
//
// 只表示下一 Turn 从 B 开始工作。
// A 中所有文件保持原样。
//
// 当前正在运行的 Turn 已经拥有 Runtime Snapshot，因此不受影响。
func (s *Service) Update(
	ctx context.Context,
	id string,
	input UpdateInput,
) (AgentInfo, error) {
	existing, err :=
		s.store.Get(
			ctx,
			id,
		)

	if err != nil {
		return AgentInfo{},
			err
	}

	normalized, err :=
		s.normalizeInput(
			existing.Agent.ID,
			input.Name,
			input.Instruction,
			input.ModelID,
			input.WorkspaceMode,
			input.WorkspacePath,
		)

	if err != nil {
		return AgentInfo{},
			err
	}

	normalized.EnabledSkills, err = s.normalizeEnabledSkills(ctx, input.EnabledSkills)
	if err != nil {
		return AgentInfo{}, err
	}

	var normalizedMCPTools []humbertmcp.ToolSelection
	if input.EnabledMCPTools != nil {
		normalizedMCPTools, err = s.normalizeEnabledMCPTools(ctx, *input.EnabledMCPTools)
		if err != nil {
			return AgentInfo{}, err
		}
	}

	var normalizedBuiltinTools []string
	if input.EnabledBuiltinTools != nil {
		normalizedBuiltinTools, err = normalizeBuiltinToolSelection(*input.EnabledBuiltinTools)
		if err != nil {
			return AgentInfo{}, err
		}
	}
	var normalizedSandbox sandbox.AgentPolicy
	if input.Sandbox != nil {
		normalizedSandbox, err = normalizeSandboxPolicy(*input.Sandbox)
		if err != nil {
			return AgentInfo{}, err
		}
	}

	var normalizedModelRoles ModelRoles
	if input.ModelRoles != nil {
		normalizedModelRoles, err = s.normalizeAndValidateModelRoles(ctx, *input.ModelRoles)
		if err != nil {
			return AgentInfo{}, err
		}
	}

	if err :=
		s.ensureModelUsable(
			ctx,
			normalized.ModelID,
		); err != nil {

		return AgentInfo{},
			err
	}

	// Workspace 必须先准备成功，再更新 Agent Profile。
	//
	// 否则可能产生：
	//
	//	Profile 已经切到新 Workspace
	//	    ↓
	//	目录却无法访问
	//
	// 的半完成状态。
	if s.workspaces != nil {
		if _, err :=
			s.workspaces.Resolve(
				ctx,
				existing.Agent.ID,
				normalized.WorkspaceMode,
				normalized.WorkspacePath,
			); err != nil {

			return AgentInfo{},
				fmt.Errorf(
					"准备新的 Agent Workspace 失败: %w",
					err,
				)
		}
	}

	existing.Agent.Name =
		normalized.Name

	existing.Agent.Instruction =
		normalized.Instruction

	existing.Agent.ModelID =
		normalized.ModelID

	if input.ModelRoles != nil {
		existing.Agent.ModelRoles = normalizedModelRoles
	}

	existing.Agent.EnabledSkills = append([]string(nil), normalized.EnabledSkills...)

	if input.EnabledMCPTools != nil {
		existing.Agent.EnabledMCPTools = cloneMCPSelections(normalizedMCPTools)
	}
	if input.EnabledBuiltinTools != nil {
		existing.Agent.EnabledBuiltinTools = cloneStringsPreserveNil(normalizedBuiltinTools)
	}
	if input.Sandbox != nil {
		existing.Agent.Sandbox = normalizedSandbox
	}

	existing.Agent.WorkspaceMode =
		normalized.WorkspaceMode

	existing.Agent.WorkspacePath =
		normalized.WorkspacePath

	existing.Agent.UpdatedAt =
		time.Now().UTC()

	if err :=
		s.store.Update(
			ctx,
			existing.Agent,
		); err != nil {

		return AgentInfo{},
			err
	}

	s.logger.Info(
		ctx,
		"Agent Profile 已更新",
		"operation",
		"agent.update",
		"agent_id",
		id,
		"model_id",
		existing.Agent.ModelID,
		"skill_count",
		len(existing.Agent.EnabledSkills),
		"mcp_server_selection_count",
		len(existing.Agent.EnabledMCPTools),
		"builtin_tool_count",
		len(existing.Agent.EnabledBuiltinTools),
		"sandbox_profile",
		string(existing.Agent.Sandbox.Profile),
		"workspace_mode",
		string(
			existing.Agent.WorkspaceMode,
		),
	)

	return s.Get(
		ctx,
		id,
	)
}

// UpdateProfile 只修改 Agent 的身份与系统指令，不触碰模型、能力、Sandbox 或 Workspace。
func (s *Service) UpdateProfile(ctx context.Context, id, name, instruction string) (AgentInfo, error) {
	existing, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return AgentInfo{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AgentInfo{}, errors.New("Agent 名称不能为空")
	}
	if len([]rune(name)) > 100 {
		return AgentInfo{}, errors.New("Agent 名称不能超过 100 个字符")
	}
	existing.Agent.Name = name
	existing.Agent.Instruction = strings.TrimSpace(instruction)
	existing.Agent.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, existing.Agent); err != nil {
		return AgentInfo{}, err
	}
	return s.Get(ctx, existing.Agent.ID)
}

// SetModel 只修改 Agent 默认模型。局部命令不能覆盖其它 Profile 字段。
func (s *Service) SetModel(ctx context.Context, id, modelID string) (AgentInfo, error) {
	existing, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return AgentInfo{}, err
	}
	modelID = strings.TrimSpace(modelID)
	if err := s.ensureModelUsable(ctx, modelID); err != nil {
		return AgentInfo{}, err
	}
	existing.Agent.ModelID = modelID
	existing.Agent.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, existing.Agent); err != nil {
		return AgentInfo{}, err
	}
	return s.Get(ctx, existing.Agent.ID)
}

// SetModelRoles 只修改 Utility/Memory/Vision 模型角色，不触碰 Chat Model 或其它 Profile 字段。
func (s *Service) SetModelRoles(ctx context.Context, id string, roles ModelRoles) (AgentInfo, error) {
	existing, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return AgentInfo{}, err
	}
	normalized, err := s.normalizeAndValidateModelRoles(ctx, roles)
	if err != nil {
		return AgentInfo{}, err
	}
	existing.Agent.ModelRoles = normalized
	existing.Agent.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, existing.Agent); err != nil {
		return AgentInfo{}, err
	}
	return s.Get(ctx, existing.Agent.ID)
}

// SetSkills 只替换 Agent 的 Skill 引用。
func (s *Service) SetSkills(ctx context.Context, id string, skillNames []string) (AgentInfo, error) {
	existing, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return AgentInfo{}, err
	}
	normalized, err := s.normalizeEnabledSkills(ctx, skillNames)
	if err != nil {
		return AgentInfo{}, err
	}
	existing.Agent.EnabledSkills = append([]string(nil), normalized...)
	existing.Agent.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, existing.Agent); err != nil {
		return AgentInfo{}, err
	}
	return s.Get(ctx, existing.Agent.ID)
}

// UpdateSecurity 只替换 Agent 的内置工具选择与 Sandbox 覆盖。
func (s *Service) UpdateSecurity(ctx context.Context, id string, builtinTools []string, policy sandbox.AgentPolicy) (AgentInfo, error) {
	existing, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return AgentInfo{}, err
	}
	normalizedTools, err := normalizeBuiltinToolSelection(builtinTools)
	if err != nil {
		return AgentInfo{}, err
	}
	normalizedPolicy, err := normalizeSandboxPolicy(policy)
	if err != nil {
		return AgentInfo{}, err
	}
	existing.Agent.EnabledBuiltinTools = cloneStringsPreserveNil(normalizedTools)
	existing.Agent.Sandbox = normalizedPolicy
	existing.Agent.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, existing.Agent); err != nil {
		return AgentInfo{}, err
	}
	return s.Get(ctx, existing.Agent.ID)
}

// Delete 通过持久化状态机删除完整 Agent Aggregate。
//
// Agent 内部目录（Profile、Session sidecar、Memory 等）属于 Humbert 自有数据；删除 Agent
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

// enrichModelDisplayNames 将 Agent Profile 中的 ModelID 投影为 UI 需要的展示名称。
//
// 文件存储移除了原先 agents LEFT JOIN models 的能力，因此这一跨领域投影放在
// Service 层完成。一次 List 只读取一次 ModelRegistry，避免对每个 Agent 重复扫描
// models.json。模型已经被删除或 Agent 没有默认模型时，展示名称保持为空，Agent
// Profile 本身仍然可以被用户修复。
func (s *Service) enrichModelDisplayNames(
	ctx context.Context,
	values []AgentInfo,
) ([]AgentInfo, error) {
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

	modelValues, err := s.models.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 Agent Model 展示信息失败: %w", err)
	}

	displayNames := make(map[string]string, len(modelValues))
	for _, value := range modelValues {
		displayNames[value.Model.ID] = value.Model.DisplayName
	}

	for index := range values {
		values[index].ModelDisplayName = displayNames[values[index].Agent.ModelID]
	}

	return values, nil
}

type normalizedInput struct {
	Name string

	Instruction string

	ModelID string

	EnabledSkills []string

	WorkspaceMode workspace.Mode

	WorkspacePath string
}

func (s *Service) normalizeInput(
	agentID string,
	name string,
	instruction string,
	modelID string,
	workspaceMode workspace.Mode,
	workspacePath string,
) (normalizedInput, error) {
	name =
		strings.TrimSpace(
			name,
		)

	if name == "" {
		return normalizedInput{},
			errors.New(
				"Agent 名称不能为空",
			)
	}

	if len(name) >
		maxAgentNameLength {

		return normalizedInput{},
			fmt.Errorf(
				"Agent 名称长度不能超过 %d",
				maxAgentNameLength,
			)
	}

	instruction =
		strings.TrimSpace(
			instruction,
		)

	if len(instruction) >
		maxInstructionLength {

		return normalizedInput{},
			fmt.Errorf(
				"Agent Instruction 长度不能超过 %d 字节",
				maxInstructionLength,
			)
	}

	mode :=
		workspace.Mode(
			strings.ToLower(
				strings.TrimSpace(
					string(
						workspaceMode,
					),
				),
			),
		)

	if mode == "" {
		mode =
			workspace.ModeManaged
	}

	workspacePath =
		strings.TrimSpace(
			workspacePath,
		)

	switch mode {
	case workspace.ModeManaged:
		// Managed Workspace 的路径必须只由 Agent ID 推导。
		//
		// 即使恶意客户端提交 workspace_path，
		// 也不会进入 Agent Profile。
		workspacePath = ""

	case workspace.ModeCustom:
		if workspacePath == "" {
			return normalizedInput{},
				errors.New(
					"Custom Workspace 必须选择一个目录",
				)
		}

	default:
		return normalizedInput{},
			fmt.Errorf(
				"%w: %q",
				workspace.ErrInvalidMode,
				mode,
			)
	}

	// agentID 当前主要用于让 WorkspaceManager 做 UUID 校验。
	if s.workspaces != nil {
		if err :=
			s.workspaces.Validate(
				context.Background(),
				agentID,
				mode,
				workspacePath,
			); err != nil {

			// 真正带调用 Context 的校验还会在 Create/Update 中再次执行。
			//
			// 这里只处理完全不依赖 I/O 的明显错误。
			if errors.Is(
				err,
				workspace.ErrInvalidAgentID,
			) ||
				errors.Is(
					err,
					workspace.ErrInvalidMode,
				) ||
				errors.Is(
					err,
					workspace.ErrInvalidPath,
				) {

				return normalizedInput{},
					err
			}
		}
	}

	return normalizedInput{
		Name: name,

		Instruction: instruction,

		ModelID: strings.TrimSpace(
			modelID,
		),

		WorkspaceMode: mode,

		WorkspacePath: workspacePath,
	}, nil
}

// normalizeEnabledSkills 校验并规范化 Agent Skill 引用。
//
// 空选择始终合法；非空选择必须经过 Skill Catalog，防止 Profile 保存不存在或无效 Skill。
// Catalog 返回的稳定顺序也让 config.json diff、Runtime Snapshot Revision 与测试保持确定性。
func (s *Service) normalizeEnabledSkills(
	ctx context.Context,
	names []string,
) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if s.skills == nil {
		return nil, errors.New("Skill Catalog 未初始化，不能保存 enabled_skills")
	}
	result, err := s.skills.NormalizeAndValidateSelection(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("Agent Skill 配置无效: %w", err)
	}
	return result, nil
}

// normalizeEnabledMCPTools 校验并规范化 Agent MCP Tool 引用。
func (s *Service) normalizeEnabledMCPTools(
	ctx context.Context,
	values []humbertmcp.ToolSelection,
) ([]humbertmcp.ToolSelection, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if s.mcp == nil {
		return nil, errors.New("MCP Catalog 未初始化，不能保存 enabled_mcp_tools")
	}
	result, err := s.mcp.NormalizeAndValidateSelection(ctx, values)
	if err != nil {
		return nil, fmt.Errorf("Agent MCP Tool 配置无效: %w", err)
	}
	return cloneMCPSelections(result), nil
}

func cloneMCPSelections(values []humbertmcp.ToolSelection) []humbertmcp.ToolSelection {
	result := make([]humbertmcp.ToolSelection, len(values))
	for index, value := range values {
		result[index] = humbertmcp.ToolSelection{
			ServerID: value.ServerID,
			Tools:    append([]string(nil), value.Tools...),
		}
	}
	return result
}

// normalizeBuiltinToolSelection 只负责名称规范化；真实存在性由 Tool Registry 在 Runtime
// Resolve 时校验。这样 Agent Domain 不需要反向依赖 Application Tool Registry。
func normalizeBuiltinToolSelection(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if len(name) > 64 {
			return nil, fmt.Errorf("Builtin Tool 名称过长: %q", name)
		}
		for i, ch := range name {
			valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_'
			if !valid || (i == 0 && ch >= '0' && ch <= '9') {
				return nil, fmt.Errorf("Builtin Tool 名称无效: %q", name)
			}
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeSandboxPolicy(value sandbox.AgentPolicy) (sandbox.AgentPolicy, error) {
	if err := value.Validate(); err != nil {
		return sandbox.AgentPolicy{}, fmt.Errorf("Agent Sandbox 配置无效: %w", err)
	}
	// Profile / NetworkMode / NativeMode 为空都表示继承应用级默认策略。
	// 不在 Agent 保存时固化 fallback，否则修改全局默认值不会真正影响这些 Agent。
	if value.Profile != "" {
		value.Profile = sandbox.NormalizeProfile(value.Profile, sandbox.ProfileWorkspaceOnly)
	}
	if value.NetworkMode != "" {
		value.NetworkMode = sandbox.NormalizeNetworkMode(value.NetworkMode, sandbox.NetworkPublic)
	}
	if value.NativeMode != "" {
		value.NativeMode = sandbox.NormalizeNativeMode(value.NativeMode, sandbox.NativePreferred)
	}
	write, err := normalizeSandboxPaths(value.AdditionalWritePaths)
	if err != nil {
		return sandbox.AgentPolicy{}, err
	}
	value.AdditionalWritePaths = write
	return value, nil
}

func normalizeSandboxPaths(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, raw := range values {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		root, err := sandbox.CanonicalRoot(raw)
		if err != nil {
			return nil, fmt.Errorf("Sandbox 目录 %q 无效: %w", raw, err)
		}
		key := root
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, root)
	}
	sort.Strings(result)
	return result, nil
}

func cloneStringsPreserveNil(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

// SetMCPToolsForAgent 显式替换一个 Agent 的 MCP Tool Selection。
//
// 这是未来“连接器”设置页唯一写入口；普通 Agent 基础设置 Update 在没有提交该字段时会
// 保留当前选择，避免旧 UI 或其它领域操作意外清空 MCP 能力。
func (s *Service) SetMCPToolsForAgent(
	ctx context.Context,
	agentID string,
	values []humbertmcp.ToolSelection,
) (AgentInfo, error) {
	if ctx == nil {
		return AgentInfo{}, errors.New("保存 Agent MCP Tool 配置失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return AgentInfo{}, fmt.Errorf("保存 Agent MCP Tool 配置被取消: %w", err)
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return AgentInfo{}, errors.New("保存 Agent MCP Tool 配置失败: AgentID 不能为空")
	}

	normalized, err := s.normalizeEnabledMCPTools(ctx, values)
	if err != nil {
		return AgentInfo{}, err
	}
	existing, err := s.store.Get(ctx, agentID)
	if err != nil {
		return AgentInfo{}, fmt.Errorf("读取 Agent Profile 失败: %w", err)
	}
	existing.Agent.EnabledMCPTools = cloneMCPSelections(normalized)
	existing.Agent.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, existing.Agent); err != nil {
		return AgentInfo{}, fmt.Errorf("保存 Agent MCP Tool 配置失败: %w", err)
	}

	s.logger.Info(
		ctx,
		"Agent MCP Tool 配置已更新",
		"operation", "agent.mcp_tools.update",
		"agent_id", agentID,
		"server_selection_count", len(normalized),
	)
	return s.Get(ctx, agentID)
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

// EnableSkillForAgent 把一个已经安装且有效的 Skill 加入指定 Agent Profile。
//
// 该方法主要服务 install_skill Tool 的“安装后从下一 Turn 启用”语义。它不会改变当前正在
// 执行的 Runtime Snapshot：当前 Turn 的 SkillSet 已经冻结，只有下一次 ResolveTurn 才会读取
// 更新后的 enabled_skills。这样既避免运行中能力漂移，也让用户通过对话安装 Skill 后无需再
// 打开设置页手工勾选。
//
// 为避免出现重复名称，现有 EnabledSkills 与新名称一起重新经过 Skill Catalog 规范化与校验。
func (s *Service) EnableSkillForAgent(
	ctx context.Context,
	agentID string,
	skillName string,
) (AgentInfo, error) {
	if ctx == nil {
		return AgentInfo{}, errors.New("启用 Agent Skill 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return AgentInfo{}, fmt.Errorf("启用 Agent Skill 被取消: %w", err)
	}
	agentID = strings.TrimSpace(agentID)
	skillName = strings.TrimSpace(skillName)
	if agentID == "" || skillName == "" {
		return AgentInfo{}, errors.New("启用 Agent Skill 失败: AgentID/SkillName 不能为空")
	}

	existing, err := s.Get(ctx, agentID)
	if err != nil {
		return AgentInfo{}, fmt.Errorf("读取 Agent Profile 失败: %w", err)
	}
	for _, enabled := range existing.Agent.EnabledSkills {
		if enabled == skillName {
			return existing, nil
		}
	}

	next := append([]string(nil), existing.Agent.EnabledSkills...)
	next = append(next, skillName)
	updated, err := s.SetSkills(ctx, agentID, next)
	if err != nil {
		return AgentInfo{}, fmt.Errorf("保存 Agent Skill 配置失败: %w", err)
	}

	s.logger.Info(
		ctx,
		"Agent Skill 已启用",
		"operation", "agent.skill.enable",
		"agent_id", agentID,
		"skill_name", skillName,
	)
	return updated, nil
}

// DisableSkillForAgent 从指定 Agent Profile 中移除一个 Skill 引用。
//
// 与 Enable 不同，Disable 不要求目标 Skill 当前仍然存在或有效：用户可能正是在修复一个
// 已被手工删除/破坏的 stale enabled_skills 引用。移除能力本身是单调收紧操作，因此这里
// 直接更新 Profile，不让其它遗留坏引用阻塞本次修复；后续普通 Agent Update 仍会对完整
// enabled_skills 做严格 Catalog 校验。
//
// 当前正在执行的 Runtime Snapshot 已经冻结，因此该修改仍只影响下一 Turn。
func (s *Service) DisableSkillForAgent(
	ctx context.Context,
	agentID string,
	skillName string,
) (AgentInfo, error) {
	if ctx == nil {
		return AgentInfo{}, errors.New("禁用 Agent Skill 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return AgentInfo{}, fmt.Errorf("禁用 Agent Skill 被取消: %w", err)
	}
	agentID = strings.TrimSpace(agentID)
	skillName = strings.TrimSpace(skillName)
	if agentID == "" || skillName == "" {
		return AgentInfo{}, errors.New("禁用 Agent Skill 失败: AgentID/SkillName 不能为空")
	}

	existing, err := s.store.Get(ctx, agentID)
	if err != nil {
		return AgentInfo{}, fmt.Errorf("读取 Agent Profile 失败: %w", err)
	}

	next := make([]string, 0, len(existing.Agent.EnabledSkills))
	removed := false
	for _, enabled := range existing.Agent.EnabledSkills {
		if strings.TrimSpace(enabled) == skillName {
			removed = true
			continue
		}
		next = append(next, enabled)
	}
	if !removed {
		return s.Get(ctx, agentID)
	}

	existing.Agent.EnabledSkills = next
	existing.Agent.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, existing.Agent); err != nil {
		return AgentInfo{}, fmt.Errorf("保存 Agent Skill 配置失败: %w", err)
	}

	s.logger.Info(
		ctx,
		"Agent Skill 已禁用",
		"operation", "agent.skill.disable",
		"agent_id", agentID,
		"skill_name", skillName,
	)
	return s.Get(ctx, agentID)
}

// AgentsUsingSkill 返回当前引用指定 Skill 的 Agent。
//
// SkillService 在删除安装包前使用该方法做引用保护。它只扫描 Agent Profile，不修改任何
// Profile；用户必须先从相关 Agent 中取消 Skill，再显式删除安装包。
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

// ensureModelUsable 校验 Agent 默认 Model。
//
// Agent 允许暂时没有默认模型。
// 一旦指定 Model，则必须存在且处于 Enabled 状态。
func (s *Service) normalizeAndValidateModelRoles(ctx context.Context, roles ModelRoles) (ModelRoles, error) {
	roles = ModelRoles{
		UtilityModelID: strings.TrimSpace(roles.UtilityModelID),
		MemoryModelID:  strings.TrimSpace(roles.MemoryModelID),
		VisionModelID:  strings.TrimSpace(roles.VisionModelID),
	}
	for label, modelID := range map[string]string{
		"Utility": roles.UtilityModelID,
		"Memory":  roles.MemoryModelID,
		"Vision":  roles.VisionModelID,
	} {
		if modelID == "" {
			continue
		}
		if err := s.ensureModelUsable(ctx, modelID); err != nil {
			return ModelRoles{}, fmt.Errorf("%s Model 无效: %w", label, err)
		}
	}
	return roles, nil
}

func (s *Service) ensureModelUsable(
	ctx context.Context,
	modelID string,
) error {
	modelID =
		strings.TrimSpace(
			modelID,
		)

	if modelID == "" {
		return nil
	}

	if s.models == nil {
		return errors.New(
			"Model Registry 未初始化",
		)
	}

	modelList, err :=
		s.models.ListModels(
			ctx,
		)

	if err != nil {
		return fmt.Errorf(
			"读取 Model Registry 失败: %w",
			err,
		)
	}

	for _, item := range modelList {

		if item.Model.ID !=
			modelID {

			continue
		}

		if !item.Model.Enabled {
			return fmt.Errorf(
				"%w: %s",
				models.ErrModelDisabled,
				modelID,
			)
		}

		return nil
	}

	return fmt.Errorf(
		"%w: %s",
		models.ErrModelNotFound,
		modelID,
	)
}

func (s *Service) rollbackCreatedAgent(
	id string,
) error {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			agentCreateRollbackTimeout,
		)

	defer cancel()

	state, err := s.store.BeginDelete(ctx, id)
	if err != nil {
		return err
	}
	return s.resumeDeletion(ctx, state)
}
