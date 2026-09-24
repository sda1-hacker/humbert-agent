package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
)

const permissionServiceTimeout = 10 * time.Second

// PermissionRuleDTO 是设置页可以安全展示的一条 Permission Rule。
//
// DTO 不包含原始 Tool Arguments，只暴露 CapabilityIdentity 中可安全展示的身份摘要。
// 后续扩展 Browser Host 等能力时，也必须坚持只暴露规则本身，而不是某次调用的 Secret、
// 文件正文或其他敏感参数。
type PermissionRuleDTO struct {
	ID string `json:"id"`

	AgentID   string `json:"agentID"`
	AgentName string `json:"agentName"`

	SessionID string `json:"sessionID,omitempty"`

	ToolName string `json:"toolName"`
	Action   string `json:"action"`
	Scope    string `json:"scope"`

	CapabilityKind string `json:"capabilityKind,omitempty"`

	Command               string `json:"command,omitempty"`
	Executable            string `json:"executable,omitempty"`
	InvocationFingerprint string `json:"invocationFingerprint,omitempty"`

	SkillName     string `json:"skillName,omitempty"`
	SkillIdentity string `json:"skillIdentity,omitempty"`
	Script        string `json:"script,omitempty"`

	MCPServerID          string `json:"mcpServerID,omitempty"`
	MCPServerName        string `json:"mcpServerName,omitempty"`
	MCPServerFingerprint string `json:"mcpServerFingerprint,omitempty"`
	MCPTool              string `json:"mcpTool,omitempty"`

	SandboxFingerprint string `json:"sandboxFingerprint,omitempty"`

	CreatedAt string `json:"createdAt"`
}

// PermissionStateDTO 是“权限与审批”设置页的完整可管理状态。
//
// PersistentRules 来自 config/permissions.json；SessionRules 只来自当前进程内指定 Session，
// 应用重启后自然消失。默认策略来自 PermissionEngine 当前运行配置，与通过 Viper 写回的
// config.yaml 保持一致。
type PermissionStateDTO struct {
	Enabled bool `json:"enabled"`

	ReadAction  string `json:"readAction"`
	WriteAction string `json:"writeAction"`
	ExecAction  string `json:"execAction"`

	ApprovalTimeoutMS int `json:"approvalTimeoutMS"`

	PersistentRules []PermissionRuleDTO `json:"persistentRules"`
	SessionRules    []PermissionRuleDTO `json:"sessionRules"`
}

// UpdatePermissionSettingsRequest 是设置页允许修改的 Permission 应用级配置。
//
// Action 使用 allow/ask/deny；ApprovalTimeoutMS 使用毫秒。后端会再次规范化和校验，前端
// 表单约束不能被视作安全边界。
type UpdatePermissionSettingsRequest struct {
	Enabled bool `json:"enabled"`

	ReadAction  string `json:"readAction"`
	WriteAction string `json:"writeAction"`
	ExecAction  string `json:"execAction"`

	ApprovalTimeoutMS int `json:"approvalTimeoutMS"`
}

// PermissionService 是 Permission Domain 的 Wails Desktop Adapter。
//
// PermissionEngine 保持与桌面框架无关；本 Service 只负责 DTO 投影、Viper 配置更新和设置页
// 管理。持久化 Rule 仍由 permission.Store 原子提交，Session Rule 仍由 Engine 内存管理。
type PermissionService struct {
	core *coreapp.Application
}

// NewPermissionService 创建 Permission Desktop Service。
func NewPermissionService(core *coreapp.Application) *PermissionService {
	return &PermissionService{core: core}
}

// ServiceName 返回稳定的 Wails Service 名称。
func (s *PermissionService) ServiceName() string {
	return "PermissionService"
}

