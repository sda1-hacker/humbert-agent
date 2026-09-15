package projects

import (
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// Project 表示用户工作的长期上下文。Workspace 属于 Project，而不是 Agent。
// Agent 负责人格、模型和能力；Session 负责一次会话；Project 负责工作目录与项目身份。
type Project struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	AgentID       string         `json:"agent_id"`
	WorkspaceMode workspace.Mode `json:"workspace_mode"`
	WorkspacePath string         `json:"workspace_path,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type CreateInput struct {
	ID            string
	Name          string
	AgentID       string
	WorkspaceMode workspace.Mode
	WorkspacePath string
}

type UpdateInput struct {
	Name          string
	WorkspaceMode workspace.Mode
	WorkspacePath string
}
