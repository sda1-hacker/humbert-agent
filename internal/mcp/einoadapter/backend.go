package einoadapter

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/tool/mcp/officialmcp"
	officialmcpsession "github.com/cloudwego/eino-ext/components/tool/mcp/officialmcp/session"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// Backend 是 Humbert MCP 的统一 SessionPool + RuntimeBackend，支持 stdio 与
// Streamable HTTP。Tool Schema、Tool Call 适配以及底层 MCP Session 的建立/重连均由
// Eino-Ext officialmcp 生态完成；Backend 只负责 Humbert 的连接复用、安全边界、
// Credential 注入与运行态投影。
type Backend struct {
	cfg         config.MCPConfig
	adapter     *Adapter
	logger      *logging.Logger
	credentials humbertmcp.CredentialReader
	sandbox     *sandbox.Manager

	mu           sync.Mutex
	sessions     map[string]*sessionEntry
	connecting   map[string]*connectionAttempt
	statuses     map[string]runtimeStatusEntry
	retryBackoff map[string]runtimeRetryState
	generations  map[string]uint64
	closed       bool
}

type sessionEntry struct {
	serverID    string
	policyKey   string
	fingerprint string
	session     *officialmcpsession.Session
	cleanup     func()
	createdAt   time.Time
	lastUsedAt  time.Time
}

type runtimeStatusEntry struct {
	fingerprint string
	status      humbertmcp.RuntimeStatus
}

type runtimeRetryState struct {
	fingerprint string
	generation  uint64
	failures    int
	retryAt     time.Time
	message     string
}

type connectionAttempt struct {
	serverID string
	done     chan struct{}
	cancel   context.CancelFunc
	session  *officialmcpsession.Session
	err      error
}

func NewBackend(
	cfg config.MCPConfig,
	authorizer humberttools.Authorizer,
	credentials humbertmcp.CredentialReader,
	sandboxManager *sandbox.Manager,
	logger *logging.Logger,
) (*Backend, error) {
	if logger == nil {
		return nil, errors.New("MCP Runtime Backend Logger 不能为空")
	}
	if sandboxManager == nil {
		return nil, errors.New("MCP Runtime Backend SandboxManager 不能为空")
	}
	adapter, err := New(cfg, authorizer)
	if err != nil {
		return nil, err
	}
	return &Backend{
		cfg:          cfg,
		adapter:      adapter,
		logger:       logger,
		credentials:  credentials,
		sandbox:      sandboxManager,
		sessions:     make(map[string]*sessionEntry),
		connecting:   make(map[string]*connectionAttempt),
		statuses:     make(map[string]runtimeStatusEntry),
		retryBackoff: make(map[string]runtimeRetryState),
		generations:  make(map[string]uint64),
	}, nil
}

func (b *Backend) Resolve(ctx context.Context, request humbertmcp.ResolveRequest) (humbertmcp.RuntimeSnapshot, error) {
	if ctx == nil {
		return humbertmcp.RuntimeSnapshot{}, errors.New("解析 MCP Runtime 失败: context.Context 不能为空")
	}
	serverByID := make(map[string]humbertmcp.Server, len(request.Servers))
	for _, server := range request.Servers {
		serverByID[server.ID] = server
	}

	result := humbertmcp.RuntimeSnapshot{Revision: request.Revision}
	seenExposed := make(map[string]struct{})
	for _, selection := range request.Selections {
		server, ok := serverByID[selection.ServerID]
		if !ok {
			return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("MCP Runtime 缺少 Server %s", selection.ServerID)
		}
		policy := request.Scope.SandboxPolicy()
		if server.Transport == humbertmcp.TransportStreamableHTTP && !policy.AllowsNetwork() {
			return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("MCP Server %q 需要网络，但当前 Agent Sandbox 已禁用网络", server.Name)
		}
		session, err := b.runtimeSession(ctx, server, policy)
		if err != nil {
			return humbertmcp.RuntimeSnapshot{}, err
		}
		serverSnapshot, err := b.adapter.BuildTools(
			ctx,
			server,
			selection.Tools,
			session,
			request.Scope,
			func(callErr error) {
				if callErr == nil {
					b.markOperationSuccess(server, session)
					return
				}
				if officialmcp.IsConnectionError(callErr) {
					b.invalidateSession(server.ID, humbertmcp.ServerFingerprint(server), session, callErr)
					return
				}
				b.recordOperationError(server, session, callErr)
			},
		)
		if err != nil {
			if officialmcp.IsConnectionError(err) {
				b.invalidateSession(server.ID, humbertmcp.ServerFingerprint(server), session, err)
			} else {
				b.recordOperationError(server, session, err)
			}
			return humbertmcp.RuntimeSnapshot{}, err
		}
		b.markOperationSuccess(server, session)
		for _, name := range serverSnapshot.ToolNames {
			if _, exists := seenExposed[name]; exists {
				return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("MCP Tool exposed name 冲突: %s", name)
			}
			seenExposed[name] = struct{}{}
		}
		result.Tools = append(result.Tools, serverSnapshot.Tools...)
		result.Descriptors = append(result.Descriptors, serverSnapshot.Descriptors...)
		result.ToolNames = append(result.ToolNames, serverSnapshot.ToolNames...)
	}
	return result, nil
}

