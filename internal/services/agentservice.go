package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const agentServiceTimeout = 10 * time.Second

// AgentDTO 是 Wails 暴露给 Vue 的 Agent 数据。
//
// workspacePath:
//
//   - managed 时为空；
//   - custom 时为用户选择路径。
//
// workspaceDisplayPath:
//
//   - managed 时返回真正的 ~/.humbert-agent/workspaces/<agent-id>；
//   - custom 时返回用户选择目录。
type AgentDTO struct {
	ID                     string             `json:"id"`
	Name                   string             `json:"name"`
	Avatar                 string             `json:"avatar"`
	SubagentEnabled        bool               `json:"subagentEnabled"`
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

// SandboxPolicyDTO 是 Agent Profile 中可编辑的 Sandbox 覆盖配置。
type SandboxPolicyDTO struct {
	Profile              string   `json:"profile"`
	AdditionalWritePaths []string `json:"additionalWritePaths"`
	NetworkMode          string   `json:"networkMode"`
	NativeMode           string   `json:"nativeMode"`
}

// AgentSecurityRequest 只更新 Agent 的 Capability/Sandbox，不影响 Model、Workspace 或 Skills。
type AgentSecurityRequest struct {
	EnabledBuiltinTools []string         `json:"enabledBuiltinTools"`
	Sandbox             SandboxPolicyDTO `json:"sandbox"`
}

// AgentProfileRequest 只修改 Agent 身份/系统指令。
type AgentProfileRequest struct {
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	Instruction string `json:"instruction"`
}

// AgentModelRequest 只修改默认模型。
type AgentModelRequest struct {
	ModelID string `json:"modelID"`
}

// AgentModelRolesDTO 是 Agent 的可选辅助模型角色。Chat Model 仍由 ModelID 表示。
type AgentModelRolesDTO struct {
	UtilityModelID string `json:"utilityModelID"`
}

// AgentModelRolesRequest 只更新辅助模型角色。空字符串表示使用 Runtime 回退链。
type AgentModelRolesRequest struct {
	UtilityModelID string `json:"utilityModelID"`
}

// AgentSkillsRequest 只修改启用的 Skill 引用。
type AgentSkillsRequest struct {
	EnabledSkills []string `json:"enabledSkills"`
}

// BuiltinToolDTO 是 Settings/Agent UI 可选择的内置 Capability。
type BuiltinToolDTO struct {
	Name     string `json:"name"`
	Risk     string `json:"risk"`
	Category string `json:"category"`
	Label    string `json:"label"`
}

// SandboxStatusDTO 描述当前平台实际可用的原生隔离能力及全局默认值。
type SandboxStatusDTO struct {
	Platform             string `json:"platform"`
	Backend              string `json:"backend"`
	Available            bool   `json:"available"`
	Reason               string `json:"reason"`
	Filesystem           bool   `json:"filesystem"`
	ProcessTree          bool   `json:"processTree"`
	Network              bool   `json:"network"`
	DefaultProfile       string `json:"defaultProfile"`
	DefaultNetworkMode   string `json:"defaultNetworkMode"`
	DefaultNativeMode    string `json:"defaultNativeMode"`
	CommandGracePeriodMS int    `json:"commandGracePeriodMS"`
	ShellEnabled         bool   `json:"shellEnabled"`
	ShellRuntimeActive   bool   `json:"shellRuntimeActive"`
}

// SandboxSettingsRequest 是 Settings/Sandbox 可修改的应用级默认策略。
type SandboxSettingsRequest struct {
	DefaultProfile       string `json:"defaultProfile"`
	DefaultNetworkMode   string `json:"defaultNetworkMode"`
	DefaultNativeMode    string `json:"defaultNativeMode"`
	CommandGracePeriodMS int    `json:"commandGracePeriodMS"`
	ShellEnabled         bool   `json:"shellEnabled"`
}

// SandboxDiagnosticCheckDTO 复用领域层自检结果，保持桌面 API 的字段名和 JSON 结构。
type SandboxDiagnosticCheckDTO = sandbox.DiagnosticCheck

// SandboxDiagnosticsDTO 是用户显式运行安全自检后的实测结果。
type SandboxDiagnosticsDTO = sandbox.Diagnostics

// CreateAgentRequest 是创建 Agent 的 Desktop DTO。
type CreateAgentRequest struct {
	Name                   string             `json:"name"`
	Avatar                 string             `json:"avatar"`
	SubagentEnabled        bool               `json:"subagentEnabled"`
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

// UpdateAgentRequest 是修改 Agent 的 Desktop DTO。
type UpdateAgentRequest struct {
	Name                string              `json:"name"`
	Avatar              string              `json:"avatar"`
	SubagentEnabled     bool                `json:"subagentEnabled"`
	Instruction         string              `json:"instruction"`
	ModelID             string              `json:"modelID"`
	ModelRoles          *AgentModelRolesDTO `json:"modelRoles"`
	EnabledSkills       []string            `json:"enabledSkills"`
	WorkspaceMode       string              `json:"workspaceMode"`
	WorkspacePath       string              `json:"workspacePath"`
	EnabledBuiltinTools *[]string           `json:"enabledBuiltinTools"`

	// nil 表示未提交；空对象表示恢复应用默认策略。
	Sandbox *SandboxPolicyDTO `json:"sandbox"`
}

// AgentService 是 Agent Domain Service 的 Wails Adapter。
//
// 文件夹选择也放在这里，是因为 Native Dialog 属于 Desktop Adapter，
// 不应该进入 workspace 或 agents Domain Package。
type AgentService struct {
	deps AgentDependencies
}

// NewAgentService 创建 Agent Desktop Service。
func NewAgentService(deps AgentDependencies) *AgentService { return &AgentService{deps: deps} }

// ServiceName 返回 Wails Service Name。
func (s *AgentService) ServiceName() string {
	return "AgentService"
}

// ListAgents 返回全部 Agent。
func (s *AgentService) ListAgents() ([]AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)

	defer cancel()

	values, err := s.deps.Agents.List(ctx)

	if err != nil {
		return nil, fmt.Errorf("读取 Agent 列表失败: %w", err)
	}

	result := make([]AgentDTO, 0, len(values))
	if len(values) == 0 {
		return result, nil
	}
	// 工具目录和平台状态属于应用，不属于某个 Agent。一次列表请求只投影一次，
	// 避免 Agent 越多越频繁读取、排序 Registry，也使这一批 DTO 使用一致的展示信息。
	catalog, status := s.ListBuiltinTools(), s.GetSandboxStatus()

	for _, value := range values {

		dto, err := s.toDTOWithCatalog(value, catalog, status)

		if err != nil {
			return nil, err
		}

		result = append(result, dto)
	}

	return result, nil
}

// GetAgent 返回指定 Agent。
func (s *AgentService) GetAgent(id string) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)

	defer cancel()

	value, err := s.deps.Agents.Get(ctx, id)

	if err != nil {
		return AgentDTO{}, fmt.Errorf("读取 Agent 失败: %w", err)
	}

	return s.toDTO(value)
}

