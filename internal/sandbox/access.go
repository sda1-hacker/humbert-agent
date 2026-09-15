package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// AccessLevel 描述 PathGuard 对一个目录树授予的文件系统能力。
//
// 能力按从低到高递增：
//   - BLOCKED: 完全禁止访问。
//   - READ_ONLY: 允许读取、列目录和搜索。
//   - READ_WRITE: 在 READ_ONLY 基础上允许创建和修改，但不允许删除/移动。
//   - FULL: 允许完整文件管理，包括删除和移动。
//
// READ_WRITE 与 FULL 的差异属于 Humbert 应用层语义。Seatbelt/Bubblewrap 等原生
// Sandbox 通常只能表达 RO/RW 目录，不能可靠地区分 write 与 unlink/rename；因此
// NativeFilesystemView 会把 READ_WRITE 和 FULL 都编译成原生“可写”目录。
type AccessLevel uint8

const (
	AccessBlocked AccessLevel = iota
	AccessReadOnly
	AccessReadWrite
	AccessFull
)

func (a AccessLevel) String() string {
	switch a {
	case AccessBlocked:
		return "BLOCKED"
	case AccessReadOnly:
		return "READ_ONLY"
	case AccessReadWrite:
		return "READ_WRITE"
	case AccessFull:
		return "FULL"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", uint8(a))
	}
}

func (a AccessLevel) Validate() error {
	switch a {
	case AccessBlocked, AccessReadOnly, AccessReadWrite, AccessFull:
		return nil
	default:
		return fmt.Errorf("不支持的 PathGuard AccessLevel: %d", a)
	}
}

// PathOperation 是 PathGuard 的业务动作。Tool 必须声明自己实际要做的动作，不能再用
// 一个笼统的“write=true”同时代表创建、覆盖、删除和移动。
type PathOperation string

const (
	OpRead       PathOperation = "read"
	OpList       PathOperation = "list"
	OpSearch     PathOperation = "search"
	OpCreate     PathOperation = "create"
	OpModify     PathOperation = "modify"
	OpDelete     PathOperation = "delete"
	OpMoveSource PathOperation = "move_source"
	OpMoveTarget PathOperation = "move_target"
)

func (op PathOperation) Validate() error {
	switch op {
	case OpRead, OpList, OpSearch, OpCreate, OpModify, OpDelete, OpMoveSource, OpMoveTarget:
		return nil
	default:
		return fmt.Errorf("不支持的 PathGuard PathOperation: %q", op)
	}
}

// RequiredAccess 返回执行该动作所需的最低访问级别。
func (op PathOperation) RequiredAccess() AccessLevel {
	switch op {
	case OpRead, OpList, OpSearch:
		return AccessReadOnly
	case OpCreate, OpModify:
		return AccessReadWrite
	case OpDelete, OpMoveSource, OpMoveTarget:
		return AccessFull
	default:
		return AccessFull
	}
}

// allowsMissingTarget 表示 PathGuard 是否允许目标在解析时尚不存在。
func (op PathOperation) allowsMissingTarget() bool {
	switch op {
	case OpCreate, OpModify, OpMoveTarget:
		return true
	default:
		return false
	}
}

func (a AccessLevel) Allows(op PathOperation) bool {
	return a >= op.RequiredAccess()
}

// RuleSource 用于审计和诊断，说明一条路径规则是如何进入 EffectivePolicy 的。
type RuleSource string

const (
	RuleSourceProtected       RuleSource = "system_protected"
	RuleSourceProtectedFile   RuleSource = "system_protected_file"
	RuleSourceWorkspace       RuleSource = "workspace"
	RuleSourceStandardHome    RuleSource = "profile_standard_home"
	RuleSourceAdditionalWrite RuleSource = "additional_write"
	RuleSourceMCPFilesystem   RuleSource = "mcp_filesystem"
	RuleSourceMCPLauncher     RuleSource = "mcp_launcher_runtime"
	RuleSourceUnrestricted    RuleSource = "unrestricted"
)