func (b *Backend) DiscoverTools(ctx context.Context, server humbertmcp.Server) ([]humbertmcp.ToolCatalogItem, error) {
	session, err := b.session(ctx, server)
	if err != nil {
		return nil, err
	}
	items, err := b.adapter.DiscoverTools(ctx, server, session)
	if err != nil {
		if officialmcp.IsConnectionError(err) {
			b.invalidateSession(server.ID, humbertmcp.ServerFingerprint(server), session, err)
		} else {
			b.recordOperationError(server, session, err)
		}
		return nil, err
	}
	b.markOperationSuccess(server, session)
	return items, nil
}

func (b *Backend) RuntimeStatus(server humbertmcp.Server) humbertmcp.RuntimeStatus {
	fingerprint := humbertmcp.ServerFingerprint(server)
	b.mu.Lock()
	statusEntry, ok := b.statuses[server.ID]
	b.mu.Unlock()
	if !ok || statusEntry.fingerprint != fingerprint {
		return humbertmcp.RuntimeStatus{State: humbertmcp.ConnectionDisconnected}
	}
	status := statusEntry.status
	if status.State == "" {
		if status.Connected {
			status.State = humbertmcp.ConnectionConnected
		} else if status.LastError != "" {
			status.State = humbertmcp.ConnectionError
		} else {
			status.State = humbertmcp.ConnectionDisconnected
		}
	}
	status.Connected = status.State == humbertmcp.ConnectionConnected || status.State == humbertmcp.ConnectionDegraded
	return status
}

func (b *Backend) Invalidate(serverID string) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return
	}
	b.mu.Lock()
	b.generations[serverID]++
	for key := range b.retryBackoff {
		if strings.HasPrefix(key, serverID+"|") {
			delete(b.retryBackoff, key)
		}
	}
	for _, attempt := range b.connecting {
		if attempt != nil && attempt.serverID == serverID && attempt.cancel != nil {
			attempt.cancel()
		}
	}
	b.mu.Unlock()
	b.invalidateSession(serverID, "", nil, nil)
}

