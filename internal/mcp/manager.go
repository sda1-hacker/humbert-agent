package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// Manager 管理 MCP Server 控制面配置与 Agent Tool Selection 的规范化。
//
// RuntimeBackend 负责建立真实 stdio / Streamable HTTP Session；控制面与 Runtime 仍保持解耦。
type Manager struct {
	store  *Store
	config config.MCPConfig
	logger *logging.Logger

	mu               sync.RWMutex
	revision         uint64
	backend          RuntimeBackend
	referenceChecker ServerReferenceChecker
	catalogCache     map[string]catalogCacheEntry
}

type catalogCacheEntry struct {
	fingerprint string
	fetchedAt   time.Time
	items       []ToolCatalogItem
}

func NewManager(store *Store, cfg config.MCPConfig, logger *logging.Logger) (*Manager, error) {
	if store == nil {
		return nil, errors.New("MCP Manager Store 不能为空")
	}
	if logger == nil {
		return nil, errors.New("MCP Manager Logger 不能为空")
	}
	return &Manager{
		store:        store,
		config:       cfg,
		logger:       logger,
		revision:     1,
		catalogCache: make(map[string]catalogCacheEntry),
	}, nil
}

func (m *Manager) SetRuntimeBackend(backend RuntimeBackend) {
	m.mu.Lock()
	m.backend = backend
	m.catalogCache = make(map[string]catalogCacheEntry)
	m.revision++
	m.mu.Unlock()
}

// DiscoverTools 返回 Server 当前 Tool Catalog。控制面允许短 TTL 缓存，避免设置页和
// Agent 连接器页面频繁重复 tools/list；真正的 Runtime BuildTools 仍由 Eino officialmcp
// 每轮校验选择的 Tool，因此缓存不会放宽运行时安全边界。
func (m *Manager) DiscoverTools(ctx context.Context, serverID string) ([]ToolCatalogItem, error) {
	return m.DiscoverToolsFresh(ctx, serverID, false)
}

// DiscoverToolsFresh 在 force=true 时忽略控制面 Catalog 缓存并执行真实 tools/list。
func (m *Manager) DiscoverToolsFresh(ctx context.Context, serverID string, force bool) ([]ToolCatalogItem, error) {
	if err := validateContext(ctx, "发现 MCP Tools"); err != nil {
		return nil, err
	}
	server, err := m.store.Get(ctx, strings.TrimSpace(serverID))
	if err != nil {
		return nil, err
	}
	if !server.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrServerDisabled, server.Name)
	}
	fingerprint := ServerFingerprint(server)
	ttl := time.Duration(m.config.CatalogTTLMS) * time.Millisecond
	if !force && ttl > 0 {
		m.mu.RLock()
		cached, ok := m.catalogCache[server.ID]
		m.mu.RUnlock()
		if ok && cached.fingerprint == fingerprint && time.Since(cached.fetchedAt) <= ttl {
			items := cloneCatalog(cached.items)
			for index := range items {
				items[index].Risk = ToolRisk(server, items[index].RawName)
				_, items[index].RiskOverridden = server.ToolRisks[items[index].RawName]
			}
			return items, nil
		}
	}
	m.mu.RLock()
	backend := m.backend
	m.mu.RUnlock()
	control, ok := backend.(RuntimeControlBackend)
	if !ok || control == nil {
		return nil, ErrRuntimeUnavailable
	}
	items, err := control.DiscoverTools(ctx, server)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Risk = ToolRisk(server, items[index].RawName)
		_, items[index].RiskOverridden = server.ToolRisks[items[index].RawName]
	}
	m.mu.Lock()
	m.catalogCache[server.ID] = catalogCacheEntry{fingerprint: fingerprint, fetchedAt: time.Now().UTC(), items: cloneCatalog(items)}
	m.mu.Unlock()
	return cloneCatalog(items), nil
}

// TestConnection 使用与 Runtime 相同的 Session Backend 建立连接并完成一次 tools/list。
func (m *Manager) TestConnection(ctx context.Context, serverID string) (int, error) {
	items, err := m.DiscoverToolsFresh(ctx, serverID, true)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

// Close 关闭所有受 Manager 持有的 MCP Session / 子进程。
func (m *Manager) Close() error {
	m.mu.RLock()
	backend := m.backend
	m.mu.RUnlock()
	control, ok := backend.(RuntimeControlBackend)
	if !ok || control == nil {
		return nil
	}
	return control.Close()
}

func (m *Manager) SetReferenceChecker(checker ServerReferenceChecker) {
	m.mu.Lock()
	m.referenceChecker = checker
	m.mu.Unlock()
}

func (m *Manager) Revision() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.revision
}