// CreateAgent 创建 Agent。
func (s *AgentService) CreateAgent(request CreateAgentRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)

	defer cancel()

	createInput := agents.CreateInput{Sandbox: sandboxPolicyFromDTO(request.Sandbox), SubagentEnabled: request.SubagentEnabled}
	if request.BuiltinToolsConfigured {
		if err := s.validateBuiltinToolNames(request.EnabledBuiltinTools); err != nil {
			return AgentDTO{}, err
		}
		createInput.EnabledBuiltinTools = request.EnabledBuiltinTools
	}

	value, err :=
		s.deps.Agents.
			Create(
				ctx,
				agents.CreateInput{
					Name: request.Name,

					Avatar: request.Avatar,

					SubagentEnabled: createInput.SubagentEnabled,

					Instruction: request.Instruction,

					ModelID: request.ModelID,

					ModelRoles: modelRolesFromDTO(request.ModelRoles),

					EnabledSkills:       append([]string(nil), request.EnabledSkills...),
					EnabledBuiltinTools: createInput.EnabledBuiltinTools,
					Sandbox:             createInput.Sandbox,

					WorkspaceMode: workspace.Mode(request.WorkspaceMode),

					WorkspacePath: request.WorkspacePath,
				},
			)

	if err != nil {
		return AgentDTO{}, fmt.Errorf("创建 Agent 失败: %w", err)
	}

	return s.toDTO(value)
}