func (b *Backend) invalidateSession(serverID, fingerprint string, expected *officialmcpsession.Session, cause error) {
	now := time.Now().UTC()
	toClose := make([]*sessionEntry, 0, 2)
	var history *sessionEntry

	b.mu.Lock()
	matchedExpected := expected == nil
	for key, entry := range b.sessions {
		if entry == nil || entry.serverID != serverID {
			continue
		}
		if fingerprint != "" && entry.fingerprint != fingerprint {
			continue
		}
		if expected != nil && entry.session != expected {
			continue
		}
		matchedExpected = true
		if history == nil || entry.lastUsedAt.After(history.lastUsedAt) {
			history = entry
		}
		if entry.session != nil || entry.cleanup != nil {
			toClose = append(toClose, entry)
		}
		delete(b.sessions, key)
	}
	// 旧 Session 的失败/回收不能覆盖并发建立的新 Session 状态。
	if !matchedExpected {
		b.mu.Unlock()
		return
	}

	statusFingerprint := fingerprint
	if statusFingerprint == "" && history != nil {
		statusFingerprint = history.fingerprint
	}
	if statusFingerprint == "" {
		if previous, ok := b.statuses[serverID]; ok {
			statusFingerprint = previous.fingerprint
		}
	}
	sibling := b.latestLiveSessionLocked(serverID, statusFingerprint)
	if statusFingerprint != "" {
		status := humbertmcp.RuntimeStatus{}
		if previous, ok := b.statuses[serverID]; ok && previous.fingerprint == statusFingerprint {
			status = previous.status
		}
		if cause == nil {
			if sibling != nil {
				status.State = humbertmcp.ConnectionConnected
				status.Connected = true
			} else {
				status.State = humbertmcp.ConnectionDisconnected
				status.Connected = false
			}
		} else {
			// 同一个 Server 可能同时存在 control session 与多个 Sandbox Policy 的
			// runtime session。一个 Session 因连接错误失效时，只要 sibling session
			// 仍然可用，Server 的全局控制面状态应是 degraded，而不是 error/disconnected。
			if sibling != nil {
				status.State = humbertmcp.ConnectionDegraded
				status.Connected = true
			} else {
				status.State = humbertmcp.ConnectionError
				status.Connected = false
			}
			status.LastError = logging.RedactText(cause.Error(), 512)
			status.LastErrorAt = now
		}
		if sibling != nil {
			status.ConnectedAt = sibling.createdAt
			status.LastUsedAt = sibling.lastUsedAt
		} else if history != nil {
			status.ConnectedAt = history.createdAt
			status.LastUsedAt = history.lastUsedAt
		}
		b.statuses[serverID] = runtimeStatusEntry{fingerprint: statusFingerprint, status: status}
	} else if cause == nil {
		delete(b.statuses, serverID)
	}
	b.mu.Unlock()

	for _, entry := range toClose {
		_ = closeSessionEntry(entry)
	}
}

// latestLiveSessionLocked 返回同一 Server/Fingerprint 仍在复用的最近 Session。
// 调用方必须持有 b.mu。一个 Server 可以同时有 control 与多个 Sandbox Runtime Session，
// 因此控制面状态不能仅由某一个 Session 的成败决定。
func (b *Backend) latestLiveSessionLocked(serverID, fingerprint string) *sessionEntry {
	var latest *sessionEntry
	for _, entry := range b.sessions {
		if entry == nil || entry.serverID != serverID || entry.session == nil {
			continue
		}
		if fingerprint != "" && entry.fingerprint != fingerprint {
			continue
		}
		if latest == nil || entry.lastUsedAt.After(latest.lastUsedAt) {
			latest = entry
		}
	}
	return latest
}

func closeSessionEntry(entry *sessionEntry) error {
	if entry == nil {
		return nil
	}
	var err error
	if entry.session != nil {
		err = entry.session.Close()
	}
	if entry.cleanup != nil {
		entry.cleanup()
		entry.cleanup = nil
	}
	return err
}

func (b *Backend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	entries := b.sessions
	attempts := b.connecting
	b.sessions = make(map[string]*sessionEntry)
	b.connecting = make(map[string]*connectionAttempt)
	b.statuses = make(map[string]runtimeStatusEntry)
	b.retryBackoff = make(map[string]runtimeRetryState)
	b.mu.Unlock()

	for _, attempt := range attempts {
		if attempt != nil && attempt.cancel != nil {
			attempt.cancel()
		}
	}

	var result error
	for _, entry := range entries {
		result = errors.Join(result, closeSessionEntry(entry))
	}
	return result
}

func (b *Backend) session(ctx context.Context, server humbertmcp.Server) (*officialmcpsession.Session, error) {
	return b.sessionWithPolicy(ctx, server, "control", nil)
}

func (b *Backend) runtimeSession(ctx context.Context, server humbertmcp.Server, policy sandbox.EffectivePolicy) (*officialmcpsession.Session, error) {
	policyKey, effectivePolicy, err := runtimeSessionTarget(server, policy)
	if err != nil {
		return nil, err
	}
	return b.sessionWithPolicy(ctx, server, policyKey, effectivePolicy)
}

