package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/viper"
)

// NormalizeSandboxConfig 规范化可由设置页更新的 Sandbox 默认配置。
func NormalizeSandboxConfig(cfg SandboxConfig) SandboxConfig {
	cfg.DefaultProfile = strings.ToLower(strings.TrimSpace(cfg.DefaultProfile))
	cfg.DefaultNetworkMode = strings.ToLower(strings.TrimSpace(cfg.DefaultNetworkMode))
	cfg.NativeMode = strings.ToLower(strings.TrimSpace(cfg.NativeMode))
	return cfg
}

// ValidateSandboxConfig 与启动配置使用同一组约束，避免运行期能保存但下次启动失败。
func ValidateSandboxConfig(cfg SandboxConfig) error {
	cfg = NormalizeSandboxConfig(cfg)
	switch cfg.DefaultProfile {
	case "workspace_only", "standard", "full_access":
	default:
		return fmt.Errorf("security.sandbox.default_profile 不支持: %q", cfg.DefaultProfile)
	}
	switch cfg.DefaultNetworkMode {
	case "none", "public", "all":
	default:
		return fmt.Errorf("security.sandbox.default_network_mode 不支持: %q", cfg.DefaultNetworkMode)
	}
	switch cfg.NativeMode {
	case "preferred", "required", "off":
	default:
		return fmt.Errorf("security.sandbox.native_mode 不支持: %q", cfg.NativeMode)
	}
	if cfg.CommandGracePeriodMS < 100 || cfg.CommandGracePeriodMS > 30000 {
		return errors.New("security.sandbox.command_grace_period_ms 必须位于 100-30000 之间")
	}
	return nil
}

// NormalizeAndValidateShellCommands 规范化本地程序白名单。
// 设置页只允许保存“程序名”，拒绝路径和 shell 片段，避免把白名单变成任意可执行路径入口。
func NormalizeAndValidateShellCommands(enabled bool, values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("只允许填写程序名称，不能包含路径: %q", raw)
		}
		if strings.ContainsAny(name, " \t\r\n;&|$`(){}[]<>\"") {
			return nil, fmt.Errorf("程序名称包含不允许的 shell 字符: %q", raw)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	if enabled && len(result) == 0 {
		return nil, errors.New("启用本地程序后至少需要允许一个程序")
	}
	return result, nil
}

// SaveSandboxConfig 只更新 security.sandbox.*，使用统一原子 config writer。
// 保留该函数供只修改 Sandbox 的内部调用使用。
func SaveSandboxConfig(ctx context.Context, configFile string, cfg SandboxConfig) error {
	cfg = NormalizeSandboxConfig(cfg)
	if err := ValidateSandboxConfig(cfg); err != nil {
		return fmt.Errorf("保存 Sandbox 配置失败: %w", err)
	}
	return saveConfigMutation(ctx, configFile, "保存 Sandbox 配置", func(v *viper.Viper) {
		v.Set("security.sandbox.default_profile", cfg.DefaultProfile)
		v.Set("security.sandbox.default_network_mode", cfg.DefaultNetworkMode)
		v.Set("security.sandbox.native_mode", cfg.NativeMode)
		v.Set("security.sandbox.command_grace_period_ms", cfg.CommandGracePeriodMS)
	})
}

// SaveSandboxAndShellConfig 原子保存“安全”页面可编辑的全局设置。
// run_command / run_skill_script 的注册发生在应用启动阶段，因此 shell 设置写盘后需要重启 Humbert
// 才能让 Tool Registry 使用新的开关和白名单；Sandbox 默认策略本身仍会立即热更新。
func SaveSandboxAndShellConfig(
	ctx context.Context,
	configFile string,
	cfg SandboxConfig,
	shellEnabled bool,
	shellAllowedCommands []string,
) error {
	cfg = NormalizeSandboxConfig(cfg)
	if err := ValidateSandboxConfig(cfg); err != nil {
		return fmt.Errorf("保存安全设置失败: %w", err)
	}
	commands, err := NormalizeAndValidateShellCommands(shellEnabled, shellAllowedCommands)
	if err != nil {
		return fmt.Errorf("保存安全设置失败: %w", err)
	}
	return saveConfigMutation(ctx, configFile, "保存安全设置", func(v *viper.Viper) {
		v.Set("security.sandbox.default_profile", cfg.DefaultProfile)
		v.Set("security.sandbox.default_network_mode", cfg.DefaultNetworkMode)
		v.Set("security.sandbox.native_mode", cfg.NativeMode)
		v.Set("security.sandbox.command_grace_period_ms", cfg.CommandGracePeriodMS)
		v.Set("security.shell_enabled", shellEnabled)
		v.Set("security.shell_allowed_commands", commands)
	})
}
