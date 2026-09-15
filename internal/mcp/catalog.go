package mcp

import (
	"context"
	"time"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// ToolCatalogItem 是 MCP Server 对外暴露 Tool 的 Humbert 投影。
//
// RawName 是 Agent Profile 中持久化的稳定引用；ExposedName 是进入 Eino/LLM 的
// namespaced 名称。Annotations 只用于 UI 提示，绝不直接决定 Humbert Risk。
type ToolCatalogItem struct {
	RawName        string                 `json:"raw_name"`
	ExposedName    string                 `json:"exposed_name"`
	Description    string                 `json:"description"`
	InputSchema    map[string]any         `json:"input_schema,omitempty"`
	Annotations    map[string]any         `json:"annotations,omitempty"`
	Risk           humberttools.RiskLevel `json:"risk"`
	RiskOverridden bool                   `json:"risk_overridden"`
}

// ConnectionState 是 MCP Server 在 Desktop 控制面的连接状态。
type ConnectionState string

const (
	ConnectionDisconnected ConnectionState = "disconnected"
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionDegraded     ConnectionState = "degraded"
	ConnectionError        ConnectionState = "error"
	ConnectionDisabled     ConnectionState = "disabled"
)

// RuntimeStatus 是 SessionPool 当前对一个 Server 的非敏感运行态投影。
// 它不会触发连接；Disabled 状态由 Manager 根据持久化 Server 配置覆盖。
type RuntimeStatus struct {
	State         ConnectionState `json:"state"`
	Connected     bool            `json:"connected"`
	ConnectedAt   time.Time       `json:"connected_at,omitempty"`
	LastUsedAt    time.Time       `json:"last_used_at,omitempty"`
	LastSuccessAt time.Time       `json:"last_success_at,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	LastErrorAt   time.Time       `json:"last_error_at,omitempty"`
}

// RuntimeControlBackend 在 RuntimeBackend 之外提供 Desktop 控制面的连接/发现能力。
type RuntimeControlBackend interface {
	RuntimeBackend
	DiscoverTools(ctx context.Context, server Server) ([]ToolCatalogItem, error)
	RuntimeStatus(server Server) RuntimeStatus
	Invalidate(serverID string)
	Close() error
}