// runtimeSessionTarget 明确 MCP Connector 与 Agent Sandbox 的边界。
//
// stdio MCP Server 是用户显式配置并启用的本地 Connector，本身拥有独立的 command/args、
// allowed paths 与 Credential。设置页 TestConnection 使用 control session；如果 Runtime 又把
// 同一个 Connector 强行包进 Agent Workspace Seatbelt/bwrap，会出现“设置页测试成功、聊天时
// initialize: EOF/Abort trap”的不一致。stdio Runtime 因此复用同一 control session，不再套
// Agent 文件系统 Sandbox；具体 MCP Tool 是否暴露/调用仍由 Agent Tool Selection、Permission
// 与 Approval 控制。
//
// NetworkNone 是例外：一个不受 Agent 进程 Sandbox 包裹的任意 stdio Connector 可能自行访问
// 网络，Humbert 无法可靠证明它满足 NetworkNone，因此这里 fail-closed。HTTP MCP 仍使用当前
// Agent policy 创建独立 runtime session。
func runtimeSessionTarget(server humbertmcp.Server, policy sandbox.EffectivePolicy) (string, *sandbox.EffectivePolicy, error) {
	if server.Transport == humbertmcp.TransportStdio {
		if policy.NetworkMode == sandbox.NetworkNone {
			return "", nil, fmt.Errorf("MCP Server %q 使用 stdio Connector；当前 Agent Sandbox 禁止网络时无法安全复用未包裹进程 Sandbox 的 Connector", server.Name)
		}
		return "control", nil, nil
	}
	cloned := policy
	return sandboxPolicyKey(cloned), &cloned, nil
}

func (b *Backend) sessionWithPolicy(ctx context.Context, server humbertmcp.Server, policyKey string, policy *sandbox.EffectivePolicy) (*officialmcpsession.Session, error) {
	if err := humbertmcp.ValidateServerForRuntime(server); err != nil {
		return nil, err
	}
	fingerprint := humbertmcp.ServerFingerprint(server)
	cacheKey := server.ID + "|" + policyKey
	now := time.Now().UTC()

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("MCP Runtime Backend 已关闭")
	}
	generation := b.generations[server.ID]
	if policyKey != "control" {
		if retry := b.retryBackoff[cacheKey]; retry.fingerprint == fingerprint && retry.generation == generation && now.Before(retry.retryAt) {
			remaining := time.Until(retry.retryAt).Round(time.Second)
			if remaining < time.Second {
				remaining = time.Second
			}
			message := strings.TrimSpace(retry.message)
			if message == "" {
				message = "最近一次 MCP initialize 失败"
			}
			b.mu.Unlock()
			return nil, fmt.Errorf("%w: Server %q 将在 %s 后自动重试；最近错误: %s", humbertmcp.ErrRuntimeRetryBackoff, server.Name, remaining, message)
		}
	}
	if existing := b.sessions[cacheKey]; existing != nil && existing.fingerprint == fingerprint && existing.session != nil {
		existing.lastUsedAt = now
		statusEntry := b.statuses[server.ID]
		if statusEntry.fingerprint == fingerprint {
			if statusEntry.status.State == "" || statusEntry.status.State == humbertmcp.ConnectionDisconnected {
				statusEntry.status.State = humbertmcp.ConnectionConnected
			}
			statusEntry.status.Connected = true
			statusEntry.status.LastUsedAt = now
			b.statuses[server.ID] = statusEntry
		}
		connected := existing.session
		b.mu.Unlock()
		return connected, nil
	}

	attemptKey := fmt.Sprintf("%s|%s|%d", cacheKey, fingerprint, generation)
	if pending := b.connecting[attemptKey]; pending != nil {
		done := pending.done
		b.mu.Unlock()
		select {
		case <-done:
			return b.sessionAfterAttempt(cacheKey, fingerprint, generation, pending)
		case <-ctx.Done():
			return nil, fmt.Errorf("等待 MCP Server %q 连接被取消: %w", server.Name, ctx.Err())
		}
	}

	old := b.sessions[cacheKey]
	delete(b.sessions, cacheKey)
	attemptCtx, attemptCancel := context.WithCancel(ctx)
	attempt := &connectionAttempt{
		serverID: server.ID,
		done:     make(chan struct{}),
		cancel:   attemptCancel,
	}
	b.connecting[attemptKey] = attempt
	if b.generations[server.ID] == generation {
		b.markConnectingLocked(server.ID, fingerprint)
	}
	b.mu.Unlock()

	if old != nil {
		_ = closeSessionEntry(old)
	}

	var connected *officialmcpsession.Session
	var connectedCleanup func()
	var err error
	switch server.Transport {
	case humbertmcp.TransportStdio:
		connected, connectedCleanup, err = b.connectStdio(attemptCtx, server, policy)
	case humbertmcp.TransportStreamableHTTP:
		connected, err = b.connectHTTP(attemptCtx, server, policy)
	default:
		err = fmt.Errorf("MCP transport %q 不支持", server.Transport)
	}
	attemptCancel()
	if err != nil {
		if policyKey != "control" {
			b.recordRuntimeRetryFailure(server.ID, cacheKey, fingerprint, generation, err)
		}
		b.recordConnectError(server.ID, fingerprint, generation, err)
		b.finishConnectionAttempt(attemptKey, attempt, nil, err)
		return nil, err
	}

	now = time.Now().UTC()
	entry := &sessionEntry{
		serverID:    server.ID,
		policyKey:   policyKey,
		fingerprint: fingerprint,
		session:     connected,
		cleanup:     connectedCleanup,
		createdAt:   now,
		lastUsedAt:  now,
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = closeSessionEntry(entry)
		err = errors.New("MCP Runtime Backend 已关闭")
		b.finishConnectionAttempt(attemptKey, attempt, nil, err)
		return nil, err
	}
	if b.generations[server.ID] != generation {
		b.mu.Unlock()
		_ = closeSessionEntry(entry)
		err = errors.New("MCP Server 配置在连接过程中发生变化，请重试")
		b.finishConnectionAttempt(attemptKey, attempt, nil, err)
		return nil, err
	}
	// 理论上同一个 cacheKey/fingerprint/generation 只会有一个 in-flight 连接；这里仍保留
	// winner 检查，防止未来连接策略调整后意外覆盖已经可用的 Session。
	if existing := b.sessions[cacheKey]; existing != nil && existing.fingerprint == fingerprint && existing.session != nil {
		winner := existing.session
		b.mu.Unlock()
		_ = closeSessionEntry(entry)
		b.finishConnectionAttempt(attemptKey, attempt, winner, nil)
		return winner, nil
	}
	b.sessions[cacheKey] = entry
	delete(b.retryBackoff, cacheKey)
	b.statuses[server.ID] = runtimeStatusEntry{
		fingerprint: fingerprint,
		status: humbertmcp.RuntimeStatus{
			State:         humbertmcp.ConnectionConnected,
			Connected:     true,
			ConnectedAt:   now,
			LastUsedAt:    now,
			LastSuccessAt: now,
		},
	}
	b.mu.Unlock()
	b.finishConnectionAttempt(attemptKey, attempt, connected, nil)
	return connected, nil
}