// State 返回当前默认 Policy、持久化 Agent Rule 以及指定 Session 的临时 Rule。
//
// sessionID 可以为空，此时 SessionRules 返回空列表。这样设置页即使没有打开会话，也仍然
// 可以管理应用默认策略与长期规则，而不会泄露其他 Session 的临时授权。
func (s *PermissionService) State(sessionID string) (PermissionStateDTO, error) {
	if err := s.validate(); err != nil {
		return PermissionStateDTO{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), permissionServiceTimeout)
	defer cancel()

	persistentRules, err := s.core.Permissions().ListPersistentRules(ctx)
	if err != nil {
		return PermissionStateDTO{}, fmt.Errorf("读取持久化 Permission Rules 失败: %w", err)
	}

	agentNames, err := s.agentNames(ctx)
	if err != nil {
		return PermissionStateDTO{}, err
	}
	mcpServerNames, mcpNamesErr := s.mcpServerNames(ctx)
	if mcpNamesErr != nil {
		// Permission 设置本身是安全恢复入口。即使 MCP Store 损坏，也必须允许用户
		// 查看并撤销既有规则，因此 MCP 展示名称解析失败时降级为占位名称。
		mcpServerNames = map[string]string{}
		s.core.Logger().Warn(ctx, "读取 Permission Rule MCP Server 展示名称失败",
			"operation", "permission.rule.mcp_names",
			"error", mcpNamesErr,
		)
	}

	sessionID = strings.TrimSpace(sessionID)
	sessionRules := s.core.Permissions().ListSessionRules(sessionID)

	cfg := s.core.Permissions().Config()
	return PermissionStateDTO{
		Enabled:           cfg.Enabled,
		ReadAction:        cfg.ReadAction,
		WriteAction:       cfg.WriteAction,
		ExecAction:        cfg.ExecAction,
		ApprovalTimeoutMS: cfg.ApprovalTimeoutMS,
		PersistentRules:   projectPermissionRules(persistentRules, agentNames, mcpServerNames),
		SessionRules:      projectPermissionRules(sessionRules, agentNames, mcpServerNames),
	}, nil
}

// UpdateSettings 使用 Viper 更新 config.yaml，并让 PermissionEngine 与 ApprovalManager 在当前
// 进程立即采用新配置。
//
// 已经 Pending 的 Approval 保留创建时的 ExpiresAt；新超时只作用于后续审批。已有长期或
// Session Rule 也不被默认策略修改覆盖，因为它们本来就比默认 Risk Policy 优先。
func (s *PermissionService) UpdateSettings(request UpdatePermissionSettingsRequest) error {
	if err := s.validate(); err != nil {
		return err
	}

	cfg := config.PermissionConfig{
		Enabled:           request.Enabled,
		ReadAction:        request.ReadAction,
		WriteAction:       request.WriteAction,
		ExecAction:        request.ExecAction,
		ApprovalTimeoutMS: request.ApprovalTimeoutMS,
	}
	cfg = config.NormalizePermissionConfig(cfg)
	if err := config.ValidatePermissionConfig(cfg); err != nil {
		return fmt.Errorf("Permission 设置无效: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), permissionServiceTimeout)
	defer cancel()
	if err := config.SavePermissionConfig(ctx, s.core.Config().Paths.ConfigFile, cfg); err != nil {
		return err
	}
	if err := s.core.Permissions().UpdateConfig(cfg); err != nil {
		return fmt.Errorf("更新运行时 Permission Policy 失败: %w", err)
	}
	if err := s.core.Approvals().UpdateTimeout(time.Duration(cfg.ApprovalTimeoutMS) * time.Millisecond); err != nil {
		return fmt.Errorf("更新 Approval Timeout 失败: %w", err)
	}

	s.core.Logger().Info(
		ctx,
		"Permission 默认策略已更新",
		"operation", "permission.settings.update",
		"enabled", cfg.Enabled,
		"read_action", cfg.ReadAction,
		"write_action", cfg.WriteAction,
		"exec_action", cfg.ExecAction,
		"approval_timeout_ms", cfg.ApprovalTimeoutMS,
	)
	return nil
}

// DeleteRule 撤销一个 Agent Scope 长期 Rule。
//
// 删除只影响后续 Tool 调用；已经获批并开始 Resume 的调用仍使用 checkpoint 中被冻结的
// 决策，不会在 Tool 执行一半时被设置页撤销。
func (s *PermissionService) DeleteRule(ruleID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return errors.New("Permission Rule ID 不能为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), permissionServiceTimeout)
	defer cancel()
	if err := s.core.Permissions().DeletePersistentRule(ctx, ruleID); err != nil {
		return fmt.Errorf("删除 Permission Rule 失败: %w", err)
	}
	return nil
}

// DeleteSessionRule 撤销当前进程中指定 Session 的一条临时 Rule。
func (s *PermissionService) DeleteSessionRule(sessionID string, ruleID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.core.Permissions().DeleteSessionRule(sessionID, ruleID); err != nil {
		return fmt.Errorf("删除 Session Permission Rule 失败: %w", err)
	}
	s.core.Logger().Info(
		context.Background(),
		"Session Permission Rule 已撤销",
		"operation", "permission.session_rule.delete",
		"session_id", strings.TrimSpace(sessionID),
		"rule_id", strings.TrimSpace(ruleID),
	)
	return nil
}

// ClearSessionRules 清除当前 Session 的全部临时授权。
func (s *PermissionService) ClearSessionRules(sessionID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("Session ID 不能为空")
	}
	s.core.Permissions().ClearSessionRules(sessionID)
	s.core.Logger().Info(
		context.Background(),
		"Session Permission Rules 已清除",
		"operation", "permission.session_rule.clear",
		"session_id", sessionID,
	)
	return nil
}

// ClearPersistentAllows 撤销所有 Agent Scope 长期 Allow Rule，但保留显式 Deny。
//
// 这是设置页的“恢复谨慎模式”入口。保留 Deny 可以避免用户为了收紧权限创建的安全规则
// 被一次批量撤销操作意外删除。
func (s *PermissionService) ClearPersistentAllows() (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), permissionServiceTimeout)
	defer cancel()
	count, err := s.core.Permissions().ClearPersistentAllows(ctx)
	if err != nil {
		return 0, fmt.Errorf("撤销全部长期授权失败: %w", err)
	}
	return count, nil
}

