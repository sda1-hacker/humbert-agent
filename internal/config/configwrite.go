package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/spf13/viper"
)

var configWriteMu sync.Mutex

// saveConfigMutation 对 Humbert 自己的 config.yaml 做串行、原子更新。
// mutate 只负责修改 Viper 中明确属于调用方的配置键；文件校验、fsync、rename 与
// Windows 回滚都集中在这里，避免不同 Settings Service 各自维护一套写盘逻辑。
func saveConfigMutation(
	ctx context.Context,
	configFile string,
	operation string,
	mutate func(*viper.Viper),
) error {
	if ctx == nil {
		return fmt.Errorf("%s失败: context.Context 不能为空", operation)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s被取消: %w", operation, err)
	}
	configFile = strings.TrimSpace(configFile)
	if configFile == "" {
		return fmt.Errorf("%s失败: config.yaml 路径不能为空", operation)
	}
	if mutate == nil {
		return fmt.Errorf("%s失败: 配置更新函数不能为空", operation)
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	if err := validateWritableConfigFile(configFile); err != nil {
		return fmt.Errorf("%s失败: %w", operation, err)
	}

	v := viper.New()
	v.SetConfigFile(configFile)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取现有 config.yaml 失败: %w", err)
	}
	mutate(v)

	parentDir := filepath.Dir(configFile)
	temp, err := os.CreateTemp(parentDir, ".humbert-config-*.yaml")
	if err != nil {
		return fmt.Errorf("创建 config.yaml 临时文件失败: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("关闭 config.yaml 临时文件失败: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return fmt.Errorf("准备 config.yaml 临时文件失败: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	if err := v.WriteConfigAs(tempPath); err != nil {
		return fmt.Errorf("使用 Viper 写入 config.yaml 临时文件失败: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tempPath, 0o600); err != nil {
			return fmt.Errorf("设置 config.yaml 临时文件权限失败: %w", err)
		}
	}

	file, err := os.OpenFile(tempPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("打开 config.yaml 临时文件以同步失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("同步 config.yaml 临时文件失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 config.yaml 临时文件失败: %w", err)
	}

	if err := replaceConfigFile(tempPath, configFile); err != nil {
		return fmt.Errorf("提交 config.yaml 失败: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(configFile, 0o600); err != nil {
			return fmt.Errorf("设置 config.yaml 权限失败: %w", err)
		}
	}

	committed = true
	return nil
}

func validateWritableConfigFile(path string) error {
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("读取 config.yaml 父目录状态失败: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return errors.New("config.yaml 父目录必须是真实目录")
	}

	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("读取 config.yaml 状态失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("拒绝写入符号链接 config.yaml")
	}
	if !info.Mode().IsRegular() {
		return errors.New("config.yaml 必须是普通文件")
	}
	return nil
}

func replaceConfigFile(source string, destination string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(source, destination)
	}

	backup := destination + ".settings-backup"
	_ = os.Remove(backup)
	if err := os.Rename(destination, backup); err != nil {
		return fmt.Errorf("备份旧 config.yaml 失败: %w", err)
	}
	if err := os.Rename(source, destination); err != nil {
		_ = os.Rename(backup, destination)
		return err
	}
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("清理 config.yaml 备份失败: %w", err)
	}
	return nil
}
