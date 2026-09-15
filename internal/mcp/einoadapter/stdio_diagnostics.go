package einoadapter

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
)

const filesystemMCPPackage = "@modelcontextprotocol/server-filesystem"

// validateStdioLaunch 对最常见的“进程根本无法正确启动”问题做本地预检。
//
// 这不是另一套 MCP 实现：真正的 Session/initialize/tools/list 仍全部交给 Eino
// officialmcp/session。这里仅在进入 Eino 前检查 executable 与少数已知 CLI 参数，
// 以避免把 command/path 错误包装成难以理解的 initialize 失败。
func validateStdioLaunch(server humbertmcp.Server, workingDirectory string) (string, error) {
	if server.Stdio == nil {
		return "", errors.New("stdio MCP Server 缺少启动配置")
	}
	command := strings.TrimSpace(server.Stdio.Command)
	if command == "" {
		return "", errors.New("stdio MCP 启动命令不能为空")
	}
	resolved, err := resolveStdioExecutable(command)
	if err != nil {
		return "", err
	}
	if err := validateKnownStdioArgs(server.Stdio.Args, workingDirectory); err != nil {
		return "", err
	}
	return resolved, nil
}

func resolveStdioExecutable(command string) (string, error) {
	command = strings.TrimSpace(command)
	if filepath.IsAbs(command) {
		info, err := os.Stat(command)
		if err != nil {
			return "", fmt.Errorf("stdio MCP 启动命令 %q 不可用: %w", command, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("stdio MCP 启动命令 %q 指向目录而不是可执行程序", command)
		}
		return command, nil
	}
	if resolved, err := exec.LookPath(command); err == nil {
		return resolved, nil
	}

	// Wails/macOS 从 Finder 启动时 PATH 往往比用户 Terminal 更短。这里仅补充几个
	// 约定俗成的 executable 目录，不读取 shell rc，也不执行 shell，从而保持行为可预测。
	candidates := []string{}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin")
	case "linux":
		candidates = append(candidates, "/usr/local/bin", "/usr/bin", "/bin")
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".local", "bin"))
		}
	}
	for _, dir := range candidates {
		candidate := filepath.Join(dir, command)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf(
		"找不到 stdio MCP 启动命令 %q；桌面应用的 PATH 可能与 Terminal 不同，请改用可执行文件绝对路径或确认该命令已安装",
		command,
	)
}