// UpdateAgent 修改 Agent。
func (s *AgentService) UpdateAgent(id string, request UpdateAgentRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)

	defer cancel()

	var enabledBuiltinTools *[]string
	var sandboxPolicy *sandbox.AgentPolicy
	var modelRoles *agents.ModelRoles
	if request.ModelRoles != nil {
		roles := modelRolesFromDTO(*request.ModelRoles)
		modelRoles = &roles
	}
	if request.Sandbox != nil {
		policy := sandboxPolicyFromDTO(*request.Sandbox)
		sandboxPolicy = &policy
	}
	if request.EnabledBuiltinTools != nil {
		if err := s.validateBuiltinToolNames(*request.EnabledBuiltinTools); err != nil {
			return AgentDTO{}, err
		}
		enabled := *request.EnabledBuiltinTools
		enabledBuiltinTools = &enabled
	}
	subagentEnabled := request.SubagentEnabled

	value, err :=
		s.deps.Agents.
			Update(
				ctx,
				id,
				agents.UpdateInput{
					Name: request.Name,

					Avatar: request.Avatar,

					SubagentEnabled: &subagentEnabled,

					Instruction: request.Instruction,

					ModelID: request.ModelID,

					ModelRoles: modelRoles,

					EnabledSkills:       append([]string(nil), request.EnabledSkills...),
					EnabledBuiltinTools: enabledBuiltinTools,
					Sandbox:             sandboxPolicy,

					WorkspaceMode: workspace.Mode(request.WorkspaceMode),

					WorkspacePath: request.WorkspacePath,
				},
			)

	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent 失败: %w", err)
	}

	return s.toDTO(value)
}

// UpdateAgentProfile 只修改 Agent 名称与 Instruction。
func (s *AgentService) UpdateAgentProfile(id string, request AgentProfileRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.deps.Agents.UpdateProfile(ctx, id, request.Name, request.Avatar, request.Instruction)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent Profile 失败: %w", err)
	}
	return s.toDTO(value)
}

// SetAgentModel 只修改 Agent 默认模型。
func (s *AgentService) SetAgentModel(id string, request AgentModelRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.deps.Agents.SetModel(ctx, id, request.ModelID)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("切换 Agent Model 失败: %w", err)
	}
	return s.toDTO(value)
}

// SetAgentModelRoles 只修改 Utility 模型角色。
func (s *AgentService) SetAgentModelRoles(id string, request AgentModelRolesRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.deps.Agents.SetModelRoles(ctx, id, agents.ModelRoles{UtilityModelID: request.UtilityModelID})
	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent Model Roles 失败: %w", err)
	}
	return s.toDTO(value)
}

// SetAgentSkills 只修改 Agent Skill 选择。
func (s *AgentService) SetAgentSkills(id string, request AgentSkillsRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.deps.Agents.SetSkills(ctx, id, request.EnabledSkills)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent Skills 失败: %w", err)
	}
	return s.toDTO(value)
}

// ListBuiltinTools 返回当前 Registry 中可供 Agent 选择的 Builtin Tool。
func (s *AgentService) ListBuiltinTools() []BuiltinToolDTO {
	descriptors := s.deps.Tools.List()
	result := make([]BuiltinToolDTO, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.Internal {
			continue
		}
		// MCP Tool 不进入 Builtin Registry；保留判断可防未来 Registry 合并时误暴露。
		if descriptor.MCPOrigin != nil {
			continue
		}
		result = append(result, BuiltinToolDTO{
			Name:     descriptor.Name,
			Risk:     string(descriptor.Risk),
			Category: builtinToolCategory(descriptor.Name),
			Label:    builtinToolLabel(descriptor.Name),
		})
	}
	return result
}

