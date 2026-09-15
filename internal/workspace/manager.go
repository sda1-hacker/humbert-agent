package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

const (
	managedDirectoryMode fs.FileMode = 0o700
)

// Manager 管理 Humbert Agent Workspace。
//
// Manager 不长期持有任何 *os.Root 或文件描述符。
//
// 生命周期设计：
//
//	Application
//	    ↓
//	Manager
//	    ↓
//	Runtime Turn / Tool
//	    ↓
//	OpenRoot()
//	    ↓
//	执行文件操作
//	    ↓
//	Close()
//
// 因此一个 Agent 即使长期不使用，也不会因为 Workspace
// 持有任何系统文件句柄。
//
// Managed Root 示例：
//
//	~/.humbert-agent/workspaces
//
// Agent Workspace:
//
//	~/.humbert-agent/workspaces/<agent-id>
//
// Custom Workspace 则直接使用用户明确选择的目录。
type Manager struct {
	logger *logging.Logger

	managedRoot string

	mu sync.RWMutex

	closed bool
}

// NewManager 创建 Workspace Manager。
//
// managedRoot 只表示 Humbert 默认管理目录。
// Custom Workspace 可以位于 managedRoot 之外。
func NewManager(
	ctx context.Context,
	managedRoot string,
	logger *logging.Logger,
) (*Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"初始化 Workspace Manager 被取消: %w",
			err,
		)
	}

	if logger == nil {
		return nil, errors.New(
			"Workspace Manager Logger 不能为空",
		)
	}

	managedRoot =
		strings.TrimSpace(
			managedRoot,
		)

	if managedRoot == "" {
		return nil, errors.New(
			"Managed Workspace Root 不能为空",
		)
	}

	absoluteRoot, err :=
		filepath.Abs(
			filepath.Clean(
				managedRoot,
			),
		)

	if err != nil {
		return nil, fmt.Errorf(
			"解析 Managed Workspace Root 失败: %w",
			err,
		)
	}

	if err :=
		os.MkdirAll(
			absoluteRoot,
			managedDirectoryMode,
		); err != nil {

		return nil, fmt.Errorf(
			"创建 Managed Workspace Root 失败: %w",
			err,
		)
	}

	info, err :=
		os.Stat(
			absoluteRoot,
		)

	if err != nil {
		return nil, fmt.Errorf(
			"检查 Managed Workspace Root 失败: %w",
			err,
		)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf(
			"%w: Managed Workspace Root 不是目录",
			ErrUnsafeWorkspace,
		)
	}

	// managedRoot 属于应用可信配置。
	//
	// 如果用户自己把 ~/.humbert-agent 放在 symlink 中，
	// 这里解析成最终物理路径即可。
	resolvedRoot, err :=
		filepath.EvalSymlinks(
			absoluteRoot,
		)

	if err != nil {
		return nil, fmt.Errorf(
			"解析 Managed Workspace Root 真实路径失败: %w",
			err,
		)
	}

	manager :=
		&Manager{
			logger: logger,

			managedRoot: resolvedRoot,
		}

	logger.Info(
		ctx,
		"Workspace Manager 已初始化",
		"operation",
		"workspace.initialize",
		"managed_root",
		resolvedRoot,
	)

	return manager, nil
}

// ManagedRoot 返回 Humbert 默认 Workspace 总目录。
//
// 该路径用于 UI 展示和诊断。
// Agent Tool 不应该自己拼接该路径进行文件操作。
func (m *Manager) ManagedRoot() string {
	return m.managedRoot
}

// ManagedPath 返回指定 Agent 的默认 Workspace 路径。
//
// 本方法只计算路径，不创建目录。
func (m *Manager) ManagedPath(
	agentID string,
) (string, error) {
	normalizedAgentID, err :=
		normalizeAgentID(
			agentID,
		)

	if err != nil {
		return "", err
	}

	return filepath.Join(
		m.managedRoot,
		normalizedAgentID,
	), nil
}