// sessionAfterAttempt 防止等待同一 in-flight 连接的调用方在 Server 已被
// Invalidate/Disconnect 后拿到一个刚刚关闭的旧 Session。连接成功只是必要条件；
// 返回前仍要确认 generation、fingerprint 与 SessionPool 当前条目完全一致。
func (b *Backend) sessionAfterAttempt(
	cacheKey string,
	fingerprint string,
	generation uint64,
	attempt *connectionAttempt,
) (*officialmcpsession.Session, error) {
	if attempt == nil {
		return nil, errors.New("MCP 连接状态无效")
	}
	if attempt.err != nil {
		return nil, attempt.err
	}
	if attempt.session == nil {
		return nil, errors.New("MCP 连接已结束但未返回 Session")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errors.New("MCP Runtime Backend 已关闭")
	}
	if b.generations[attempt.serverID] != generation {
		return nil, errors.New("MCP Server 配置在连接完成后发生变化，请重试")
	}
	entry := b.sessions[cacheKey]
	if entry == nil || entry.session != attempt.session || entry.fingerprint != fingerprint {
		return nil, errors.New("MCP Session 在连接完成后已失效，请重试")
	}
	entry.lastUsedAt = time.Now().UTC()
	return attempt.session, nil
}

// markConnectingLocked 更新 Server 聚合状态。一个 Server 可以同时拥有 control 与多个
// Sandbox Runtime Session；当 sibling Session 仍健康时，新 Policy 的连接尝试不应把整个
// Server 从 connected/degraded 短暂覆盖成 connecting。调用方必须持有 b.mu。
func (b *Backend) markConnectingLocked(serverID, fingerprint string) {
	sibling := b.latestLiveSessionLocked(serverID, fingerprint)
	status := humbertmcp.RuntimeStatus{}
	if previous, ok := b.statuses[serverID]; ok && previous.fingerprint == fingerprint {
		status = previous.status
	}
	if sibling == nil {
		status.State = humbertmcp.ConnectionConnecting
		status.Connected = false
		b.statuses[serverID] = runtimeStatusEntry{fingerprint: fingerprint, status: status}
		return
	}

	status.Connected = true
	status.ConnectedAt = sibling.createdAt
	status.LastUsedAt = sibling.lastUsedAt
	if status.LastError != "" {
		status.State = humbertmcp.ConnectionDegraded
	} else {
		status.State = humbertmcp.ConnectionConnected
	}
	b.statuses[serverID] = runtimeStatusEntry{fingerprint: fingerprint, status: status}
}

