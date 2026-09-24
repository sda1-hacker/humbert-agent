package app

import (
	"context"
	"fmt"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/collaboration"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/contextartifact"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	builtin "github.com/sda1-hacker/humbert-agent/internal/tools/builtin"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// registerCollaborationTools 暴露同步 Agent-as-Tool 能力。Factory 持有应用级 Manager；
// 每个 Turn 仍由 Registry.Build 创建隔离实例并冻结父 Runtime Scope。
func registerCollaborationTools(registry *humberttools.Registry, manager *collaboration.Manager) error {
	if registry == nil || manager == nil {
		return fmt.Errorf("注册协作工具失败: 依赖不完整")
	}
	factories := []func(*collaboration.Manager) (humberttools.Factory, error){
		builtin.NewListAgentsFactory,
		builtin.NewRunAgentFactory,
	}
	for _, build := range factories {
		factory, err := build(manager)
		if err != nil {
			return err
		}
		if err := registry.Register(factory); err != nil {
			return fmt.Errorf("注册 Tool %q 失败: %w", factory.Descriptor().Name, err)
		}
	}
	return nil
}

// buildToolRegistry 构建 Application 生命周期内唯一 ToolRegistry。
//
// Registry 保存的是 Factory，而不是某一次 Turn 的 Tool Instance。RuntimeResolver
// 每次创建 Snapshot 时会调用 Registry.Resolve，并把当时的 Tool Revision 与
// Workspace Scope 一起冻结。
//
// 因此：
//
//	Registry revision N
//	       ↓
//	Turn A RuntimeSnapshot
//	       ↓
//	Build 独立 Tool Instances
//	       ↓
//	Turn A 始终使用 revision N
//
// 后续 Registry 即使增加 Skill/MCP/Plugin Tool，也不会改变已经运行中的 Turn。
func buildToolRegistry(
	ctx context.Context,
	configFile string,
	workspaceManager *workspace.Manager,
	sandboxManager *sandbox.Manager,
	authorizer humberttools.Authorizer,
	skillManager *skills.Manager,
	agentSkillEnable builtin.AgentSkillEnableFunc,
	historyRepository builtin.HistoryRepository,
	artifactStore *contextartifact.Store,
	logger *logging.Logger,
) (*humberttools.Registry, error) {
	if ctx == nil {
		return nil, fmt.Errorf(
			"初始化 ToolRegistry 失败: context.Context 不能为空",
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"初始化 ToolRegistry 被取消: %w",
			err,
		)
	}

	if workspaceManager == nil {
		return nil, fmt.Errorf(
			"初始化 ToolRegistry 失败: WorkspaceManager 不能为空",
		)
	}

	if sandboxManager == nil {
		return nil, fmt.Errorf("初始化 ToolRegistry 失败: SandboxManager 不能为空")
	}

	if authorizer == nil {
		return nil, fmt.Errorf("初始化 ToolRegistry 失败: Authorizer 不能为空")
	}

	if skillManager == nil {
		return nil, fmt.Errorf("初始化 ToolRegistry 失败: SkillManager 不能为空")
	}

	if agentSkillEnable == nil {
		return nil, fmt.Errorf("初始化 ToolRegistry 失败: AgentSkillEnableFunc 不能为空")
	}

	if historyRepository == nil {
		return nil, fmt.Errorf("初始化 ToolRegistry 失败: HistoryRepository 不能为空")
	}
	if artifactStore == nil {
		return nil, fmt.Errorf("初始化 ToolRegistry 失败: ContextArtifactStore 不能为空")
	}

	if logger == nil {
		return nil, fmt.Errorf(
			"初始化 ToolRegistry 失败: Logger 不能为空",
		)
	}

	toolConfig, err :=
		config.LoadToolConfig(
			configFile,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"加载 Tool 配置失败: %w",
			err,
		)
	}

	// Tool Registry 不再自行推导权限。Capability 是否存在仍由 ToolConfig 决定，
	// 每次调用的 Allow/Deny/Ask 则统一委托给 Application 注入的 PermissionEngine。

	registry, err :=
		humberttools.NewRegistry(
			authorizer,
			artifactStore,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建 ToolRegistry 失败: %w",
			err,
		)
	}

	register :=
		func(
			factory humberttools.Factory,
		) error {
			if err :=
				registry.Register(
					factory,
				); err != nil {
				return fmt.Errorf(
					"注册 Tool %q 失败: %w",
					factory.Descriptor().Name,
					err,
				)
			}

			return nil
		}
	registerBuilt := func(name string, factory humberttools.Factory, err error) error {
		if err != nil {
			return fmt.Errorf("创建 %s Factory 失败: %w", name, err)
		}
		return register(factory)
	}

	// 上下文恢复能力属于 Humbert 的运行时可靠性基础设施，始终随 Runtime 提供，
	// 不受 Agent 的 Builtin 选择开关影响。这里刻意只暴露两个内部只读 Tool：
	// session_history 负责搜索/读取旧会话，context_resource 负责读取被移出工作窗口的
	// 超大工具结果和历史文本附件。这样既保留按需恢复能力，又减少模型侧 Tool Schema。
	sessionHistoryFactory, err := builtin.NewSessionHistoryFactory(historyRepository)
	if err != nil {
		return nil, fmt.Errorf("创建 session_history Factory 失败: %w", err)
	}
	if err := register(sessionHistoryFactory); err != nil {
		return nil, err
	}
	contextResourceFactory, err := builtin.NewContextResourceFactory(artifactStore, historyRepository)
	if err != nil {
		return nil, fmt.Errorf("创建 context_resource Factory 失败: %w", err)
	}
	if err := register(contextResourceFactory); err != nil {
		return nil, err
	}
	attachmentReader, ok := historyRepository.(builtin.DocumentAttachmentReader)
	if !ok {
		return nil, fmt.Errorf("创建 extract_document Factory 失败: Session 不支持读取附件")
	}
	extractDocumentFactory, err := builtin.NewExtractDocumentFactory(historyRepository, attachmentReader, toolConfig.Files.Enabled)
	if err != nil {
		return nil, fmt.Errorf("创建 extract_document Factory 失败: %w", err)
	}
	if err := register(extractDocumentFactory); err != nil {
		return nil, err
	}

	// install_skill 是 Skills 控制面的唯一 Agent 可写入口。它只负责下载安装并可选择修改
	// 当前 Agent 的 enabled_skills，不会执行包内 scripts。RiskWrite 让默认 Permission Policy
	// 在真正产生副作用前要求用户审批。
	installSkillFactory, err :=
		builtin.NewInstallSkillFactory(
			skillManager,
			agentSkillEnable,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建 install_skill Factory 失败: %w",
			err,
		)
	}
	if err := register(installSkillFactory); err != nil {
		return nil, err
	}

	if toolConfig.Files.Enabled {
		readLimits :=
			builtin.FileLimits{
				MaxReadableFileBytes: toolConfig.Files.MaxReadableFileBytes,
				MaxReadOutputBytes:   toolConfig.Files.MaxReadOutputBytes,
				DefaultReadLines:     toolConfig.Files.DefaultReadLines,
				MaxReadLines:         toolConfig.Files.MaxReadLines,
				MaxListEntries:       toolConfig.Files.MaxListEntries,
			}

		// 构造失败与注册失败都在这里统一带上工具名，避免为每个文件工具重复装配代码。
		listFilesFactory, err := builtin.NewListFilesFactory(workspaceManager, readLimits)
		if err := registerBuilt("list_files", listFilesFactory, err); err != nil {
			return nil, err
		}
		readFileFactory, err := builtin.NewReadFileFactory(workspaceManager, readLimits)
		if err := registerBuilt("read_file", readFileFactory, err); err != nil {
			return nil, err
		}
		writeFileFactory, err := builtin.NewWriteFileFactory(workspaceManager, toolConfig.Files.MaxWritableFileBytes)
		if err := registerBuilt("write_file", writeFileFactory, err); err != nil {
			return nil, err
		}
		editFileFactory, err := builtin.NewEditFileFactory(workspaceManager, toolConfig.Files.MaxWritableFileBytes, toolConfig.Files.MaxReadOutputBytes)
		if err := registerBuilt("edit_file", editFileFactory, err); err != nil {
			return nil, err
		}

		for _, extraFactory := range []humberttools.Factory{
			builtin.NewGlobFilesFactory(),
			builtin.NewGrepFilesFactory(),
			builtin.NewDeleteFileFactory(),
		} {
			if err := register(extraFactory); err != nil {
				return nil, err
			}
		}

		copyFileFactory, err := builtin.NewCopyFileFactory(toolConfig.Files.MaxWritableFileBytes)
		if err := registerBuilt("copy_file", copyFileFactory, err); err != nil {
			return nil, err
		}
		moveFileFactory, err := builtin.NewMoveFileFactory(toolConfig.Files.MaxWritableFileBytes)
		if err := registerBuilt("move_file", moveFileFactory, err); err != nil {
			return nil, err
		}
		applyPatchFactory, err := builtin.NewApplyPatchFactory(toolConfig.Files.MaxWritableFileBytes)
		if err := registerBuilt("apply_patch", applyPatchFactory, err); err != nil {
			return nil, err
		}
	}
	if err := register(builtin.NewBrowserFactory()); err != nil {
		return nil, err
	}

	if toolConfig.WebSearch.Enabled {
		webSearchFactory, err :=
			builtin.NewWebSearchFactory(
				toolConfig.WebSearch.Provider,
				time.Duration(
					toolConfig.WebSearch.TimeoutSeconds,
				)*time.Second,
				toolConfig.WebSearch.DefaultResults,
				toolConfig.WebSearch.MaxResults,
				builtin.WebSearchCredentials{},
			)
		if err != nil {
			return nil, fmt.Errorf(
				"创建 web_search Factory 失败: %w",
				err,
			)
		}

		if err :=
			register(
				webSearchFactory,
			); err != nil {
			return nil, err
		}
	}

	if toolConfig.WebFetch.Enabled {
		webFetchFactory, err :=
			builtin.NewWebFetchFactory(
				time.Duration(
					toolConfig.WebFetch.TimeoutSeconds,
				)*time.Second,
				toolConfig.WebFetch.MaxBodyBytes,
				toolConfig.WebFetch.DefaultMaxChars,
				toolConfig.WebFetch.MaxChars,
				toolConfig.WebFetch.MaxRedirects,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"创建 web_fetch Factory 失败: %w",
				err,
			)
		}

		if err :=
			register(
				webFetchFactory,
			); err != nil {
			return nil, err
		}
	}

	if toolConfig.Command.Enabled {
		commandLimits := builtin.CommandLimits{
			DefaultTimeout: time.Duration(
				toolConfig.Command.DefaultTimeoutSeconds,
			) * time.Second,
			MaxTimeout: time.Duration(
				toolConfig.Command.MaxTimeoutSeconds,
			) * time.Second,
			MaxOutputBytes: toolConfig.Command.MaxOutputBytes,
			MaxArgs:        toolConfig.Command.MaxArgs,
			MaxArgBytes:    toolConfig.Command.MaxArgBytes,
		}
		safeEnvironment := config.SafeCommandEnvironment()
		runCommandFactory, err :=
			builtin.NewRunCommandFactory(
				workspaceManager,
				sandboxManager.Runner(),
				toolConfig.Command.AllowedCommands,
				commandLimits,
				safeEnvironment,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"创建 run_command Factory 失败: %w",
				err,
			)
		}

		if err :=
			register(
				runCommandFactory,
			); err != nil {
			return nil, err
		}

		// 标准 Agent Skills 允许 scripts/。Humbert 不把安装目录直接开放给 Shell，
		// 而是通过独立的 RiskExec Tool 将当前 Turn 冻结的 Skill Stage 到 Workspace 后执行。
		runSkillScriptFactory, err := builtin.NewRunSkillScriptFactory(
			skillManager,
			workspaceManager,
			sandboxManager.Runner(),
			toolConfig.Command.AllowedCommands,
			commandLimits,
			safeEnvironment,
		)
		if err != nil {
			return nil, fmt.Errorf("创建 run_skill_script Factory 失败: %w", err)
		}
		if err := register(runSkillScriptFactory); err != nil {
			return nil, err
		}

		gitFactories := []func(*sandbox.Runner, []string, int) (humberttools.Factory, error){
			builtin.NewGitStatusFactory, builtin.NewGitDiffFactory, builtin.NewGitLogFactory,
		}
		for _, makeGitFactory := range gitFactories {
			gitFactory, gitErr := makeGitFactory(sandboxManager.Runner(), safeEnvironment, toolConfig.Command.MaxOutputBytes)
			if gitErr != nil {
				return nil, fmt.Errorf("创建 Git Factory 失败: %w", gitErr)
			}
			if err := register(gitFactory); err != nil {
				return nil, err
			}
		}
	}

	if err := register(builtin.NewCurrentTimeFactory()); err != nil {
		return nil, err
	}
	if err := register(builtin.NewUpdatePlanFactory()); err != nil {
		return nil, err
	}

	descriptors :=
		registry.List()

	names :=
		make(
			[]string,
			0,
			len(descriptors),
		)

	for _, descriptor := range descriptors {
		names =
			append(
				names,
				descriptor.Name,
			)
	}

	logger.Info(
		ctx,
		"ToolRegistry 已初始化",
		"operation",
		"tool_registry.initialize",
		"tool_revision",
		registry.Revision(),
		"tool_count",
		len(names),
		"tools",
		names,
		"local_exec_enabled",
		toolConfig.Command.Enabled,
	)

	return registry, nil
}