// Validate 校验 Workspace 配置。
//
// 与 Resolve 的区别：
//
// Validate:
//
//   - 不创建 Managed Agent 目录；
//   - 用于 Agent Create 写入 Profile 之前进行输入检查。
//
// Resolve:
//
//   - Managed 模式会确保目录存在；
//   - Custom 模式会解析真实目录；
//   - 用于真正进入 Runtime。
func (m *Manager) Validate(
	ctx context.Context,
	agentID string,
	mode Mode,
	configuredPath string,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"校验 Workspace 被取消: %w",
			err,
		)
	}

	if err := m.ensureOpen(); err != nil {
		return err
	}

	if _, err :=
		normalizeAgentID(
			agentID,
		); err != nil {

		return err
	}

	normalizedMode, err :=
		normalizeMode(mode)

	if err != nil {
		return err
	}

	switch normalizedMode {
	case ModeManaged:
		return nil

	case ModeCustom:
		_, _, err :=
			resolveCustomPath(
				ctx,
				configuredPath,
			)

		return err

	default:
		return fmt.Errorf(
			"%w: %q",
			ErrInvalidMode,
			mode,
		)
	}
}

// Resolve 将 Agent Workspace 配置解析成当前 Turn 真正使用的 Workspace。
//
// Managed:
//
//	~/.humbert-agent/workspaces/<agent-id>
//
// Custom:
//
//	用户选择目录
//
// 特别重要：
//
// Custom Workspace 不可用时，本方法直接返回 ErrUnavailable。
// 绝不能偷偷切换为 Managed Workspace，否则 Agent 可能在错误目录中
// 执行文件修改。
func (m *Manager) Resolve(
	ctx context.Context,
	agentID string,
	mode Mode,
	configuredPath string,
) (Workspace, error) {
	if err := ctx.Err(); err != nil {
		return Workspace{},
			fmt.Errorf(
				"解析 Workspace 被取消: %w",
				err,
			)
	}

	if err := m.ensureOpen(); err != nil {
		return Workspace{}, err
	}

	normalizedAgentID, err :=
		normalizeAgentID(
			agentID,
		)

	if err != nil {
		return Workspace{}, err
	}

	normalizedMode, err :=
		normalizeMode(mode)

	if err != nil {
		return Workspace{}, err
	}

	switch normalizedMode {
	case ModeManaged:
		rootDir, err :=
			m.ensureManagedWorkspace(
				ctx,
				normalizedAgentID,
			)

		if err != nil {
			return Workspace{},
				err
		}

		return Workspace{
			AgentID: normalizedAgentID,

			Mode: ModeManaged,

			ConfiguredPath: "",

			RootDir: rootDir,
		}, nil

	case ModeCustom:
		configured,
			resolved,
			err :=
			resolveCustomPath(
				ctx,
				configuredPath,
			)

		if err != nil {
			return Workspace{},
				err
		}

		return Workspace{
			AgentID: normalizedAgentID,

			Mode: ModeCustom,

			ConfiguredPath: configured,

			RootDir: resolved,
		}, nil

	default:
		return Workspace{},
			fmt.Errorf(
				"%w: %q",
				ErrInvalidMode,
				mode,
			)
	}
}

