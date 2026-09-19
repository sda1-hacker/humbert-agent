package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/sda1-hacker/humbert-agent/internal/workspaceview"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/contextartifact"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/credential"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/mcp/einoadapter"
	"github.com/sda1-hacker/humbert-agent/internal/memory"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/preferences"
	"github.com/sda1-hacker/humbert-agent/internal/proactive"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const Version = "0.1.0"

// Status 表示 Humbert Core 当前健康状态。
//
// StorageReady 表示文件级配置和 Agent Profile 可以正常读取。SQLite 已完全移出
// Runtime，因此状态对象不再暴露 DatabaseFile / DatabaseReady。
type Status struct {
	Name string

	Version string

	Ready bool

	DataDir string

	ConfigFile string

	ConfigDir string

	AgentsDir string

	StorageReady bool

	ModelRevision uint64

	ToolRevision uint64

	MCPRevision uint64

	StartedAt time.Time

	Uptime time.Duration
}

// Application 是 Humbert Core Composition Root。
//
// 它只负责：
//   - 创建模块；
//   - 注入依赖；
//   - 管理应用生命周期；
//   - 按正确顺序关闭资源。
//
// 业务逻辑不应该进入 Application。文件 Store 本身不拥有后台 goroutine，因此
// Shutdown 只需要先停止 Runtime，再关闭 Workspace/EventBus/Logger。
type Application struct {
	config *config.Config

	logger *logging.Logger

	credentials *credential.Store

	events *eventbus.Bus

	workspaces *workspace.Manager

	// workspaceView 是桌面工作区页面的只读查询层。
	// 它不拥有新的文件生命周期，只投影 Agent 当前 Workspace 与 Session 工具事务。
	workspaceView *workspaceview.Service

	sandbox *sandbox.Manager

	permissions *permission.Engine

	preferences *preferences.Store

	approvals *approval.Manager

	tools *humberttools.Registry

	skills *skills.Manager

	mcp *humbertmcp.Manager

	models *models.Registry

	agents *agents.Service

	sessions *sessions.Service

	contextEngine *contextengine.Engine

	memory *memory.Manager

	runtime *agentruntime.Service

	tasks *tasks.Manager

	notifications *notifications.Service

	proactive *proactive.Manager

	startedAt time.Time

	ready atomic.Bool

	shutdownOnce sync.Once

	shutdownErr error
}