func (b *Backend) finishConnectionAttempt(
	attemptKey string,
	attempt *connectionAttempt,
	session *officialmcpsession.Session,
	err error,
) {
	if attempt == nil {
		return
	}
	b.mu.Lock()
	attempt.session = session
	attempt.err = err
	if current := b.connecting[attemptKey]; current == attempt {
		delete(b.connecting, attemptKey)
	}
	close(attempt.done)
	b.mu.Unlock()
}

func (b *Backend) recordRuntimeRetryFailure(serverID, cacheKey, fingerprint string, generation uint64, cause error) {
	serverID = strings.TrimSpace(serverID)
	if b == nil || serverID == "" || strings.TrimSpace(cacheKey) == "" || cause == nil {
		return
	}
	now := time.Now().UTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.generations[serverID] != generation {
		return
	}
	if b.retryBackoff == nil {
		b.retryBackoff = make(map[string]runtimeRetryState)
	}
	previous := b.retryBackoff[cacheKey]
	failures := 1
	if previous.fingerprint == fingerprint && previous.generation == generation && previous.failures > 0 {
		failures = previous.failures + 1
	}
	if failures > 4 {
		failures = 4
	}
	delay := 15 * time.Second * time.Duration(1<<(failures-1))
	if delay > 2*time.Minute {
		delay = 2 * time.Minute
	}
	b.retryBackoff[cacheKey] = runtimeRetryState{
		fingerprint: fingerprint,
		generation:  generation,
		failures:    failures,
		retryAt:     now.Add(delay),
		message:     logging.RedactText(cause.Error(), 384),
	}
}

