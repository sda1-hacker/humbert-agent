package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"

	"github.com/wailsapp/wails/v3/pkg/application"
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
	ID string `json:"id"`

	Name string `json:"name"`

	Avatar string `json:"avatar"`

	SubagentEnabled bool `json:"subagentEnabled"`

	Instruction string `json:"instruction"`

	ModelID string `json:"modelID"`

	ModelDisplayName string `json:"modelDisplayName"`

	ModelRoles AgentModelRolesDTO `json:"modelRoles"`

	EnabledSkills []string `json:"enabledSkills"`

	EnabledBuiltinTools    []string         `json:"enabledBuiltinTools"`
	BuiltinToolsConfigured bool             `json:"builtinToolsConfigured"`
	AvailableBuiltinTools  []BuiltinToolDTO `json:"availableBuiltinTools"`
	Sandbox                SandboxPolicyDTO `json:"sandbox"`
	SandboxStatus          SandboxStatusDTO `json:"sandboxStatus"`

	WorkspaceMode string `json:"workspaceMode"`

	WorkspacePath string `json:"workspacePath"`

	WorkspaceDisplayPath string `json:"workspaceDisplayPath"`

	CreatedAt string `json:"createdAt"`

	UpdatedAt string `json:"updatedAt"`
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
	MemoryModelID  string `json:"memoryModelID"`
}

// AgentModelRolesRequest 只更新辅助模型角色。空字符串表示使用 Runtime 回退链。
type AgentModelRolesRequest struct {
	UtilityModelID string `json:"utilityModelID"`
	MemoryModelID  string `json:"memoryModelID"`
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
	Platform             string   `json:"platform"`
	Backend              string   `json:"backend"`
	Available            bool     `json:"available"`
	Reason               string   `json:"reason"`
	Filesystem           bool     `json:"filesystem"`
	ProcessTree          bool     `json:"processTree"`
	Network              bool     `json:"network"`
	DefaultProfile       string   `json:"defaultProfile"`
	DefaultNetworkMode   string   `json:"defaultNetworkMode"`
	DefaultNativeMode    string   `json:"defaultNativeMode"`
	CommandGracePeriodMS int      `json:"commandGracePeriodMS"`
	ShellEnabled         bool     `json:"shellEnabled"`
	ShellAllowedCommands []string `json:"shellAllowedCommands"`
	ShellRuntimeActive   bool     `json:"shellRuntimeActive"`
}

// SandboxSettingsRequest 是 Settings/Sandbox 可修改的应用级默认策略。
type SandboxSettingsRequest struct {
	DefaultProfile       string   `json:"defaultProfile"`
	DefaultNetworkMode   string   `json:"defaultNetworkMode"`
	DefaultNativeMode    string   `json:"defaultNativeMode"`
	CommandGracePeriodMS int      `json:"commandGracePeriodMS"`
	ShellEnabled         bool     `json:"shellEnabled"`
	ShellAllowedCommands []string `json:"shellAllowedCommands"`
}

// SandboxDiagnosticCheckDTO 是一次安全自检的单项结果。
type SandboxDiagnosticCheckDTO struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// SandboxDiagnosticsDTO 汇总 PathGuard 与原生 Process Sandbox 的实测结果。
type SandboxDiagnosticsDTO struct {
	Summary string                      `json:"summary"`
	Checks  []SandboxDiagnosticCheckDTO `json:"checks"`
}

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
	Name                   string             `json:"name"`
	Avatar                 string             `json:"avatar"`
	SubagentEnabled        bool               `json:"subagentEnabled"`
	Instruction            string             `json:"instruction"`
	ModelID                string             `json:"modelID"`
	ModelRolesConfigured   bool               `json:"modelRolesConfigured"`
	ModelRoles             AgentModelRolesDTO `json:"modelRoles"`
	EnabledSkills          []string           `json:"enabledSkills"`
	WorkspaceMode          string             `json:"workspaceMode"`
	WorkspacePath          string             `json:"workspacePath"`
	BuiltinToolsConfigured bool               `json:"builtinToolsConfigured"`
	EnabledBuiltinTools    []string           `json:"enabledBuiltinTools"`

	// SandboxConfigured 区分“调用方没有修改 Sandbox”和“显式把 Sandbox
	// 改回继承应用默认值”。这与 BuiltinToolsConfigured 的语义一致，避免
	// 只修改 Model 等其它字段时把安全策略意外覆盖为零值。
	SandboxConfigured bool             `json:"sandboxConfigured"`
	Sandbox           SandboxPolicyDTO `json:"sandbox"`
}

