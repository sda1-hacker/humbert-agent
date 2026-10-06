package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
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
func NormalizeRelativePath(input string) (string, error) {
	if len(input) > maxRelativePathBytes {
		return "", fmt.Errorf("%w: 路径长度不能超过 %d 字节", ErrInvalidPath, maxRelativePathBytes)
	}
	if strings.ContainsRune(input, '\x00') {
		return "", fmt.Errorf("%w: 路径中不能包含 NUL 字符", ErrInvalidPath)
	}
	input = strings.TrimSpace(input)
	if input == "" || input == "." {
		return ".", nil
	}
	// 模型统一使用 /，由宿主系统转换分隔符。Windows 的 C:foo 虽不一定是
	// 绝对路径，仍有 Volume 语义，因此两种形式都在进入 os.Root 前拒绝。
	path := filepath.FromSlash(input)
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: Workspace Tool 不允许使用绝对路径", ErrInvalidPath)
	}
	if filepath.VolumeName(path) != "" {
		return "", fmt.Errorf("%w: Workspace Tool 不允许使用 Volume Path", ErrInvalidPath)
	}
	cleaned := filepath.Clean(path)
	// a/../../secret 在清理后仍指向父目录；只规范化内部的 a/../b，不能放行逃逸。
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrPathTraversal, input)
	}
	return cleaned, nil
}

// resolveCustomPath 同时返回用户配置路径与解析符号链接后的真实根目录。
// 配置值用于保存和展示，真实值用于打开受限文件句柄，不能把两者混成一个字段。
// 校验句柄立即关闭；Manager 不缓存句柄，后续 OpenRoot 仍重新核验目录状态。
func resolveCustomPath(ctx context.Context, configuredPath string) (configured, resolved string, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", fmt.Errorf("校验 Custom Workspace 被取消: %w", err)
	}
	configuredPath = strings.TrimSpace(configuredPath)
	if configuredPath == "" {
		return "", "", fmt.Errorf("%w: Custom Workspace Path 不能为空", ErrInvalidPath)
	}
	if !filepath.IsAbs(configuredPath) {
		return "", "", fmt.Errorf("%w: Custom Workspace 必须使用绝对路径", ErrInvalidPath)
	}
	absolutePath, err := filepath.Abs(filepath.Clean(configuredPath))
	if err != nil {
		return "", "", fmt.Errorf("解析 Custom Workspace 绝对路径失败: %w", err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s: %w", ErrUnavailable, absolutePath, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%w: Custom Workspace 不是目录", ErrInvalidPath)
	}
	realPath, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return "", "", fmt.Errorf("%w: 解析 Custom Workspace 真实路径失败: %w", ErrUnavailable, err)
	}
	root, err := os.OpenRoot(realPath)
	if err != nil {
		return "", "", fmt.Errorf("%w: Custom Workspace 无法打开: %w", ErrUnavailable, err)
	}
	if err := root.Close(); err != nil {
		return "", "", fmt.Errorf("关闭 Custom Workspace 校验句柄失败: %w", err)
	}
	return absolutePath, realPath, nil
}

// normalizeMode 的空值继承 managed，其他值必须明确属于受支持的工作区模式。
func normalizeMode(mode Mode) (Mode, error) {
	value := Mode(strings.ToLower(strings.TrimSpace(string(mode))))
	if value == "" {
		return ModeManaged, nil
	}
	switch value {
	case ModeManaged, ModeCustom:
		return value, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidMode, mode)
	}
}

// normalizeAgentID 统一 UUID 的字符串形式，防止同一 Agent 产生多种目录定位结果。
func normalizeAgentID(agentID string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("%w: Agent ID 不能为空", ErrInvalidAgentID)
	}
	parsed, err := uuid.Parse(agentID)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidAgentID, err)
	}
	return parsed.String(), nil
}
