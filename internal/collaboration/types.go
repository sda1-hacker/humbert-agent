package collaboration

import (
	"context"
	"time"

	"github.com/cloudwego/eino/adk"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	ListAgentsToolName = "list_agents"
	RunAgentToolName   = "run_agent"
)

type Status string

const (
	StatusRunning         Status = "running"
	StatusWaitingApproval Status = "waiting_approval"
	StatusSucceeded       Status = "succeeded"
	StatusFailed          Status = "failed"
	StatusInterrupted     Status = "interrupted"
)

// Run 是一次子 Agent 调用的轻量审计记录。它不是 Session，也不保存子 Agent 的逐步
// 对话；父 Session 中的 ToolCall/ToolResult 才是用户对话的持久化事实来源。
type Run struct {
	SchemaVersion int `json:"schema_version"`

	ID              string `json:"id"`
	ParentAgentID   string `json:"parent_agent_id"`
	ParentSessionID string `json:"parent_session_id"`
	ParentRequestID string `json:"parent_request_id"`
	ParentRunID     string `json:"parent_run_id"`
	ToolCallID      string `json:"tool_call_id"`

	ChildAgentID   string `json:"child_agent_id"`
	ChildAgentName string `json:"child_agent_name"`
	Task           string `json:"task"`
	Status         Status `json:"status"`
	Result         string `json:"result,omitempty"`
	Error          string `json:"error,omitempty"`

	StartedAt  time.Time  `json:"started_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type AgentSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type BuildAgentInput struct {
	ChildAgentID string
	ParentScope  humberttools.Scope
}

type BuiltAgent struct {
	Agent     adk.Agent
	AgentID   string
	AgentName string
}

// AgentBuilder 由 Runtime Resolver 实现。协作层只负责编排 Eino AgentTool，不复制
// Model、Skill、MCP、Sandbox 的解析逻辑。
type AgentBuilder interface {
	BuildChildAgent(ctx context.Context, input BuildAgentInput) (BuiltAgent, error)
}

type SessionDirectoryResolver interface {
	SessionDirectory(ctx context.Context, sessionID string) (string, error)
}

type RunInput struct {
	ChildAgentID string
	Task         string
	ParentScope  humberttools.Scope
}

type RunOutput struct {
	RunID          string `json:"run_id"`
	ChildAgentID   string `json:"child_agent_id"`
	ChildAgentName string `json:"child_agent_name"`
	Status         Status `json:"status"`
	Result         string `json:"result"`
}