// AgentService 是 Agent Domain Service 的 Wails Adapter。
//
// 文件夹选择也放在这里，是因为 Native Dialog 属于 Desktop Adapter，
// 不应该进入 workspace 或 agents Domain Package。
type AgentService struct {
	core *coreapp.Application
}

// NewAgentService 创建 Agent Desktop Service。
func NewAgentService(
	core *coreapp.Application,
) *AgentService {
	return &AgentService{
		core: core,
	}
}

// ServiceName 返回 Wails Service Name。
func (s *AgentService) ServiceName() string {
	return "AgentService"
}

// ListAgents 返回全部 Agent。
func (s *AgentService) ListAgents() (
	[]AgentDTO,
	error,
) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			agentServiceTimeout,
		)

	defer cancel()

	values, err :=
		s.core.Agents().
			List(ctx)

	if err != nil {
		return nil, fmt.Errorf(
			"读取 Agent 列表失败: %w",
			err,
		)
	}

	result :=
		make(
			[]AgentDTO,
			0,
			len(values),
		)

	for _, value := range values {

		dto, err :=
			s.toDTO(value)

		if err != nil {
			return nil, err
		}

		result = append(
			result,
			dto,
		)
	}

	return result, nil
}

// GetAgent 返回指定 Agent。
func (s *AgentService) GetAgent(
	id string,
) (
	AgentDTO,
	error,
) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			agentServiceTimeout,
		)

	defer cancel()

	value, err :=
		s.core.Agents().
			Get(
				ctx,
				id,
			)

	if err != nil {
		return AgentDTO{},
			fmt.Errorf(
				"读取 Agent 失败: %w",
				err,
			)
	}

	return s.toDTO(value)
}

// CreateAgent 创建 Agent。
func (s *AgentService) CreateAgent(
	request CreateAgentRequest,
) (
	AgentDTO,
	error,
) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			agentServiceTimeout,
		)

	defer cancel()

	createInput := agents.CreateInput{
		Sandbox: sandboxPolicyFromDTO(request.Sandbox), SubagentEnabled: request.SubagentEnabled,
	}
	if request.BuiltinToolsConfigured {
		if err := s.validateBuiltinToolNames(request.EnabledBuiltinTools); err != nil {
			return AgentDTO{}, err
		}
		createInput.EnabledBuiltinTools = filterRemovedBuiltinTools(request.EnabledBuiltinTools)
	}

	value, err :=
		s.core.Agents().
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

					WorkspaceMode: workspace.Mode(
						request.WorkspaceMode,
					),

					WorkspacePath: request.WorkspacePath,
				},
			)

	if err != nil {
		return AgentDTO{},
			fmt.Errorf(
				"创建 Agent 失败: %w",
				err,
			)
	}

	return s.toDTO(value)
}

// UpdateAgent 修改 Agent。
func (s *AgentService) UpdateAgent(
	id string,
	request UpdateAgentRequest,
) (
	AgentDTO,
	error,
) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			agentServiceTimeout,
		)

	defer cancel()

	var enabledBuiltinTools *[]string
	var sandboxPolicy *sandbox.AgentPolicy
	var modelRoles *agents.ModelRoles
	if request.ModelRolesConfigured {
		roles := modelRolesFromDTO(request.ModelRoles)
		modelRoles = &roles
	}
	if request.SandboxConfigured {
		policy := sandboxPolicyFromDTO(request.Sandbox)
		sandboxPolicy = &policy
	}
	if request.BuiltinToolsConfigured {
		if err := s.validateBuiltinToolNames(request.EnabledBuiltinTools); err != nil {
			return AgentDTO{}, err
		}
		enabled := filterRemovedBuiltinTools(request.EnabledBuiltinTools)
		enabledBuiltinTools = &enabled
	}
	subagentEnabled := request.SubagentEnabled

	value, err :=
		s.core.Agents().
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

					WorkspaceMode: workspace.Mode(
						request.WorkspaceMode,
					),

					WorkspacePath: request.WorkspacePath,
				},
			)

	if err != nil {
		return AgentDTO{},
			fmt.Errorf(
				"更新 Agent 失败: %w",
				err,
			)
	}

	return s.toDTO(value)
}