// Bootstrap 初始化 Humbert Core。
//
// 持久化依赖顺序：
//
//	Config/Viper
//	    ↓
//	TranscriptStore(JSONL)
//	    ├── Model Store(config/*.json)
//	    ├── Agent Store(agents/*/config.json)
//	    ├── Session Store(agents/*/sessions/*/{config.json,session.jsonl})
//
// 每个 Session 的 config.json 只保存控制面配置；session.jsonl v3 是 Message / Thinking /
// ToolCall / ToolResult 的唯一持久化事实来源。
// Runtime Event 只服务实时 UI，运行审计进入统一结构化日志，不再创建 runstore/runtrace。
// 不同 Session 的消息写入由 TranscriptStore 的 per-file Mutex 独立串行化。
func Bootstrap(ctx context.Context) (*Application, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("应用启动被取消: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("加载应用配置失败: %w", err)
	}

	logger, err := logging.New(cfg.Logging, cfg.Paths.LogFile)
	if err != nil {
		return nil, fmt.Errorf("初始化统一日志失败: %w", err)
	}
	loggerOwned := true
	defer func() {
		if loggerOwned {
			_ = logger.Close()
		}
	}()

	logger.Info(
		ctx,
		"Humbert Core 正在启动",
		"operation", "application.bootstrap",
		"version", Version,
		"data_dir", cfg.Paths.HomeDir,
	)

	credentials, err := credential.New(cfg.Paths.SecretsDir)
	if err != nil {
		return nil, fmt.Errorf("初始化 CredentialStore 失败: %w", err)
	}

	transcriptStore, err := transcript.NewStore(cfg.Paths.AgentsDir)
	if err != nil {
		return nil, fmt.Errorf("初始化 TranscriptStore 失败: %w", err)
	}

	managedWorkspaceRoot := filepath.Join(cfg.Paths.HomeDir, "workspaces")
	workspaceManager, err := workspace.NewManager(ctx, managedWorkspaceRoot, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化 Workspace Manager 失败: %w", err)
	}
	workspaceOwned := true
	defer func() {
		if workspaceOwned {
			_ = workspaceManager.Close()
		}
	}()

	sandboxManager, err := sandbox.NewManager(sandbox.Config{
		DefaultProfile:     sandbox.Profile(cfg.Security.Sandbox.DefaultProfile),
		DefaultNetworkMode: sandbox.NetworkMode(cfg.Security.Sandbox.DefaultNetworkMode),
		DefaultNativeMode:  sandbox.NativeMode(cfg.Security.Sandbox.NativeMode),
		CommandGracePeriod: time.Duration(cfg.Security.Sandbox.CommandGracePeriodMS) * time.Millisecond,
	}, cfg.Paths.HomeDir)
	if err != nil {
		return nil, fmt.Errorf("初始化 Sandbox Manager 失败: %w", err)
	}

	permissionStore, err := permission.NewStore(ctx, cfg.Paths.PermissionsFile)
	if err != nil {
		return nil, fmt.Errorf("初始化 Permission Store 失败: %w", err)
	}
	permissionEngine, err := permission.NewEngine(cfg.Security.Permissions, permissionStore, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化 Permission Engine 失败: %w", err)
	}
	approvalManager, err := approval.NewManager(
		time.Duration(cfg.Security.Permissions.ApprovalTimeoutMS)*time.Millisecond,
		permissionEngine,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Approval Manager 失败: %w", err)
	}
	preferenceStore, err := preferences.NewStore(ctx, cfg.Paths.PreferencesFile)
	if err != nil {
		return nil, fmt.Errorf("初始化 Preferences Store 失败: %w", err)
	}

	skillManager, err := skills.NewManager(ctx, cfg.Paths.SkillsDir, cfg.Runtime.Skills, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化 Skill Manager 失败: %w", err)
	}

	mcpStore, err := humbertmcp.NewStore(ctx, cfg.Paths.MCPServersFile)
	if err != nil {
		return nil, fmt.Errorf("初始化 MCP Store 失败: %w", err)
	}
	mcpManager, err := humbertmcp.NewManager(mcpStore, cfg.Runtime.MCP, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化 MCP Manager 失败: %w", err)
	}

	modelStore, err := models.NewStore(
		ctx,
		cfg.Paths.ProvidersFile,
		cfg.Paths.ModelsFile,
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Model Store 失败: %w", err)
	}
	agentStore, err := agents.NewStore(ctx, cfg.Paths.AgentsDir, transcriptStore)
	if err != nil {
		return nil, fmt.Errorf("初始化 Agent Store 失败: %w", err)
	}

	modelRegistry := models.NewRegistry(
		modelStore,
		credentials,
		logger,
		models.WithModelReferenceChecker(agentStore),
	)

	agentService := agents.NewService(
		agentStore,
		modelRegistry,
		logger,
		agents.WithWorkspaceManager(workspaceManager),
		agents.WithSkillCatalog(skillManager),
		agents.WithMCPCatalog(mcpManager),
	)
	if err := agentService.RecoverDeletions(ctx); err != nil {
		logger.Warn(
			ctx,
			"部分 Agent 删除恢复失败，将在下次启动或用户重试时继续",
			"operation", "agent.delete.recover",
			"error", err,
		)
	}
	mcpManager.SetReferenceChecker(agentService)

	mcpRuntimeBackend, err := einoadapter.NewBackend(cfg.Runtime.MCP, permissionEngine, credentials, sandboxManager, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化 MCP Runtime Backend 失败: %w", err)
	}
	mcpManager.SetRuntimeBackend(mcpRuntimeBackend)

	sessionStore, err := sessions.NewStore(ctx, transcriptStore)
	if err != nil {
		return nil, fmt.Errorf("初始化 Session Store 失败: %w", err)
	}
	for _, issue := range sessionStore.Issues() {
		logger.Warn(
			ctx,
			"Session 数据损坏，已隔离且不影响应用启动",
			"operation", "session.recovery.isolate",
			"agent_id", issue.AgentID,
			"session_id", issue.SessionID,
			"error", issue.Error,
		)
	}
	sessionService := sessions.NewService(
		sessionStore,
		agentService,
		workspaceManager,
		logger,
	)

	// 工作区页面与 Agent Runtime 共用同一个 WorkspaceManager。
	// UI 只浏览当前文件系统，不再扫描 Session 推导产物，因此这里不依赖 SessionService。
	workspaceViewService, err := workspaceview.NewService(agentService, workspaceManager)
	if err != nil {
		return nil, fmt.Errorf("初始化 Workspace View Service 失败: %w", err)
	}

	contextArtifactStore, err := contextartifact.NewStore(sessionService)
	if err != nil {
		return nil, fmt.Errorf("初始化 Context Artifact Store 失败: %w", err)
	}

	// ToolRegistry 在 SessionService 之后创建，使内部 history/context_artifact Tool 能读取
	// 当前 Session 的受控完整记录与大结果 sidecar。普通 Builtin 仍保持原有注册语义。
	toolRegistry, err := buildToolRegistry(
		ctx,
		cfg.Paths.ConfigFile,
		workspaceManager,
		sandboxManager,
		permissionEngine,
		skillManager,
		func(callCtx context.Context, agentID string, skillName string) error {
			_, enableErr := agentService.EnableSkillForAgent(callCtx, agentID, skillName)
			return enableErr
		},
		sessionService,
		contextArtifactStore,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 ToolRegistry 失败: %w", err)
	}

	// Context 与 Memory 共用同一个近似 Token Estimator，保证自动刷新阈值、
	// Compaction Planner 和 Composer Usage 使用一致口径。Session Memory 是派生状态，
	// Store 只通过 SessionService 获取受控目录，不自行拼接用户输入路径。
	tokenEstimator := contextengine.NewApproxEstimator()
	memoryStore, err := memory.NewStore(sessionService)
	if err != nil {
		return nil, fmt.Errorf("初始化 Session Memory Store 失败: %w", err)
	}
	memoryManager, err := memory.NewManager(
		cfg.Runtime.Context,
		memoryStore,
		sessionService,
		tokenEstimator,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Session Memory Manager 失败: %w", err)
	}
	contextEngine, err := contextengine.NewEngine(
		cfg.Runtime.Context,
		sessionService,
		tokenEstimator,
		logger,
		memoryManager,
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 ContextEngine 失败: %w", err)
	}

	events := eventbus.New()
	eventsOwned := true
	defer func() {
		if eventsOwned {
			events.Close()
		}
	}()

	runtimeReporter := newRuntimeEventReporter(
		func(eventCtx context.Context, event agentruntime.Event) {
			if eventCtx == nil {
				eventCtx = context.Background()
			}
			if err := events.Publish(eventCtx, agentruntime.TopicEvent, event); err != nil {
				logger.Warn(
					context.Background(),
					"发布 Runtime Event 失败",
					"operation", "runtime.event.publish",
					"request_id", event.RequestID,
					"session_id", event.SessionID,
					"run_id", event.RunID,
					"error", err,
				)
			}
		},
	)

	runtimeResolver := agentruntime.NewResolver(
		agentService,
		sessionService,
		modelRegistry,
		workspaceManager,
		sandboxManager,
		toolRegistry,
		skillManager,
		mcpManager,
		contextEngine,
		memoryManager,
		runtimeReporter,
	)
	if err := runtimeResolver.Validate(); err != nil {
		return nil, fmt.Errorf("RuntimeResolver 配置无效: %w", err)
	}

	runtimeExecutor := agentruntime.NewExecutor()
	runtimeService := agentruntime.NewService(
		runtimeResolver,
		runtimeExecutor,
		sessionService,
		events,
		logger,
		approvalManager,
	)

	taskStore, err := tasks.NewStore(ctx, cfg.Paths.AgentsDir)
	if err != nil {
		return nil, fmt.Errorf("初始化 Task Store 失败: %w", err)
	}
	for _, issue := range taskStore.Issues() {
		logger.Warn(ctx, "Task 数据损坏，已隔离且不影响其它任务", "operation", "task.recovery.isolate", "agent_id", issue.AgentID, "task_id", issue.TaskID, "run_id", issue.RunID, "error", issue.Error)
	}
	taskManager, err := tasks.NewManager(taskStore, agentService, sessionService, runtimeService, events, logger)
	if err != nil {
		return nil, fmt.Errorf("初始化 Task Manager 失败: %w", err)
	}
	notificationService := notifications.New(notifications.NewEventProvider(events))
	taskManager.SetNotificationService(notificationService)
	if err := taskManager.Start(ctx); err != nil {
		return nil, fmt.Errorf("启动 Task Scheduler 失败: %w", err)
	}

	proactiveStore, err := proactive.NewStore(ctx, filepath.Join(cfg.Paths.ConfigDir, "proactive.json"))
	if err != nil {
		_ = taskManager.Close(context.Background())
		return nil, fmt.Errorf("初始化主动助手状态存储失败: %w", err)
	}
	proactiveManager, err := proactive.NewManager(
		proactiveStore,
		taskManager,
		events,
		notificationService,
		proactive.NewWorkspaceMonitor(agentService, workspaceManager),
		logger,
	)
	if err != nil {
		_ = taskManager.Close(context.Background())
		return nil, fmt.Errorf("初始化主动助手 Manager 失败: %w", err)
	}
	if err := proactiveManager.Start(ctx); err != nil {
		_ = taskManager.Close(context.Background())
		return nil, fmt.Errorf("启动主动助手失败: %w", err)
	}

	application := &Application{
		config:        cfg,
		logger:        logger,
		credentials:   credentials,
		events:        events,
		workspaces:    workspaceManager,
		workspaceView: workspaceViewService,
		sandbox:       sandboxManager,
		permissions:   permissionEngine,
		preferences:   preferenceStore,
		approvals:     approvalManager,
		tools:         toolRegistry,
		skills:        skillManager,
		mcp:           mcpManager,
		models:        modelRegistry,
		agents:        agentService,
		sessions:      sessionService,
		contextEngine: contextEngine,
		memory:        memoryManager,
		runtime:       runtimeService,
		tasks:         taskManager,
		notifications: notificationService,
		proactive:     proactiveManager,
		startedAt:     time.Now().UTC(),
	}
	application.ready.Store(true)

	loggerOwned = false
	workspaceOwned = false
	eventsOwned = false

	logger.Info(
		ctx,
		"Humbert Core 初始化完成",
		"operation", "application.bootstrap",
		"config_dir", cfg.Paths.ConfigDir,
		"agents_dir", cfg.Paths.AgentsDir,
		"skills_dir", cfg.Paths.SkillsDir,
		"mcp_servers_file", cfg.Paths.MCPServersFile,
		"managed_workspace_root", managedWorkspaceRoot,
		"model_revision", modelRegistry.Revision(),
		"tool_revision", toolRegistry.Revision(),
		"mcp_revision", mcpManager.Revision(),
	)

	return application, nil
}

