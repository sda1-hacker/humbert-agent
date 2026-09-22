package permission

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

// Authorizer 是 Tool Registry 依赖的统一 Permission Boundary。
//
// Tool 只依赖本接口，不依赖 Engine 的持久化实现。后续 Skill/MCP/Plugin 仍可复用同一个
// Evaluate 调用。
type Authorizer interface {
	Evaluate(ctx context.Context, request Request) (Decision, error)
}

// Engine 负责 Permission Policy 的运行时计算和规则生命周期。
//
// 持久化 Agent Rule 来自 Store；Session Rule 仅存在内存，并在 Session/应用生命周期内
// 生效。Engine 不负责 Eino Interrupt/Resume，那属于 Approval + Runtime 层。
type Engine struct {
	configMu sync.RWMutex
	config   config.PermissionConfig
	store    *Store
	logger   *logging.Logger

	mu           sync.RWMutex
	sessionRules map[string][]Rule
}

// NewEngine 创建 PermissionEngine。
func NewEngine(cfg config.PermissionConfig, store *Store, logger *logging.Logger) (*Engine, error) {
	cfg = config.NormalizePermissionConfig(cfg)
	if err := config.ValidatePermissionConfig(cfg); err != nil {
		return nil, fmt.Errorf("初始化 PermissionEngine 配置无效: %w", err)
	}
	if store == nil {
		return nil, errors.New("PermissionEngine Store 不能为空")
	}
	if logger == nil {
		return nil, errors.New("PermissionEngine Logger 不能为空")
	}
	return &Engine{
		config:       cfg,
		store:        store,
		logger:       logger,
		sessionRules: make(map[string][]Rule),
	}, nil
}

