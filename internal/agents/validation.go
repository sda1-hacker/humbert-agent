package agents

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/avatar"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// profileFields 是身份与指令的共同校验结果。完整表单和局部保存都复用它，
// 防止一个入口允许的名称、头像或指令被另一个入口拒绝，也避免局部命令绕过长度限制。
type profileFields struct {
	Name, Avatar, Instruction string
}

func normalizeProfile(name, profileAvatar, instruction string) (profileFields, error) {
	name, instruction = strings.TrimSpace(name), strings.TrimSpace(instruction)
	if name == "" {
		return profileFields{}, errors.New("Agent 名称不能为空")
	}
	// 名称限制按字符计算，中文与英文使用同一个上限；指令按字节限制存储和输入规模。
	if len([]rune(name)) > maxAgentNameLength {
		return profileFields{}, fmt.Errorf("Agent 名称长度不能超过 %d", maxAgentNameLength)
	}
	if len(instruction) > maxInstructionLength {
		return profileFields{}, fmt.Errorf("Agent Instruction 长度不能超过 %d 字节", maxInstructionLength)
	}
	normalizedAvatar, err := avatar.NormalizeDataURL(profileAvatar)
	if err != nil {
		return profileFields{}, fmt.Errorf("Agent 头像无效: %w", err)
	}
	return profileFields{Name: name, Avatar: normalizedAvatar, Instruction: instruction}, nil
}

type normalizedInput struct {
	profileFields
	ModelID       string
	WorkspaceMode workspace.Mode
	WorkspacePath string
}

// normalizeInput 只规范化表单字段，不读文件、不查模型，也不创建目录。
// 工作区的真实可用性由 Create/Update 使用调用方 Context 检查；这里不再用
// context.Background 重复访问目录，保证取消信号可以传到正式的工作区操作。
func normalizeInput(name, profileAvatar, instruction, modelID string, mode workspace.Mode, path string) (normalizedInput, error) {
	profile, err := normalizeProfile(name, profileAvatar, instruction)
	if err != nil {
		return normalizedInput{}, err
	}
	mode = workspace.Mode(strings.ToLower(strings.TrimSpace(string(mode))))
	if mode == "" {
		mode = workspace.ModeManaged
	}
	path = strings.TrimSpace(path)
	switch mode {
	case workspace.ModeManaged:
		// 托管目录只能由 Agent ID 推导，客户端传来的自定义路径不能进入 Profile。
		path = ""
	case workspace.ModeCustom:
		if path == "" {
			return normalizedInput{}, errors.New("Custom Workspace 必须选择一个目录")
		}
	default:
		return normalizedInput{}, fmt.Errorf("%w: %q", workspace.ErrInvalidMode, mode)
	}
	return normalizedInput{profileFields: profile, ModelID: strings.TrimSpace(modelID), WorkspaceMode: mode, WorkspacePath: path}, nil
}

// normalizeEnabledSkills 只向 Skill Catalog 校验引用，不参与安装和 Runtime 装配。
// 空选择合法；非空选择必须有 Catalog，不能把错误配置静默保存成可用能力。
func (s *Service) normalizeEnabledSkills(ctx context.Context, names []string) ([]string, error) {
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

// normalizeEnabledMCPTools 保留 Server ID 与原始工具名的绑定。连接和执行由 MCP 负责。
func (s *Service) normalizeEnabledMCPTools(ctx context.Context, values []humbertmcp.ToolSelection) ([]humbertmcp.ToolSelection, error) {
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

// normalizeBuiltinToolSelection 保留 nil 与显式空集合的区别：nil 继承默认工具，
// 空集合表示用户禁用全部普通工具。存在性由 Runtime Registry 校验，避免领域反向依赖装配层。
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
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result, nil
}

// normalizeSandboxPolicy 校验并去重额外目录；空策略继续继承应用默认值，不能在保存时
// 固化默认策略，否则之后修改全局安全配置不会影响这些 Agent。
func normalizeSandboxPolicy(value sandbox.AgentPolicy) (sandbox.AgentPolicy, error) {
	if err := value.Validate(); err != nil {
		return sandbox.AgentPolicy{}, fmt.Errorf("Agent Sandbox 配置无效: %w", err)
	}
	if value.Profile != "" {
		value.Profile = sandbox.NormalizeProfile(value.Profile, sandbox.ProfileWorkspaceOnly)
	}
	if value.NetworkMode != "" {
		value.NetworkMode = sandbox.NormalizeNetworkMode(value.NetworkMode, sandbox.NetworkPublic)
	}
	if value.NativeMode != "" {
		value.NativeMode = sandbox.NormalizeNativeMode(value.NativeMode, sandbox.NativePreferred)
	}
	paths, err := normalizeSandboxPaths(value.AdditionalWritePaths)
	if err != nil {
		return sandbox.AgentPolicy{}, err
	}
	value.AdditionalWritePaths = paths
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
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, root)
		}
	}
	sort.Strings(result)
	return result, nil
}

// normalizeAndValidateModelRoles 只校验实际设置的 Utility 模型；留空由 Runtime 回退 Chat。
func (s *Service) normalizeAndValidateModelRoles(ctx context.Context, roles ModelRoles) (ModelRoles, error) {
	roles.UtilityModelID = strings.TrimSpace(roles.UtilityModelID)
	if err := s.ensureModelUsable(ctx, roles.UtilityModelID); err != nil {
		return ModelRoles{}, fmt.Errorf("Utility Model 无效: %w", err)
	}
	return roles, nil
}

// ensureModelUsable 允许未配置模型的 Agent 被保存和修复；一旦指定模型，必须存在且启用。
func (s *Service) ensureModelUsable(ctx context.Context, modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	if s.models == nil {
		return errors.New("Model Registry 未初始化")
	}
	values, err := s.models.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("读取 Model Registry 失败: %w", err)
	}
	for _, item := range values {
		if item.Model.ID == modelID {
			if !item.Model.Enabled {
				return fmt.Errorf("%w: %s", models.ErrModelDisabled, modelID)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: %s", models.ErrModelNotFound, modelID)
}