func sandboxPolicyKey(policy sandbox.EffectivePolicy) string {
	parts := []string{string(policy.Profile), string(policy.NetworkMode), string(policy.NativeMode), policy.WorkspaceRoot}
	for _, rule := range policy.PathRules {
		parts = append(parts, fmt.Sprintf("%s:%d:%s", rule.Root, rule.Access, rule.Source))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("runtime:%x", sum[:12])
}

func (b *Backend) recordConnectError(serverID, fingerprint string, generation uint64, err error) {
	if err == nil {
		return
	}
	now := time.Now().UTC()
	b.mu.Lock()
	if b.generations[serverID] != generation {
		b.mu.Unlock()
		return
	}
	// 同一个 Server 可能同时存在 control session 与不同 Sandbox Policy 的 runtime
	// session。某个并发连接失败不能把健康 sibling 全局标记成 disconnected/error，
	// 但需要留下 degraded + LastError，方便设置页准确提示“部分连接异常”。
	sibling := b.latestLiveSessionLocked(serverID, fingerprint)
	status := humbertmcp.RuntimeStatus{}
	if previous, ok := b.statuses[serverID]; ok && previous.fingerprint == fingerprint {
		status = previous.status
	}
	if sibling != nil {
		status.State = humbertmcp.ConnectionDegraded
		status.Connected = true
		status.ConnectedAt = sibling.createdAt
		status.LastUsedAt = sibling.lastUsedAt
	} else {
		status.State = humbertmcp.ConnectionError
		status.Connected = false
	}
	status.LastError = logging.RedactText(err.Error(), 512)
	status.LastErrorAt = now
	b.statuses[serverID] = runtimeStatusEntry{fingerprint: fingerprint, status: status}
	b.mu.Unlock()
}

func (b *Backend) findExpectedSessionLocked(serverID, fingerprint string, expected *officialmcpsession.Session) *sessionEntry {
	for _, entry := range b.sessions {
		if entry == nil || entry.serverID != serverID || entry.session != expected || entry.fingerprint != fingerprint {
			continue
		}
		return entry
	}
	return nil
}

func (b *Backend) markOperationSuccess(server humbertmcp.Server, expected *officialmcpsession.Session) {
	now := time.Now().UTC()
	fingerprint := humbertmcp.ServerFingerprint(server)
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.findExpectedSessionLocked(server.ID, fingerprint, expected)
	if entry == nil {
		return
	}
	entry.lastUsedAt = now
	status := b.statuses[server.ID]
	if status.fingerprint != fingerprint {
		return
	}
	status.status.State = humbertmcp.ConnectionConnected
	status.status.Connected = true
	status.status.LastUsedAt = now
	status.status.LastSuccessAt = now
	status.status.LastError = ""
	status.status.LastErrorAt = time.Time{}
	b.statuses[server.ID] = status
}

func (b *Backend) recordOperationError(server humbertmcp.Server, expected *officialmcpsession.Session, cause error) {
	if cause == nil {
		return
	}
	now := time.Now().UTC()
	fingerprint := humbertmcp.ServerFingerprint(server)
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.findExpectedSessionLocked(server.ID, fingerprint, expected)
	if entry == nil {
		return
	}
	entry.lastUsedAt = now
	status := b.statuses[server.ID]
	if status.fingerprint != fingerprint {
		status.fingerprint = fingerprint
		status.status.ConnectedAt = entry.createdAt
	}
	status.status.State = humbertmcp.ConnectionDegraded
	status.status.Connected = true
	status.status.LastUsedAt = now
	status.status.LastError = logging.RedactText(cause.Error(), 512)
	status.status.LastErrorAt = now
	b.statuses[server.ID] = status
}

func (b *Backend) connectStdio(ctx context.Context, server humbertmcp.Server, policy *sandbox.EffectivePolicy) (*officialmcpsession.Session, func(), error) {
	if server.Stdio == nil {
		return nil, nil, errors.New("stdio MCP Server 缺少启动配置")
	}
	environment, err := resolveStdioEnvironment(ctx, b.credentials, server)
	if err != nil {
		return nil, nil, err
	}
	command := strings.TrimSpace(server.Stdio.Command)
	args := append([]string(nil), server.Stdio.Args...)
	workingDirectory := strings.TrimSpace(server.Stdio.WorkingDirectory)
	if policy != nil {
		if workingDirectory == "" {
			workingDirectory = policy.WorkspaceRoot
		} else {
			decision, resolveErr := policy.CheckPath(workingDirectory, sandbox.OpList)
			if resolveErr != nil {
				return nil, nil, fmt.Errorf("stdio MCP WorkingDirectory 被 Agent Sandbox 拒绝: %w", resolveErr)
			}
			workingDirectory = decision.CanonicalPath
		}
	}

	resolvedCommand, err := validateStdioLaunch(server, workingDirectory)
	if err != nil {
		return nil, nil, fmt.Errorf("连接 stdio MCP Server %q 失败: %w", server.Name, err)
	}
	command = resolvedCommand
	ensureResolvedStdioCommandOnPath(environment, command)
	applySandboxedStdioLauncherDefaults(
		environment,
		server,
		command,
		policy != nil && policy.Profile != sandbox.ProfileFullAccess,
	)

	cleanups := make([]func(), 0, 2)
	cleanup := func() {
		for index := len(cleanups) - 1; index >= 0; index-- {
			if cleanups[index] != nil {
				cleanups[index]()
			}
		}
	}
	connected := false
	defer func() {
		if !connected {
			cleanup()
		}
	}()

	if policy != nil {
		effectivePolicy, policyErr := expandKnownStdioSandboxPolicy(*policy, server, workingDirectory)
		if policyErr != nil {
			return nil, nil, policyErr
		}
		effectivePolicy, policyErr = expandStdioLauncherSandboxPolicy(effectivePolicy, command)
		if policyErr != nil {
			return nil, nil, policyErr
		}
		wrappedCommand, wrappedArgs, wrappedCleanup, nativeUsed, wrapErr := b.sandbox.PrepareExternalCommand(effectivePolicy, command, args, workingDirectory)
		if wrapErr != nil {
			return nil, nil, fmt.Errorf("准备 stdio MCP Sandbox 失败: %w", wrapErr)
		}
		command, args = wrappedCommand, wrappedArgs
		if wrappedCleanup != nil {
			// Seatbelt profile / bwrap 辅助资源必须保留到 Session 真正关闭。officialmcp
			// 可能使用原 command/args 做透明重连；连接建立后立即删除 profile 会让后续
			// reconnect 直接失败。
			cleanups = append(cleanups, wrappedCleanup)
		}
		b.logger.Info(ctx, "stdio MCP Sandbox 已准备", "operation", "mcp.sandbox.prepare", "server_id", server.ID, "backend", policy.Capability.Backend, "native_used", nativeUsed)
	}

	wrappedCommand, wrappedArgs, stderrCapture, captureErr := prepareStdioStderrCapture(command, args)
	if captureErr != nil {
		return nil, nil, captureErr
	}
	command, args = wrappedCommand, wrappedArgs
	if stderrCapture != nil {
		cleanups = append(cleanups, stderrCapture.Cleanup)
	}

	connectCtx := ctx
	cancel := func() {}
	if b.cfg.ConnectTimeoutMS > 0 {
		connectCtx, cancel = context.WithTimeout(ctx, time.Duration(b.cfg.ConnectTimeoutMS)*time.Millisecond)
	}
	defer cancel()

	session, err := officialmcpsession.Connect(connectCtx, officialmcpsession.ServerConfig{
		Name: server.Name,
		Transport: officialmcpsession.TransportConfig{
			Type:    officialmcpsession.TransportStdio,
			Command: command,
			Args:    args,
			CWD:     workingDirectory,
			Env:     environment,
		},
	})
	if err != nil {
		childStderr := ""
		if stderrCapture != nil {
			childStderr = logging.RedactText(stderrCapture.Read(4096), 2048)
		}
		return nil, nil, enrichStdioConnectError(server, err, policy != nil, childStderr)
	}
	connected = true
	b.logger.Info(ctx, "stdio MCP Server 已连接", "operation", "mcp.connect", "server_id", server.ID, "server_key", server.Key)
	return session, cleanup, nil
}

func (b *Backend) connectHTTP(ctx context.Context, server humbertmcp.Server, policy *sandbox.EffectivePolicy) (*officialmcpsession.Session, error) {
	if server.HTTP == nil {
		return nil, errors.New("Streamable HTTP MCP Server 缺少连接配置")
	}

	headers, err := resolveCredentialHeaders(ctx, b.credentials, server)
	if err != nil {
		return nil, err
	}
	var httpClient *http.Client
	if policy == nil {
		httpClient, err = newStreamableHTTPClient(server.HTTP.Endpoint, headers)
	} else {
		allowLocal := policy.NetworkMode == sandbox.NetworkAll
		allowPrivate := policy.NetworkMode == sandbox.NetworkAll
		httpClient, err = newStreamableHTTPClientWithAccess(server.HTTP.Endpoint, headers, allowLocal, allowPrivate)
	}
	if err != nil {
		return nil, fmt.Errorf("创建 Streamable HTTP Client 失败: %w", err)
	}

	connectCtx := ctx
	cancel := func() {}
	if b.cfg.ConnectTimeoutMS > 0 {
		connectCtx, cancel = context.WithTimeout(ctx, time.Duration(b.cfg.ConnectTimeoutMS)*time.Millisecond)
	}
	defer cancel()

	session, err := officialmcpsession.Connect(connectCtx, officialmcpsession.ServerConfig{
		Name: server.Name,
		Transport: officialmcpsession.TransportConfig{
			Type:       officialmcpsession.TransportStreamableHTTP,
			URL:        server.HTTP.Endpoint,
			HTTPClient: httpClient,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("连接 Streamable HTTP MCP Server %q 失败: %w", server.Name, err)
	}
	b.logger.Info(ctx, "Streamable HTTP MCP Server 已连接", "operation", "mcp.connect", "server_id", server.ID, "server_key", server.Key)
	return session, nil
}