func (m *Manager) List(ctx context.Context) ([]Server, error) {
	return m.store.List(ctx)
}

func (m *Manager) Get(ctx context.Context, id string) (Server, error) {
	return m.store.Get(ctx, id)
}

// RuntimeStatus 返回当前 SessionPool 对指定 Server 的只读状态，不会主动建立连接。
func (m *Manager) RuntimeStatus(ctx context.Context, id string) (RuntimeStatus, error) {
	server, err := m.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return RuntimeStatus{}, err
	}
	m.mu.RLock()
	backend := m.backend
	m.mu.RUnlock()
	control, ok := backend.(RuntimeControlBackend)
	if !ok || control == nil {
		if !server.Enabled {
			return RuntimeStatus{State: ConnectionDisabled}, nil
		}
		return RuntimeStatus{State: ConnectionDisconnected}, nil
	}
	status := control.RuntimeStatus(server)
	if !server.Enabled {
		// Disabled 只覆盖当前状态，不抹掉最近成功/失败时间，便于控制面诊断。
		status.State = ConnectionDisabled
		status.Connected = false
		return status, nil
	}
	if status.State == "" {
		status.State = ConnectionDisconnected
	}
	status.Connected = status.State == ConnectionConnected || status.State == ConnectionDegraded
	return status, nil
}

// SetEnabled 启用或停用 MCP Server。停用只影响连接与 Runtime 加载，配置、Credential、
// Tool Risk 与 Agent Tool Selection 全部保留，重新启用后可以继续使用。
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) (Server, error) {
	if err := validateContext(ctx, "更新 MCP Server 启用状态"); err != nil {
		return Server{}, err
	}
	server, err := m.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return Server{}, err
	}
	if server.Enabled == enabled {
		return cloneServer(server), nil
	}
	server.Enabled = enabled
	server.UpdatedAt = time.Now().UTC()
	if err := m.store.Update(ctx, server); err != nil {
		return Server{}, err
	}
	// 无论启用还是停用都回收旧连接；重新启用时按需建立新的 Session。
	m.invalidateRuntimeSession(server.ID)
	m.invalidateCatalog(server.ID)
	m.bumpRevision()
	m.logger.Info(ctx, "MCP Server 启用状态已更新", "operation", "mcp.server.enabled.update", "server_id", server.ID, "server_key", server.Key, "enabled", enabled)
	return cloneServer(server), nil
}

// Disconnect 主动关闭指定 Server 的可复用 Session，不修改 Server/Agent 配置。
func (m *Manager) Disconnect(ctx context.Context, id string) error {
	if err := validateContext(ctx, "断开 MCP Server"); err != nil {
		return err
	}
	server, err := m.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	m.invalidateRuntimeSession(server.ID)
	m.logger.Info(ctx, "MCP Server 已手动断开", "operation", "mcp.server.disconnect", "server_id", server.ID, "server_key", server.Key)
	return nil
}

// SetToolRisk 保存用户对某个 raw MCP Tool 的 Humbert Risk Override。
// write 是默认值，因此写回 write 会删除 Override，保持 servers.json 最小化。
func (m *Manager) SetToolRisk(ctx context.Context, serverID, rawToolName string, risk humberttools.RiskLevel) (Server, error) {
	if err := validateContext(ctx, "更新 MCP Tool Risk"); err != nil {
		return Server{}, err
	}
	server, err := m.store.Get(ctx, strings.TrimSpace(serverID))
	if err != nil {
		return Server{}, err
	}
	rawName, err := normalizeRawToolName(rawToolName)
	if err != nil {
		return Server{}, err
	}
	switch risk {
	case humberttools.RiskRead, humberttools.RiskWrite, humberttools.RiskExec:
	default:
		return Server{}, fmt.Errorf("MCP Tool Risk %q 无效", risk)
	}
	if server.ToolRisks == nil {
		server.ToolRisks = make(map[string]humberttools.RiskLevel)
	}
	if risk == humberttools.RiskWrite {
		delete(server.ToolRisks, rawName)
	} else {
		server.ToolRisks[rawName] = risk
	}
	if len(server.ToolRisks) == 0 {
		server.ToolRisks = nil
	}
	server.UpdatedAt = time.Now().UTC()
	if err := validateServer(server); err != nil {
		return Server{}, err
	}
	if err := m.store.Update(ctx, server); err != nil {
		return Server{}, err
	}
	// Risk 只影响下一轮 Tool Descriptor/Permission，不改变连接身份，因此不关闭 Session。
	m.bumpRevision()
	m.logger.Info(ctx, "MCP Tool Risk 已更新", "operation", "mcp.tool.risk.update", "server_id", server.ID, "server_key", server.Key, "tool", rawName, "risk", risk)
	return cloneServer(server), nil
}