// GetSandboxStatus 返回当前平台能力探测结果，不触发任何子进程。
func (s *AgentService) GetSandboxStatus() SandboxStatusDTO {
	manager := s.deps.Sandbox
	capability := manager.Capability()
	cfg := manager.Config()
	runtimeShellActive := false
	for _, descriptor := range s.deps.Tools.List() {
		if descriptor.Name == "run_command" {
			runtimeShellActive = true
			break
		}
	}
	return SandboxStatusDTO{
		Platform: capability.Platform, Backend: capability.Backend, Available: capability.Available,
		Reason: capability.Reason, Filesystem: capability.Filesystem, ProcessTree: capability.ProcessTree,
		Network: capability.Network, DefaultProfile: string(cfg.DefaultProfile),
		DefaultNetworkMode: string(cfg.DefaultNetworkMode), DefaultNativeMode: string(cfg.DefaultNativeMode),
		CommandGracePeriodMS: int(cfg.CommandGracePeriod / time.Millisecond),
		ShellEnabled:         s.deps.Config.Security.ShellEnabled,
		ShellRuntimeActive:   runtimeShellActive,
	}
}

// UpdateSandboxSettings 持久化应用级 Sandbox 默认策略，并立即更新当前进程中的 Manager。
// 已经开始的 Runtime Turn 使用冻结的 EffectivePolicy，不会被设置页中途改变。
func (s *AgentService) UpdateSandboxSettings(request SandboxSettingsRequest) (SandboxStatusDTO, error) {
	cfg := config.NormalizeSandboxConfig(config.SandboxConfig{
		DefaultProfile:       request.DefaultProfile,
		DefaultNetworkMode:   request.DefaultNetworkMode,
		NativeMode:           request.DefaultNativeMode,
		CommandGracePeriodMS: request.CommandGracePeriodMS,
	})
	if err := config.ValidateSandboxConfig(cfg); err != nil {
		return SandboxStatusDTO{}, fmt.Errorf("Sandbox 设置无效: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	if err := config.SaveSandboxAndShellConfig(ctx, s.deps.Config.Paths.ConfigFile, cfg, request.ShellEnabled); err != nil {
		return SandboxStatusDTO{}, err
	}

	runtimeCfg := sandbox.Config{
		DefaultProfile:     sandbox.Profile(cfg.DefaultProfile),
		DefaultNetworkMode: sandbox.NetworkMode(cfg.DefaultNetworkMode),
		DefaultNativeMode:  sandbox.NativeMode(cfg.NativeMode),
		CommandGracePeriod: time.Duration(cfg.CommandGracePeriodMS) * time.Millisecond,
	}
	if err := s.deps.Sandbox.UpdateConfig(runtimeCfg); err != nil {
		return SandboxStatusDTO{}, fmt.Errorf("更新运行时 Sandbox Policy 失败: %w", err)
	}
	s.deps.Config.Security.Sandbox = cfg
	s.deps.Config.Security.ShellEnabled = request.ShellEnabled

	s.deps.Logger.Info(
		ctx,
		"Sandbox 默认策略已更新",
		"operation", "sandbox.settings.update",
		"default_profile", cfg.DefaultProfile,
		"default_network_mode", cfg.DefaultNetworkMode,
		"native_mode", cfg.NativeMode,
		"command_grace_period_ms", cfg.CommandGracePeriodMS,
		"shell_enabled", request.ShellEnabled,
	)
	return s.GetSandboxStatus(), nil
}

// RunSandboxDiagnostics 在临时目录中验证 PathGuard，并在平台支持时实际验证原生文件系统隔离。
// 自检不会读取用户文件，也不会修改 Workspace；所有探针文件都位于 OS 临时目录。
func (s *AgentService) RunSandboxDiagnostics() (SandboxDiagnosticsDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	return s.deps.Sandbox.Diagnose(ctx, s.deps.Config.Paths.SecretsDir, s.deps.Config.Paths.ConfigFile)
}

// UpdateAgentSecurity 显式替换 Agent Builtin Tool 与 Sandbox 配置。
func (s *AgentService) UpdateAgentSecurity(id string, request AgentSecurityRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	enabled := request.EnabledBuiltinTools
	if err := s.validateBuiltinToolNames(enabled); err != nil {
		return AgentDTO{}, err
	}
	policy := sandbox.AgentPolicy{
		Profile:              sandbox.Profile(request.Sandbox.Profile),
		AdditionalWritePaths: append([]string(nil), request.Sandbox.AdditionalWritePaths...),
		NetworkMode:          sandbox.NetworkMode(request.Sandbox.NetworkMode),
		NativeMode:           sandbox.NativeMode(request.Sandbox.NativeMode),
	}
	updated, err := s.deps.Agents.UpdateSecurity(ctx, id, enabled, policy)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent Security 失败: %w", err)
	}
	return s.toDTO(updated)
}