// Evaluate 按“显式拒绝优先 → Session Allow → Agent Allow → 默认 Risk Policy”的顺序计算结论。
//
// 显式 Deny 无论来自 Session 还是 Agent Scope 都优先于 Allow，避免一个较宽的长期授权覆盖
// 用户后来创建的拒绝规则。底层 Workspace Path Guard、Command Allowlist 等安全边界在真实
// Tool 内仍然继续执行，Permission Allow 永远不能绕过这些约束。
func (e *Engine) Evaluate(ctx context.Context, request Request) (Decision, error) {
	if ctx == nil {
		return Decision{}, errors.New("Permission 校验失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("Permission 校验被取消: %w", err)
	}
	if err := request.Validate(); err != nil {
		return Decision{}, err
	}

	cfg := e.Config()
	if !cfg.Enabled && request.ToolName != "schedule_task" {
		return Decision{Action: ActionAllow, Reason: "Permission 系统已在配置中关闭"}, nil
	}

	presentation, err := BuildPresentation(request)
	if err != nil {
		return Decision{}, fmt.Errorf("构建 Tool Approval 展示信息失败: %w", err)
	}

	sessionMatches := e.matchingSessionRules(request)
	persistentRules, err := e.store.List(ctx)
	if err != nil {
		return Decision{}, fmt.Errorf("读取 Agent Permission Rules 失败: %w", err)
	}
	persistentMatches := matchingRules(persistentRules, request)

	// 显式拒绝规则具有最高 Permission 优先级。这样即使某个 Agent 之前被长期允许 write_file，
	// 用户后来创建的 deny 仍然可以立即收紧权限，而不依赖规则写入顺序。
	if rule, ok := newestRuleWithAction(sessionMatches, persistentMatches, ActionDeny); ok {
		return Decision{
			Action:       ActionDeny,
			Reason:       "命中显式拒绝 Permission Rule",
			RuleID:       rule.ID,
			Identity:     request.Identity.Normalize(),
			Presentation: presentation,
		}, nil
	}

	// install_skill 的远程来源当前没有参数级 Rule 约束，因此即使旧版本已经留下
	// Session/Agent Allow，也不能继续自动放行。Deny 仍然在上方优先生效。这样升级后
	// 不需要用户先手工清理旧 permissions.json 才能获得新的安全语义。
	if request.ToolName != "install_skill" && request.ToolName != "schedule_task" {
		if rule, ok := newestRuleWithAction(sessionMatches, nil, ActionAllow); ok {
			return Decision{
				Action:       ActionAllow,
				Reason:       "命中 Session Permission Rule",
				RuleID:       rule.ID,
				Identity:     request.Identity.Normalize(),
				Presentation: presentation,
			}, nil
		}
		if rule, ok := newestRuleWithAction(nil, persistentMatches, ActionAllow); ok {
			return Decision{
				Action:       ActionAllow,
				Reason:       "命中 Agent Permission Rule",
				RuleID:       rule.ID,
				Identity:     request.Identity.Normalize(),
				Presentation: presentation,
			}, nil
		}
	}

	action := e.defaultActionWithConfig(cfg, request.Risk)
	reason := "使用 Capability 默认风险策略"
	if request.ToolName == "install_skill" {
		// Permission 系统启用时，远程 Skill 安装必须逐次确认，不能因为 write 默认策略
		// 被配置为 allow 就静默下载安装网络内容。显式 Deny 已在上方优先处理。
		action = ActionAsk
		reason = "远程 Skill 安装要求逐次审批"
	}
	if request.ToolName == "schedule_task" {
		action = ActionAsk
		reason = "对话安排任务要求逐次确认"
	}
	decision := Decision{
		Action:       action,
		Reason:       reason,
		Identity:     request.Identity.Normalize(),
		Presentation: presentation,
	}
	if action == ActionAsk {
		decision.ApprovalID = uuid.NewString()
	}
	return decision, nil
}

// Grant 把用户批准转换成 Session 或 Agent Scope Allow Rule。
//
// once 不创建任何规则；session/agent Allow 会保存当前完整 CapabilityIdentity；Workspace/Sandbox、
// 可执行文件、Skill 内容或 MCP Server 安全身份变化后都会重新询问。
func (e *Engine) Grant(ctx context.Context, grant ApprovalGrant) (*Rule, error) {
	if grant.Scope == GrantOnce {
		if ctx == nil {
			return nil, errors.New("保存 Permission 授权失败: context.Context 不能为空")
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("保存 Permission 授权被取消: %w", err)
		}
		if err := grant.Request.Validate(); err != nil {
			return nil, fmt.Errorf("保存 Permission 授权失败: %w", err)
		}
		return nil, nil
	}

	// install_skill 的 source_url 每次都可能指向完全不同的远程内容。当前 Permission
	// CapabilityIdentity 当前没有绑定远程仓库内容身份，因此不能把一次安装批准扩大成整个
	// Session 或 Agent 对所有未来 URL 的 Allow。长期 Deny 仍然允许，因为它只会收紧权限。
	if grant.Request.ToolName == "install_skill" || grant.Request.ToolName == "schedule_task" {
		return nil, fmt.Errorf(
			"%w: %s 只允许单次批准，不能创建 Session/Agent Allow Rule",
			ErrInvalidApprovalScope,
			grant.Request.ToolName,
		)
	}

	return e.createRule(ctx, grant, ActionAllow)
}

// DenyAgent 把当前 Ask 请求保存为 Agent Scope 持久化拒绝规则。
//
// 拒绝规则与长期 Allow 使用同一份 permissions.json，但 Evaluate 会让所有显式 Deny 优先。
// 第一阶段只提供 Agent Scope 持久化拒绝，不提供 Session Deny；普通“拒绝”仍只拒绝当前
// ToolCall，不会意外改变后续对话策略。
func (e *Engine) DenyAgent(ctx context.Context, grant ApprovalGrant) (*Rule, error) {
	grant.Scope = GrantAgent
	return e.createRule(ctx, grant, ActionDeny)
}

func (e *Engine) createRule(ctx context.Context, grant ApprovalGrant, action Action) (*Rule, error) {
	if ctx == nil {
		return nil, errors.New("保存 Permission Rule 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("保存 Permission Rule 被取消: %w", err)
	}
	if err := grant.Request.Validate(); err != nil {
		return nil, fmt.Errorf("保存 Permission Rule 失败: %w", err)
	}
	if action != ActionAllow && action != ActionDeny {
		return nil, fmt.Errorf("保存 Permission Rule 失败: Action %q 不合法", action)
	}
	if grant.Scope != GrantSession && grant.Scope != GrantAgent {
		return nil, fmt.Errorf("%w: %q", ErrInvalidApprovalScope, grant.Scope)
	}

	identity := grant.Request.Identity.Normalize()
	if action == ActionDeny {
		identity = identity.DenyScope()
	}

	rule := Rule{
		ID:        uuid.NewString(),
		AgentID:   grant.Request.AgentID,
		ToolName:  grant.Request.ToolName,
		Action:    action,
		Scope:     grant.Scope,
		Identity:  identity,
		CreatedAt: time.Now().UTC(),
	}
	if grant.Scope == GrantSession {
		rule.SessionID = grant.Request.SessionID
		if err := rule.Validate(); err != nil {
			return nil, err
		}
		e.mu.Lock()
		rules := e.sessionRules[rule.SessionID]
		filtered := make([]Rule, 0, len(rules)+1)
		for _, existing := range rules {
			if sameRuleTarget(existing, rule) {
				continue
			}
			filtered = append(filtered, existing)
		}
		filtered = append(filtered, rule)
		e.sessionRules[rule.SessionID] = filtered
		e.mu.Unlock()
	} else {
		if err := rule.Validate(); err != nil {
			return nil, err
		}
		if err := e.store.Upsert(ctx, rule); err != nil {
			return nil, err
		}
	}

	e.logger.Info(
		ctx,
		"Permission 规则已创建",
		"operation", "permission.rule.create",
		"agent_id", rule.AgentID,
		"session_id", rule.SessionID,
		"tool", rule.ToolName,
		"action", rule.Action,
		"scope", rule.Scope,
		"rule_id", rule.ID,
		"capability_kind", rule.Identity.Kind,
		"command", rule.Identity.Command,
		"executable", rule.Identity.Executable,
		"skill", rule.Identity.SkillName,
		"skill_identity", rule.Identity.SkillIdentity,
		"mcp_server_id", rule.Identity.MCPServerID,
		"mcp_server_fingerprint", rule.Identity.MCPServerFingerprint,
		"sandbox_fingerprint", rule.Identity.SandboxFingerprint,
	)
	copy := rule
	return &copy, nil
}

// ListPersistentRules 返回设置页可管理的 Agent Scope Rule。
func (e *Engine) ListPersistentRules(ctx context.Context) ([]Rule, error) {
	return e.store.List(ctx)
}

// DeletePersistentRule 删除一个 Agent Scope Rule。
func (e *Engine) DeletePersistentRule(ctx context.Context, ruleID string) error {
	ruleID = strings.TrimSpace(ruleID)
	if err := e.store.Delete(ctx, ruleID); err != nil {
		return err
	}
	e.logger.Info(
		ctx,
		"Permission 长期规则已删除",
		"operation", "permission.rule.delete",
		"rule_id", ruleID,
	)
	return nil
}

// ClearPersistentAllows 删除所有持久化 Agent Allow Rule，但保留显式 Deny。
//
// “撤销全部长期授权”是一个紧急收紧权限动作，因此不能顺手删除用户为了安全保存的长期
// Deny。设置页仍可逐条删除 Deny Rule。
func (e *Engine) ClearPersistentAllows(ctx context.Context) (int, error) {
	count, err := e.store.DeleteByAction(ctx, ActionAllow)
	if err != nil {
		return 0, err
	}
	e.logger.Info(
		ctx,
		"Permission 长期授权已批量撤销",
		"operation", "permission.rule.clear_persistent_allows",
		"deleted_count", count,
	)
	return count, nil
}

// ListSessionRules 返回指定 Session 当前进程内的临时规则快照。
//
// Session Rule 不写磁盘；应用重启后自然消失。返回副本避免设置页或调用方修改 Engine 内部
// slice。若 sessionID 为空，返回空集合而不是泄露其他 Session 的临时规则。
func (e *Engine) ListSessionRules(sessionID string) []Rule {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return []Rule{}
	}
	e.mu.RLock()
	rules := append([]Rule(nil), e.sessionRules[sessionID]...)
	e.mu.RUnlock()
	return rules
}

// DeleteSessionRule 撤销当前进程中的一条 Session Scope Rule。
func (e *Engine) DeleteSessionRule(sessionID string, ruleID string) error {
	sessionID = strings.TrimSpace(sessionID)
	ruleID = strings.TrimSpace(ruleID)
	if sessionID == "" || ruleID == "" {
		return errors.New("删除 Session Permission Rule 失败: SessionID/RuleID 不能为空")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	rules := e.sessionRules[sessionID]
	filtered := make([]Rule, 0, len(rules))
	found := false
	for _, rule := range rules {
		if rule.ID == ruleID {
			found = true
			continue
		}
		filtered = append(filtered, rule)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrRuleNotFound, ruleID)
	}
	if len(filtered) == 0 {
		delete(e.sessionRules, sessionID)
	} else {
		e.sessionRules[sessionID] = filtered
	}
	return nil
}

// ClearSessionRules 在 Session 被删除或用户显式重置权限时释放临时授权。
func (e *Engine) ClearSessionRules(sessionID string) {
	e.mu.Lock()
	delete(e.sessionRules, strings.TrimSpace(sessionID))
	e.mu.Unlock()
}

// UpdateConfig 原子替换 Engine 的默认 Permission Policy。
//
// 已经创建的 Session/Agent Rule 不受影响；新默认值只在没有任何规则命中时生效。设置页会
// 先把配置通过 Viper 持久化，再调用本方法，因此运行中行为与下次启动保持一致。
func (e *Engine) UpdateConfig(cfg config.PermissionConfig) error {
	cfg = config.NormalizePermissionConfig(cfg)
	if err := config.ValidatePermissionConfig(cfg); err != nil {
		return err
	}
	e.configMu.Lock()
	e.config = cfg
	e.configMu.Unlock()
	return nil
}

// Config 返回只读配置快照，供 Settings Service 展示默认策略。
func (e *Engine) Config() config.PermissionConfig {
	e.configMu.RLock()
	cfg := e.config
	e.configMu.RUnlock()
	return cfg
}

func (e *Engine) matchingSessionRules(request Request) []Rule {
	e.mu.RLock()
	rules := append([]Rule(nil), e.sessionRules[request.SessionID]...)
	e.mu.RUnlock()
	return matchingRules(rules, request)
}

func matchingRules(rules []Rule, request Request) []Rule {
	result := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if ruleMatches(rule, request) {
			result = append(result, rule)
		}
	}
	return result
}