func validateKnownStdioArgs(args []string, workingDirectory string) error {
	packageIndex := -1
	for index, raw := range args {
		value := strings.TrimSpace(raw)
		if value == filesystemMCPPackage || strings.HasPrefix(value, filesystemMCPPackage+"@") {
			packageIndex = index
			break
		}
	}
	if packageIndex < 0 {
		return nil
	}

	// Humbert 当前 officialmcp Client 没有向 filesystem server 注入 MCP Roots，
	// 因此使用 reference filesystem server 时至少需要一个 CLI allowed path。
	if packageIndex+1 >= len(args) {
		return fmt.Errorf(
			"Filesystem MCP 缺少允许目录；请在 %s 后至少再填写一个真实存在的目录，并保证参数每行一个",
			filesystemMCPPackage,
		)
	}

	foundPath := false
	for _, raw := range args[packageIndex+1:] {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		foundPath = true
		if strings.HasPrefix(value, "~") {
			return fmt.Errorf("Filesystem MCP 目录 %q 不能使用 ~；stdio argv 不经过 shell 展开，请填写绝对路径", value)
		}
		candidate := value
		if !filepath.IsAbs(candidate) && strings.TrimSpace(workingDirectory) != "" {
			candidate = filepath.Join(workingDirectory, candidate)
		}
		info, err := os.Stat(candidate)
		if err != nil {
			return fmt.Errorf("Filesystem MCP 允许目录 %q 不可用: %w", value, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("Filesystem MCP 允许路径 %q 不是目录", value)
		}
	}
	if !foundPath {
		return fmt.Errorf("Filesystem MCP 缺少允许目录；请在 %s 后填写目录", filesystemMCPPackage)
	}
	return nil
}

// expandKnownStdioSandboxPolicy 把“由 MCP Server 配置本身显式授予”的本地目录加入
// stdio 子进程 Sandbox。当前只处理 reference filesystem server，因为其 CLI allowed paths
// 具有稳定且公开的语义；未知 MCP Server 不做任何猜测。
//
// 这不是放宽整个 Agent Sandbox：新增路径只对该 MCP 子进程生效，并且仍拒绝 Humbert 的
// 硬保护 PathRule。用户既然显式配置并启用了 filesystem MCP 的 allowed directory，该目录
// 就是 Connector 自身的能力边界；否则 WorkspaceOnly 会让 server 在 initialize 前因目录
// 不可见直接退出，客户端只能得到难以诊断的 EOF。
func expandKnownStdioSandboxPolicy(
	policy sandbox.EffectivePolicy,
	server humbertmcp.Server,
	workingDirectory string,
) (sandbox.EffectivePolicy, error) {
	if server.Stdio == nil || policy.Profile == sandbox.ProfileFullAccess {
		return policy, nil
	}
	packageIndex := -1
	for index, raw := range server.Stdio.Args {
		value := strings.TrimSpace(raw)
		if value == filesystemMCPPackage || strings.HasPrefix(value, filesystemMCPPackage+"@") {
			packageIndex = index
			break
		}
	}
	if packageIndex < 0 {
		return policy, nil
	}

	result := policy
	for _, raw := range server.Stdio.Args[packageIndex+1:] {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		candidate := value
		if !filepath.IsAbs(candidate) {
			if strings.TrimSpace(workingDirectory) == "" {
				candidate = filepath.Join(policy.WorkspaceRoot, candidate)
			} else {
				candidate = filepath.Join(workingDirectory, candidate)
			}
		}
		var err error
		result, err = result.WithPathAccess(candidate, sandbox.AccessReadWrite, sandbox.RuleSourceMCPFilesystem)
		if err != nil {
			return sandbox.EffectivePolicy{}, fmt.Errorf("Filesystem MCP 允许目录 %q 无法加入 Sandbox: %w", value, err)
		}
	}
	return result, nil
}

type stdioStderrCapture struct {
	path       string
	statusPath string
}

// prepareStdioStderrCapture 用固定 shell wrapper 只重定向 stdio MCP 子进程的 stderr，
// stdout 仍原样保留给 MCP JSON-RPC。所有外部 command/args 都通过位置参数传递，不拼入
// shell source，因此不会把用户参数解释成 shell 语法。
//
// officialmcp v0.1.x 不暴露 exec.Cmd/Stderr hook；这是 Humbert 在不 fork Eino 的前提下
// 获取“initialize: EOF”真实根因的最小兼容层。capture 文件必须跟 Session 同生命周期保留，
// 因为 officialmcp 可能复用原 command/args 做透明重连。
func prepareStdioStderrCapture(command string, args []string) (string, []string, *stdioStderrCapture, error) {
	if runtime.GOOS == "windows" {
		return command, append([]string(nil), args...), nil, nil
	}
	file, err := os.CreateTemp("", "humbert-mcp-stderr-*.log")
	if err != nil {
		return "", nil, nil, fmt.Errorf("创建 MCP stderr 临时文件失败: %w", err)
	}
	name := file.Name()
	if err := os.Chmod(name, 0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", nil, nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", nil, nil, err
	}

	statusPath := name + ".status"
	// $1/$2 分别是日志与退出码文件；shift 后 $@ 从原 command 开始。fd 3 保留协议
	// stdout；stderr 进入持续消费的固定 pipeline：只保存最前 8 KiB，其余丢弃。第一段
	// pipeline 在子 shell 中把真实 MCP 进程退出码写入 statusPath，父 shell 最后恢复该退出码。
	wrapperArgs := []string{
		"-c",
		`log="$1"; status_file="$2"; shift 2; exec 3>&1; { "$@" 1>&3; code=$?; printf '%s' "$code" >"$status_file"; } 2>&1 | { /usr/bin/head -c 8192 >"$log"; /bin/cat >/dev/null; }; code=$(/bin/cat "$status_file" 2>/dev/null || printf '1'); /bin/rm -f "$status_file"; exit "$code"`,
		"humbert-mcp-stderr",
		name,
		statusPath,
		command,
	}
	wrapperArgs = append(wrapperArgs, args...)
	return "/bin/sh", wrapperArgs, &stdioStderrCapture{path: name, statusPath: statusPath}, nil
}

func (capture *stdioStderrCapture) Read(limit int64) string {
	if capture == nil || strings.TrimSpace(capture.path) == "" {
		return ""
	}
	file, err := os.Open(capture.path)
	if err != nil {
		return ""
	}
	defer file.Close()
	if limit <= 0 {
		limit = 4096
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return ""
	}
	if int64(len(data)) > limit {
		data = data[:limit]
		return strings.TrimSpace(string(data)) + "...[TRUNCATED]"
	}
	return strings.TrimSpace(string(data))
}

func (capture *stdioStderrCapture) Cleanup() {
	if capture == nil || strings.TrimSpace(capture.path) == "" {
		return
	}
	_ = os.Remove(capture.path)
	if strings.TrimSpace(capture.statusPath) != "" {
		_ = os.Remove(capture.statusPath)
	}
}

// expandStdioLauncherSandboxPolicy 为 package-manager launcher 只开放其自身 runtime 安装
// 根目录的只读访问。典型例子是 ~/.nvm/versions/node/vX/bin/npx：Seatbelt 只开放 bin
// 目录时，npx 的 symlink target 和 node_modules/npm 位于 sibling lib 目录，会在 initialize
// 前退出。这里不会开放整个 Home，只追加 READ_ONLY PathRule。
func expandStdioLauncherSandboxPolicy(policy sandbox.EffectivePolicy, resolvedCommand string) (sandbox.EffectivePolicy, error) {
	if policy.Profile == sandbox.ProfileFullAccess || !isKnownStdioPackageManager(resolvedCommand) {
		return policy, nil
	}
	result := policy
	for _, root := range stdioLauncherRuntimeRoots(resolvedCommand) {
		var err error
		result, err = result.WithPathAccess(root, sandbox.AccessReadOnly, sandbox.RuleSourceMCPLauncher)
		if err != nil {
			// launcher runtime 目录若与硬保护目录冲突就跳过；不要为了启动 npx/npm
			// 自动扩大敏感路径访问。
			continue
		}
	}
	if err := result.Validate(); err != nil {
		return sandbox.EffectivePolicy{}, fmt.Errorf("扩展 MCP launcher Sandbox 失败: %w", err)
	}
	return result, nil
}

func isKnownStdioPackageManager(command string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(command)))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	switch base {
	case "npx", "npm", "pnpm", "pnpx", "yarn", "bun", "bunx":
		return true
	default:
		return false
	}
}

