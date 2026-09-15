package einoadapter

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
)

// resolveStdioEnvironment 返回交给 Eino officialmcp/session 的环境覆盖项。
//
// officialmcp/session 会在启动 stdio 子进程时继承 os.Environ()，因此 Humbert 必须把
// 未授权的父进程变量显式覆盖为空值，避免 OPENAI_API_KEY、云凭据等 Humbert Secret
// 被任意本地 MCP 子进程继承。Go os/exec 对重复环境键采用最后一个值，而 Eino 会把
// TransportConfig.Env 追加到父进程环境之后，所以这里的覆盖项会成为子进程最终值。
//
// 只有启动进程所需的最小系统环境保留原值；业务 Secret 只能通过 Server 明确配置的
// Credential 引用注入。
func resolveStdioEnvironment(
	ctx context.Context,
	reader humbertmcp.CredentialReader,
	server humbertmcp.Server,
) (map[string]string, error) {
	environment := isolatedStdioEnvironmentOverrides()
	if server.Stdio == nil || len(server.Stdio.Env) == 0 {
		return environment, nil
	}
	if reader == nil {
		return nil, errors.New("MCP stdio Server 配置了 Environment Credential，但 CredentialStore 不可用")
	}
	for _, item := range server.Stdio.Env {
		name := strings.TrimSpace(item.Name)
		credentialID := strings.TrimSpace(item.CredentialID)
		value, err := reader.Get(ctx, credentialID)
		if err != nil {
			return nil, fmt.Errorf("读取 MCP stdio 环境变量 %q Credential 失败: %w", name, err)
		}
		environment[name] = value
	}
	return environment, nil
}

func isolatedStdioEnvironmentOverrides() map[string]string {
	allowed := make(map[string]struct{})
	for _, name := range baseStdioEnvironmentNames() {
		allowed[canonicalEnvName(name)] = struct{}{}
	}

	environment := make(map[string]string)
	for _, item := range os.Environ() {
		name, value, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		canonical := canonicalEnvName(name)
		_, keep := allowed[canonical]
		if !keep && runtime.GOOS != "windows" && strings.HasPrefix(canonical, "LC_") {
			keep = true
		}
		if keep {
			if sanitized, ok := safeInheritedStdioEnvironmentValue(name, value); ok {
				environment[name] = sanitized
			} else {
				environment[name] = ""
			}
		} else {
			// Eino officialmcp/session 会先放入 os.Environ()，再追加这里的 Env。
			// 空值覆盖可以保留 Eino 的 Session 管理能力，同时阻止秘密值泄漏。
			environment[name] = ""
		}
	}
	return environment
}

func baseStdioEnvironmentNames() []string {
	if runtime.GOOS == "windows" {
		return []string{
			"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP",
			"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
			"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "NPM_CONFIG_REGISTRY",
		}
	}
	return []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
		"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy",
		"NPM_CONFIG_REGISTRY", "npm_config_registry",
	}
}

// safeInheritedStdioEnvironmentValue 只允许把不含内嵌 Credential 的网络环境继承给
// 本地 MCP launcher。很多桌面/企业网络依赖 HTTP(S)_PROXY 或自定义 npm registry；如果
// Humbert 把这些变量全部清空，npx/bunx 会在 initialize 前因无法下载 package 而直接退出，
// Eino 最终只能看到 EOF。带 userinfo 的 proxy/registry URL 仍拒绝继承，Secret 必须通过
// MCP Server 显式 Environment Credential 配置。
func safeInheritedStdioEnvironmentValue(name, value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value, true
	}
	canonical := strings.ToUpper(strings.TrimSpace(name))
	switch canonical {
	case "NO_PROXY":
		return value, true
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NPM_CONFIG_REGISTRY":
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
			return "", false
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "socks5", "socks5h":
			return value, true
		default:
			return "", false
		}
	default:
		return value, true
	}
}

func canonicalEnvName(value string) string {
	value = strings.TrimSpace(value)
	if runtime.GOOS == "windows" {
		return strings.ToUpper(value)
	}
	return value
}