// SelectSandboxDirectory 打开原生目录选择器，用于配置额外写入目录。
func (s *AgentService) SelectSandboxDirectory(currentPath string) (string, error) {
	return selectDirectory("选择 Sandbox 目录", currentPath, true)
}

// DeleteAgent 删除完整 Agent Aggregate。
//
// 删除 Agent 会一并删除它的全部 Session、附件、Profile 与 Managed
// Workspace；Custom Workspace 只解除引用。
func (s *AgentService) DeleteAgent(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	if err := s.deps.Lifecycle.DeleteAgent(ctx, id); err != nil {
		return fmt.Errorf("删除 Agent 失败: %w", err)
	}
	return nil
}

// SelectWorkspaceDirectory 打开系统原生目录选择器。
//
// currentPath 只用于指定 Dialog 初始目录。
// 即使该参数来自前端，也绝不会被直接当作 Agent Workspace 保存；
// 真正保存前仍然会经过 WorkspaceManager.Validate。
//
// 用户取消时返回空字符串。
func (s *AgentService) SelectWorkspaceDirectory(currentPath string) (string, error) {
	return selectDirectory("选择 Agent Workspace", currentPath, true)
}

// selectDirectory 共用原生目录选择逻辑；是否允许新建目录仍由具体功能决定。
func selectDirectory(title, currentPath string, canCreate bool) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("Wails Application 尚未初始化")
	}
	dialog := app.Dialog.OpenFile().SetTitle(title).CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(canCreate)
	currentPath = strings.TrimSpace(currentPath)
	if currentPath != "" {
		if info, err := os.Stat(currentPath); err == nil && info.IsDir() {
			dialog.SetDirectory(currentPath)
		}
	}
	path, err := dialog.PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("打开目录选择器失败: %w", err)
	}
	return strings.TrimSpace(path), nil
}

func (s *AgentService) toDTO(value agents.AgentInfo) (AgentDTO, error) {
	return s.toDTOWithCatalog(value, s.ListBuiltinTools(), s.GetSandboxStatus())
}