func (m *Manager) Create(ctx context.Context, input CreateServerInput) (Server, error) {
	if err := validateContext(ctx, "创建 MCP Server"); err != nil {
		return Server{}, err
	}
	now := time.Now().UTC()
	value := Server{
		ID:        uuid.NewString(),
		Key:       strings.TrimSpace(input.Key),
		Name:      strings.TrimSpace(input.Name),
		Enabled:   true,
		Transport: input.Transport,
		Stdio:     cloneStdio(input.Stdio),
		HTTP:      cloneHTTP(input.HTTP),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := validateServer(value); err != nil {
		return Server{}, err
	}
	if err := m.store.Create(ctx, value); err != nil {
		return Server{}, err
	}
	m.bumpRevision()
	m.logger.Info(ctx, "MCP Server 已创建", "operation", "mcp.server.create", "server_id", value.ID, "server_key", value.Key, "transport", value.Transport)
	return cloneServer(value), nil
}

func (m *Manager) Update(ctx context.Context, id string, input UpdateServerInput) (Server, error) {
	if err := validateContext(ctx, "更新 MCP Server"); err != nil {
		return Server{}, err
	}
	existing, err := m.store.Get(ctx, id)
	if err != nil {
		return Server{}, err
	}
	existing.Name = strings.TrimSpace(input.Name)
	existing.Transport = input.Transport
	existing.Stdio = cloneStdio(input.Stdio)
	existing.HTTP = cloneHTTP(input.HTTP)
	existing.UpdatedAt = time.Now().UTC()
	if err := validateServer(existing); err != nil {
		return Server{}, err
	}
	if err := m.store.Update(ctx, existing); err != nil {
		return Server{}, err
	}
	m.invalidateRuntimeSession(existing.ID)
	m.invalidateCatalog(existing.ID)
	m.bumpRevision()
	m.logger.Info(ctx, "MCP Server 已更新", "operation", "mcp.server.update", "server_id", existing.ID, "server_key", existing.Key, "transport", existing.Transport)
	return cloneServer(existing), nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := validateContext(ctx, "删除 MCP Server"); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	m.mu.RLock()
	checker := m.referenceChecker
	m.mu.RUnlock()
	if checker != nil {
		count, err := checker.CountAgentsUsingMCPServer(ctx, id)
		if err != nil {
			return fmt.Errorf("检查 MCP Server Agent 引用失败: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("%w: server_id=%s agents=%d", ErrServerInUse, id, count)
		}
	}
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	m.invalidateRuntimeSession(id)
	m.invalidateCatalog(id)
	m.bumpRevision()
	m.logger.Info(ctx, "MCP Server 已删除", "operation", "mcp.server.delete", "server_id", id)
	return nil
}

// NormalizeAndValidateSelection 规范化 Agent enabled_mcp_tools。
//
// 这里只校验 Server 存在与 raw Tool 名称结构；Tool 是否真实存在由 Discovery
// Catalog 在保存/Runtime Resolve 时进一步收紧。返回结果按 ServerID / ToolName 稳定排序。
func (m *Manager) NormalizeAndValidateSelection(ctx context.Context, values []ToolSelection) ([]ToolSelection, error) {
	if err := validateContext(ctx, "校验 Agent MCP Tool 配置"); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	servers, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	serverSet := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		serverSet[server.ID] = struct{}{}
	}

	merged := make(map[string]map[string]struct{})
	for _, selection := range values {
		serverID := strings.TrimSpace(selection.ServerID)
		if serverID == "" {
			return nil, errors.New("Agent MCP Tool 配置包含空 ServerID")
		}
		if _, exists := serverSet[serverID]; !exists {
			return nil, fmt.Errorf("%w: %s", ErrServerNotFound, serverID)
		}
		toolSet := merged[serverID]
		if toolSet == nil {
			toolSet = make(map[string]struct{})
			merged[serverID] = toolSet
		}
		for _, rawName := range selection.Tools {
			name, err := normalizeRawToolName(rawName)
			if err != nil {
				return nil, fmt.Errorf("Server %s Tool 配置无效: %w", serverID, err)
			}
			toolSet[name] = struct{}{}
		}
	}

	serverIDs := make([]string, 0, len(merged))
	for serverID, toolSet := range merged {
		if len(toolSet) == 0 {
			continue
		}
		if m.config.MaxToolsPerServer > 0 && len(toolSet) > m.config.MaxToolsPerServer {
			return nil, fmt.Errorf("Server %s 启用 Tool 数量 %d 超过上限 %d", serverID, len(toolSet), m.config.MaxToolsPerServer)
		}
		serverIDs = append(serverIDs, serverID)
	}
	sort.Strings(serverIDs)

	result := make([]ToolSelection, 0, len(serverIDs))
	for _, serverID := range serverIDs {
		toolSet := merged[serverID]
		toolNames := make([]string, 0, len(toolSet))
		for name := range toolSet {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		result = append(result, ToolSelection{ServerID: serverID, Tools: toolNames})
	}
	return result, nil
}

// ResolveRuntimeSnapshot 为当前 Turn 冻结 MCP ToolSet。
//
// 空选择永远成功，因此 MCP-01 引入后不会改变现有 Agent 行为。非空选择在 MCP-02 的
// SessionPool/RuntimeBackend 注入前明确失败，避免配置了 MCP 却静默丢弃能力。
func (m *Manager) ResolveRuntimeSnapshot(ctx context.Context, values []ToolSelection, scope humberttools.Scope) (RuntimeSnapshot, error) {
	return m.resolveRuntimeSnapshot(ctx, values, scope)
}

// ResolveRuntimeSnapshotAvailable 为真实 Agent Turn 构建“当前可用”的 MCP ToolSet。
//
// MCP 是可选扩展能力：一个 Connector 因 stdio initialize、网络、Credential 或远端服务
// 故障而不可用时，不应该让整个普通聊天 Turn 无法启动。本方法按 Server 独立 Resolve，
// 成功的 Server 继续进入当前 Turn，失败的 Server 写入 Failures 并从本 Turn 排除。
//
// 配置/选择本身不合法、Tool exposed name 冲突、Scope 无效等本地结构错误仍然 fail-closed；
// ctx 取消/超时也必须立即返回，不能伪装成 MCP 能力降级。严格诊断和设置页测试仍可继续
// 使用 ResolveRuntimeSnapshot。
func (m *Manager) ResolveRuntimeSnapshotAvailable(ctx context.Context, values []ToolSelection, scope humberttools.Scope) (RuntimeSnapshot, error) {
	normalized, err := m.NormalizeAndValidateSelection(ctx, values)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	m.mu.RLock()
	revision := m.revision
	backend := m.backend
	m.mu.RUnlock()
	if len(normalized) == 0 {
		return RuntimeSnapshot{Revision: revision}, nil
	}
	if err := scope.Validate(); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("MCP Runtime Tool Scope 无效: %w", err)
	}

	type activeServerSelection struct {
		server    Server
		selection ToolSelection
	}
	active := make([]activeServerSelection, 0, len(normalized))
	for _, selection := range normalized {
		server, err := m.store.Get(ctx, selection.ServerID)
		if err != nil {
			return RuntimeSnapshot{}, err
		}
		if !server.Enabled {
			continue
		}
		active = append(active, activeServerSelection{server: server, selection: selection})
	}
	if len(active) == 0 {
		return RuntimeSnapshot{Revision: revision}, nil
	}

	result := RuntimeSnapshot{Revision: revision}
	if backend == nil {
		for _, item := range active {
			result.Failures = append(result.Failures, runtimeServerFailure(item.server, ErrRuntimeUnavailable))
		}
		return result, nil
	}

	seenExposed := make(map[string]struct{})
	auditServers := make([]RuntimeServerSnapshot, 0, len(active))
	for _, item := range active {
		part, resolveErr := backend.Resolve(ctx, ResolveRequest{
			Revision:   revision,
			Servers:    []Server{item.server},
			Selections: cloneSelections([]ToolSelection{item.selection}),
			Scope:      scope,
		})
		if resolveErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return RuntimeSnapshot{}, ctxErr
			}
			failure := runtimeServerFailure(item.server, resolveErr)
			result.Failures = append(result.Failures, failure)
			if !errors.Is(resolveErr, ErrRuntimeRetryBackoff) {
				m.logger.Warn(
					ctx,
					"MCP Server 当前不可用，本 Turn 将跳过该 Server",
					"operation", "mcp.runtime.server.skipped",
					"server_id", item.server.ID,
					"server_name", item.server.Name,
					"transport", item.server.Transport,
					"error", failure.Error,
				)
			}
			continue
		}

		for _, name := range part.ToolNames {
			if _, exists := seenExposed[name]; exists {
				return RuntimeSnapshot{}, fmt.Errorf("MCP Tool exposed name 冲突: %s", name)
			}
			seenExposed[name] = struct{}{}
		}
		result.Tools = append(result.Tools, part.Tools...)
		result.Descriptors = append(result.Descriptors, part.Descriptors...)
		result.ToolNames = append(result.ToolNames, part.ToolNames...)
		auditServers = append(auditServers, RuntimeServerSnapshot{
			ServerID:    item.server.ID,
			ServerKey:   item.server.Key,
			ServerName:  item.server.Name,
			Fingerprint: ServerFingerprint(item.server),
			Transport:   string(item.server.Transport),
		})
	}

	return finalizeRuntimeSnapshot(result, revision, auditServers)
}