// Config 返回应用配置。
func (a *Application) Config() *config.Config {
	return a.config
}

// Logger 返回统一 Logger。
func (a *Application) Logger() *logging.Logger {
	return a.logger
}

// Credentials 返回 Credential Store。
func (a *Application) Credentials() *credential.Store {
	return a.credentials
}

// Events 返回内部 EventBus。
func (a *Application) Events() *eventbus.Bus {
	return a.events
}

// Workspaces 返回 WorkspaceManager。
func (a *Application) Workspaces() *workspace.Manager {
	return a.workspaces
}

// WorkspaceView 返回桌面工作区只读查询服务。
//
// 调用方只能通过它读取当前 Agent Workspace 的受控视图，不能取得任意物理路径读写能力。
func (a *Application) WorkspaceView() *workspaceview.Service {
	return a.workspaceView
}

// Sandbox 返回跨平台 Sandbox Manager。
func (a *Application) Sandbox() *sandbox.Manager {
	return a.sandbox
}

// Permissions 返回 PermissionEngine。Desktop 设置页只通过 Service 使用它管理长期规则；
// Tool Registry 则持有同一个 Engine 作为运行时 Authorizer。
func (a *Application) Permissions() *permission.Engine {
	return a.permissions
}

// Preferences 返回应用级用户资料与界面偏好存储。
func (a *Application) Preferences() *preferences.Store {
	return a.preferences
}