// applySandboxedStdioLauncherDefaults 为需要在 Workspace/Standard Sandbox 中启动的
// package-manager launcher 提供一个不依赖真实用户 Home 的临时运行环境。
//
// 典型场景是 npx -y @modelcontextprotocol/server-filesystem。Native Sandbox 会刻意
// 隐藏 ~/.npm、~/.config 等 Workspace 外目录；如果仍把真实 HOME 交给 npx，npm 会在
// initialize 前因为 cache/config 不可访问而直接退出，Eino 侧最终只看到 EOF。
//
// 这里不放宽 Sandbox 文件边界，而是把 launcher 的临时 HOME/cache 定向到 /tmp。
// 用户显式配置的同名 Environment Credential 始终优先，不会被这些默认值覆盖。
func applySandboxedStdioLauncherDefaults(environment map[string]string, server humbertmcp.Server, resolvedCommand string, isolated bool) {
	if !isolated || runtime.GOOS == "windows" || environment == nil {
		return
	}
	base := strings.ToLower(strings.TrimSpace(filepath.Base(resolvedCommand)))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	switch base {
	case "npx", "npm", "pnpm", "pnpx", "yarn", "bun", "bunx":
	default:
		return
	}

	explicit := make(map[string]struct{})
	if server.Stdio != nil {
		for _, item := range server.Stdio.Env {
			name := canonicalEnvName(item.Name)
			if name != "" {
				explicit[name] = struct{}{}
			}
		}
	}
	setDefault := func(name, value string) {
		if _, ok := explicit[canonicalEnvName(name)]; ok {
			return
		}
		environment[name] = value
	}

	// /tmp 在 Linux Bubblewrap 中是独立 tmpfs；macOS Seatbelt 允许 /private/tmp，
	// /tmp 会由系统解析到同一临时区域。这样既能让 npx 正常工作，又不会暴露真实 Home。
	setDefault("HOME", "/tmp")
	setDefault("TMPDIR", "/tmp")
	setDefault("XDG_CACHE_HOME", "/tmp/humbert-xdg-cache")
	setDefault("XDG_CONFIG_HOME", "/tmp/humbert-xdg-config")
	setDefault("XDG_DATA_HOME", "/tmp/humbert-xdg-data")
	setDefault("COREPACK_HOME", "/tmp/humbert-corepack")
	setDefault("NPM_CONFIG_CACHE", "/tmp/humbert-npm-cache")
	setDefault("npm_config_cache", "/tmp/humbert-npm-cache")
	setDefault("NPM_CONFIG_UPDATE_NOTIFIER", "false")
	setDefault("NPM_CONFIG_FUND", "false")
	setDefault("NPM_CONFIG_AUDIT", "false")
}

// ensureResolvedStdioCommandOnPath 让 officialmcp 通过绝对路径启动 launcher 时，launcher
// 内部仍能通过 /usr/bin/env 找到同一工具链目录中的 node/bun 等 runtime。
//
// macOS Wails 从 Finder 启动时 PATH 常常只有系统目录；resolveStdioExecutable 虽然可以在
// /opt/homebrew/bin 等位置找到 npx，但 npx 的 shebang 通常仍是 /usr/bin/env node。如果不把
// 已解析 executable 所在目录加入子进程 PATH，进程会在 MCP initialize 前直接退出，客户端
// 只能看到 EOF。
func ensureResolvedStdioCommandOnPath(environment map[string]string, resolvedCommand string) {
	if environment == nil || runtime.GOOS == "windows" {
		return
	}
	resolvedCommand = strings.TrimSpace(resolvedCommand)
	if resolvedCommand == "" {
		return
	}

	candidates := []string{filepath.Dir(resolvedCommand)}
	if realPath, err := filepath.EvalSymlinks(resolvedCommand); err == nil && strings.TrimSpace(realPath) != "" {
		candidates = append(candidates, filepath.Dir(realPath))
	}

	current := environment["PATH"]
	if strings.TrimSpace(current) == "" {
		current = os.Getenv("PATH")
	}
	parts := filepath.SplitList(current)
	seen := make(map[string]struct{}, len(parts)+len(candidates))
	result := make([]string, 0, len(parts)+len(candidates))
	appendPath := func(value string) {
		value = filepath.Clean(strings.TrimSpace(value))
		if value == "" || value == "." {
			return
		}
		key := value
		if runtime.GOOS == "darwin" {
			key = strings.ToLower(value)
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	for _, candidate := range candidates {
		appendPath(candidate)
	}
	for _, value := range parts {
		appendPath(value)
	}
	if len(result) > 0 {
		environment["PATH"] = strings.Join(result, string(os.PathListSeparator))
	}
}
