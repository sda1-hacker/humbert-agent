package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const policySchemaVersion = 2

type policyDocument struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

// legacyPolicyDocumentV1 只用于一次性读取 Permission v1 文件。
// v1 Allow 没有 Sandbox/Executable/Skill 等安全身份，无法安全迁移，因此升级时撤销；
// v1 Deny 会迁移成更宽但只会收紧权限的 v2 Deny Identity。
type legacyPolicyDocumentV1 struct {
	Version int            `json:"version"`
	Rules   []legacyRuleV1 `json:"rules"`
}

type legacyRuleV1 struct {
	ID        string            `json:"id"`
	AgentID   string            `json:"agentId"`
	SessionID string            `json:"sessionId,omitempty"`
	ToolName  string            `json:"tool"`
	Action    Action            `json:"action"`
	Scope     GrantScope        `json:"scope"`
	Condition legacyConditionV1 `json:"condition,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}

type legacyConditionV1 struct {
	Command              string    `json:"command,omitempty"`
	MCPServerID          string    `json:"mcpServerId,omitempty"`
	MCPServerFingerprint string    `json:"mcpServerFingerprint,omitempty"`
	MCPToolRisk          RiskLevel `json:"mcpToolRisk,omitempty"`
}

// Store 持久化 Agent Scope Permission Rule。
//
// 文件使用 atomicfile 的“临时文件 + fsync + rename”提交，避免桌面应用异常退出留下半截
// JSON。Store 自己串行化 read-modify-write；Session 临时规则不进入本 Store，由 Engine
// 的内存状态管理。
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore 创建 Permission Policy Store，并在首次启动时初始化空文档。
func NewStore(ctx context.Context, path string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("初始化 Permission Store 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("初始化 Permission Store 被取消: %w", err)
	}
	if path == "" {
		return nil, errors.New("初始化 Permission Store 失败: 文件路径不能为空")
	}

	store := &Store{path: path}
	store.mu.Lock()
	defer store.mu.Unlock()

	if _, err := os.Lstat(path); err == nil {
		if _, err := store.loadLocked(ctx); err != nil {
			return nil, fmt.Errorf("读取已有 Permission Policy 失败: %w", err)
		}
		return store, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("检查 Permission Policy 文件失败: %w", err)
	}

	if err := atomicfile.WriteJSON(ctx, path, 0o600, policyDocument{
		Version: policySchemaVersion,
		Rules:   []Rule{},
	}); err != nil {
		return nil, fmt.Errorf("初始化 Permission Policy 文件失败: %w", err)
	}

	return store, nil
}

// List 返回按创建时间和 ID 稳定排序的 Rule Snapshot。
func (s *Store) List(ctx context.Context) ([]Rule, error) {
	if ctx == nil {
		return nil, errors.New("读取 Permission Rules 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取 Permission Rules 被取消: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	result := append([]Rule(nil), doc.Rules...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

// Upsert 新增或替换一个 Agent Scope Rule。
//
// 除相同 ID 外，相同 Agent + Capability 逻辑目标的长期规则也会被新结论替换。这样用户从
// “始终允许”改为“始终拒绝”（或反向）时只保留一个明确结论，不依赖规则顺序。
func (s *Store) Upsert(ctx context.Context, rule Rule) error {
	if ctx == nil {
		return errors.New("保存 Permission Rule 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存 Permission Rule 被取消: %w", err)
	}
	if err := rule.Validate(); err != nil {
		return fmt.Errorf("保存 Permission Rule 失败: %w", err)
	}
	if rule.Scope != GrantAgent {
		return errors.New("Permission Store 只允许持久化 Agent Scope Rule")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}

	filtered := make([]Rule, 0, len(doc.Rules)+1)
	for _, existing := range doc.Rules {
		// 同一个 Agent + Capability 逻辑目标只保留一条长期结论。用户后来选择“始终拒绝”
		// 或“始终允许”时应覆盖旧结论，而不是依赖文件中的先后顺序制造冲突。
		if existing.ID == rule.ID || samePersistentTarget(existing, rule) {
			continue
		}
		filtered = append(filtered, existing)
	}
	doc.Rules = append(filtered, rule)

	if err := atomicfile.WriteJSON(ctx, s.path, 0o600, doc); err != nil {
		return fmt.Errorf("写入 Permission Policy 失败: %w", err)
	}
	return nil
}

// Delete 删除一个持久化 Agent Scope Rule。
func (s *Store) Delete(ctx context.Context, ruleID string) error {
	if ctx == nil {
		return errors.New("删除 Permission Rule 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("删除 Permission Rule 被取消: %w", err)
	}
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return errors.New("删除 Permission Rule 失败: RuleID 不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}

	filtered := make([]Rule, 0, len(doc.Rules))
	found := false
	for _, rule := range doc.Rules {
		if rule.ID == ruleID {
			found = true
			continue
		}
		filtered = append(filtered, rule)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrRuleNotFound, ruleID)
	}
	doc.Rules = filtered

	if err := atomicfile.WriteJSON(ctx, s.path, 0o600, doc); err != nil {
		return fmt.Errorf("写入 Permission Policy 失败: %w", err)
	}
	return nil
}

// DeleteByAction 批量删除指定 Action 的持久化规则，并返回实际删除数量。
//
// 设置页“撤销全部长期授权”只删除 Allow，显式 Deny 会保留。整个 read-modify-write 在
// Store 锁内完成并一次原子提交，避免逐条 Delete 导致中途失败留下半完成状态。
func (s *Store) DeleteByAction(ctx context.Context, action Action) (int, error) {
	if ctx == nil {
		return 0, errors.New("批量删除 Permission Rule 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("批量删除 Permission Rule 被取消: %w", err)
	}
	if action != ActionAllow && action != ActionDeny {
		return 0, fmt.Errorf("批量删除 Permission Rule 失败: Action %q 不合法", action)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked(ctx)
	if err != nil {
		return 0, err
	}
	filtered := make([]Rule, 0, len(doc.Rules))
	deleted := 0
	for _, rule := range doc.Rules {
		if rule.Action == action {
			deleted++
			continue
		}
		filtered = append(filtered, rule)
	}
	if deleted == 0 {
		return 0, nil
	}
	doc.Rules = filtered
	if err := atomicfile.WriteJSON(ctx, s.path, 0o600, doc); err != nil {
		return 0, fmt.Errorf("写入 Permission Policy 失败: %w", err)
	}
	return deleted, nil
}

func samePersistentTarget(a Rule, b Rule) bool {
	if a.AgentID != b.AgentID || a.ToolName != b.ToolName || a.Scope != GrantAgent || b.Scope != GrantAgent {
		return false
	}
	return capabilityLogicalTarget(a.Identity) == capabilityLogicalTarget(b.Identity)
}

func (s *Store) loadLocked(ctx context.Context) (policyDocument, error) {
	var envelope struct {
		Version int             `json:"version"`
		Rules   json.RawMessage `json:"rules"`
	}
	if err := atomicfile.ReadJSON(ctx, s.path, &envelope); err != nil {
		return policyDocument{}, fmt.Errorf("读取 Permission Policy 失败: %w", err)
	}

	switch envelope.Version {
	case policySchemaVersion:
		var rules []Rule
		if err := decodeStrictJSON(envelope.Rules, &rules); err != nil {
			return policyDocument{}, fmt.Errorf("解析 Permission v2 Rules 失败: %w", err)
		}
		doc := policyDocument{Version: policySchemaVersion, Rules: rules}
		if err := validatePolicyDocument(doc); err != nil {
			return policyDocument{}, err
		}
		return doc, nil

	case 1:
		var rules []legacyRuleV1
		if err := decodeStrictJSON(envelope.Rules, &rules); err != nil {
			return policyDocument{}, fmt.Errorf("解析 Permission v1 Rules 失败: %w", err)
		}
		legacy := legacyPolicyDocumentV1{Version: 1, Rules: rules}
		doc := migratePolicyV1(legacy)
		if err := validatePolicyDocument(doc); err != nil {
			return policyDocument{}, fmt.Errorf("迁移 Permission v1 Policy 失败: %w", err)
		}
		if err := atomicfile.WriteJSON(ctx, s.path, 0o600, doc); err != nil {
			return policyDocument{}, fmt.Errorf("写回 Permission v2 Policy 失败: %w", err)
		}
		return doc, nil

	default:
		return policyDocument{}, fmt.Errorf("不支持的 Permission Policy Version: %d", envelope.Version)
	}
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("包含多个 JSON 值")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func validatePolicyDocument(doc policyDocument) error {
	if doc.Version != policySchemaVersion {
		return fmt.Errorf("不支持的 Permission Policy Version: %d", doc.Version)
	}
	for _, rule := range doc.Rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("Permission Policy 包含非法 Rule %q: %w", rule.ID, err)
		}
		if rule.Scope != GrantAgent {
			return fmt.Errorf("Permission Policy Rule %q 不是 Agent Scope", rule.ID)
		}
	}
	return nil
}

func migratePolicyV1(legacy legacyPolicyDocumentV1) policyDocument {
	doc := policyDocument{Version: policySchemaVersion, Rules: []Rule{}}
	for _, old := range legacy.Rules {
		// v1 Allow 没有足够的安全身份，升级后必须重新审批。
		if old.Action != ActionDeny || old.Scope != GrantAgent {
			continue
		}
		identity := CapabilityIdentity{
			Version: CapabilityIdentityVersion,
			Kind:    inferLegacyCapabilityKind(old),
			Tool:    strings.TrimSpace(old.ToolName),
		}
		if identity.Kind == CapabilityCommand {
			identity.Command = strings.TrimSpace(old.Condition.Command)
		}
		if identity.Kind == CapabilityMCP {
			identity.MCPServerID = strings.TrimSpace(old.Condition.MCPServerID)
		}
		identity = identity.Normalize()
		rule := Rule{
			ID:        strings.TrimSpace(old.ID),
			AgentID:   strings.TrimSpace(old.AgentID),
			ToolName:  strings.TrimSpace(old.ToolName),
			Action:    ActionDeny,
			Scope:     GrantAgent,
			Identity:  identity,
			CreatedAt: old.CreatedAt,
		}
		if rule.ID == "" || rule.AgentID == "" || rule.ToolName == "" {
			continue
		}
		if rule.CreatedAt.IsZero() {
			rule.CreatedAt = time.Now().UTC()
		}
		if err := rule.Validate(); err != nil {
			continue
		}
		doc.Rules = append(doc.Rules, rule)
	}
	return doc
}

func inferLegacyCapabilityKind(rule legacyRuleV1) CapabilityKind {
	if strings.TrimSpace(rule.Condition.MCPServerID) != "" {
		return CapabilityMCP
	}
	switch strings.TrimSpace(rule.ToolName) {
	case "run_command":
		return CapabilityCommand
	case "run_skill_script":
		return CapabilitySkillScript
	default:
		return CapabilityBuiltin
	}
}
