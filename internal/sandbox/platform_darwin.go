//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func probeNativeCapability() Capability {
	path := "/usr/bin/sandbox-exec"
	if _, err := os.Stat(path); err != nil {
		return Capability{Platform: "darwin", Backend: "seatbelt", Available: false, Reason: "系统未提供 /usr/bin/sandbox-exec", ProcessTree: true}
	}
	return Capability{Platform: "darwin", Backend: "seatbelt", Available: true, Filesystem: true, ProcessTree: true, Network: true}
}

func prepareNativeCommand(policy EffectivePolicy, executable string, rawArgs []string, _ string) (string, []string, func(), bool, error) {
	if policy.NativeMode == NativeOff || !policy.Capability.Available {
		if policy.NativeMode == NativeRequired {
			return "", nil, nil, false, fmt.Errorf("Seatbelt 不可用: %s", policy.Capability.Reason)
		}
		return executable, append([]string(nil), rawArgs...), nil, false, nil
	}
	profile, err := buildSeatbeltProfile(policy, executable)
	if err != nil {
		return "", nil, nil, false, err
	}
	file, err := os.CreateTemp("", "humbert-seatbelt-*.sb")
	if err != nil {
		return "", nil, nil, false, fmt.Errorf("创建 Seatbelt Profile 失败: %w", err)
	}
	name := file.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := os.Chmod(name, 0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, nil, false, err
	}
	if _, err := file.WriteString(profile); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, nil, false, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, nil, false, err
	}
	args := []string{"-f", name, executable}
	args = append(args, rawArgs...)
	return "/usr/bin/sandbox-exec", args, cleanup, true, nil
}

func buildSeatbeltProfile(policy EffectivePolicy, executable string) (string, error) {
	quote := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"` }
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")

	// 进程本身需要能够 exec/fork，并读取同一 Sandbox 中的进程信息。这里不要使用
	// 过宽的 process*：显式列出本地运行时正常启动所需的能力，便于后续审计。
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow signal (target same-sandbox))\n")
	b.WriteString("(allow process-info* (target same-sandbox))\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow ipc-posix-sem)\n")
	b.WriteString("(allow iokit-open (iokit-registry-entry-class \"RootDomainUserClient\"))\n")

	// getcwd()/路径解析会访问根目录和 macOS 顶层别名的 metadata。
	b.WriteString("(allow file-read* file-test-existence (literal \"/\"))\n")
	for _, path := range []string{"/etc", "/tmp", "/var"} {
		if _, err := os.Lstat(path); err == nil {
			fmt.Fprintf(&b, "(allow file-read-metadata file-test-existence (literal %s))\n", quote(path))
		}
	}

	if policy.Profile == ProfileFullAccess {
		// FullAccess 是显式逃生模式：文件系统退回当前用户权限；Seatbelt 仍承担
		// NetworkNone 与进程级隔离语义。
		b.WriteString("(allow file-read* file-write* file-test-existence file-map-executable)\n")
	} else {
		view := policy.NativeFilesystem()

		// macOS 本地程序除了可执行文件本身，还需要 dyld/framework、时区、系统配置和
		// 设备节点。只允许这些稳定的系统 Runtime 路径读取；用户文件仍由 PathRules 决定。
		systemReadRoots := []string{
			"/System",
			"/usr",
			"/bin",
			"/sbin",
			"/Library",
			"/Applications",
			"/private/etc",
			"/private/var/db/timezone",
			"/private/var/db",
			"/var/db",
			"/dev",
			"/opt",
			"/private/tmp",
			"/tmp",
			"/var/tmp",
			"/private/var/tmp",
		}
		for _, root := range systemReadRoots {
			if _, err := os.Stat(root); err == nil {
				fmt.Fprintf(&b, "(allow file-read* file-test-existence (subpath %s))\n", quote(filepath.Clean(root)))
			}
		}

		// dyld 的 executable mapping 是独立的 Seatbelt operation。只允许系统 Runtime、
		// 初始程序的 Runtime root，以及当前 Policy 已允许读取的目录映射可执行页。
		// 缺少这条规则时，Python/Node 等动态运行时可能在任何 stdout 产生前就被系统终止。
		mapRoots := make([]string, 0, len(systemReadRoots)+len(view.ReadOnlyRoots)+len(view.WritableRoots)+4)
		mapRoots = append(mapRoots, systemReadRoots...)
		mapRoots = append(mapRoots, seatbeltExecutableRuntimeRoots(executable)...)
		mapRoots = append(mapRoots, view.ReadOnlyRoots...)
		mapRoots = append(mapRoots, view.WritableRoots...)
		for _, root := range collapseCoveredRoots(sortedPaths(mapRoots)) {
			if _, err := os.Stat(root); err == nil {
				fmt.Fprintf(&b, "(allow file-map-executable (subpath %s))\n", quote(filepath.Clean(root)))
			}
		}

		// Homebrew、自定义工具链等 executable 可能位于系统 Runtime root 以外；初始程序
		// 已通过程序名和执行路径校验，因此额外开放其 Runtime 根目录的只读访问。
		for _, root := range seatbeltExecutableRuntimeRoots(executable) {
			if _, err := os.Stat(root); err == nil {
				fmt.Fprintf(&b, "(allow file-read* file-test-existence (subpath %s))\n", quote(filepath.Clean(root)))
			}
		}

		// Seatbelt 对深层目录执行 open/create 时可能会检查父目录 metadata。
		// 只开放祖先目录本身的 metadata，不开放其子项内容。
		metadataRoots := append([]string{}, view.ReadOnlyRoots...)
		metadataRoots = append(metadataRoots, view.WritableRoots...)
		for _, ancestor := range seatbeltAncestorMetadataRoots(metadataRoots) {
			fmt.Fprintf(&b, "(allow file-read-metadata file-test-existence (literal %s))\n", quote(ancestor))
		}

		for _, root := range view.ReadOnlyRoots {
			fmt.Fprintf(&b, "(allow file-read* file-test-existence (subpath %s))\n", quote(root))
		}
		for _, root := range view.WritableRoots {
			fmt.Fprintf(&b, "(allow file-read* file-write* file-test-existence (subpath %s))\n", quote(root))
		}

		// 常见 Runtime 设备。stdout/stderr 通常是 pipe fd，但部分语言运行时会显式打开
		// /dev/null、/dev/urandom 或 /dev/fd/*。
		b.WriteString("(allow file-read* file-write* file-test-existence (literal \"/dev/null\"))\n")
		b.WriteString("(allow file-read* file-write* file-test-existence (literal \"/dev/zero\"))\n")
		b.WriteString("(allow file-read* file-test-existence (literal \"/dev/random\"))\n")
		b.WriteString("(allow file-read* file-test-existence (literal \"/dev/urandom\"))\n")
		b.WriteString("(allow file-read-data file-write-data file-test-existence (subpath \"/dev/fd\"))\n")

		// allow 父目录（例如 Standard 模式读取整个 Home）后，必须重新显式封锁敏感子目录。
		// 同时封锁 executable mapping 与 existence test，避免通过另一类 Seatbelt operation
		// 绕过 BLOCKED 的语义。
		for _, root := range view.BlockedRoots {
			if _, err := os.Stat(root); err == nil {
				fmt.Fprintf(&b, "(deny file-read* file-write* file-test-existence file-map-executable (subpath %s))\n", quote(filepath.Clean(root)))
			}
		}
		// 单文件敏感规则使用 literal，而不是 subpath。这样 ~/.netrc、~/.npmrc、
		// Humbert config.yaml 等固定文件即使位于 Standard Home READ_ONLY 之下，
		// 本地 Python/Node 也无法绕过 PathGuard 直接读取。
		for _, path := range view.BlockedFiles {
			fmt.Fprintf(&b, "(deny file-read* file-write* file-test-existence file-map-executable (literal %s))\n", quote(filepath.Clean(path)))
		}
	}
	if policy.NetworkMode != NetworkNone {
		b.WriteString("(allow network-outbound)\n(allow network-inbound)\n")
	}
	return b.String(), nil
}