// PathRule 是 PathGuard 的唯一持久化运行期路径规则。
// 默认情况下 Root 作用于自身及所有后代路径；RuleSourceProtectedFile 是精确单文件规则。
type PathRule struct {
	Root   string      `json:"root"`
	Access AccessLevel `json:"access"`
	Source RuleSource  `json:"source"`
}

func (r PathRule) Validate() error {
	if err := r.Access.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Root) == "" {
		return errors.New("PathGuard Rule Root 不能为空")
	}
	if !filepath.IsAbs(r.Root) {
		return fmt.Errorf("PathGuard Rule Root 必须是绝对路径: %s", r.Root)
	}
	if strings.TrimSpace(string(r.Source)) == "" {
		return errors.New("PathGuard Rule Source 不能为空")
	}
	return nil
}

// PathDecision 是 PathGuard 对一次文件系统动作的完整裁决。
type PathDecision struct {
	CanonicalPath string        `json:"canonical_path"`
	Operation     PathOperation `json:"operation"`
	Required      AccessLevel   `json:"required_access"`
	Access        AccessLevel   `json:"access"`
	MatchedRoot   string        `json:"matched_root,omitempty"`
	Source        RuleSource    `json:"source,omitempty"`
	Allowed       bool          `json:"allowed"`
	Reason        string        `json:"reason,omitempty"`
}

// NativeFilesystemView 是由 PathRules 临时编译出的 OS Sandbox 视图。
// 它不是第二份安全状态，只是 Seatbelt/Bubblewrap 这类后端所需的 RO/RW 表达。
type NativeFilesystemView struct {
	ReadOnlyRoots []string
	WritableRoots []string
	BlockedRoots  []string
	BlockedFiles  []string
}

// WithPathAccess 返回一个只在当前 Runtime 子作用域生效的 Policy 副本。
// 典型用途是 filesystem MCP 显式配置的 allowed directory 或 package-manager launcher
// 的 runtime 目录。PathRules 仍然是唯一事实来源。
func (p EffectivePolicy) WithPathAccess(root string, access AccessLevel, source RuleSource) (EffectivePolicy, error) {
	if p.Profile == ProfileFullAccess {
		return p, nil
	}
	if access == AccessBlocked {
		return EffectivePolicy{}, errors.New("WithPathAccess 只能追加授权规则，BLOCKED 应由 Manager 构建")
	}
	root, err := CanonicalRoot(root)
	if err != nil {
		return EffectivePolicy{}, err
	}

	// 不允许把一个明确位于硬保护路径内部的子目录重新授权。
	for _, rule := range p.PathRules {
		if rule.Access == AccessBlocked && pathRuleMatches(rule, root) {
			return EffectivePolicy{}, fmt.Errorf("%s 位于受保护路径 %s 内", root, rule.Root)
		}
	}

	result := p
	result.PathRules = append([]PathRule(nil), p.PathRules...)
	result.PathRules = appendGrantRule(result.PathRules, PathRule{Root: root, Access: access, Source: source})
	result.PathRules = sortedPathRules(result.PathRules)
	if err := result.Validate(); err != nil {
		return EffectivePolicy{}, err
	}
	return result, nil
}