// Approvals 返回当前进程的 Human Approval Manager。普通 Desktop 调用通过 RuntimeService
// ResolveApproval，不应直接修改 Manager 状态。
func (a *Application) Approvals() *approval.Manager {
	return a.approvals
}

// Tools 返回 ToolRegistry。
func (a *Application) Tools() *humberttools.Registry {
	return a.tools
}

// Skills 返回应用级 Skill Manager。Agent 只保存 Skill 名称引用；实际 Package 与 Runtime
// Snapshot 都由该 Manager 管理。
func (a *Application) Skills() *skills.Manager {
	return a.skills
}

// MCP 返回应用级 MCP Manager。Server 配置属于全局控制面，Agent Profile 只保存 Tool 引用。
func (a *Application) MCP() *humbertmcp.Manager {
	return a.mcp
}

// Models 返回 ModelRegistry。
func (a *Application) Models() *models.Registry {
	return a.models
}

// Agents 返回 AgentService。
func (a *Application) Agents() *agents.Service {
	return a.agents
}

// Sessions 返回 SessionService。
func (a *Application) Sessions() *sessions.Service {
	return a.sessions
}

// Runtime 返回 RuntimeService。
func (a *Application) Runtime() *agentruntime.Service {
	return a.runtime
}

// Tasks 返回应用级主动任务管理器。Task 归属 Agent，不引入 Project 聚合。
func (a *Application) Tasks() *tasks.Manager {
	return a.tasks
}

