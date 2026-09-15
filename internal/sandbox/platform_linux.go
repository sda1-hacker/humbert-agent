//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const bubblewrapProbeTimeout = 3 * time.Second

func probeNativeCapability() Capability {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return Capability{
			Platform: "linux", Backend: "bubblewrap", Available: false,
			Reason: "未找到 bwrap；请安装 bubblewrap", ProcessTree: true,
		}
	}

	// 不能只用 LookPath 判断 Bubblewrap 可用：部分发行版/容器虽然安装了 bwrap，
	// 但 user namespace 或 setuid 配置不允许真正建立 Sandbox。启动时执行一次最小
	// namespace 探针，让 Capability 代表“实际可用”而不是“二进制存在”。
	if err := probeBubblewrap(path, false); err != nil {
		return Capability{
			Platform: "linux", Backend: filepath.Base(path), Available: false,
			Reason:      "Bubblewrap 已安装，但无法建立文件系统 namespace: " + err.Error(),
			ProcessTree: true,
		}
	}

	capability := Capability{
		Platform: "linux", Backend: filepath.Base(path), Available: true,
		Filesystem: true, ProcessTree: true, Network: true,
	}
	if err := probeBubblewrap(path, true); err != nil {
		// 文件隔离仍然可用；只是 NetworkNone 不能被可靠兑现。Runner 会在用户请求
		// NetworkNone 时 fail-closed，而不会静默退回宿主网络。
		capability.Network = false
		capability.Reason = "Bubblewrap 文件隔离可用，但 network namespace 不可用: " + err.Error()
	}
	return capability
}

func probeBubblewrap(bwrap string, isolateNetwork bool) error {
	truePath, err := exec.LookPath("true")
	if err != nil {
		truePath = "/bin/true"
	}
	ctx, cancel := context.WithTimeout(context.Background(), bubblewrapProbeTimeout)
	defer cancel()

	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--ro-bind", "/", "/",
		"--proc", "/proc",
		"--dev", "/dev",
	}
	if isolateNetwork {
		args = append(args, "--unshare-net")
	}
	args = append(args, "--", truePath)
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("探针超时: %w", ctx.Err())
	}
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 240 {
			message = message[:240] + "…"
		}
		if message != "" {
			return fmt.Errorf("%v: %s", err, message)
		}
		return err
	}
	return nil
}

func prepareNativeCommand(policy EffectivePolicy, executable string, rawArgs []string, workingDirectory string) (string, []string, func(), bool, error) {
	if policy.NativeMode == NativeOff || !policy.Capability.Available {
		if policy.NativeMode == NativeRequired {
			return "", nil, nil, false, fmt.Errorf("Bubblewrap 不可用: %s", policy.Capability.Reason)
		}
		return executable, append([]string(nil), rawArgs...), nil, false, nil
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return "", nil, nil, false, err
	}
	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--cap-drop", "ALL",
	}
	view := policy.NativeFilesystem()

	if policy.Profile == ProfileFullAccess {
		// 显式 FullAccess 保持宿主文件系统可见，但仍运行在独立 pid/ipc/uts namespace，
		// 并可通过 --unshare-net 实现 NetworkNone。
		args = append(args, "--bind", "/", "/")
	} else {
		// Bubblewrap 会自动创建 bind destination 缺失的父目录，因此这里不再维护一套
		// --dir 预创建逻辑，避免把文件型 destination 错建成目录。
		systemRoots := linuxSystemRuntimeRoots()
		for _, root := range systemRoots {
			args = append(args, "--ro-bind", root, root)
		}

		// PATH 白名单程序可能来自 pyenv/Homebrew/custom toolchain。不要为了一个程序
		// 暴露整个 /opt 或整个用户 Home；只开放 executable 目录以及同 Runtime 下
		// 实际存在的 lib/lib64/share 兄弟目录。
		for _, root := range linuxExecutableRuntimeRoots(executable) {
			if !visibleUnderRoots(root, view.ReadOnlyRoots, view.WritableRoots, systemRoots) {
				args = append(args, "--ro-bind", root, root)
			}
		}

		// /etc/resolv.conf、/etc/localtime 在很多发行版上是指向 /run 的 symlink。
		// /etc 本身被 bind 进来并不足以访问 symlink target，因此把当前真实 target
		// 单独只读映射；不会因此暴露整个 /run。
		for _, target := range linuxResolvedSystemTargets() {
			if !visibleUnderRoots(target, view.ReadOnlyRoots, view.WritableRoots, systemRoots) {
				args = append(args, "--ro-bind", target, target)
			}
		}

		for _, root := range view.ReadOnlyRoots {
			args = append(args, "--ro-bind", root, root)
		}
		for _, root := range view.WritableRoots {
			args = append(args, "--bind", root, root)
		}

		// BLOCKED 必须最后覆盖父级 RO/RW bind。例如 Standard 把整个 Home 只读
		// 映射后，再用 tmpfs 遮蔽 ~/.ssh、~/.aws 等敏感子目录。
		for _, root := range view.BlockedRoots {
			if _, err := os.Stat(root); err == nil && visibleUnderRoots(root, view.ReadOnlyRoots, view.WritableRoots) {
				args = append(args, "--tmpfs", root)
			}
		}
		// 单文件敏感规则不能使用 --tmpfs（目标不是目录）。把 /dev/null 只读 bind
		// 到敏感文件位置，保留路径结构但使内容不可读取；应用层 PathGuard 仍会直接
		// 返回 BLOCKED。只有当前真实存在且位于可见 RO/RW 树中的文件才需要遮蔽。
		for _, path := range view.BlockedFiles {
			info, err := os.Stat(path)
			if err != nil || info.IsDir() || !visibleUnderRoots(path, view.ReadOnlyRoots, view.WritableRoots) {
				continue
			}
			args = append(args, "--ro-bind", "/dev/null", path)
		}
	}

	// 覆盖宿主 /proc、/dev、/tmp；尤其 /tmp 使用独立 tmpfs，避免读取宿主临时文件。
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp")
	if policy.NetworkMode == NetworkNone {
		args = append(args, "--unshare-net")
	}
	if workingDirectory == "" {
		workingDirectory = policy.WorkspaceRoot
	}
	args = append(args, "--chdir", workingDirectory, "--", executable)
	args = append(args, rawArgs...)
	return bwrap, args, nil, true, nil
}