// UpdateAgentProfile 只修改 Agent 名称与 Instruction。
func (s *AgentService) UpdateAgentProfile(id string, request AgentProfileRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.core.Agents().UpdateProfile(ctx, id, request.Name, request.Avatar, request.Instruction)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent Profile 失败: %w", err)
	}
	return s.toDTO(value)
}

// SetAgentModel 只修改 Agent 默认模型。
func (s *AgentService) SetAgentModel(id string, request AgentModelRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.core.Agents().SetModel(ctx, id, request.ModelID)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("切换 Agent Model 失败: %w", err)
	}
	return s.toDTO(value)
}

// SetAgentModelRoles 只修改 Utility/Memory 模型角色。
func (s *AgentService) SetAgentModelRoles(id string, request AgentModelRolesRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.core.Agents().SetModelRoles(ctx, id, agents.ModelRoles{
		UtilityModelID: request.UtilityModelID,
		MemoryModelID:  request.MemoryModelID,
	})
	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent Model Roles 失败: %w", err)
	}
	return s.toDTO(value)
}

// SetAgentSkills 只修改 Agent Skill 选择。
func (s *AgentService) SetAgentSkills(id string, request AgentSkillsRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	value, err := s.core.Agents().SetSkills(ctx, id, request.EnabledSkills)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent Skills 失败: %w", err)
	}
	return s.toDTO(value)
}