// Proactive 返回主动助手运行时。它负责事件判断、心跳巡检、通知和内部 Agent 自动执行。
func (a *Application) Proactive() *proactive.Manager {
	return a.proactive
}

// Notifications 返回统一通知服务。Desktop Adapter 通过事件订阅消费通知。
func (a *Application) Notifications() *notifications.Service {
	return a.notifications
}

// Status 返回当前 Core 状态，并主动读取文件级 Source of Truth 做健康检查。
func (a *Application) Status(ctx context.Context) (Status, error) {
	status := Status{
		Name:          a.config.App.Name,
		Version:       Version,
		Ready:         a.ready.Load(),
		DataDir:       a.config.Paths.HomeDir,
		ConfigFile:    a.config.Paths.ConfigFile,
		ConfigDir:     a.config.Paths.ConfigDir,
		AgentsDir:     a.config.Paths.AgentsDir,
		ModelRevision: a.models.Revision(),
		ToolRevision:  a.tools.Revision(),
		MCPRevision:   a.mcp.Revision(),
		StartedAt:     a.startedAt,
		Uptime:        time.Since(a.startedAt),
	}

	if !status.Ready {
		return status, nil
	}

	if _, err := a.models.ListProviders(ctx); err != nil {
		return status, fmt.Errorf("Provider 文件存储健康检查失败: %w", err)
	}
	if _, err := a.models.ListModels(ctx); err != nil {
		return status, fmt.Errorf("Model 文件存储健康检查失败: %w", err)
	}
	if _, err := a.agents.List(ctx); err != nil {
		return status, fmt.Errorf("Agent 文件存储健康检查失败: %w", err)
	}
	if _, err := a.skills.List(ctx); err != nil {
		return status, fmt.Errorf("Skill 文件存储健康检查失败: %w", err)
	}
	if _, err := a.mcp.List(ctx); err != nil {
		return status, fmt.Errorf("MCP 文件存储健康检查失败: %w", err)
	}
	if _, err := a.tasks.List(ctx); err != nil {
		return status, fmt.Errorf("Task 文件存储健康检查失败: %w", err)
	}

	status.StorageReady = true
	return status, nil
}

// Shutdown 按逆依赖顺序关闭 Humbert Core。
func (a *Application) Shutdown(ctx context.Context) error {
	a.shutdownOnce.Do(func() {
		started := time.Now()
		a.ready.Store(false)

		a.logger.Info(
			ctx,
			"Humbert Core 开始关闭",
			"operation", "application.shutdown",
		)

		var shutdownErrors []error

		// 主动助手必须先停止产生通知/内部 Agent Run，再关闭 Task Scheduler。
		if a.proactive != nil {
			if err := a.proactive.Close(ctx); err != nil {
				shutdownErrors = append(shutdownErrors, fmt.Errorf("关闭主动助手失败: %w", err))
			}
		}

		// Task Scheduler 必须先停止产生新 Run，再关闭 Runtime。
		if err := a.tasks.Close(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("关闭 Task Manager 失败: %w", err))
		}

		// Runtime 随后关闭。只有所有受控 Agent Turn 都退出后，才能安全关闭
		// Workspace watcher 和 EventBus。文件 Store 没有独立后台资源需要 Close。
		if err := a.runtime.Close(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("关闭 RuntimeService 失败: %w", err))
			a.logger.Error(
				context.Background(),
				"关闭 RuntimeService 失败",
				"operation", "application.shutdown",
				"error", err,
			)
		}

		if err := a.mcp.Close(); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("关闭 MCP Runtime Backend 失败: %w", err))
			a.logger.Error(
				context.Background(),
				"关闭 MCP Runtime Backend 失败",
				"operation", "application.shutdown",
				"error", err,
			)
		}

		if err := a.workspaces.Close(); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("关闭 WorkspaceManager 失败: %w", err))
		}

		a.events.Close()

		a.logger.Info(
			context.Background(),
			"Humbert Core 已关闭",
			"operation", "application.shutdown",
			"duration_ms", time.Since(started).Milliseconds(),
		)

		if err := a.logger.Close(); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("关闭 Logger 失败: %w", err))
		}

		a.shutdownErr = errors.Join(shutdownErrors...)
	})

	return a.shutdownErr
}