func runtimeServerFailure(server Server, err error) RuntimeServerFailure {
	message := "MCP Runtime 不可用"
	if err != nil {
		message = logging.RedactText(err.Error(), 512)
	}
	return RuntimeServerFailure{
		ServerID:   server.ID,
		ServerKey:  server.Key,
		ServerName: server.Name,
		Transport:  string(server.Transport),
		Error:      message,
	}
}

// ResolveRuntimeSnapshotBestEffort 只用于 Context Usage 等只读投影。
//
// 这里有意不调用 RuntimeBackend，也不会启动 stdio 子进程或访问远程 HTTP MCP Server。
// Context Usage 是高频 UI 查询；如果它为了 Token 估算主动 initialize MCP，会让一个不可用
// Connector 反复制造连接错误，也会把“只读状态查询”变成有副作用的外部 IO。
//
// 投影优先复用当前 Server Fingerprint 对应的内存 Catalog Cache；即使缓存超过控制面 TTL，
// 对只读 Token 估算仍可继续使用，因为 Server 配置变化会主动 invalidate Catalog。若本进程
// 尚无 Catalog，则至少保留 Tool 名称、Risk 与 MCP Origin，Schema 估算会更保守但不会阻塞 UI。
func (m *Manager) ResolveRuntimeSnapshotBestEffort(ctx context.Context, values []ToolSelection, scope humberttools.Scope) (RuntimeSnapshot, error) {
	return m.resolveRuntimeProjection(ctx, values, scope)
}

