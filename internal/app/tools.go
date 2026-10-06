package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/collaboration"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
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
	factories := []func(*collaboration.Manager) (humberttools.Factory, error){builtin.NewListAgentsFactory, builtin.NewRunAgentFactory}
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

// toolDependencies 是默认工具组合需要的显式依赖；不允许通过 Application 查找服务。
// 每个 Factory 仍只接收自己的依赖。BrowserVision 是跨模型/Agent 的观察用例，由装配层注入。
type toolDependencies struct {
	Workspaces  *workspace.Manager
	Sandbox     *sandbox.Manager
	Skills      *skills.Manager
	EnableSkill builtin.AgentSkillEnableFunc
	// 同一个会话服务拥有历史、附件和结果归档，避免装配两份平行来源。
	Sessions           *sessions.Service
	BrowserProfileRoot string
	BrowserVision      builtin.BrowserVisionInspector
}

// buildToolRegistry 是默认工具唯一的装配位置；业务实现仍留在各个 Eino Factory 中。
// Registry 保存 Factory，每轮创建隔离的 Eino 工具实例，权限统一使用注入的 Authorizer。
func buildToolRegistry(ctx context.Context, configFile string, deps toolDependencies, authorizer humberttools.Authorizer, logger *logging.Logger) (_ *humberttools.Registry, resultErr error) {
	if ctx == nil || logger == nil || deps.Sessions == nil {
		return nil, fmt.Errorf("ToolRegistry 的 Context/Logger/Sessions 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, err := config.LoadToolConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("加载 Tool 配置失败: %w", err)
	}
	registry, err := humberttools.NewRegistry(authorizer, deps.Sessions)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, registry.Close())
		}
	}()
	register := func(factory humberttools.Factory, err error) error {
		if err != nil {
			return err
		}
		if err := registry.Register(factory); err != nil {
			// 注册被拒绝的工厂尚未转移所有权，立即释放它，避免遗留连接。
			if closer, ok := factory.(io.Closer); ok {
				err = errors.Join(err, closer.Close())
			}
			return fmt.Errorf("注册默认 Tool 失败: %w", err)
		}
		return nil
	}
	// 历史恢复与大结果读取属于上下文可靠性能力，保持现有 Internal 选择语义。
	if err := register(builtin.NewSessionHistoryFactory(deps.Sessions)); err != nil {
		return nil, err
	}
	if err := register(builtin.NewContextResourceFactory(deps.Sessions, deps.Sessions)); err != nil {
		return nil, err
	}
	reader := deps.Sessions
	if err := register(builtin.NewExtractDocumentFactory(deps.Sessions, reader, cfg.Files.Enabled)); err != nil {
		return nil, err
	}
	if err := register(builtin.NewInstallSkillFactory(deps.Skills, deps.EnableSkill)); err != nil {
		return nil, err
	}
	if cfg.Files.Enabled {
		limits := builtin.FileLimits{MaxReadableFileBytes: cfg.Files.MaxReadableFileBytes, MaxReadLines: cfg.Files.MaxReadLines, MaxListEntries: cfg.Files.MaxListEntries}
		factories, err := builtin.NewFilesystemFactories(limits, cfg.Files.MaxWritableFileBytes)
		if err != nil {
			return nil, err
		}
		for _, factory := range append(factories, builtin.NewDeleteFileFactory()) {
			if err := register(factory, nil); err != nil {
				return nil, err
			}
		}
		if err := register(builtin.NewCopyFileFactory(cfg.Files.MaxWritableFileBytes, reader)); err != nil {
			return nil, err
		}
		if err := register(builtin.NewMoveFileFactory(cfg.Files.MaxWritableFileBytes)); err != nil {
			return nil, err
		}
		if err := register(builtin.NewApplyPatchFactory(cfg.Files.MaxWritableFileBytes)); err != nil {
			return nil, err
		}
	}
	if deps.BrowserProfileRoot == "" {
		return nil, errors.New("浏览器 Profile 目录不能为空")
	}
	browser := builtin.NewBrowserFactory(filepath.Clean(deps.BrowserProfileRoot), deps.BrowserVision)
	browser.SetAttachmentWriter(deps.Sessions)
	if err := register(browser, nil); err != nil {
		return nil, err
	}
	if cfg.WebSearch.Enabled {
		if err := register(builtin.NewWebSearchFactory(cfg.WebSearch.Provider, time.Duration(cfg.WebSearch.TimeoutSeconds)*time.Second,
			cfg.WebSearch.DefaultResults, cfg.WebSearch.MaxResults)); err != nil {
			return nil, err
		}
	}
	if cfg.WebFetch.Enabled {
		if err := register(builtin.NewWebFetchFactory(time.Duration(cfg.WebFetch.TimeoutSeconds)*time.Second, cfg.WebFetch.MaxBodyBytes,
			cfg.WebFetch.DefaultMaxChars, cfg.WebFetch.MaxChars, cfg.WebFetch.MaxRedirects)); err != nil {
			return nil, err
		}
	}
	if cfg.Command.Enabled {
		limits := builtin.CommandLimits{DefaultTimeout: time.Duration(cfg.Command.DefaultTimeoutSeconds) * time.Second,
			MaxTimeout: time.Duration(cfg.Command.MaxTimeoutSeconds) * time.Second, MaxOutputBytes: cfg.Command.MaxOutputBytes,
			MaxArgs: cfg.Command.MaxArgs, MaxArgBytes: cfg.Command.MaxArgBytes}
		environment := config.SafeCommandEnvironment()
		if err := register(builtin.NewRunCommandFactory(deps.Workspaces, deps.Sandbox.Runner(), limits, environment)); err != nil {
			return nil, err
		}
		// Skill 脚本仍经受控工具执行，Stage 与身份检查由原 Factory 保留。
		if err := register(builtin.NewRunSkillScriptFactory(deps.Skills, deps.Workspaces, deps.Sandbox.Runner(), limits, environment)); err != nil {
			return nil, err
		}
		gitBuilders := []func(*sandbox.Runner, []string, int) (humberttools.Factory, error){builtin.NewGitStatusFactory, builtin.NewGitDiffFactory, builtin.NewGitLogFactory}
		for _, build := range gitBuilders {
			if err := register(build(deps.Sandbox.Runner(), environment, cfg.Command.MaxOutputBytes)); err != nil {
				return nil, err
			}
		}
	}
	if err := register(builtin.NewCurrentTimeFactory(), nil); err != nil {
		return nil, err
	}
	if err := register(builtin.NewUpdatePlanFactory(), nil); err != nil {
		return nil, err
	}
	descriptors := registry.List()
	names := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		names = append(names, descriptor.Name)
	}
	logger.Info(ctx, "ToolRegistry 已初始化", "operation", "tool_registry.initialize", "tool_revision", registry.Revision(),
		"tool_count", len(names), "tools", names, "local_exec_enabled", cfg.Command.Enabled)
	return registry, nil
}
