// Package commandenv 统一桌面进程的程序查找规则；审批身份和真实执行必须使用同一解析器。
package commandenv

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Path 补足 Finder/桌面快捷方式启动时缺少的开发工具目录，不运行用户 shell 配置，
// 也不引入完整宿主环境或相对 PATH（避免把当前项目里的同名程序误当成系统工具）。
func Path() string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	if runtime.GOOS != "windows" {
		home, _ := os.UserHomeDir()
		dirs = append(dirs, "/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/local/go/bin",
			"/usr/bin", "/bin", "/usr/sbin", "/sbin")
		if home != "" {
			for _, name := range []string{".local/bin", ".cargo/bin", "go/bin", ".volta/bin", ".asdf/shims", ".local/share/mise/shims", ".pyenv/shims"} {
				dirs = append(dirs, filepath.Join(home, name))
			}
		}
	}
	seen := map[string]bool{}
	result := []string{}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		key := dir
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, dir)
		}
	}
	return strings.Join(result, string(os.PathListSeparator))
}

func Validate(command string) error {
	if strings.TrimSpace(command) == "" {
		return errors.New("程序名称或路径不能为空")
	}
	if len(command) > 4096 || strings.ContainsRune(command, '\x00') {
		return errors.New("程序名称或路径无效")
	}
	return nil
}

// Resolve 接受名称、绝对路径和相对工作目录的程序路径，始终返回解析符号链接后的绝对路径。
// 参数仍通过 argv 传入，不经过 shell。
func Resolve(command, workingDirectory string) (string, error) {
	command = strings.TrimSpace(command)
	if err := Validate(command); err != nil {
		return "", err
	}
	candidates := []string{}
	if filepath.IsAbs(command) {
		candidates = append(candidates, command)
	} else if strings.ContainsAny(command, `/\`) {
		if !filepath.IsAbs(workingDirectory) {
			return "", errors.New("相对程序路径需要绝对工作目录")
		}
		candidates = append(candidates, filepath.Join(workingDirectory, command))
	} else {
		for _, dir := range filepath.SplitList(Path()) {
			candidates = append(candidates, filepath.Join(dir, command))
		}
	}
	for _, candidate := range candidates {
		executable, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(executable)
		if err != nil {
			return "", fmt.Errorf("解析程序真实路径失败: %w", err)
		}
		return filepath.Clean(resolved), nil
	}
	return "", fmt.Errorf("找不到可执行程序 %q；请安装该程序或提供其完整路径", command)
}

func Name(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	name := filepath.Base(command)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	return name
}