func (m *Manager) resolveRuntimeSnapshot(
	ctx context.Context,
	values []ToolSelection,
	scope humberttools.Scope,
) (RuntimeSnapshot, error) {
	normalized, err := m.NormalizeAndValidateSelection(ctx, values)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	m.mu.RLock()
	revision := m.revision
	backend := m.backend
	m.mu.RUnlock()
	if len(normalized) == 0 {
		return RuntimeSnapshot{Revision: revision}, nil
	}
	if err := scope.Validate(); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("MCP Runtime Tool Scope 无效: %w", err)
	}
	servers := make([]Server, 0, len(normalized))
	activeSelections := make([]ToolSelection, 0, len(normalized))
	auditServers := make([]RuntimeServerSnapshot, 0, len(normalized))
	for _, selection := range normalized {
		server, err := m.store.Get(ctx, selection.ServerID)
		if err != nil {
			return RuntimeSnapshot{}, err
		}
		// Disabled Server 的 Agent Tool Selection 被保留，但当前 Turn 不加载这些 Tool。
		if !server.Enabled {
			continue
		}
		servers = append(servers, server)
		activeSelections = append(activeSelections, selection)
		auditServers = append(auditServers, RuntimeServerSnapshot{
			ServerID:    server.ID,
			ServerKey:   server.Key,
			ServerName:  server.Name,
			Fingerprint: ServerFingerprint(server),
			Transport:   string(server.Transport),
		})
	}
	if len(activeSelections) == 0 {
		return RuntimeSnapshot{Revision: revision}, nil
	}
	if backend == nil {
		return RuntimeSnapshot{}, ErrRuntimeUnavailable
	}
	result, err := backend.Resolve(ctx, ResolveRequest{
		Revision:   revision,
		Servers:    servers,
		Selections: cloneSelections(activeSelections),
		Scope:      scope,
	})
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return finalizeRuntimeSnapshot(result, revision, auditServers)
}