// ListBuiltinTools 返回当前 Registry 中可供 Agent 选择的 Builtin Tool。
func (s *AgentService) ListBuiltinTools() []BuiltinToolDTO {
	descriptors := s.core.Tools().List()
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
	manager := s.core.Sandbox()
	capability := manager.Capability()
	cfg := manager.Config()
	runtimeShellActive := false
	for _, descriptor := range s.core.Tools().List() {
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
		ShellEnabled:         s.core.Config().Security.ShellEnabled,
		ShellAllowedCommands: append([]string(nil), s.core.Config().Security.ShellAllowedCommands...),
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
	commands, err := config.NormalizeAndValidateShellCommands(request.ShellEnabled, request.ShellAllowedCommands)
	if err != nil {
		return SandboxStatusDTO{}, fmt.Errorf("本地程序设置无效: %w", err)
	}
	if err := config.SaveSandboxAndShellConfig(
		ctx,
		s.core.Config().Paths.ConfigFile,
		cfg,
		request.ShellEnabled,
		commands,
	); err != nil {
		return SandboxStatusDTO{}, err
	}

	runtimeCfg := sandbox.Config{
		DefaultProfile:     sandbox.Profile(cfg.DefaultProfile),
		DefaultNetworkMode: sandbox.NetworkMode(cfg.DefaultNetworkMode),
		DefaultNativeMode:  sandbox.NativeMode(cfg.NativeMode),
		CommandGracePeriod: time.Duration(cfg.CommandGracePeriodMS) * time.Millisecond,
	}
	if err := s.core.Sandbox().UpdateConfig(runtimeCfg); err != nil {
		return SandboxStatusDTO{}, fmt.Errorf("更新运行时 Sandbox Policy 失败: %w", err)
	}
	s.core.Config().Security.Sandbox = cfg
	s.core.Config().Security.ShellEnabled = request.ShellEnabled
	s.core.Config().Security.ShellAllowedCommands = append([]string(nil), commands...)

	s.core.Logger().Info(
		ctx,
		"Sandbox 默认策略已更新",
		"operation", "sandbox.settings.update",
		"default_profile", cfg.DefaultProfile,
		"default_network_mode", cfg.DefaultNetworkMode,
		"native_mode", cfg.NativeMode,
		"command_grace_period_ms", cfg.CommandGracePeriodMS,
		"shell_enabled", request.ShellEnabled,
		"shell_allowed_commands", strings.Join(commands, ","),
	)
	return s.GetSandboxStatus(), nil
}

// RunSandboxDiagnostics 在临时目录中验证 PathGuard，并在平台支持时实际验证原生文件系统隔离。
// 自检不会读取用户文件，也不会修改 Workspace；所有探针文件都位于 OS 临时目录。
func (s *AgentService) RunSandboxDiagnostics() (SandboxDiagnosticsDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()

	root, err := os.MkdirTemp("", "humbert-sandbox-diagnostics-*")
	if err != nil {
		return SandboxDiagnosticsDTO{}, fmt.Errorf("创建 Sandbox 自检目录失败: %w", err)
	}
	defer os.RemoveAll(root)

	workspaceRoot := filepath.Join(root, "workspace")
	outsideRoot := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		return SandboxDiagnosticsDTO{}, fmt.Errorf("创建自检 Workspace 失败: %w", err)
	}
	if err := os.MkdirAll(outsideRoot, 0o700); err != nil {
		return SandboxDiagnosticsDTO{}, fmt.Errorf("创建自检外部目录失败: %w", err)
	}

	policy, err := s.core.Sandbox().Resolve(ctx, workspaceRoot, sandbox.AgentPolicy{
		// 使用 Standard 才能验证“普通 Home 可读，但敏感文件仍被硬保护”的真实产品语义。
		Profile: sandbox.ProfileStandard, NetworkMode: sandbox.NetworkPublic, NativeMode: sandbox.NativeRequired,
	})
	if err != nil {
		return SandboxDiagnosticsDTO{}, fmt.Errorf("构建自检 Sandbox Policy 失败: %w", err)
	}

	checks := make([]SandboxDiagnosticCheckDTO, 0, 7)
	appendCheck := func(key, label, status, detail string) {
		checks = append(checks, SandboxDiagnosticCheckDTO{Key: key, Label: label, Status: status, Detail: detail})
	}

	insidePath := filepath.Join(workspaceRoot, "inside.txt")
	if decision, checkErr := policy.CheckPath(insidePath, sandbox.OpCreate); checkErr == nil && decision.Allowed {
		appendCheck("pathguard_workspace", "工作目录写入", "pass", "工作目录内的创建操作已正确允许。")
	} else {
		appendCheck("pathguard_workspace", "工作目录写入", "fail", errorDetail(checkErr, "工作目录内的创建操作被错误拒绝。"))
	}

	outsidePath := filepath.Join(outsideRoot, "outside.txt")
	if _, checkErr := policy.CheckPath(outsidePath, sandbox.OpCreate); checkErr != nil {
		appendCheck("pathguard_outside", "工作目录外写入阻止", "pass", "工作目录外的创建操作已正确阻止。")
	} else {
		appendCheck("pathguard_outside", "工作目录外写入阻止", "fail", "工作目录外的创建操作被错误允许。")
	}

	protectedProbe := filepath.Join(s.core.Config().Paths.SecretsDir, "diagnostic-probe")
	if _, checkErr := policy.CheckPath(protectedProbe, sandbox.OpRead); checkErr != nil {
		appendCheck("pathguard_protected", "敏感目录保护", "pass", "Humbert 的敏感数据目录已正确阻止访问。")
	} else {
		appendCheck("pathguard_protected", "敏感目录保护", "fail", "Humbert 的敏感数据目录被错误允许读取。")
	}

	protectedFileProbe := s.core.Config().Paths.ConfigFile
	if decision, checkErr := policy.CheckPath(protectedFileProbe, sandbox.OpRead); checkErr != nil && decision.Source == sandbox.RuleSourceProtectedFile {
		appendCheck("pathguard_protected_file", "敏感文件保护", "pass", "Humbert 配置文件位于普通 Home 可读范围内，但已被单文件硬保护规则正确阻止。")
	} else if checkErr != nil {
		appendCheck("pathguard_protected_file", "敏感文件保护", "warning", "敏感文件读取被阻止，但未命中预期的单文件规则："+checkErr.Error())
	} else {
		appendCheck("pathguard_protected_file", "敏感文件保护", "fail", "Humbert 配置文件被错误允许读取。")
	}

	linkPath := filepath.Join(workspaceRoot, "escape-link")
	if linkErr := os.Symlink(outsideRoot, linkPath); linkErr != nil {
		appendCheck("pathguard_symlink", "符号链接逃逸保护", "warning", "当前平台无法创建用于自检的符号链接："+linkErr.Error())
	} else if _, checkErr := policy.CheckPath(filepath.Join(linkPath, "escape.txt"), sandbox.OpCreate); checkErr != nil {
		appendCheck("pathguard_symlink", "符号链接逃逸保护", "pass", "指向工作目录外的符号链接已正确阻止。")
	} else {
		appendCheck("pathguard_symlink", "符号链接逃逸保护", "fail", "符号链接错误地允许写出工作目录。")
	}

	capability := s.core.Sandbox().Capability()
	if !capability.Available {
		appendCheck("native_filesystem", "本地程序文件隔离", "warning", "当前系统的本地程序隔离不可用："+capability.Reason)
	} else if !capability.Filesystem {
		detail := "当前平台不能可靠限制本地程序的文件系统访问。Humbert 已启用 fail-closed：标准/严格保护下不会静默启动未受文件隔离的 Python、Node、Skill 或 stdio MCP。"
		if strings.TrimSpace(capability.Reason) != "" {
			detail += " " + capability.Reason
		}
		appendCheck("native_filesystem", "本地程序文件隔离", "warning", detail)
	} else {
		touchPath, lookupErr := exec.LookPath("touch")
		if lookupErr != nil {
			appendCheck("native_filesystem", "本地程序文件隔离", "warning", "找不到系统 touch 命令，无法执行文件写入自检。")
		} else {
			touchPath, _ = filepath.Abs(touchPath)
			insideNative := filepath.Join(workspaceRoot, "native-inside.txt")
			var insideStderr bytes.Buffer
			insideResult, runErr := s.core.Sandbox().Runner().Run(ctx, policy, sandbox.ProcessSpec{
				Executable: touchPath, Args: []string{insideNative}, Dir: workspaceRoot, Env: []string{},
				Stdout: io.Discard, Stderr: &insideStderr,
			})
			if runErr != nil || insideResult.ExitCode != 0 {
				detail := errorDetail(runErr, "受保护的本地程序无法在工作目录内创建测试文件。")
				if runErr == nil && strings.TrimSpace(insideResult.TerminationDetail) != "" {
					detail += " 进程终态：" + insideResult.TerminationReason + "（" + insideResult.TerminationDetail + "）"
				}
				if text := strings.TrimSpace(insideStderr.String()); text != "" {
					detail += " " + text
				}
				appendCheck("native_filesystem", "本地程序文件隔离", "fail", detail)
			} else {
				outsideNative := filepath.Join(outsideRoot, "native-outside.txt")
				var outsideStderr bytes.Buffer
				outsideResult, outsideErr := s.core.Sandbox().Runner().Run(ctx, policy, sandbox.ProcessSpec{
					Executable: touchPath, Args: []string{outsideNative}, Dir: workspaceRoot, Env: []string{},
					Stdout: io.Discard, Stderr: &outsideStderr,
				})
				_, statErr := os.Stat(outsideNative)
				blocked := outsideErr == nil && outsideResult.ExitCode != 0 && errors.Is(statErr, os.ErrNotExist)
				if blocked {
					appendCheck("native_filesystem", "本地程序文件隔离", "pass", "本地程序可以写入工作目录，并且无法写入工作目录外。")
				} else {
					detail := "本地程序写入工作目录外的操作没有被可靠阻止。"
					if outsideErr != nil {
						detail = outsideErr.Error()
					} else if strings.TrimSpace(outsideResult.TerminationDetail) != "" {
						detail += " 进程终态：" + outsideResult.TerminationReason + "（" + outsideResult.TerminationDetail + "）"
					}
					if text := strings.TrimSpace(outsideStderr.String()); text != "" {
						detail += " " + text
					}
					appendCheck("native_filesystem", "本地程序文件隔离", "fail", detail)
				}
			}
		}
	}

	summary := "pass"
	for _, check := range checks {
		if check.Status == "fail" {
			summary = "fail"
			break
		}
		if check.Status == "warning" && summary == "pass" {
			summary = "warning"
		}
	}
	return SandboxDiagnosticsDTO{Summary: summary, Checks: checks}, nil
}

