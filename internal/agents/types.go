package agents

import (
	"time"

	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// Agent 是 Humbert Agent 的稳定 Profile。
//
// Agent 本身只是持久化配置实体，不代表一个长期驻留的 Eino Runtime。
//
// Runtime 会在每个 User Turn 开始时读取 Agent Profile，
// 构造不可变 Runtime Snapshot，然后在 Turn 完成后释放。
type Agent struct {
	ID string `json:"id"`

	Name string `json:"name"`

	Instruction string `json:"instruction"`

	ModelID string `json:"model_id,omitempty"`

	// EnabledSkills 保存这个 Agent 明确启用的 Skill 名称。Skill 包本身位于应用级
	// ~/.humbert-agent/skills；Profile 只保存引用，不复制 Skill 内容。
	EnabledSkills []string `json:"enabled_skills,omitempty"`

	// EnabledMCPTools 保存这个 Agent 明确启用的 MCP Server/raw Tool 选择。
	// Server 配置位于应用级 mcp/servers.json；Agent Profile 只保存引用。
	EnabledMCPTools []humbertmcp.ToolSelection `json:"enabled_mcp_tools,omitempty"`

	// EnabledBuiltinTools 保存 Agent 显式启用的内置 Tool。nil 表示升级前 Profile，
	// Runtime 使用 Registry 默认集合；非 nil（包括空 slice）表示用户已经明确选择。
	EnabledBuiltinTools []string `json:"enabled_builtin_tools"`

	// Sandbox 保存 Agent 的文件、进程与网络边界。零值表示继承应用级默认安全策略。
	Sandbox sandbox.AgentPolicy `json:"sandbox,omitempty"`

	// WorkspaceMode/WorkspacePath 是 v1 Profile 的迁移兼容字段。
	// 新 Runtime 不再读取它们；Workspace 的权威配置属于 projects.Project。
	// 在所有旧 Agent 都完成 Project 迁移后的未来 schema 版本中可删除。
	WorkspaceMode workspace.Mode `json:"workspace_mode"`
	WorkspacePath string         `json:"workspace_path,omitempty"`

	CreatedAt time.Time `json:"created_at"`

	UpdatedAt time.Time `json:"updated_at"`
}

// AgentInfo 是供 Application/Adapter 使用的完整 Agent 信息。
//
// ModelDisplayName 由 Agent Service 从 ModelRegistry 投影得到。
// Agent Store 本身不跨领域读取 models.json。
type AgentInfo struct {
	Agent Agent

	ModelDisplayName string
}

// CreateInput 是创建 Agent 的领域输入。
type CreateInput struct {
	Name string

	Instruction string

	ModelID string

	EnabledSkills []string

	EnabledMCPTools []humbertmcp.ToolSelection

	EnabledBuiltinTools []string

	Sandbox sandbox.AgentPolicy

	WorkspaceMode workspace.Mode `json:"workspace_mode"`

	WorkspacePath string `json:"workspace_path,omitempty"`
}

// UpdateInput 是修改 Agent 的领域输入。
//
// Workspace 修改只改变后续 Turn 的配置，
// 不会搬迁、删除或者复制原 Workspace 中的任何文件。
type UpdateInput struct {
	Name string

	Instruction string

	ModelID string

	EnabledSkills []string

	// nil 表示普通 Agent Update 保留现有 MCP Tool Selection；非 nil（包括空 slice）
	// 表示调用方显式替换。这避免尚未感知 MCP 的旧前端表单保存时清空配置。
	EnabledMCPTools *[]humbertmcp.ToolSelection

	// nil 保留现有选择；非 nil（包括空 slice）显式替换。
	EnabledBuiltinTools *[]string

	// nil 保留现有 Sandbox；非 nil 显式替换。
	Sandbox *sandbox.AgentPolicy

	WorkspaceMode workspace.Mode `json:"workspace_mode"`

	WorkspacePath string `json:"workspace_path,omitempty"`
}