func (s *PermissionService) validate() error {
	if s == nil || s.core == nil || s.core.Permissions() == nil || s.core.Approvals() == nil {
		return errors.New("PermissionService 尚未正确初始化")
	}
	return nil
}

func (s *PermissionService) agentNames(ctx context.Context) (map[string]string, error) {
	agents, err := s.core.Agents().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 Permission Rule 对应 Agent 失败: %w", err)
	}
	result := make(map[string]string, len(agents))
	for _, agent := range agents {
		result[agent.Agent.ID] = agent.Agent.Name
	}
	return result, nil
}

func (s *PermissionService) mcpServerNames(ctx context.Context) (map[string]string, error) {
	servers, err := s.core.MCP().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 Permission Rule 对应 MCP Server 失败: %w", err)
	}
	result := make(map[string]string, len(servers))
	for _, server := range servers {
		result[server.ID] = server.Name
	}
	return result, nil
}

func projectPermissionRules(rules []permission.Rule, agentNames map[string]string, mcpServerNames map[string]string) []PermissionRuleDTO {
	result := make([]PermissionRuleDTO, 0, len(rules))
	for _, rule := range rules {
		name := strings.TrimSpace(agentNames[rule.AgentID])
		if name == "" {
			name = "已删除的 Agent"
		}
		mcpName := ""
		if rule.Identity.MCPServerID != "" {
			mcpName = strings.TrimSpace(mcpServerNames[rule.Identity.MCPServerID])
			if mcpName == "" {
				mcpName = "已删除或已修改的 MCP Server"
			}
		}
		identity := rule.Identity.Normalize()
		result = append(result, PermissionRuleDTO{
			ID:                    rule.ID,
			AgentID:               rule.AgentID,
			AgentName:             name,
			SessionID:             rule.SessionID,
			ToolName:              rule.ToolName,
			Action:                string(rule.Action),
			Scope:                 string(rule.Scope),
			CapabilityKind:        string(identity.Kind),
			Command:               identity.Command,
			Executable:            identity.Executable,
			InvocationFingerprint: identity.InvocationFingerprint,
			SkillName:             identity.SkillName,
			SkillIdentity:         identity.SkillIdentity,
			Script:                identity.Script,
			MCPServerID:           identity.MCPServerID,
			MCPServerName:         mcpName,
			MCPServerFingerprint:  identity.MCPServerFingerprint,
			MCPTool:               identity.MCPTool,
			SandboxFingerprint:    identity.SandboxFingerprint,
			CreatedAt:             rule.CreatedAt.Format(time.RFC3339),
		})
	}
	return result
}