func errorDetail(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

// UpdateAgentSecurity 显式替换 Agent Builtin Tool 与 Sandbox 配置。
func (s *AgentService) UpdateAgentSecurity(id string, request AgentSecurityRequest) (AgentDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	enabled := filterRemovedBuiltinTools(request.EnabledBuiltinTools)
	if err := s.validateBuiltinToolNames(enabled); err != nil {
		return AgentDTO{}, err
	}
	policy := sandbox.AgentPolicy{
		Profile:              sandbox.Profile(request.Sandbox.Profile),
		AdditionalWritePaths: append([]string(nil), request.Sandbox.AdditionalWritePaths...),
		NetworkMode:          sandbox.NetworkMode(request.Sandbox.NetworkMode),
		NativeMode:           sandbox.NativeMode(request.Sandbox.NativeMode),
	}
	updated, err := s.core.Agents().UpdateSecurity(ctx, id, enabled, policy)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("更新 Agent Security 失败: %w", err)
	}
	return s.toDTO(updated)
}

// SelectSandboxDirectory 打开原生目录选择器，用于 Additional Read/Write Path。
func (s *AgentService) SelectSandboxDirectory(currentPath string) (string, error) {
	return selectDirectory("选择 Sandbox 目录", currentPath)
}

// DeleteAgent 删除完整 Agent Aggregate。
//
// 删除 Agent 会一并删除它的全部 Session、附件、Session Memory、Profile 与 Managed
// Workspace；Custom Workspace 只解除引用。
func (s *AgentService) DeleteAgent(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), agentServiceTimeout)
	defer cancel()
	releaseTasks := s.core.Tasks().SuspendAgent(id)
	defer releaseTasks()

	deletedSessions, err := s.core.Runtime().DeleteAgent(ctx, id, func(deleteCtx context.Context) error {
		return s.core.Agents().Delete(deleteCtx, id)
	})
	if err != nil {
		return fmt.Errorf("删除 Agent 失败: %w", err)
	}
	if s.core.Permissions() != nil {
		for _, sessionID := range deletedSessions {
			s.core.Permissions().ClearSessionRules(sessionID)
		}
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
	return selectDirectory("选择 Agent Workspace", currentPath)
}

