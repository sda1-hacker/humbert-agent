package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/commandenv"
)

// routineOperation 只自动放行能明确识别的常规操作；未知命令交给用户确认。
// 风险判断不是沙盒：所有获准调用仍受 PathGuard、原生文件边界和网络策略约束。
func routineOperation(request Request) bool {
	var args map[string]any
	if request.Arguments != "" && json.Unmarshal([]byte(request.Arguments), &args) != nil {
		return false
	}
	switch request.ToolName {
	case "delete_file", "move_file", "install_skill", "schedule_task", "run_skill_script":
		return false
	}
	// 内置只读工具不应因为目标在工作区之外就被当成风险操作。
	// 真实访问范围由冻结的 Sandbox + PathGuard 校验；自动执行不会授予新目录权限。
	// 外部 MCP 的风险声明不能直接套用这个内置工具规则。
	if request.Risk == RiskRead && request.Identity.Kind == CapabilityBuiltin {
		return true
	}
	if !pathsInside(args, request.WorkspaceRoot) {
		return false
	}
	if request.ToolName == "run_command" {
		return routineCommand(request, args)
	}
	if request.Risk == RiskRead {
		return true
	}
	switch request.ToolName {
	case "write_file", "edit_file", "apply_patch", "copy_file", "update_plan":
		return true
	}
	return false
}

func pathsInside(value any, root string) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			switch key {
			case "path", "file_path", "directory", "working_directory", "source", "destination", "root", "directory_path":
				if path, ok := item.(string); ok && path != "" && !insideWorkspace(path, root) {
					return false
				}
			}
			if !pathsInside(item, root) {
				return false
			}
		}
	case []any:
		for _, item := range value {
			if !pathsInside(item, root) {
				return false
			}
		}
	}
	return true
}

// 新文件也解析已存在的父目录，避免工作区内的符号链接把“普通编辑”带到其他目录。
func insideWorkspace(path, root string) bool {
	if root == "" {
		return false
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(canonicalRoot, path)
	}
	path = filepath.Clean(path)
	suffix := ""
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			relative, err := filepath.Rel(canonicalRoot, filepath.Join(resolved, suffix))
			return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
		}
		if !os.IsNotExist(err) || filepath.Dir(path) == path {
			return false
		}
		suffix = filepath.Join(filepath.Base(path), suffix)
		path = filepath.Dir(path)
	}
}

func routineCommand(request Request, input map[string]any) bool {
	// 自动运行开发检查需要 OS 文件隔离；路径或名字相同不等于可信的系统程序。
	if !request.NativeSandbox {
		return false
	}
	name := commandenv.Name(request.Identity.Command)
	resolved, err := commandenv.Resolve(name, "")
	if err != nil || resolved != request.Identity.Executable || insideWorkspace(resolved, request.WorkspaceRoot) {
		return false
	}
	values, _ := input["args"].([]any)
	args := make([]string, 0, len(values))
	for _, value := range values {
		arg, ok := value.(string)
		if !ok {
			return false
		}
		args = append(args, arg)
		// 绝对路径或父目录引用都需按真实位置检查，包括 -o=/outside/file 一类参数。
		path := arg
		if strings.HasPrefix(path, "-") {
			if _, after, ok := strings.Cut(path, "="); ok {
				path = after
			}
		}
		if (filepath.IsAbs(path) || strings.Contains(path, "..")) && !insideWorkspace(path, request.WorkspaceRoot) {
			return false
		}
		flag := strings.SplitN(arg, "=", 2)[0]
		switch flag {
		case "--pre", "--hostname-bin", "--pre-glob", "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-toolexec", "-vettool", "-overlay", "-modfile":
			return false
		}
	}
	switch name {
	case "pwd", "ls", "cat", "head", "tail", "wc", "stat", "du", "file", "rg", "grep", "find":
		return true
	case "go":
		if len(args) == 0 {
			return true
		}
		switch args[0] {
		case "version", "vet", "test", "build", "list":
			return true
		case "env":
			for _, arg := range args[1:] {
				if arg == "-w" || arg == "-u" {
					return false
				}
			}
			return true
		}
	}
	return false
}
