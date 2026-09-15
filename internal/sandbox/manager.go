package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Manager 负责把全局默认值、Agent 配置与当前 Workspace 冻结成 EffectivePolicy。
type Manager struct {
	mu             sync.RWMutex
	config         Config
	homeDir        string
	protectedRules []PathRule
	capability     Capability
	runner         *Runner
}

func NewManager(config Config, homeDir string) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	homeDir = strings.TrimSpace(homeDir)
	if homeDir == "" {
		return nil, errors.New("Sandbox HomeDir 不能为空")
	}
	absolute, err := filepath.Abs(homeDir)
	if err != nil {
		return nil, fmt.Errorf("解析 Humbert HomeDir 失败: %w", err)
	}
	protected := defaultProtectedRules(filepath.Clean(absolute))
	capability := probeNativeCapability()
	manager := &Manager{
		config:         config,
		homeDir:        filepath.Clean(absolute),
		protectedRules: protected,
		capability:     capability,
	}
	manager.runner = NewRunner(manager)
	return manager, nil
}

func (m *Manager) Capability() Capability { return m.capability }

func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// UpdateConfig 原子替换应用级 Sandbox 默认配置。已经冻结到 Runtime Turn 的
// EffectivePolicy 不受影响；后续 Resolve 会使用新配置。
func (m *Manager) UpdateConfig(config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	m.config = config
	m.mu.Unlock()
	return nil
}

func (m *Manager) Runner() *Runner { return m.runner }