// resolveRuntimeProjection 构造不执行外部 IO 的 MCP Schema 投影。返回的 Tools 只实现
// BaseTool.Info，用于 ContextEngine 的 Token 估算；绝不能用于真实 Turn 执行。
func (m *Manager) resolveRuntimeProjection(
	ctx context.Context,
	values []ToolSelection,
	scope humberttools.Scope,
) (RuntimeSnapshot, error) {
	normalized, err := m.NormalizeAndValidateSelection(ctx, values)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	m.mu.RLock()
	revision := m.revision
	m.mu.RUnlock()
	if len(normalized) == 0 {
		return RuntimeSnapshot{Revision: revision}, nil
	}
	if err := scope.Validate(); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("MCP Runtime Tool Scope 无效: %w", err)
	}

	result := RuntimeSnapshot{Revision: revision}
	auditServers := make([]RuntimeServerSnapshot, 0, len(normalized))
	seenExposed := make(map[string]struct{})
	for _, selection := range normalized {
		server, err := m.store.Get(ctx, selection.ServerID)
		if err != nil {
			return RuntimeSnapshot{}, err
		}
		if !server.Enabled {
			continue
		}

		auditServers = append(auditServers, RuntimeServerSnapshot{
			ServerID:    server.ID,
			ServerKey:   server.Key,
			ServerName:  server.Name,
			Fingerprint: ServerFingerprint(server),
			Transport:   string(server.Transport),
		})
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
	return finalizeRuntimeSnapshot(result, revision, auditServers)
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

func finalizeRuntimeSnapshot(result RuntimeSnapshot, revision uint64, auditServers []RuntimeServerSnapshot) (RuntimeSnapshot, error) {
	result.Revision = revision
	result.Servers = append([]RuntimeServerSnapshot(nil), auditServers...)
	result.AuditTools = make([]RuntimeToolSnapshot, 0, len(result.Descriptors))
	for _, descriptor := range result.Descriptors {
		if descriptor.MCPOrigin == nil {
			continue
		}
		result.AuditTools = append(result.AuditTools, RuntimeToolSnapshot{
			ServerID:    descriptor.MCPOrigin.ServerID,
			RawName:     descriptor.MCPOrigin.RawToolName,
			ExposedName: descriptor.Name,
			Risk:        descriptor.Risk,
		})
	}
	return result, nil
}

func (m *Manager) bumpRevision() {
	m.mu.Lock()
	m.revision++
	m.mu.Unlock()
}

func cloneStdio(value *StdioConfig) *StdioConfig {
	if value == nil {
		return nil
	}
	result := *value
	result.Command = strings.TrimSpace(result.Command)
	result.WorkingDirectory = strings.TrimSpace(result.WorkingDirectory)
	result.Args = append([]string(nil), value.Args...)
	result.Env = append([]StdioEnvCredential(nil), value.Env...)
	for index := range result.Env {
		result.Env[index].Name = strings.TrimSpace(result.Env[index].Name)
		result.Env[index].CredentialID = strings.TrimSpace(result.Env[index].CredentialID)
	}
	sort.Slice(result.Env, func(i, j int) bool {
		return strings.ToLower(result.Env[i].Name) < strings.ToLower(result.Env[j].Name)
	})
	return &result
}

func cloneHTTP(value *HTTPConfig) *HTTPConfig {
	if value == nil {
		return nil
	}
	result := *value
	result.Endpoint = strings.TrimSpace(result.Endpoint)
	result.BearerCredentialID = strings.TrimSpace(result.BearerCredentialID)
	result.Headers = append([]HTTPHeaderCredential(nil), value.Headers...)
	for index := range result.Headers {
		result.Headers[index].Name = strings.TrimSpace(result.Headers[index].Name)
		result.Headers[index].CredentialID = strings.TrimSpace(result.Headers[index].CredentialID)
	}
	sort.Slice(result.Headers, func(i, j int) bool {
		return strings.ToLower(result.Headers[i].Name) < strings.ToLower(result.Headers[j].Name)
	})
	return &result
}

func (m *Manager) invalidateCatalog(serverID string) {
	m.mu.Lock()
	delete(m.catalogCache, strings.TrimSpace(serverID))
	m.mu.Unlock()
}

func (m *Manager) invalidateRuntimeSession(serverID string) {
	m.mu.RLock()
	backend := m.backend
	m.mu.RUnlock()
	if control, ok := backend.(RuntimeControlBackend); ok && control != nil {
		control.Invalidate(serverID)
	}
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
