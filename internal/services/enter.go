package services

import (
	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// All 返回 Humbert Desktop 当前需要注册的全部 Wails Service。
//
// 所有 Service 注册集中在一个地方，可以避免 main.go 随着业务增加
// 逐渐变成几十行 Service 初始化代码。
func All(
	core *coreapp.Application,
) []application.Service {
	return []application.Service{
		application.NewService(
			NewAppService(core),
		),

		application.NewService(
			NewPreferenceService(core),
		),

		application.NewService(
			NewModelService(core),
		),

		application.NewService(
			NewAgentService(core),
		),

		application.NewService(
			NewSessionService(core),
		),

		application.NewService(
			NewPermissionService(core),
		),

		application.NewService(
			NewSkillService(core),
		),

		application.NewService(
			NewMCPService(core),
		),

		application.NewService(
			NewChatService(core),
		),

		application.NewService(
			NewTaskService(core),
		),
	}
}