func (m *Manager) Resolve(ctx context.Context, workspaceRoot string, requested AgentPolicy) (EffectivePolicy, error) {
	if ctx == nil {
		return EffectivePolicy{}, errors.New("Sandbox Resolve Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return EffectivePolicy{}, err
	}
	if err := requested.Validate(); err != nil {
		return EffectivePolicy{}, err
	}

	workspaceRoot, err := CanonicalRoot(workspaceRoot)
	if err != nil {
		return EffectivePolicy{}, fmt.Errorf("Sandbox Workspace 无效: %w", err)
	}
	config := m.Config()
	profile := NormalizeProfile(requested.Profile, config.DefaultProfile)
	network := NormalizeNetworkMode(requested.NetworkMode, config.DefaultNetworkMode)
	native := NormalizeNativeMode(requested.NativeMode, config.DefaultNativeMode)

	if profile != ProfileFullAccess {
		if err := rejectProtectedPath(workspaceRoot, m.protectedRules); err != nil {
			return EffectivePolicy{}, fmt.Errorf("Agent Workspace 与受保护目录冲突: %w", err)
		}
	}

	pathRules := make([]PathRule, 0, len(m.protectedRules)+1+len(requested.AdditionalWritePaths))
	if profile != ProfileFullAccess {
		for _, rule := range m.protectedRules {
			pathRules = appendPathRule(pathRules, rule)
		}
		pathRules = appendPathRule(pathRules, PathRule{Root: workspaceRoot, Access: AccessFull, Source: RuleSourceWorkspace})

		if profile == ProfileStandard {
			// Standard 允许读取用户 Home 下普通文件，但硬保护目录仍由 BLOCKED 规则覆盖。
			if userHome, homeErr := os.UserHomeDir(); homeErr == nil {
				if root, rootErr := CanonicalRoot(userHome); rootErr == nil {
					pathRules = appendGrantRule(pathRules, PathRule{Root: root, Access: AccessReadOnly, Source: RuleSourceStandardHome})
				}
			}
		}
	}

	for _, raw := range requested.AdditionalWritePaths {
		root, err := CanonicalRoot(raw)
		if err != nil {
			return EffectivePolicy{}, fmt.Errorf("额外读写目录 %q 无效: %w", raw, err)
		}
		if profile != ProfileFullAccess {
			if err := rejectProtectedPath(root, m.protectedRules); err != nil {
				return EffectivePolicy{}, fmt.Errorf("额外读写目录 %q 与受保护目录冲突: %w", raw, err)
			}
			pathRules = appendGrantRule(pathRules, PathRule{Root: root, Access: AccessReadWrite, Source: RuleSourceAdditionalWrite})
		}
	}

	policy := EffectivePolicy{
		Profile:       profile,
		NetworkMode:   network,
		NativeMode:    native,
		WorkspaceRoot: workspaceRoot,
		PathRules:     sortedPathRules(pathRules),
		Capability:    m.capability,
	}
	if err := policy.Validate(); err != nil {
		return EffectivePolicy{}, err
	}
	return policy, nil
}

func rejectProtectedPath(root string, protectedRules []PathRule) error {
	for _, rule := range protectedRules {
		if rule.Access != AccessBlocked || !pathRuleMatches(rule, root) {
			continue
		}
		return fmt.Errorf("%s 位于受保护路径 %s 内", root, rule.Root)
	}
	return nil
}

// defaultProtectedRules 构建 Humbert 的硬保护路径。目录型规则保护整棵目录树；
// ProtectedFile 只保护一个精确文件。所有规则都高于 Standard Home 的 READ_ONLY 授权，
// 也不能被 Agent AdditionalWrite 或 Runtime MCP grant 覆盖。
func defaultProtectedRules(appHome string) []PathRule {
	userHome, _ := os.UserHomeDir()
	return protectedRulesFor(appHome, userHome)
}

// protectedRulesFor 被测试直接使用，确保不同平台的默认敏感路径可以在不依赖真实用户
// Home 的情况下做回归验证。
func protectedRulesFor(appHome, userHome string) []PathRule {
	rules := []PathRule{}

	// Humbert 自身控制面与用户会话数据不应该被 Agent 当作普通 Home 文件读取。
	for _, candidate := range []string{
		filepath.Join(appHome, "secrets"),
		filepath.Join(appHome, "config"),
		filepath.Join(appHome, "agents"),
		filepath.Join(appHome, "mcp"),
		filepath.Join(appHome, "logs"),
	} {
		rules = appendProtectedPathRuleCandidates(rules, candidate, RuleSourceProtected)
	}
	for _, candidate := range []string{
		filepath.Join(appHome, "config.yaml"),
	} {
		rules = appendProtectedPathRuleCandidates(rules, candidate, RuleSourceProtectedFile)
	}

	userHome = strings.TrimSpace(userHome)
	if userHome == "" {
		return sortedPathRules(rules)
	}
	userHome = filepath.Clean(userHome)

	// 高价值凭据目录。即使 Standard Profile 允许读取整个 Home，这些目录仍硬拒绝。
	directories := []string{
		filepath.Join(userHome, ".ssh"),
		filepath.Join(userHome, ".gnupg"),
		filepath.Join(userHome, ".aws"),
		filepath.Join(userHome, ".kube"),
		filepath.Join(userHome, ".docker"),
		filepath.Join(userHome, ".azure"),
		filepath.Join(userHome, ".config", "gcloud"),
		filepath.Join(userHome, ".config", "gh"),
		filepath.Join(userHome, ".local", "share", "keyrings"),
	}
	if runtime.GOOS == "darwin" {
		directories = append(directories, filepath.Join(userHome, "Library", "Keychains"))
	}
	if runtime.GOOS == "windows" {
		directories = append(directories,
			filepath.Join(userHome, "AppData", "Roaming", "Microsoft", "Credentials"),
			filepath.Join(userHome, "AppData", "Local", "Microsoft", "Credentials"),
		)
	}
	for _, candidate := range directories {
		rules = appendProtectedPathRuleCandidates(rules, candidate, RuleSourceProtected)
	}

	// 常见的单文件凭据和命令历史。这里刻意保护“用户级固定位置”，而不是递归阻止
	// Workspace 内的 .env：开发 Agent 往往需要处理项目自己的 .env.example/.env；真正
	// 的 Workspace 内容属于用户显式交给 Agent 的工作区。用户 Home 根目录/配置目录下的
	// 个人凭据则不应该因为 Standard READ_ONLY 被读取。
	files := []string{
		filepath.Join(userHome, ".env"),
		filepath.Join(userHome, ".netrc"),
		filepath.Join(userHome, "_netrc"),
		filepath.Join(userHome, ".git-credentials"),
		filepath.Join(userHome, ".npmrc"),
		filepath.Join(userHome, ".pypirc"),
		filepath.Join(userHome, ".pgpass"),
		filepath.Join(userHome, ".my.cnf"),
		filepath.Join(userHome, ".curlrc"),
		filepath.Join(userHome, ".gem", "credentials"),
		filepath.Join(userHome, ".cargo", "credentials"),
		filepath.Join(userHome, ".cargo", "credentials.toml"),
		filepath.Join(userHome, ".config", "pip", "pip.conf"),
		filepath.Join(userHome, ".config", "pypoetry", "auth.toml"),
		filepath.Join(userHome, ".config", "containers", "auth.json"),
		filepath.Join(userHome, ".config", "helm", "registry", "config.json"),
		filepath.Join(userHome, ".config", "composer", "auth.json"),
		filepath.Join(userHome, ".config", "rclone", "rclone.conf"),
		filepath.Join(userHome, ".zsh_history"),
		filepath.Join(userHome, ".bash_history"),
		filepath.Join(userHome, ".python_history"),
		filepath.Join(userHome, ".node_repl_history"),
		filepath.Join(userHome, ".mysql_history"),
		filepath.Join(userHome, ".psql_history"),
	}
	for _, candidate := range files {
		rules = appendProtectedPathRuleCandidates(rules, candidate, RuleSourceProtectedFile)
	}
	return sortedPathRules(rules)
}

// appendProtectedPathRuleCandidates 同时记录敏感路径的逻辑位置和当前真实位置。
// 例如 ~/.aws -> /Volumes/secret/aws 或 ~/.netrc -> /Volumes/secret/netrc 时，PathGuard
// 会先 canonicalize 用户输入；同时保留 symlink target 可以避免 canonical path 绕过 BLOCKED。
func appendProtectedPathRuleCandidates(rules []PathRule, candidate string, source RuleSource) []PathRule {
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return rules
	}
	absolute = filepath.Clean(absolute)
	rules = appendPathRule(rules, PathRule{Root: absolute, Access: AccessBlocked, Source: source})
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		rules = appendPathRule(rules, PathRule{Root: filepath.Clean(resolved), Access: AccessBlocked, Source: source})
	}
	return rules
}

