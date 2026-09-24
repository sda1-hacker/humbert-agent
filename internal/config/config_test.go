package config

import "testing"

func TestFreshConfigDoesNotEnableLocalCommands(t *testing.T) {
	t.Setenv("HUMBERT_SECURITY_SHELL_ENABLED", "")
	cfg, err := loadFromHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.ShellEnabled {
		t.Fatal("fresh installation should not enable local commands")
	}
}
