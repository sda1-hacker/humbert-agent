package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

// Manager 管理 ~/.humbert-agent/skills 下的本地 Skill Package。
//
// Manager 的职责边界：
//   - 扫描与验证 Skill 包；
//   - 安装/更新/重新安装/删除本地 Skill；
//   - 校验 Agent 的 enabled_skills；
//   - 为一次 Runtime Turn 构造不可变 Skill Snapshot。
//
// Manager 自身不运行脚本、不调用外部进程，也不修改 Agent Profile。scripts/ 默认仍作为
// Skill 文档资源按需读取；需要执行时只能由独立的 run_skill_script Tool 将当前 Turn 冻结的
// Package Stage 到 Workspace 后执行，不能绕过 Workspace、Sandbox、Permission 或 Approval。
type Manager struct {
	rootDir string

	config config.SkillConfig

	logger *logging.Logger

	sourceRegistry *RemoteSkillSourceRegistry

	mu sync.RWMutex
}

// NewManager 创建 Skill Manager 并验证 Skills Root。
func NewManager(
	ctx context.Context,
	rootDir string,
	cfg config.SkillConfig,
	logger *logging.Logger,
) (*Manager, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("初始化 Skill Manager 被取消: %w", err)
	}
	if logger == nil {
		return nil, errors.New("Skill Manager Logger 不能为空")
	}
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil, errors.New("Skills Root 不能为空")
	}
	absolute, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("解析 Skills Root 绝对路径失败: %w", err)
	}
	absolute = filepath.Clean(absolute)

	sourceRegistry, err := NewDefaultRemoteSkillSourceRegistry()
	if err != nil {
		return nil, fmt.Errorf("初始化 Remote Skill Source Registry 失败: %w", err)
	}

	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("读取 Skills Root 失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Skills Root 不能是符号链接")
	}
	if !info.IsDir() {
		return nil, errors.New("Skills Root 不是目录")
	}

	return &Manager{
		rootDir:        absolute,
		config:         cfg,
		logger:         logger,
		sourceRegistry: sourceRegistry,
	}, nil
}

// RegisterRemoteSourceResolver 为当前 Manager 注册一个额外的远程 Skill 来源。
//
// 这是 Humbert 对第三方 Skill Registry / 企业内部代码托管的扩展点。Resolver 只能做纯 URL
// 规范化，最终下载仍统一经过 Manager 的 SSRF、Redirect、ZIP 与 Package 安全边界。
func (m *Manager) RegisterRemoteSourceResolver(resolver RemoteSkillSourceResolver) error {
	if err := m.validate(); err != nil {
		return err
	}
	if err := m.sourceRegistry.Register(resolver); err != nil {
		return fmt.Errorf("注册 Remote Skill Source Resolver 失败: %w", err)
	}
	return nil
}

// RemoteSourceResolvers 返回当前 Manager 已注册的来源 Resolver 名称。
func (m *Manager) RemoteSourceResolvers() []string {
	if m == nil || m.sourceRegistry == nil {
		return nil
	}
	return m.sourceRegistry.Names()
}

// RootDir 返回 Humbert 受控 Skill 安装目录。
func (m *Manager) RootDir() string {
	if m == nil {
		return ""
	}
	return m.rootDir
}

