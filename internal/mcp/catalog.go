package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

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

// resolveRuntimeProjection 构造不执行外部 IO 的 MCP Schema 投影。返回的 Tools 只实现
// BaseTool.Info，用于 ContextEngine 的 Token 估算；绝不能用于真实 Turn 执行。
func (m *Manager) resolveRuntimeProjection(
	ctx context.Context,
	values []ToolSelection,
	scope humberttools.Scope,
) (RuntimeSnapshot, error) {
	request, _, err := m.runtimeRequest(ctx, values, scope)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	if request.Servers == nil {
		return RuntimeSnapshot{Revision: request.Revision}, nil
	}
	result := RuntimeSnapshot{Revision: request.Revision}
	auditServers := runtimeServerSnapshots(request.Servers)
	seenExposed := make(map[string]struct{})
	for index, server := range request.Servers {
		selection := request.Selections[index]
		catalog := m.cachedCatalogForProjection(server)
		for _, rawName := range selection.Tools {
			exposedName, err := NameExposedTool(server.Key, rawName)
			if err != nil {
				return RuntimeSnapshot{}, err
			}
			if _, exists := seenExposed[exposedName]; exists {
				return RuntimeSnapshot{}, fmt.Errorf("MCP Tool exposed name 冲突: %s", exposedName)
			}
			seenExposed[exposedName] = struct{}{}

			description := ""
			var inputSchema map[string]any
			if cached, ok := catalog[rawName]; ok {
				description = cached.Description
				inputSchema = cloneJSONMap(cached.InputSchema)
			}
			descriptor := humberttools.Descriptor{
				Name: exposedName,
				Risk: ToolRisk(server, rawName),
				MCPOrigin: &humberttools.MCPOrigin{
					ServerID:          server.ID,
					ServerName:        server.Name,
					ServerFingerprint: ServerFingerprint(server),
					RawToolName:       rawName,
				},
			}
			result.Tools = append(result.Tools, projectedMCPTool{
				name:        exposedName,
				description: description,
				inputSchema: inputSchema,
			})
			result.Descriptors = append(result.Descriptors, descriptor)
			result.ToolNames = append(result.ToolNames, exposedName)
		}
	}
	return finalizeRuntimeSnapshot(result, request.Revision, auditServers)
}

func (m *Manager) cachedCatalogForProjection(server Server) map[string]ToolCatalogItem {
	fingerprint := ServerFingerprint(server)
	m.mu.RLock()
	entry, ok := m.catalogCache[server.ID]
	m.mu.RUnlock()
	if !ok || entry.fingerprint != fingerprint {
		return nil
	}
	result := make(map[string]ToolCatalogItem, len(entry.items))
	for _, item := range entry.items {
		result[item.RawName] = item
	}
	return result
}

// projectedMCPTool 是 Context Usage 专用的 schema-only BaseTool。InputSchema 放在 Extra
// 中是为了让 ApproxEstimator 的 JSON 投影仍然能估算其体积；真实 Runtime Tool 继续由
// Eino officialmcp 构建，不会经过这个类型。
type projectedMCPTool struct {
	name        string
	description string
	inputSchema map[string]any
}

func (t projectedMCPTool) Info(context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.description}
	if len(t.inputSchema) > 0 {
		info.Extra = map[string]any{"mcp_input_schema": cloneJSONMap(t.inputSchema)}
	}
	return info, nil
}

var _ einotool.BaseTool = projectedMCPTool{}

func (m *Manager) invalidateCatalog(serverID string) {
	m.mu.Lock()
	delete(m.catalogCache, strings.TrimSpace(serverID))
	m.mu.Unlock()
}

func cloneCatalog(values []ToolCatalogItem) []ToolCatalogItem {
	if len(values) == 0 {
		return nil
	}
	result := make([]ToolCatalogItem, 0, len(values))
	for _, value := range values {
		copyValue := value
		copyValue.InputSchema = cloneJSONMap(value.InputSchema)
		copyValue.Annotations = cloneJSONMap(value.Annotations)
		result = append(result, copyValue)
	}
	return result
}

func cloneJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = cloneJSONValue(item)
	}
	return result
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneJSONValue(item)
		}
		return result
	default:
		return typed
	}
}