// toDTOWithCatalog 复用本次请求的展示快照，不缓存跨请求的平台策略。
// 每个 DTO 仍复制可选择工具的切片，调用方修改一项不会影响列表中的其他 Agent。
func (s *AgentService) toDTOWithCatalog(value agents.AgentInfo, catalog []BuiltinToolDTO, status SandboxStatusDTO) (AgentDTO, error) {
	displayPath := value.Agent.WorkspacePath

	if value.Agent.WorkspaceMode ==
		workspace.ModeManaged {

		path, err := s.deps.Workspaces.ManagedPath(value.Agent.ID)

		if err != nil {
			return AgentDTO{}, fmt.Errorf("计算 Agent Managed Workspace 路径失败: %w", err)
		}

		displayPath = path
	}

	return AgentDTO{
		ID: value.Agent.ID,

		Name: value.Agent.Name,

		Avatar: value.Agent.Avatar,

		SubagentEnabled: value.Agent.SubagentEnabled,

		Instruction: value.Agent.Instruction,

		ModelID: value.Agent.ModelID,

		ModelDisplayName: value.ModelDisplayName,

		ModelRoles: modelRolesDTO(value.Agent.ModelRoles),

		EnabledSkills: append([]string(nil), value.Agent.EnabledSkills...),

		EnabledBuiltinTools:    value.Agent.EnabledBuiltinTools,
		BuiltinToolsConfigured: value.Agent.EnabledBuiltinTools != nil,
		AvailableBuiltinTools:  append([]BuiltinToolDTO{}, catalog...),
		Sandbox:                sandboxPolicyDTO(value.Agent.Sandbox),
		SandboxStatus:          status,

		WorkspaceMode: string(value.Agent.WorkspaceMode),

		WorkspacePath: value.Agent.WorkspacePath,

		WorkspaceDisplayPath: displayPath,

		CreatedAt: value.Agent.CreatedAt.UTC().Format(time.RFC3339Nano),

		UpdatedAt: value.Agent.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func modelRolesFromDTO(value AgentModelRolesDTO) agents.ModelRoles {
	return agents.ModelRoles{UtilityModelID: value.UtilityModelID}
}

func modelRolesDTO(value agents.ModelRoles) AgentModelRolesDTO {
	return AgentModelRolesDTO{UtilityModelID: value.UtilityModelID}
}

func sandboxPolicyFromDTO(value SandboxPolicyDTO) sandbox.AgentPolicy {
	return sandbox.AgentPolicy{
		Profile:              sandbox.Profile(value.Profile),
		AdditionalWritePaths: append([]string(nil), value.AdditionalWritePaths...), NetworkMode: sandbox.NetworkMode(value.NetworkMode), NativeMode: sandbox.NativeMode(value.NativeMode),
	}
}

func sandboxPolicyDTO(value sandbox.AgentPolicy) SandboxPolicyDTO {
	return SandboxPolicyDTO{
		Profile:              string(value.Profile),
		AdditionalWritePaths: append([]string(nil), value.AdditionalWritePaths...),
		NetworkMode:          string(value.NetworkMode),
		NativeMode:           string(value.NativeMode),
	}
}

func (s *AgentService) validateBuiltinToolNames(values []string) error {
	known := make(map[string]struct{})
	for _, descriptor := range s.deps.Tools.List() {
		if descriptor.MCPOrigin == nil {
			known[descriptor.Name] = struct{}{}
		}
	}
	for _, raw := range values {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
			return fmt.Errorf("Builtin Tool 不存在或当前配置未启用: %s", name)
		}
	}
	return nil
}

func builtinToolCategory(name string) string {
	switch name {
	case "list_files", "read_file", "write_file", "edit_file", "glob_files", "grep_files", "apply_patch", "copy_file", "move_file", "delete_file":
		return "files"
	case "run_command":
		return "execution"
	case "git_status", "git_diff", "git_log":
		return "git"
	case "web_search", "web_fetch", "browser":
		return "web"
	case "install_skill":
		return "skills"
	case "list_agents", "run_agent":
		return "collaboration"
	case "get_current_time", "update_plan", "schedule_task":
		return "agent"
	default:
		return "other"
	}
}

func builtinToolLabel(name string) string {
	labels := map[string]string{
		"list_files": "列出文件", "read_file": "读取文件", "write_file": "写入文件", "edit_file": "编辑文件",
		"glob_files": "按名称查找文件", "grep_files": "搜索文件内容", "apply_patch": "批量应用补丁",
		"copy_file": "复制文件", "move_file": "移动文件", "delete_file": "删除文件", "run_command": "执行本地命令",
		"git_status": "Git 状态", "git_diff": "Git Diff", "git_log": "Git 历史", "web_search": "网页搜索",
		"web_fetch": "读取网页", "browser": "浏览网页与截图", "install_skill": "安装 Skill", "get_current_time": "当前时间", "update_plan": "更新计划", "schedule_task": "安排提醒或任务",
		"list_agents": "查看可用 Agent", "run_agent": "调用专业 Agent",
	}
	if label := labels[name]; label != "" {
		return label
	}
	return name
}