// List 扫描全部已安装 Skill。
//
// 一个坏 Skill 不会让整个列表失败。非法包会以 Valid=false 返回，用户可在设置页看到原因
// 并删除修复；只有无法读取 Skills Root 这类基础设施错误才会让 List 返回 error。
func (m *Manager) List(ctx context.Context) ([]Info, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取 Skill 列表被取消: %w", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entries, err := os.ReadDir(m.rootDir)
	if err != nil {
		return nil, fmt.Errorf("读取 Skills Root 失败: %w", err)
	}

	result := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("读取 Skill 列表被取消: %w", err)
		}
		if strings.HasPrefix(entry.Name(), ".humbert-") {
			// 安装/删除过程中使用的内部临时目录不会暴露给 UI。
			continue
		}
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}

		path := filepath.Join(m.rootDir, entry.Name())
		pkg, inspectErr := inspectPackage(ctx, path, m.config, true)
		if inspectErr != nil {
			result = append(result, Info{
				Name:          entry.Name(),
				DirectoryName: entry.Name(),
				RootDir:       path,
				Valid:         false,
				Error:         logging.SafeErrorText(inspectErr, 1024),
			})
			m.logger.Debug(
				ctx,
				"发现无效 Skill Package",
				"operation", "skill.scan.invalid",
				"skill_directory", entry.Name(),
				"error", inspectErr,
			)
			continue
		}
		result = append(result, pkg.Info)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Valid != result[j].Valid {
			return result[i].Valid
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// Get 返回一个严格验证后的已安装 Skill。
func (m *Manager) Get(ctx context.Context, name string) (Package, error) {
	if err := m.validate(); err != nil {
		return Package{}, err
	}
	name, err := normalizeSkillName(name)
	if err != nil {
		return Package{}, err
	}
	if ctx == nil {
		return Package{}, errors.New("context.Context 不能为空")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getLocked(ctx, name)
}

// NormalizeAndValidateSelection 校验 Agent enabled_skills 并返回稳定、去重的名称列表。
//
// 这里不会静默忽略不存在或无效的 Skill，因为 Agent Profile 一旦保存了无效引用，下一 Turn
// 会在 Runtime Resolve 时失败。控制面应尽早拒绝这种半完成状态。
func (m *Manager) NormalizeAndValidateSelection(
	ctx context.Context,
	names []string,
) ([]string, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}

	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name, err := normalizeSkillName(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	sort.Strings(normalized)

	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, name := range normalized {
		pkg, err := m.getLocked(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("Agent 启用 Skill %q 失败: %w", name, err)
		}
		if pkg.Info.RuntimeStatus == RuntimeStatusUnsupported {
			message := strings.TrimSpace(pkg.Info.RuntimeMessage)
			if message == "" {
				message = "当前 Humbert Runtime 暂不支持这个 Skill 的执行要求"
			}
			return nil, fmt.Errorf("Agent 启用 Skill %q 失败: %w: %s", name, ErrUnsupportedSkill, message)
		}
	}
	return normalized, nil
}

func (m *Manager) getLocked(ctx context.Context, name string) (Package, error) {
	if err := ctx.Err(); err != nil {
		return Package{}, fmt.Errorf("读取 Skill %q 被取消: %w", name, err)
	}
	path := filepath.Join(m.rootDir, name)
	pkg, err := inspectPackage(ctx, path, m.config, true)
	if err != nil {
		if errors.Is(err, ErrSkillNotFound) || errors.Is(err, os.ErrNotExist) {
			return Package{}, fmt.Errorf("%w: %s", ErrSkillNotFound, name)
		}
		return Package{}, fmt.Errorf("读取 Skill %q 失败: %w", name, err)
	}
	return pkg, nil
}

// normalizeInstalledDirectoryName 校验一个位于 Skills Root 直属层级的目录名。
//
// 与 normalizeSkillName 不同，本函数也允许删除“本来就无效”的手工目录，例如名称包含大写
// 或空格的坏包；但它严格禁止路径分隔符、`.`/`..`、绝对路径和 Humbert 内部临时目录，
// 因此 Remove 仍然只能作用于 Skills Root 的一个直接子目录。
func normalizeInstalledDirectoryName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return "", fmt.Errorf("%w: Skill 目录名无效", ErrInvalidSkill)
	}
	if strings.HasPrefix(value, ".humbert-") {
		return "", fmt.Errorf("%w: 不能操作 Humbert 内部临时目录", ErrInvalidSkill)
	}
	if filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", fmt.Errorf("%w: Skill 目录名不能是绝对路径", ErrInvalidSkill)
	}
	if strings.ContainsAny(value, `/\`) || filepath.Base(value) != value {
		return "", fmt.Errorf("%w: Skill 目录名不能包含路径分隔符", ErrInvalidSkill)
	}
	return value, nil
}

func (m *Manager) validate() error {
	if m == nil || strings.TrimSpace(m.rootDir) == "" || m.logger == nil || m.sourceRegistry == nil {
		return errors.New("Skill Manager 尚未正确初始化")
	}
	return nil
}
