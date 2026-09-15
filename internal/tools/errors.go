package tools

import "errors"

var (
	// ErrInvalidTool 表示 Tool Definition 本身配置错误。
	ErrInvalidTool = errors.New(
		"Tool 定义不合法",
	)

	// ErrToolExists 表示相同名称的 Tool 已经存在于 Registry。
	ErrToolExists = errors.New(
		"Tool 已存在",
	)

	// ErrToolNotFound 表示指定 Tool 不存在。
	ErrToolNotFound = errors.New(
		"Tool 不存在",
	)

	// ErrPermissionDenied 表示 Tool Permission Policy 拒绝执行。
	ErrPermissionDenied = errors.New(
		"Tool 权限被拒绝",
	)

	// ErrLimitExceeded 表示 Tool 输入或输出超过安全限制。
	ErrLimitExceeded = errors.New(
		"Tool 操作超过安全限制",
	)

	// ErrUnsupportedFile 表示目标文件类型不适合被当前 Tool 处理。
	//
	// 例如：
	//
	//   - binary file；
	//   - FIFO；
	//   - socket；
	//   - device file。
	ErrUnsupportedFile = errors.New(
		"不支持的文件类型",
	)
)