// OpenRoot 为一个已经冻结的 Workspace Snapshot
// 打开短生命周期安全 Root。
//
// 调用方必须负责 Close:
//
//	root, err := manager.OpenRoot(ctx, snapshot.Workspace)
//	if err != nil {
//	    return err
//	}
//	defer root.Close()
//
// 后续 File Tool 必须通过这个 *os.Root 进行文件 IO。
func (m *Manager) OpenRoot(
	ctx context.Context,
	value Workspace,
) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"打开 Workspace 被取消: %w",
			err,
		)
	}

	if err := m.ensureOpen(); err != nil {
		return nil, err
	}

	agentID, err :=
		normalizeAgentID(
			value.AgentID,
		)

	if err != nil {
		return nil, err
	}

	mode, err :=
		normalizeMode(
			value.Mode,
		)

	if err != nil {
		return nil, err
	}

	rootDir :=
		filepath.Clean(
			strings.TrimSpace(
				value.RootDir,
			),
		)

	if rootDir == "" ||
		!filepath.IsAbs(
			rootDir,
		) {

		return nil, fmt.Errorf(
			"%w: RootDir 必须是绝对路径",
			ErrInvalidPath,
		)
	}

	switch mode {
	case ModeManaged:
		expected, err :=
			m.ManagedPath(
				agentID,
			)

		if err != nil {
			return nil, err
		}

		if filepath.Clean(expected) !=
			rootDir {

			return nil, fmt.Errorf(
				"%w: Managed Workspace Root 与 Agent ID 不匹配",
				ErrUnsafeWorkspace,
			)
		}

		info, err :=
			os.Lstat(
				rootDir,
			)

		if err != nil {
			if errors.Is(
				err,
				fs.ErrNotExist,
			) {
				return nil, fmt.Errorf(
					"%w: Managed Workspace 不存在: %w",
					ErrUnavailable,
					err,
				)
			}

			return nil, fmt.Errorf(
				"检查 Managed Workspace 失败: %w",
				err,
			)
		}

		if info.Mode()&
			os.ModeSymlink != 0 {

			return nil, fmt.Errorf(
				"%w: Managed Workspace 不允许是符号链接",
				ErrUnsafeWorkspace,
			)
		}

		if !info.IsDir() {
			return nil, fmt.Errorf(
				"%w: Managed Workspace 不是目录",
				ErrUnsafeWorkspace,
			)
		}

	case ModeCustom:
		// Custom RootDir 来自 Resolve() 已经解析后的物理目录。
		//
		// 这里仍然再次检查目录存在性，因为外置磁盘可能在
		// Snapshot 创建后被拔出。
		info, err :=
			os.Stat(
				rootDir,
			)

		if err != nil {
			return nil, fmt.Errorf(
				"%w: Custom Workspace 无法访问: %w",
				ErrUnavailable,
				err,
			)
		}

		if !info.IsDir() {
			return nil, fmt.Errorf(
				"%w: Custom Workspace 已经不是目录",
				ErrUnavailable,
			)
		}

	default:
		return nil, fmt.Errorf(
			"%w: %q",
			ErrInvalidMode,
			mode,
		)
	}

	root, err :=
		os.OpenRoot(
			rootDir,
		)

	if err != nil {
		return nil, fmt.Errorf(
			"%w: 打开 Workspace Root 失败: %w",
			ErrUnavailable,
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		_ = root.Close()

		return nil, fmt.Errorf(
			"打开 Workspace 被取消: %w",
			err,
		)
	}

	return root, nil
}

// Close 让 Manager 进入关闭状态。
//
// Manager 本身没有长期文件句柄，因此这里只阻止后续 Resolve/OpenRoot。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}

	m.closed = true

	return nil
}

func (m *Manager) ensureOpen() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return ErrClosed
	}

	return nil
}

// ensureManagedWorkspace 创建或验证 Agent 默认 Workspace。
//
// Humbert 管理目录必须是真实目录，而不能是 symlink。
// 如果用户确实希望使用 symlink 指向的目录，应使用 custom 模式。
func (m *Manager) ensureManagedWorkspace(
	ctx context.Context,
	agentID string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf(
			"准备 Managed Workspace 被取消: %w",
			err,
		)
	}

	root, err :=
		os.OpenRoot(
			m.managedRoot,
		)

	if err != nil {
		return "", fmt.Errorf(
			"打开 Managed Workspace Root 失败: %w",
			err,
		)
	}

	defer root.Close()

	info, err :=
		root.Lstat(
			agentID,
		)

	if err != nil {
		if !errors.Is(
			err,
			fs.ErrNotExist,
		) {
			return "", fmt.Errorf(
				"检查 Managed Agent Workspace 失败: %w",
				err,
			)
		}

		if err :=
			root.Mkdir(
				agentID,
				managedDirectoryMode,
			); err != nil {

			if !errors.Is(
				err,
				fs.ErrExist,
			) {
				return "", fmt.Errorf(
					"创建 Managed Agent Workspace 失败: %w",
					err,
				)
			}
		}

		info, err =
			root.Lstat(
				agentID,
			)

		if err != nil {
			return "", fmt.Errorf(
				"重新检查 Managed Agent Workspace 失败: %w",
				err,
			)
		}
	}

	if info.Mode()&
		os.ModeSymlink != 0 {

		return "", fmt.Errorf(
			"%w: Managed Agent Workspace 不允许是符号链接",
			ErrUnsafeWorkspace,
		)
	}

	if !info.IsDir() {
		return "", fmt.Errorf(
			"%w: Managed Agent Workspace 不是目录",
			ErrUnsafeWorkspace,
		)
	}

	return filepath.Join(
		m.managedRoot,
		agentID,
	), nil
}