// PrepareExternalCommand 为“由第三方库拥有进程生命周期”的本地子进程准备原生 Sandbox 包装。
//
// run_command 等 Humbert 自己启动的进程应优先使用 Runner，因为 Runner 还能在 Windows
// 附加 Restricted Token / Job Object。该方法主要用于 Eino officialmcp stdio 这类只允许
// Humbert 提供 command/args/env、但不暴露 exec.Cmd hook 的集成边界。
func (m *Manager) PrepareExternalCommand(policy EffectivePolicy, command string, args []string, workingDirectory string) (string, []string, func(), bool, error) {
	if m == nil {
		return "", nil, nil, false, errors.New("Sandbox Manager 未初始化")
	}
	if err := policy.Validate(); err != nil {
		return "", nil, nil, false, err
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return "", nil, nil, false, errors.New("External Command 不能为空")
	}
	executable, err := exec.LookPath(command)
	if err != nil {
		return "", nil, nil, false, fmt.Errorf("找不到程序 %q: %w", command, err)
	}
	if !filepath.IsAbs(executable) {
		executable, err = filepath.Abs(executable)
		if err != nil {
			return "", nil, nil, false, err
		}
	}

	if err := validateProcessIsolationPolicy(policy); err != nil {
		return "", nil, nil, false, err
	}

	// Eino officialmcp stdio 当前没有公开 exec.Cmd hook。Windows 的 Restricted Token / Job
	// Object 必须设置在 exec.Cmd 上，因此无法获得 Runner 的 token/job 保护。受限 Profile 已在
	// validateProcessIsolationPolicy 中 fail-closed；这里只允许 FullAccess + Preferred 明确退回
	// 当前用户权限。Required 仍拒绝，避免把 nativeUsed=false 伪装成已隔离。
	if runtime.GOOS == "windows" && policy.NativeMode != NativeOff {
		if policy.NativeMode == NativeRequired {
			return "", nil, nil, false, errors.New("Windows 上 Eino officialmcp stdio 未暴露 Restricted Token/Job Object 进程 hook；Native Sandbox=required 时拒绝启动")
		}
		return executable, append([]string(nil), args...), nil, false, nil
	}
	return prepareNativeCommand(policy, executable, args, workingDirectory)
}
