package config

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// NormalizePermissionConfig 返回规范化后的 Permission 配置副本。
func NormalizePermissionConfig(cfg PermissionConfig) PermissionConfig {
	cfg.ReadAction = strings.ToLower(strings.TrimSpace(cfg.ReadAction))
	cfg.WriteAction = strings.ToLower(strings.TrimSpace(cfg.WriteAction))
	cfg.ExecAction = strings.ToLower(strings.TrimSpace(cfg.ExecAction))
	return cfg
}

// ValidatePermissionConfig 校验 Permission + Approval 的应用级设置。
func ValidatePermissionConfig(cfg PermissionConfig) error {
	cfg = NormalizePermissionConfig(cfg)
	for name, action := range map[string]string{
		"read_action":  cfg.ReadAction,
		"write_action": cfg.WriteAction,
		"exec_action":  cfg.ExecAction,
	} {
		switch action {
		case "allow", "deny", "ask":
		default:
			return fmt.Errorf("security.permissions.%s 只允许 allow/deny/ask，当前为 %q", name, action)
		}
	}
	if cfg.ApprovalTimeoutMS < 1000 || cfg.ApprovalTimeoutMS > 24*60*60*1000 {
		return errors.New("security.permissions.approval_timeout_ms 必须位于 1000-86400000 之间")
	}
	return nil
}

// SavePermissionConfig 只更新 security.permissions.*，其他 config.yaml 内容保持不变。
func SavePermissionConfig(ctx context.Context, configFile string, cfg PermissionConfig) error {
	cfg = NormalizePermissionConfig(cfg)
	if err := ValidatePermissionConfig(cfg); err != nil {
		return fmt.Errorf("保存 Permission 配置失败: %w", err)
	}
	return saveConfigMutation(ctx, configFile, "保存 Permission 配置", func(v *viper.Viper) {
		v.Set("security.permissions.enabled", cfg.Enabled)
		v.Set("security.permissions.read_action", cfg.ReadAction)
		v.Set("security.permissions.write_action", cfg.WriteAction)
		v.Set("security.permissions.exec_action", cfg.ExecAction)
		v.Set("security.permissions.approval_timeout_ms", cfg.ApprovalTimeoutMS)
	})
}