func stdioLauncherRuntimeRoots(command string) []string {
	command = filepath.Clean(strings.TrimSpace(command))
	if command == "" || command == "." {
		return nil
	}
	paths := []string{command}
	if realPath, err := filepath.EvalSymlinks(command); err == nil && strings.TrimSpace(realPath) != "" {
		paths = append(paths, realPath)
	}
	home, _ := os.UserHomeDir()
	home = filepath.Clean(strings.TrimSpace(home))
	seen := map[string]struct{}{}
	result := []string{}
	appendRoot := func(root string) {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" || root == "." || root == string(filepath.Separator) || (home != "" && root == home) {
			return
		}
		if _, err := os.Stat(root); err != nil {
			return
		}
		if _, exists := seen[root]; exists {
			return
		}
		seen[root] = struct{}{}
		result = append(result, root)
	}

	for _, executable := range paths {
		dir := filepath.Dir(executable)
		appendRoot(dir)
		if filepath.Base(dir) == "bin" {
			appendRoot(filepath.Dir(dir))
		}
		if home == "" {
			continue
		}
		relative, err := filepath.Rel(home, executable)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case ".nvm":
			// ~/.nvm/versions/node/<version>/...
			if len(parts) >= 4 && parts[1] == "versions" && parts[2] == "node" {
				appendRoot(filepath.Join(home, parts[0], parts[1], parts[2], parts[3]))
			} else {
				appendRoot(filepath.Join(home, ".nvm"))
			}
		case ".volta":
			appendRoot(filepath.Join(home, ".volta"))
		case ".asdf":
			appendRoot(filepath.Join(home, ".asdf"))
		case ".fnm":
			appendRoot(filepath.Join(home, ".fnm"))
		case ".bun":
			appendRoot(filepath.Join(home, ".bun"))
		case ".local":
			if len(parts) >= 3 && parts[1] == "share" && (parts[2] == "mise" || parts[2] == "fnm") {
				appendRoot(filepath.Join(home, ".local", "share", parts[2]))
			}
		case "Library":
			if len(parts) >= 2 && (parts[1] == "pnpm" || parts[1] == "Application Support") {
				// Application Support 只在后续具体路径仍属于 fnm/mise 时开放，避免把整个目录暴露。
				if parts[1] == "pnpm" {
					appendRoot(filepath.Join(home, "Library", "pnpm"))
				} else if len(parts) >= 3 && (parts[2] == "fnm" || parts[2] == "mise") {
					appendRoot(filepath.Join(home, "Library", "Application Support", parts[2]))
				}
			}
		}
	}
	return result
}

