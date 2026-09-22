package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/databackup"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// AppStatus 是桌面前端可读取的应用健康信息。
//
// Adapter DTO 与 Core Status 分离，避免未来 Core 内部字段变动直接影响 Wails
// JavaScript API。StorageReady 表示 config/*.json、Agent Profile 等文件级状态可以
// 正常解析，不再包含任何 SQLite/Database 概念。
type AppStatus struct {
	Name    string `json:"name"`
	Version string `json:"version"`

	Ready        bool `json:"ready"`
	StorageReady bool `json:"storageReady"`

	DataDir    string `json:"dataDir"`
	ConfigFile string `json:"configFile"`
	ConfigDir  string `json:"configDir"`
	AgentsDir  string `json:"agentsDir"`

	StartedAt     string `json:"startedAt"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
}

// AppService 是 Wails Desktop 暴露给 Vue 的最小应用服务。
type AppService struct {
	core *coreapp.Application
}

// NewAppService 创建桌面应用状态服务。
func NewAppService(core *coreapp.Application) *AppService {
	return &AppService{core: core}
}

// ServiceName 为 Wails 生命周期日志提供稳定服务名称。
func (s *AppService) ServiceName() string {
	return "AppService"
}

// ServiceStartup 在 Wails 启动阶段执行最后一次 Core 文件存储健康检查。
//
// 如果 Provider/Model/Agent 配置在窗口真正启动前已经损坏，则直接返回错误，避免
// 打开一个表面可用但无法正确持久化状态的桌面窗口。
func (s *AppService) ServiceStartup(
	ctx context.Context,
	options application.ServiceOptions,
) error {
	status, err := s.core.Status(ctx)
	if err != nil {
		return fmt.Errorf("检查 Humbert Core 状态失败: %w", err)
	}

	s.core.Logger().Info(
		ctx,
		"Wails AppService 已启动",
		"operation", "wails.app_service.startup",
		"ready", status.Ready,
		"storage_ready", status.StorageReady,
	)
	return nil
}

// ServiceShutdown 是 Wails Service 的生命周期结束通知。
// Core 资源所有权属于 main -> core.Application，因此这里只记录生命周期。
func (s *AppService) ServiceShutdown() error {
	s.core.Logger().Info(
		context.Background(),
		"Wails AppService 已停止",
		"operation", "wails.app_service.shutdown",
	)
	return nil
}

// Status 返回 Humbert 当前运行状态。
func (s *AppService) Status() (AppStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := s.core.Status(ctx)
	if err != nil {
		return AppStatus{}, fmt.Errorf("读取应用状态失败: %w", err)
	}

	return AppStatus{
		Name:          status.Name,
		Version:       status.Version,
		Ready:         status.Ready,
		StorageReady:  status.StorageReady,
		DataDir:       status.DataDir,
		ConfigFile:    status.ConfigFile,
		ConfigDir:     status.ConfigDir,
		AgentsDir:     status.AgentsDir,
		StartedAt:     status.StartedAt.Format(time.RFC3339),
		UptimeSeconds: int64(status.Uptime.Seconds()),
	}, nil
}

// ExportBackup 通过原生保存对话框导出完整用户数据，包含 Secrets。
// 运行中备份应在没有活跃任务时执行；恢复始终使用离线工具。
func (s *AppService) ExportBackup() (string, error) {
	app := application.Get()
	if app == nil {
		return "", fmt.Errorf("Wails Application 尚未初始化")
	}
	path, err := app.Dialog.SaveFile().SetMessage("导出 Humbert 数据备份").SetFilename("humbert-backup-"+time.Now().Format("2006-01-02")+".zip").AddFilter("ZIP 备份", "*.zip").PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("选择备份位置失败: %w", err)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := databackup.Create(ctx, s.core.Config().Paths.HomeDir, path); err != nil {
		return "", fmt.Errorf("导出备份失败: %w", err)
	}
	return path, nil
}

// ScheduleRestore 验证用户选择的备份，下次启动时在 Core 初始化前恢复。
func (s *AppService) ScheduleRestore() (string, error) {
	app := application.Get()
	if app == nil {
		return "", fmt.Errorf("Wails Application 尚未初始化")
	}
	path, err := app.Dialog.OpenFile().SetTitle("选择要恢复的 Humbert 备份").AddFilter("ZIP 备份", "*.zip").PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("选择备份文件失败: %w", err)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := databackup.ScheduleRestore(ctx, s.core.Config().Paths.HomeDir, path); err != nil {
		return "", fmt.Errorf("安排数据恢复失败: %w", err)
	}
	return path, nil
}
