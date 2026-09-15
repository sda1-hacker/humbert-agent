package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sda1-hacker/humbert-agent/frontend"
	"github.com/sda1-hacker/humbert-agent/internal/services"
	"os"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/logging"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// assets 包含 Vite 生产构建结果。
//
// wails3 dev 会将 frontend 请求代理到 Vite Dev Server；
// wails3 build 则使用 frontend/dist 中的生产资源。
//
// frontend/dist/.gitkeep 保证在第一次执行 npm build 之前该目录也存在，
// 从而使 go:embed 在普通 Go 工具分析项目时不会因为目录不存在而失败。
//

func main() {
	bootstrapLogger :=
		logging.NewBootstrap()

	core, err :=
		coreapp.Bootstrap(
			context.Background(),
		)
	if err != nil {
		bootstrapLogger.Error(
			context.Background(),
			"Humbert 启动失败",
			"error",
			err,
		)

		os.Exit(1)
	}

	exitCode := 0

	if err := runDesktop(core); err != nil {
		core.Logger().Error(
			context.Background(),
			"Wails Desktop 运行失败",
			"error",
			err,
		)

		exitCode = 1
	}

	shutdownCtx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Second,
		)

	if err := core.Shutdown(
		shutdownCtx,
	); err != nil {
		bootstrapLogger.Error(
			context.Background(),
			"Humbert 关闭过程存在错误",
			"error",
			err,
		)

		exitCode = 1
	}

	cancel()

	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// runDesktop 创建并运行 Wails 桌面 Adapter。
//
// Core Application 在进入本函数前已经初始化完成。
// 因此 Wails 只负责窗口、Frontend Bridge 和桌面生命周期，
// 不承担文件持久化、日志等领域基础设施的所有权。
func runDesktop(
	core *coreapp.Application,
) error {
	cfg := core.Config()
	logger := core.Logger()

	wailsApp := application.New(
		application.Options{
			Name:         cfg.App.Name,
			Description:  "Local-first personal AI agent",
			Logger:       logger.Slog(),
			LogLevel:     logger.Level(),
			MarshalError: marshalFrontendError,
			// 注册服务
			Services: services.All(core),
			// 前端资产
			Assets: application.AssetOptions{
				Handler: application.AssetFileServerFS(
					frontend.Assets,
				),
			},
			Mac: application.MacOptions{
				ApplicationShouldTerminateAfterLastWindowClosed: true,
			},
		},
	)

	window := wailsApp.Window.NewWithOptions(
		application.WebviewWindowOptions{
			Name:      "main",
			Title:     cfg.App.Name,
			Width:     1120,
			Height:    760,
			MinWidth:  860,
			MinHeight: 600,
			BackgroundColour: application.NewRGB(
				248,
				246,
				242,
			),
			URL: "/",
			Mac: application.MacWindow{
				TitleBar: application.MacTitleBar{
					// 使用透明标题栏，让整个窗口视觉更统一。
					AppearsTransparent: true,
					// 隐藏顶部的 Humbert 标题文字。
					// 保留 macOS 原生红黄绿窗口控制按钮。
					HideTitle: true,
					// WebView 内容真正延伸到 macOS title bar 区域。
					FullSizeContent: true,
				},
				// 不使用系统半透明/深色 Backdrop，
				// Humbert 自己负责整个窗口的背景视觉。
				Backdrop: application.MacBackdropNormal,
			},
		},
	)

	window.Center()
	window.Show()

	logger.Info(
		context.Background(),
		"Wails Desktop 已启动",
		"window",
		"main",
	)

	if err := wailsApp.Run(); err != nil {
		return fmt.Errorf(
			"运行 Wails application 失败: %w",
			err,
		)
	}

	return nil
}

// marshalFrontendError 是 Wails Go → JavaScript 错误边界。
//
// 所有返回给前端的 error 在序列化前先经过统一脱敏，
// 避免第三方 SDK 将 Authorization Header 或 Token 包含在原始错误文本中。
func marshalFrontendError(
	err error,
) []byte {
	message := logging.SafeErrorText(
		err,
		2048,
	)

	payload := struct {
		Message string `json:"message"`
	}{
		Message: message,
	}

	result, marshalErr := json.Marshal(payload)

	// 当前 payload 只包含 string，正常情况下 json.Marshal 不可能失败。
	// 如果标准库未来行为发生异常，返回固定 JSON，
	// 不能把原始错误重新暴露给前端。
	if marshalErr != nil {
		return []byte(
			`{"message":"内部错误序列化失败"}`,
		)
	}

	return result
}