// seatbeltExecutableRuntimeRoots 返回初始程序正常加载相邻 Runtime/Library 所需的
// 最小目录集合。对 /foo/bin/python3 会开放 /foo/bin 与 /foo；如果 executable 是 symlink，
// 同时考虑最终目标，避免 Homebrew/pyenv 只开放 shim 目录而遗漏真实解释器。
func seatbeltExecutableRuntimeRoots(executable string) []string {
	seen := map[string]struct{}{}
	appendRoot := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." || !filepath.IsAbs(path) {
			return
		}
		seen[path] = struct{}{}
	}
	appendExecutable := func(path string) {
		dir := filepath.Dir(path)
		appendRoot(dir)
		parent := filepath.Dir(dir)
		if parent != dir && parent != string(filepath.Separator) {
			appendRoot(parent)
		}
	}

	appendExecutable(executable)
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		appendExecutable(resolved)
	}

	result := make([]string, 0, len(seen))
	for root := range seen {
		result = append(result, root)
	}
	return sortedPaths(result)
}

func seatbeltAncestorMetadataRoots(roots []string) []string {
	seen := map[string]struct{}{}
	for _, root := range roots {
		root = filepath.Clean(root)
		if !filepath.IsAbs(root) {
			continue
		}
		for current := filepath.Dir(root); current != string(filepath.Separator) && current != "."; current = filepath.Dir(current) {
			seen[current] = struct{}{}
			next := filepath.Dir(current)
			if next == current {
				break
			}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func configurePlatformProcess(cmd *exec.Cmd, _ EffectivePolicy) (func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil, nil
}
func attachPlatformProcess(cmd *exec.Cmd, _ EffectivePolicy) (func() error, func(), error) {
	pid := cmd.Process.Pid
	return func() error { return syscall.Kill(-pid, syscall.SIGKILL) }, nil, nil
}