func selectDirectory(title, currentPath string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("Wails Application 尚未初始化")
	}
	dialog := app.Dialog.OpenFile().SetTitle(title).CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true)
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

func (s *AgentService) toDTO(
	value agents.AgentInfo,
) (
	AgentDTO,
	error,
) {
	displayPath :=
		value.Agent.WorkspacePath

	if value.Agent.WorkspaceMode ==
		workspace.ModeManaged {

		path, err :=
			s.core.Workspaces().
				ManagedPath(
					value.Agent.ID,
				)

		if err != nil {
			return AgentDTO{},
				fmt.Errorf(
					"计算 Agent Managed Workspace 路径失败: %w",
					err,
				)
		}

		displayPath =
			path
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

		EnabledBuiltinTools:    filterRemovedBuiltinTools(value.Agent.EnabledBuiltinTools),
		BuiltinToolsConfigured: value.Agent.EnabledBuiltinTools != nil,
		AvailableBuiltinTools:  s.ListBuiltinTools(),
		Sandbox:                sandboxPolicyDTO(value.Agent.Sandbox),
		SandboxStatus:          s.GetSandboxStatus(),

		WorkspaceMode: string(
			value.Agent.WorkspaceMode,
		),

		WorkspacePath: value.Agent.WorkspacePath,

		WorkspaceDisplayPath: displayPath,

		CreatedAt: value.Agent.CreatedAt.
			UTC().
			Format(
				time.RFC3339Nano,
			),

		UpdatedAt: value.Agent.UpdatedAt.
			UTC().
			Format(
				time.RFC3339Nano,
			),
	}, nil
}

func modelRolesFromDTO(value AgentModelRolesDTO) agents.ModelRoles {
	return agents.ModelRoles{
		UtilityModelID: value.UtilityModelID,
		MemoryModelID:  value.MemoryModelID,
	}
}

func modelRolesDTO(value agents.ModelRoles) AgentModelRolesDTO {
	return AgentModelRolesDTO{
		UtilityModelID: value.UtilityModelID,
		MemoryModelID:  value.MemoryModelID,
	}
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
	for _, descriptor := range s.core.Tools().List() {
		if descriptor.MCPOrigin == nil {
			known[descriptor.Name] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	for _, raw := range values {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
			if humberttools.IsRemovedBuiltinTool(name) {
				continue
			}
			return fmt.Errorf("Builtin Tool 不存在或当前配置未启用: %s", name)
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
	}
	return nil
}

func filterRemovedBuiltinTools(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || humberttools.IsRemovedBuiltinTool(value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func builtinToolCategory(name string) string {
	switch name {
	case "list_files", "read_file", "write_file", "edit_file", "glob_files", "grep_files", "apply_patch", "copy_file", "move_file", "delete_file":
		return "files"
	case "run_command":
		return "execution"
	case "git_status", "git_diff", "git_log":
		return "git"
	case "web_search", "web_fetch":
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
		"web_fetch": "读取网页", "install_skill": "安装 Skill", "get_current_time": "当前时间", "update_plan": "更新计划", "schedule_task": "安排提醒或任务",
		"list_agents": "查看可用 Agent", "run_agent": "调用专业 Agent",
	}
	if label := labels[name]; label != "" {
		return label
	}
	return name
}
