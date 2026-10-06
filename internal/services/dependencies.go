package services

import (
	"context"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/preferences"
	"github.com/sda1-hacker/humbert-agent/internal/proactive"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/searchindex"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/usecases"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// AgentDependencies 明确声明 AgentService 所需的依赖，入口不能再取得整个应用对象。
type AgentDependencies struct {
	Agents     *agents.Service
	Config     *config.Config
	Lifecycle  *usecases.AgentLifecycle
	Logger     *logging.Logger
	Sandbox    *sandbox.Manager
	Tools      *humberttools.Registry
	Workspaces *workspace.Manager
}

// AppDependencies 明确声明 AppService 所需的依赖，入口不能再取得整个应用对象。
type AppDependencies struct {
	Config *config.Config
	Logger *logging.Logger
	Status func(context.Context) (coreapp.Status, error)
}

// ChatDependencies 明确声明 ChatService 所需的依赖，入口不能再取得整个应用对象。
type ChatDependencies struct {
	Config  *config.Config
	Events  *eventbus.Bus
	Logger  *logging.Logger
	Runtime *agentruntime.Service
}

// MCPDependencies 明确声明 MCPService 所需的依赖，入口不能再取得整个应用对象。
type MCPDependencies struct {
	Agents        *agents.Service
	Configuration *usecases.MCPConfiguration
	MCP           *humbertmcp.Manager
}

// ModelDependencies 明确声明 ModelService 所需的依赖，入口不能再取得整个应用对象。
type ModelDependencies struct {
	Models *models.Registry
}

// PermissionDependencies 明确声明 PermissionService 所需的依赖，入口不能再取得整个应用对象。
type PermissionDependencies struct {
	Agents      *agents.Service
	Approvals   *approval.Manager
	Config      *config.Config
	Logger      *logging.Logger
	MCP         *humbertmcp.Manager
	Permissions *permission.Engine
}

// PreferenceDependencies 明确声明 PreferenceService 所需的依赖，入口不能再取得整个应用对象。
type PreferenceDependencies struct {
	Preferences *preferences.Store
	Sessions    *sessions.Service
}

// ProactiveDependencies 明确声明 ProactiveService 所需的依赖，入口不能再取得整个应用对象。
type ProactiveDependencies struct {
	Agents        *agents.Service
	Events        *eventbus.Bus
	Notifications *notifications.Service
	Proactive     *proactive.Manager
}

// SessionDependencies 明确声明 SessionService 所需的依赖，入口不能再取得整个应用对象。
type SessionDependencies struct {
	Lifecycle *usecases.AgentLifecycle
	Search    *searchindex.Service
	Sessions  *sessions.Service
}

// SkillDependencies 明确声明 SkillService 所需的依赖，入口不能再取得整个应用对象。
type SkillDependencies struct {
	Agents      *agents.Service
	Config      *config.Config
	Maintenance *usecases.SkillMaintenance
	Skills      *skills.Manager
}

// TaskDependencies 明确声明 TaskService 所需的依赖，入口不能再取得整个应用对象。
type TaskDependencies struct {
	Events      *eventbus.Bus
	Permissions *permission.Engine
	Sessions    *sessions.Service
	Tasks       *tasks.Manager
}

// WorkspaceDependencies 明确声明 WorkspaceService 所需的依赖，入口不能再取得整个应用对象。
type WorkspaceDependencies struct {
	Search        *searchindex.Service
	WorkspaceView *usecases.WorkspaceQuery
}
