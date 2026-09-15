package workspace

import "errors"

var (
	// ErrClosed 表示 Workspace Manager 已经进入关闭状态。
	//
	// Application Shutdown 开始以后不再允许创建新的 Workspace Root。
	ErrClosed = errors.New(
		"Workspace Manager 已关闭",
	)

	// ErrInvalidAgentID 表示 Agent ID 不是合法 UUID。
	//
	// Managed Workspace 的物理目录名称来自 Agent ID，
	// 因此必须在进入文件系统之前进行严格校验。
	ErrInvalidAgentID = errors.New(
		"Agent ID 不合法",
	)

	// ErrInvalidMode 表示 Workspace Mode 不是 Humbert 支持的模式。
	ErrInvalidMode = errors.New(
		"Workspace 模式不合法",
	)

	// ErrInvalidPath 表示 Workspace 路径格式不合法。
	ErrInvalidPath = errors.New(
		"Workspace 路径不合法",
	)

	// ErrPathTraversal 表示相对路径试图越过 Workspace Root。
	ErrPathTraversal = errors.New(
		"Workspace 路径尝试越界",
	)

	// ErrUnsafeWorkspace 表示 Humbert 管理的 Workspace
	// 被替换成 symlink、普通文件等不安全结构。
	ErrUnsafeWorkspace = errors.New(
		"Workspace 目录结构不安全",
	)

	// ErrUnavailable 表示配置的 Workspace 当前不可访问。
	//
	// 典型场景：
	//
	//   - 外置硬盘未挂载；
	//   - 用户删除了目录；
	//   - 网络磁盘断开；
	//   - 当前进程没有目录访问权限。
	//
	// Runtime 必须明确返回该错误，绝不能自动降级到默认 Workspace。
	ErrUnavailable = errors.New(
		"Workspace 当前不可用",
	)
)
