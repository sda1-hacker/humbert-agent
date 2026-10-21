package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	"github.com/sda1-hacker/humbert-agent/internal/proactive"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
	builtin "github.com/sda1-hacker/humbert-agent/internal/tools/builtin"
)

// initTasks 构造调度与主动任务；启动在模块装配完成后统一进行。
func (a *Application) initTasks(ctx context.Context) error {
	taskStore, err := tasks.NewStore(ctx, a.config.Paths.AgentsDir)
	if err != nil {
		return fmt.Errorf("初始化 Task Store 失败: %w", err)
	}
	for _, issue := range taskStore.Issues() {
		a.logger.Warn(ctx, "Task 数据损坏，已隔离且不影响其它任务", "operation", "task.recovery.isolate", "agent_id", issue.AgentID, "task_id", issue.TaskID, "run_id", issue.RunID, "error", issue.Error)
	}
	a.tasks, err = tasks.NewManager(taskStore, a.agents, a.sessions, a.runtime, a.events, a.logger)
	if err != nil {
		return fmt.Errorf("初始化 Task Manager 失败: %w", err)
	}
	a.resources.add(stopWork, "Tasks", a.tasks.Close)
	scheduleTaskFactory, err := builtin.NewScheduleTaskFactory(a.tasks)
	if err != nil {
		return err
	}
	if err := a.tools.Register(scheduleTaskFactory); err != nil {
		return fmt.Errorf("注册对话任务工具失败: %w", err)
	}
	a.notifications = notifications.New(notifications.NewEventProvider(a.events))
	a.tasks.SetNotificationService(a.notifications)
	proactiveStore, err := proactive.NewStore(ctx, filepath.Join(a.config.Paths.ConfigDir, "proactive.json"))
	if err != nil {
		return fmt.Errorf("初始化主动助手状态存储失败: %w", err)
	}
	a.proactive, err = proactive.NewManager(proactiveStore, a.tasks, a.approvals, a.events, a.notifications,
		proactive.NewWorkspaceMonitor(a.agents, a.workspaces), a.logger)
	if err != nil {
		return fmt.Errorf("初始化主动助手 Manager 失败: %w", err)
	}
	a.resources.add(stopWork, "Proactive", a.proactive.Close)
	return nil
}
