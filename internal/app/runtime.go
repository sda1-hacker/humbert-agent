package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/sda1-hacker/humbert-agent/internal/collaboration"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/usecases"
)

// initRuntime 组装工具、上下文与执行器，成功构造的资源立即登记到同一清理清单。
func (a *Application) initRuntime(ctx context.Context) (*agentruntime.Resolver, error) {
	var err error
	vision := usecases.NewVisionInspector(a.agents, a.models)
	toolDeps := toolDependencies{
		Sandbox: a.sandbox, Skills: a.skills,
		EnableSkill: func(callCtx context.Context, agentID, skillName string) error {
			_, err := a.agents.EnableSkillForAgent(callCtx, agentID, skillName)
			return err
		},
		Sessions: a.sessions,
		// Profile 位于 Cache，不把站点登录信息放进业务数据备份。
		BrowserProfileRoot: filepath.Join(filepath.Dir(a.config.Paths.ConfigFile), "cache", "browser-profiles"),
		BrowserVision:      vision.Inspect,
		ComputerVision:     vision.InspectComputer,
	}
	a.tools, err = buildToolRegistry(ctx, a.config.Paths.ConfigFile, toolDeps, a.permissions, a.logger)
	if err != nil {
		return nil, fmt.Errorf("初始化 ToolRegistry 失败: %w", err)
	}
	a.resources.add(closeResources, "Tools", func(context.Context) error { return a.tools.Close() })
	// 上下文摘要是唯一自动派生记忆；个人记忆由用户明确保存。
	tokenEstimator := contextengine.NewApproxEstimator()
	a.contextEngine, err = contextengine.NewEngine(a.config.Runtime.Context, a.sessions, tokenEstimator, a.logger)
	if err != nil {
		return nil, fmt.Errorf("初始化 ContextEngine 失败: %w", err)
	}
	a.events = eventbus.New()
	a.resources.add(closeResources, "EventBus", func(context.Context) error { a.events.Close(); return nil })
	runtimeReporter := newRuntimeEventReporter(
		func(eventCtx context.Context, event agentruntime.Event) {
			if eventCtx == nil {
				eventCtx = context.Background()
			}
			if err := a.events.Publish(eventCtx, agentruntime.TopicEvent, event); err != nil {
				a.logger.Warn(context.Background(), "发布 Runtime Event 失败", "operation", "runtime.event.publish", "request_id", event.RequestID, "session_id", event.SessionID, "run_id", event.RunID, "error", err)
			}
		},
	)
	runtimeResolver := agentruntime.NewResolver(a.agents, a.sessions, a.models, a.workspaces, a.sandbox, a.tools,
		a.skills, a.mcp, a.contextEngine, a.preferences, runtimeReporter, a.config.Runtime.MaxIterations)
	if err := runtimeResolver.Validate(); err != nil {
		return nil, fmt.Errorf("RuntimeResolver 配置无效: %w", err)
	}
	runtimeExecutor := agentruntime.NewExecutor()
	collaborationStore, err := collaboration.NewStore(a.sessions)
	if err != nil {
		return nil, fmt.Errorf("初始化 Collaboration Store 失败: %w", err)
	}
	a.collaboration, err = collaboration.NewManager(a.agents, collaborationStore, runtimeResolver)
	if err != nil {
		return nil, fmt.Errorf("初始化 Collaboration Manager 失败: %w", err)
	}
	if err := registerCollaborationTools(a.tools, a.collaboration); err != nil {
		return nil, err
	}
	a.runtime = agentruntime.NewService(runtimeResolver, runtimeExecutor, a.sessions, a.events, a.logger, a.approvals)
	a.resources.add(finishRuns, "Runtime", a.runtime.Close)
	a.runtime.AddRunLifecycleObserver(a.collaboration)
	a.runtime.AddRunLifecycleObserver(a.mcp)
	return runtimeResolver, nil
}