func enrichStdioConnectError(server humbertmcp.Server, err error, sandboxed bool, childStderr string) error {
	if err == nil {
		return nil
	}
	command := ""
	if server.Stdio != nil {
		command = strings.TrimSpace(server.Stdio.Command)
	}
	base := strings.ToLower(filepath.Base(command))
	hints := []string{
		"请先在系统终端使用相同 command/args 验证 Server 能否保持运行并完成 MCP initialize",
		"stdio Server 的普通日志必须写 stderr，stdout 只能输出 MCP JSON-RPC",
	}
	if strings.Contains(strings.ToLower(err.Error()), "eof") {
		hints = append(hints, "initialize: EOF 表示子进程在握手完成前已经退出，通常是启动参数、运行环境或 Sandbox 可见目录导致")
	}
	if sandboxed {
		hints = append(hints, "该连接由 Agent Sandbox 启动；package-manager launcher 会改用隔离的 /tmp Home/cache，若 Server 依赖真实 Home 配置请改用预安装的直接可执行文件或显式 Environment Credential")
	}
	if base == "npx" || base == "npx.cmd" {
		hints = append(hints, "npx 首次运行可能需要网络/npm cache；长期使用建议预安装 MCP package 并配置稳定的可执行文件路径")
		if runtime.GOOS == "windows" {
			hints = append(hints, "Windows 官方示例建议 command=cmd，并把 /c、npx 放在参数最前面")
		}
	}
	if server.Stdio != nil {
		for _, raw := range server.Stdio.Args {
			value := strings.TrimSpace(raw)
			if value == filesystemMCPPackage || strings.HasPrefix(value, filesystemMCPPackage+"@") {
				hints = append(hints, "Filesystem MCP 至少需要一个存在的 allowed directory；不要使用未展开的 ~ 路径")
				break
			}
		}
	}
	detail := strings.TrimSpace(childStderr)
	if detail != "" {
		return fmt.Errorf(
			"连接 stdio MCP Server %q 失败: %w；子进程 stderr: %s；排查建议：%s",
			server.Name,
			err,
			detail,
			strings.Join(hints, "；"),
		)
	}
	return fmt.Errorf(
		"连接 stdio MCP Server %q 失败: %w；排查建议：%s",
		server.Name,
		err,
		strings.Join(hints, "；"),
	)
}