// resolveCustomPath 校验用户选择的 Custom Workspace。
//
// 返回值：
//
//	configured
//	    规范化后的用户配置绝对路径。
//
//	resolved
//	    解析最外层 symlink 后，本次 Runtime 真正使用的目录。
//
// 我们允许用户明确选择一个 symlink 目录作为 Custom Workspace。
// 但 Runtime Snapshot 会冻结它当前指向的真实目录。
func resolveCustomPath(
	ctx context.Context,
	configuredPath string,
) (
	configured string,
	resolved string,
	err error,
) {
	if err := ctx.Err(); err != nil {
		return "", "",
			fmt.Errorf(
				"校验 Custom Workspace 被取消: %w",
				err,
			)
	}

	configuredPath =
		strings.TrimSpace(
			configuredPath,
		)

	if configuredPath == "" {
		return "", "",
			fmt.Errorf(
				"%w: Custom Workspace Path 不能为空",
				ErrInvalidPath,
			)
	}

	if !filepath.IsAbs(
		configuredPath,
	) {
		return "", "",
			fmt.Errorf(
				"%w: Custom Workspace 必须使用绝对路径",
				ErrInvalidPath,
			)
	}

	absolutePath, err :=
		filepath.Abs(
			filepath.Clean(
				configuredPath,
			),
		)

	if err != nil {
		return "", "",
			fmt.Errorf(
				"解析 Custom Workspace 绝对路径失败: %w",
				err,
			)
	}

	info, err :=
		os.Stat(
			absolutePath,
		)

	if err != nil {
		return "", "",
			fmt.Errorf(
				"%w: %s: %w",
				ErrUnavailable,
				absolutePath,
				err,
			)
	}

	if !info.IsDir() {
		return "", "",
			fmt.Errorf(
				"%w: Custom Workspace 不是目录",
				ErrInvalidPath,
			)
	}

	realPath, err :=
		filepath.EvalSymlinks(
			absolutePath,
		)

	if err != nil {
		return "", "",
			fmt.Errorf(
				"%w: 解析 Custom Workspace 真实路径失败: %w",
				ErrUnavailable,
				err,
			)
	}

	root, err :=
		os.OpenRoot(
			realPath,
		)

	if err != nil {
		return "", "",
			fmt.Errorf(
				"%w: Custom Workspace 无法打开: %w",
				ErrUnavailable,
				err,
			)
	}

	if err :=
		root.Close(); err != nil {

		return "", "",
			fmt.Errorf(
				"关闭 Custom Workspace 校验句柄失败: %w",
				err,
			)
	}

	return absolutePath,
		realPath,
		nil
}

func normalizeMode(
	mode Mode,
) (Mode, error) {
	value :=
		Mode(
			strings.ToLower(
				strings.TrimSpace(
					string(mode),
				),
			),
		)

	if value == "" {
		return ModeManaged, nil
	}

	switch value {
	case ModeManaged,
		ModeCustom:

		return value, nil

	default:
		return "",
			fmt.Errorf(
				"%w: %q",
				ErrInvalidMode,
				mode,
			)
	}
}

func normalizeAgentID(
	agentID string,
) (string, error) {
	agentID =
		strings.TrimSpace(
			agentID,
		)

	if agentID == "" {
		return "", fmt.Errorf(
			"%w: Agent ID 不能为空",
			ErrInvalidAgentID,
		)
	}

	parsed, err :=
		uuid.Parse(
			agentID,
		)

	if err != nil {
		return "", fmt.Errorf(
			"%w: %v",
			ErrInvalidAgentID,
			err,
		)
	}

	return parsed.String(), nil
}
