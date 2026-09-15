package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrPathDenied = errors.New("Sandbox 拒绝访问该路径")

// CheckPath 是 Humbert 文件系统访问的唯一 PathGuard 入口。
// 相对路径始终相对于 WorkspaceRoot；返回值中的 CanonicalPath 已解析 symlink。
func (p EffectivePolicy) CheckPath(input string, operation PathOperation) (PathDecision, error) {
	decision := PathDecision{Operation: operation, Required: operation.RequiredAccess(), Access: AccessBlocked}
	if err := p.Validate(); err != nil {
		return decision, err
	}
	if err := operation.Validate(); err != nil {
		return decision, err
	}

	input = normalizePlatformPath(strings.TrimSpace(input))
	if input == "" || input == "." {
		input = p.WorkspaceRoot
	} else if !filepath.IsAbs(input) {
		input = filepath.Join(p.WorkspaceRoot, input)
	}

	absolute, err := filepath.Abs(input)
	if err != nil {
		return decision, fmt.Errorf("解析绝对路径失败: %w", err)
	}
	absolute = filepath.Clean(absolute)

	canonical, err := canonicalizePath(absolute, operation.allowsMissingTarget())
	if err != nil {
		return decision, err
	}
	decision.CanonicalPath = canonical

	// FullAccess 是用户显式选择的逃生模式：文件访问退回当前 OS 用户权限。
	if p.Profile == ProfileFullAccess {
		decision.Access = AccessFull
		decision.MatchedRoot = filesystemRoot(canonical)
		decision.Source = RuleSourceUnrestricted
		decision.Allowed = true
		return decision, nil
	}

	// BLOCKED 是硬拒绝：只要命中任意受保护规则，任何父级/同级授权都不能覆盖。
	for _, rule := range p.PathRules {
		if rule.Access != AccessBlocked || !pathRuleMatches(rule, canonical) {
			continue
		}
		decision.MatchedRoot = rule.Root
		decision.Source = rule.Source
		decision.Reason = fmt.Sprintf("%s 命中受保护路径 %s", canonical, rule.Root)
		return decision, fmt.Errorf("%w: %s", ErrPathDenied, decision.Reason)
	}

	// 其余 PathRule 都是“授权型”规则。Agent 的 Additional Paths 不用于降权，因此
	// 多条规则重叠时取最高访问级别；同级别时取更具体的 root 方便审计和 os.Root。
	var matched *PathRule
	for i := range p.PathRules {
		rule := &p.PathRules[i]
		if rule.Access == AccessBlocked || !pathWithin(canonical, rule.Root) {
			continue
		}
		if matched == nil || rule.Access > matched.Access || (rule.Access == matched.Access && pathMoreSpecific(rule.Root, matched.Root)) {
			matched = rule
		}
	}
	if matched == nil {
		decision.Reason = fmt.Sprintf("%s 不在任何允许目录中", canonical)
		return decision, fmt.Errorf("%w: %s", ErrPathDenied, decision.Reason)
	}

	decision.Access = matched.Access
	decision.MatchedRoot = matched.Root
	decision.Source = matched.Source
	decision.Allowed = matched.Access.Allows(operation)
	if !decision.Allowed {
		decision.Reason = fmt.Sprintf("%s 的访问级别为 %s，%s 操作至少需要 %s", canonical, matched.Access, operation, decision.Required)
		return decision, fmt.Errorf("%w: %s", ErrPathDenied, decision.Reason)
	}
	return decision, nil
}

// CanonicalRoot 规范化用户显式配置的 Sandbox Root。目录必须存在且不能本身是 symlink。
func CanonicalRoot(path string) (string, error) {
	path = normalizePlatformPath(strings.TrimSpace(path))
	if path == "" {
		return "", errors.New("Sandbox 目录不能为空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析 Sandbox 目录失败: %w", err)
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("读取 Sandbox 目录失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Sandbox Root 不能是符号链接")
	}
	if !info.IsDir() {
		return "", errors.New("Sandbox Root 必须是目录")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("解析 Sandbox 目录真实路径失败: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func canonicalizePath(path string, allowMissing bool) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved), nil
	} else if !allowMissing || !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("解析路径真实位置失败: %w", err)
	}

	// 新文件不存在时必须先解析最近存在的父目录，防止 workspace/link/new.txt
	// 通过 symlink 绕到 Workspace 外。
	parent := filepath.Dir(path)
	base := filepath.Base(path)
	for {
		resolvedParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			return filepath.Clean(filepath.Join(resolvedParent, base)), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("解析写入路径父目录失败: %w", err)
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", fmt.Errorf("找不到写入路径的有效父目录: %s", path)
		}
		base = filepath.Join(filepath.Base(parent), base)
		parent = next
	}
}

func pathWithin(path string, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func pathMoreSpecific(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if pathEqual(a, b) {
		return false
	}
	return pathWithin(a, b)
}

func filesystemRoot(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		if volume := filepath.VolumeName(path); volume != "" {
			return volume + string(filepath.Separator)
		}
	}
	return string(filepath.Separator)
}

// normalizePlatformPath 同时识别 Windows 原生路径与 MSYS/Git-Bash 的 /c/... 表达，
// 避免只靠字符串前缀造成 OpenHanako 曾出现过的绕过类型。
func normalizePlatformPath(value string) string {
	if runtime.GOOS != "windows" {
		return value
	}
	value = strings.ReplaceAll(value, "/", `\`)
	// 经过 ReplaceAll 后原 /c/foo 为 \c\foo。
	if len(value) >= 3 && value[0] == '\\' && ((value[1] >= 'a' && value[1] <= 'z') || (value[1] >= 'A' && value[1] <= 'Z')) && value[2] == '\\' {
		value = strings.ToUpper(value[1:2]) + ":" + value[2:]
	}
	return value
}
