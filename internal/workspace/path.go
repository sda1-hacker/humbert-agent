package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// maxRelativePathBytes 是 Workspace Tool 可接受的相对路径最大长度。
	//
	// 这是输入安全上限，而不是用户配置项，因此不通过 Viper 调整。
	// 它用于在进入操作系统文件 API 前拒绝明显异常的大型路径输入。
	maxRelativePathBytes = 4096
)

// NormalizeRelativePath 将来自 Agent Tool 的不可信路径规范化为
// Workspace Root 内部可以使用的相对路径。
//
// 支持：
//
//	""
//	    -> "."
//
//	"."
//	    -> "."
//
//	"./README.md"
//	    -> "README.md"
//
//	"src/../README.md"
//	    -> "README.md"
//
// 拒绝：
//
//	"../../.ssh/id_rsa"
//
//	"/etc/passwd"
//
//	Windows:
//	"C:\\Windows"
//
// 安全模型分为两层：
//
//  1. 本函数负责输入语义校验和稳定错误分类；
//  2. 真正文件 IO 继续通过 os.Root 完成。
//
// 即使某个 Tool 未来错误地遗漏第一层检查，os.Root 仍然负责
// 防止实际文件访问逃逸 Workspace。
func NormalizeRelativePath(
	input string,
) (string, error) {
	if len(input) >
		maxRelativePathBytes {

		return "", fmt.Errorf(
			"%w: 路径长度不能超过 %d 字节",
			ErrInvalidPath,
			maxRelativePathBytes,
		)
	}

	if strings.ContainsRune(
		input,
		'\x00',
	) {
		return "", fmt.Errorf(
			"%w: 路径中不能包含 NUL 字符",
			ErrInvalidPath,
		)
	}

	input =
		strings.TrimSpace(
			input,
		)

	if input == "" ||
		input == "." {

		return ".", nil
	}

	// LLM 输出路径时通常使用 "/"。
	//
	// filepath.FromSlash 会根据宿主操作系统转换成正确分隔符，
	// 从而同时兼容 macOS/Linux/Windows。
	path :=
		filepath.FromSlash(
			input,
		)

	if filepath.IsAbs(path) {
		return "", fmt.Errorf(
			"%w: Workspace Tool 不允许使用绝对路径",
			ErrInvalidPath,
		)
	}

	// Windows 中 "C:foo" 不一定被 IsAbs 判定为绝对路径，
	// 但仍然具有 Volume 语义，因此必须显式拒绝。
	if filepath.VolumeName(path) != "" {
		return "", fmt.Errorf(
			"%w: Workspace Tool 不允许使用 Volume Path",
			ErrInvalidPath,
		)
	}

	cleaned :=
		filepath.Clean(
			path,
		)

	// 清理：
	//
	//	a/../../secret
	//
	// 后会得到：
	//
	//	../secret
	//
	// 因此只需要判断最终 clean path 是否仍然向父级逃逸。
	if cleaned == ".." ||
		strings.HasPrefix(
			cleaned,
			".."+string(
				filepath.Separator,
			),
		) {

		return "", fmt.Errorf(
			"%w: %q",
			ErrPathTraversal,
			input,
		)
	}

	return cleaned, nil
}
