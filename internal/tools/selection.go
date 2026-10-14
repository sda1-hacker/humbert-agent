package tools

import "strings"

// CurrentBuiltinName 兼容旧工具选择；退役工具不再进入模型 Schema，旧 Git 名称合并为 git。
// 原选择仍保留旧 Git 名称，由 Git 工具继续限制原来获准的动作。
func CurrentBuiltinName(name string) string {
	name = strings.TrimSpace(name)
	switch name {
	case "get_current_time", "update_plan", "apply_patch", "copy_file", "move_file", "delete_file":
		return ""
	case "git_status", "git_diff", "git_log":
		return "git"
	default:
		return name
	}
}

// CurrentBuiltinNames 只用于目录/UI 投影，不修改持久化配置或扩大旧 Git 动作范围。
func CurrentBuiltinNames(names []string) []string {
	if names == nil {
		return nil
	}
	result := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = CurrentBuiltinName(name)
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}
