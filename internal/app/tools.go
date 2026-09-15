package app

import (
	"context"
	"fmt"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	builtin "github.com/sda1-hacker/humbert-agent/internal/tools/builtin"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

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

		listFilesFactory, err :=
			builtin.NewListFilesFactory(
				workspaceManager,
				readLimits,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"创建 list_files Factory 失败: %w",
				err,
			)
		}

		if err :=
			register(
				listFilesFactory,
			); err != nil {
			return nil, err
		}

		readFileFactory, err :=
			builtin.NewReadFileFactory(
				workspaceManager,
				readLimits,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"创建 read_file Factory 失败: %w",
				err,
			)
		}

		if err :=
			register(
				readFileFactory,
			); err != nil {
			return nil, err
		}

		writeFileFactory, err :=
			builtin.NewWriteFileFactory(
				workspaceManager,
				toolConfig.Files.MaxWritableFileBytes,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"创建 write_file Factory 失败: %w",
				err,
			)
		}

		if err :=
			register(
				writeFileFactory,
			); err != nil {
			return nil, err
		}

		editFileFactory, err :=
			builtin.NewEditFileFactory(
				workspaceManager,
				toolConfig.Files.MaxWritableFileBytes,
				toolConfig.Files.MaxReadOutputBytes,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"创建 edit_file Factory 失败: %w",
				err,
			)
		}

		if err :=
			register(
				editFileFactory,
			); err != nil {
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
		if err != nil {
			return nil, fmt.Errorf("创建 copy_file Factory 失败: %w", err)
		}
		if err := register(copyFileFactory); err != nil {
			return nil, err
		}
		moveFileFactory, err := builtin.NewMoveFileFactory(toolConfig.Files.MaxWritableFileBytes)
		if err != nil {
			return nil, fmt.Errorf("创建 move_file Factory 失败: %w", err)
		}
		if err := register(moveFileFactory); err != nil {
			return nil, err
		}
		applyPatchFactory, err := builtin.NewApplyPatchFactory(toolConfig.Files.MaxWritableFileBytes)
		if err != nil {
			return nil, fmt.Errorf("创建 apply_patch Factory 失败: %w", err)
		}
		if err := register(applyPatchFactory); err != nil {
			return nil, err
		}
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