// NativeFilesystem 编译原生 Sandbox 所需的最小 RO/RW/Blocked 目录集合。
func (p EffectivePolicy) NativeFilesystem() NativeFilesystemView {
	if p.Profile == ProfileFullAccess {
		return NativeFilesystemView{}
	}
	var view NativeFilesystemView
	for _, rule := range p.PathRules {
		switch rule.Access {
		case AccessBlocked:
			if rule.Source == RuleSourceProtectedFile {
				view.BlockedFiles = appendUniquePath(view.BlockedFiles, rule.Root)
			} else {
				view.BlockedRoots = appendUniquePath(view.BlockedRoots, rule.Root)
			}
		case AccessReadOnly:
			view.ReadOnlyRoots = appendUniquePath(view.ReadOnlyRoots, rule.Root)
		case AccessReadWrite, AccessFull:
			view.WritableRoots = appendUniquePath(view.WritableRoots, rule.Root)
		}
	}

	// 若一个 RO root 已经被可写父目录覆盖，那么对 OS Sandbox 来说它没有独立意义。
	filteredReadOnly := view.ReadOnlyRoots[:0]
	for _, root := range view.ReadOnlyRoots {
		if coveredByAnyRoot(root, view.WritableRoots) {
			continue
		}
		filteredReadOnly = append(filteredReadOnly, root)
	}
	view.ReadOnlyRoots = collapseCoveredRoots(sortedPaths(filteredReadOnly))
	view.WritableRoots = collapseCoveredRoots(sortedPaths(view.WritableRoots))
	view.BlockedRoots = collapseCoveredRoots(sortedPaths(view.BlockedRoots))

	// 已被 BLOCKED 目录覆盖的单文件规则无需重复交给原生 Sandbox。
	filteredBlockedFiles := make([]string, 0, len(view.BlockedFiles))
	for _, path := range sortedPaths(view.BlockedFiles) {
		if coveredByAnyRoot(path, view.BlockedRoots) {
			continue
		}
		filteredBlockedFiles = appendUniquePath(filteredBlockedFiles, path)
	}
	view.BlockedFiles = filteredBlockedFiles
	return view
}

// pathRuleMatches 判断一个 PathRule 是否覆盖目标路径。目录型 BLOCKED 规则覆盖整棵子树；
// ProtectedFile 是精确文件规则，只匹配该 canonical path 本身。
func pathRuleMatches(rule PathRule, path string) bool {
	if rule.Source == RuleSourceProtectedFile {
		return pathEqual(path, rule.Root)
	}
	return pathWithin(path, rule.Root)
}

// appendGrantRule 合并同一 Root 上的授权规则。Agent 配置中的额外路径是“授予能力”，
// 不是“降级能力”，因此同 Root 上保留更高权限。BLOCKED 规则永远不会由这里覆盖。
func appendGrantRule(rules []PathRule, incoming PathRule) []PathRule {
	incoming.Root = filepath.Clean(incoming.Root)
	for i := range rules {
		if !pathEqual(rules[i].Root, incoming.Root) {
			continue
		}
		if rules[i].Access == AccessBlocked {
			return rules
		}
		if incoming.Access > rules[i].Access {
			rules[i] = incoming
		}
		return rules
	}
	return append(rules, incoming)
}

func appendPathRule(rules []PathRule, incoming PathRule) []PathRule {
	incoming.Root = filepath.Clean(incoming.Root)
	for i := range rules {
		if !pathEqual(rules[i].Root, incoming.Root) {
			continue
		}
		// BLOCKED 是硬拒绝，优先于任何授权。
		if rules[i].Access == AccessBlocked || incoming.Access == AccessBlocked {
			if incoming.Access == AccessBlocked {
				rules[i] = incoming
			}
			return rules
		}
		if incoming.Access > rules[i].Access {
			rules[i] = incoming
		}
		return rules
	}
	return append(rules, incoming)
}

func sortedPathRules(rules []PathRule) []PathRule {
	result := append([]PathRule(nil), rules...)
	sort.SliceStable(result, func(i, j int) bool {
		left := strings.ToLower(filepath.Clean(result[i].Root))
		right := strings.ToLower(filepath.Clean(result[j].Root))
		if left != right {
			return left < right
		}
		if result[i].Access != result[j].Access {
			return result[i].Access < result[j].Access
		}
		return result[i].Source < result[j].Source
	})
	return result
}

func appendUniquePath(values []string, value string) []string {
	for _, existing := range values {
		if pathEqual(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func pathEqual(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func sortedPaths(values []string) []string {
	result := append([]string(nil), values...)
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func coveredByAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if pathWithin(path, root) {
			return true
		}
	}
	return false
}

func collapseCoveredRoots(values []string) []string {
	result := make([]string, 0, len(values))
	for _, candidate := range values {
		covered := false
		for _, existing := range result {
			if pathWithin(candidate, existing) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, candidate)
		}
	}
	return result
}