func newestRuleWithAction(sessionRules []Rule, agentRules []Rule, action Action) (Rule, bool) {
	var selected Rule
	found := false
	consider := func(rule Rule) {
		if rule.Action != action {
			return
		}
		if !found || rule.CreatedAt.After(selected.CreatedAt) ||
			(rule.CreatedAt.Equal(selected.CreatedAt) && rule.ID > selected.ID) {
			selected = rule
			found = true
		}
	}
	for _, rule := range sessionRules {
		consider(rule)
	}
	for _, rule := range agentRules {
		consider(rule)
	}
	return selected, found
}

func ruleMatches(rule Rule, request Request) bool {
	if rule.AgentID != request.AgentID || rule.ToolName != request.ToolName {
		return false
	}
	if rule.Scope == GrantSession && rule.SessionID != request.SessionID {
		return false
	}
	current := request.Identity.Normalize()
	if rule.Action == ActionDeny {
		return rule.Identity.MatchesDeny(current)
	}
	return rule.Identity.EqualExact(current)
}

func sameRuleTarget(a Rule, b Rule) bool {
	if a.AgentID != b.AgentID || a.SessionID != b.SessionID || a.ToolName != b.ToolName || a.Scope != b.Scope {
		return false
	}
	return capabilityLogicalTarget(a.Identity) == capabilityLogicalTarget(b.Identity)
}

func capabilityLogicalTarget(identity CapabilityIdentity) string {
	identity = identity.Normalize()
	switch identity.Kind {
	case CapabilityCommand:
		return string(identity.Kind) + "|" + identity.Tool + "|" + identity.Command
	case CapabilitySkillScript:
		return string(identity.Kind) + "|" + identity.Tool + "|" + identity.SkillName + "|" + identity.Script
	case CapabilityMCP:
		return string(identity.Kind) + "|" + identity.Tool + "|" + identity.MCPServerID + "|" + identity.MCPTool
	default:
		return string(identity.Kind) + "|" + identity.Tool
	}
}

func (e *Engine) defaultActionWithConfig(cfg config.PermissionConfig, risk RiskLevel) Action {
	var raw string
	switch risk {
	case RiskRead:
		raw = cfg.ReadAction
	case RiskWrite:
		raw = cfg.WriteAction
	case RiskExec:
		raw = cfg.ExecAction
	default:
		return ActionDeny
	}

	switch Action(strings.ToLower(strings.TrimSpace(raw))) {
	case ActionAllow:
		return ActionAllow
	case ActionAsk:
		return ActionAsk
	case ActionDeny:
		return ActionDeny
	default:
		return ActionDeny
	}
}
