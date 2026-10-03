package services

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
)

// All 是唯一知道 Core 装配结果的桌面注册点；各 Service 仅接收自己的 Dependencies。
// 新模块可在桌面装配处追加已构造的 Service，不必改动既有服务或公共业务规则。
func All(core *coreapp.Application, additional ...application.Service) []application.Service {
	services := []application.Service{
		application.NewService(NewAgentService(AgentDependencies{
			Agents: core.Agents(), Config: core.Config(), Lifecycle: core.Lifecycle(), Logger: core.Logger(),
			Sandbox: core.Sandbox(), Tools: core.Tools(), Workspaces: core.Workspaces(),
		})),
		application.NewService(NewAppService(AppDependencies{Config: core.Config(), Logger: core.Logger(), Status: core.Status})),
		application.NewService(NewChatService(ChatDependencies{
			Config: core.Config(), Events: core.Events(), Logger: core.Logger(), Runtime: core.Runtime(),
		})),
		application.NewService(NewMCPService(MCPDependencies{
			Agents: core.Agents(), Configuration: core.MCPConfiguration(), MCP: core.MCP(),
		})),
		application.NewService(NewModelService(ModelDependencies{Models: core.Models()})),
		application.NewService(NewPermissionService(PermissionDependencies{
			Agents: core.Agents(), Approvals: core.Approvals(), Config: core.Config(), Logger: core.Logger(),
			MCP: core.MCP(), Permissions: core.Permissions(),
		})),
		application.NewService(NewPreferenceService(PreferenceDependencies{
			Preferences: core.Preferences(), Sessions: core.Sessions(),
		})),
		application.NewService(NewProactiveService(ProactiveDependencies{
			Agents: core.Agents(), Events: core.Events(), Notifications: core.Notifications(), Proactive: core.Proactive(),
		})),
		application.NewService(NewSessionService(SessionDependencies{
			Lifecycle: core.Lifecycle(), Search: core.Search(), Sessions: core.Sessions(),
		})),
		application.NewService(NewSkillService(SkillDependencies{
			Agents: core.Agents(), Config: core.Config(), Maintenance: core.Maintenance(), Skills: core.Skills(),
		})),
		application.NewService(NewTaskService(TaskDependencies{
			Events: core.Events(), Permissions: core.Permissions(), Sessions: core.Sessions(), Tasks: core.Tasks(),
		})),
		application.NewService(NewWorkspaceService(WorkspaceDependencies{Search: core.Search(), WorkspaceView: core.WorkspaceView()})),
	}
	return append(services, additional...)
}