func linuxSystemRuntimeRoots() []string {
	candidates := []string{
		"/usr",
		"/bin",
		"/sbin",
		"/lib",
		"/lib64",
		"/etc",
		// NixOS 的系统 Runtime/动态链接器通常位于 /nix/store。它是 immutable
		// package store，只读暴露与 /usr 类似，不授予用户数据写权限。
		"/nix/store",
		"/run/current-system",
	}
	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			roots = appendUniquePath(roots, filepath.Clean(candidate))
		}
	}
	return collapseCoveredRoots(sortedPaths(roots))
}

func linuxExecutableRuntimeRoots(executable string) []string {
	candidates := []string{filepath.Clean(executable)}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		candidates = append(candidates, filepath.Clean(resolved))
	}
	roots := []string{}
	for _, candidate := range candidates {
		dir := filepath.Dir(candidate)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			roots = appendUniquePath(roots, dir)
		}
		parent := filepath.Dir(dir)
		if filepath.Base(dir) != "bin" && filepath.Base(dir) != "sbin" {
			continue
		}
		for _, sibling := range []string{"lib", "lib64", "share"} {
			root := filepath.Join(parent, sibling)
			if info, err := os.Stat(root); err == nil && info.IsDir() {
				roots = appendUniquePath(roots, root)
			}
		}
	}
	return collapseCoveredRoots(sortedPaths(roots))
}

func linuxResolvedSystemTargets() []string {
	roots := []string{}
	for _, candidate := range []string{"/etc/resolv.conf", "/etc/localtime"} {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		resolved = filepath.Clean(resolved)
		if pathEqual(resolved, candidate) {
			continue
		}
		if _, err := os.Stat(resolved); err == nil {
			roots = appendUniquePath(roots, resolved)
		}
	}
	return sortedPaths(roots)
}

func configurePlatformProcess(cmd *exec.Cmd, _ EffectivePolicy) (func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil, nil
}

func attachPlatformProcess(cmd *exec.Cmd, _ EffectivePolicy) (func() error, func(), error) {
	pid := cmd.Process.Pid
	return func() error { return syscall.Kill(-pid, syscall.SIGKILL) }, nil, nil
}

func visibleUnderRoots(path string, groups ...[]string) bool {
	for _, values := range groups {
		for _, root := range values {
			if pathWithin(path, root) {
				return true
			}
		}
	}
	return false
}
