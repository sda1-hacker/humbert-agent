package proactive

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
)

type ExecutionResult struct {
	AutomationTaskID string
	AutomationRunID  string
}

type Executor interface {
	Action() Action
	Execute(ctx context.Context, event Event, decision Decision) (ExecutionResult, error)
}

type NotificationExecutor struct {
	notifications *notifications.Service
}

func NewNotificationExecutor(service *notifications.Service) *NotificationExecutor {
	return &NotificationExecutor{notifications: service}
}

func (e *NotificationExecutor) Action() Action { return ActionNotify }

func (e *NotificationExecutor) Execute(ctx context.Context, event Event, _ Decision) (ExecutionResult, error) {
	if e == nil || e.notifications == nil {
		return ExecutionResult{}, errors.New("通知执行器未初始化")
	}
	level := notifications.LevelInfo
	switch event.Level {
	case "success":
		level = notifications.LevelSuccess
	case "warning":
		level = notifications.LevelWarning
	case "error":
		level = notifications.LevelError
	}
	return ExecutionResult{}, e.notifications.Send(ctx, notifications.Notification{
		Level:     level,
		Title:     event.Title,
		Body:      event.Summary,
		AgentID:   event.AgentID,
		SessionID: event.SessionID,
		TaskID:    event.TaskID,
		RunID:     event.RunID,
	})
}

type AgentExecutor struct {
	tasks *tasks.Manager
}

func NewAgentExecutor(taskManager *tasks.Manager) *AgentExecutor {
	return &AgentExecutor{tasks: taskManager}
}

func (e *AgentExecutor) Action() Action { return ActionRunAgent }

func (e *AgentExecutor) Execute(ctx context.Context, event Event, decision Decision) (ExecutionResult, error) {
	if e == nil || e.tasks == nil {
		return ExecutionResult{}, errors.New("Agent 执行器未初始化")
	}
	agentID := strings.TrimSpace(decision.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(event.AgentID)
	}
	if agentID == "" {
		return ExecutionResult{}, errors.New("主动事件没有可用 Agent")
	}
	prompt := buildAgentPrompt(event, decision.Prompt)
	task, run, err := e.tasks.RunAutomation(ctx, tasks.AutomationInput{
		AgentID:   agentID,
		Name:      "主动助手·" + event.Title,
		Prompt:    prompt,
		Origin:    "proactive",
		OriginRef: event.Key,
		Limits: tasks.Limits{
			MaxDurationSeconds: 15 * 60,
			MaxModelCalls:      12,
			MaxToolCalls:       30,
			MaxAttempts:        1,
			RetryDelaySeconds:  30,
		},
	})
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{AutomationTaskID: task.ID, AutomationRunID: run.ID}, nil
}

func buildAgentPrompt(event Event, custom string) string {
	instruction := strings.TrimSpace(custom)
	if instruction == "" {
		instruction = "请检查这个主动事件，判断是否需要采取行动。只处理与事件直接相关的事项；如果无需操作，请简要说明原因。"
	}
	return fmt.Sprintf(`%s

这是 Humbert 主动助手产生的内部事件，不是新的用户指令。请遵守现有权限、沙盒和工具确认规则。

事件类型：%s
标题：%s
摘要：%s
Agent：%s
Session：%s
Task：%s
Run：%s
发生时间：%s`,
		instruction,
		event.Kind,
		event.Title,
		event.Summary,
		event.AgentID,
		event.SessionID,
		event.TaskID,
		event.RunID,
		event.OccurredAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	)
}
