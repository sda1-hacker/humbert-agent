package config

import (
	"context"
	"strings"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/commandenv"
)

func TestFreshConfigDoesNotEnableLocalCommands(t *testing.T) {
	t.Setenv("HUMBERT_SECURITY_SHELL_ENABLED", "")
	cfg, err := loadFromHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.ShellEnabled {
		t.Fatal("fresh installation should not enable local commands")
	}
	if cfg.Security.Permissions.Mode != PermissionModeRisk || cfg.Runtime.MaxIterations != 200 {
		t.Fatalf("产品默认策略没有落地: mode=%s iterations=%d", cfg.Security.Permissions.Mode, cfg.Runtime.MaxIterations)
	}
}

func TestPermissionModesPersistAcrossReload(t *testing.T) {
	home := t.TempDir()
	cfg, err := loadFromHome(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{PermissionModeFull, PermissionModeAlways, PermissionModeRisk} {
		permissions := cfg.Security.Permissions
		permissions.Mode = mode
		if err := SavePermissionConfig(context.Background(), cfg.Paths.ConfigFile, permissions); err != nil {
			t.Fatal(err)
		}
		reloaded, err := loadFromHome(home)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Security.Permissions.Mode != mode || reloaded.Security.Permissions.ApprovalTimeoutMS != permissions.ApprovalTimeoutMS {
			t.Fatalf("审批模式未持久化或误改了等待时间: %+v", reloaded.Security.Permissions)
		}
	}
}

func TestCommandEnvironmentUsesDesktopPathWithoutSecrets(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HUMBERT_TEST_SECRET", "not-for-child-process")
	found := false
	for _, value := range SafeCommandEnvironment() {
		if strings.HasPrefix(value, "PATH=") {
			found = value == "PATH="+commandenv.Path()
		}
		if strings.HasPrefix(value, "HUMBERT_TEST_SECRET=") {
			t.Fatal("补全 PATH 泄漏了非白名单环境变量")
		}
	}
	if !found {
		t.Fatal("子进程 PATH 与审批使用的查找路径不一致")
	}
}
