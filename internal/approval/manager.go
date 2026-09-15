package approval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
)

// Manager 管理当前进程中待审批请求，并把用户选择转换成 Permission Rule 与 Eino
// ResumeData。
//
// Manager 的状态迁移是并发安全的：Resolve 会先把 Pending 原子切换成 Resolving，只有
// 一个点击者能够成功取得 Resolution；Runtime 成功/失败恢复后再调用 Complete。这样即使
// Vue 因双击或网络重试重复提交，也不会执行同一个 Tool 两次。
type Manager struct {
	permissions *permission.Engine
	logger      *logging.Logger
	timeout     time.Duration

	mu       sync.RWMutex
	requests map[string]Request
}

// NewManager 创建 Approval Manager。
func NewManager(timeout time.Duration, permissions *permission.Engine, logger *logging.Logger) (*Manager, error) {
	if permissions == nil {
		return nil, errors.New("ApprovalManager PermissionEngine 不能为空")
	}
	if logger == nil {
		return nil, errors.New("ApprovalManager Logger 不能为空")
	}
	if timeout <= 0 {
		return nil, errors.New("ApprovalManager timeout 必须大于 0")
	}
	return &Manager{
		permissions: permissions,
		logger:      logger,
		timeout:     timeout,
		requests:    make(map[string]Request),
	}, nil
}

// UpdateTimeout 更新后续新 Approval Request 使用的等待超时。
//
// 已经处于 Pending 的请求保留创建时确定的 ExpiresAt，不在设置修改后被突然缩短或延长，
// 这样用户看到的当前审批生命周期保持稳定。新的 Tool Approval 会使用最新配置。
func (m *Manager) UpdateTimeout(timeout time.Duration) error {
	if timeout < time.Second || timeout > 24*time.Hour {
		return errors.New("Approval timeout 必须位于 1s-24h 之间")
	}
	m.mu.Lock()
	m.timeout = timeout
	m.mu.Unlock()
	return nil
}

// Register 把一次 Eino root-cause interrupt 注册为待审批请求。
func (m *Manager) Register(
	ctx context.Context,
	info InterruptInfo,
	interruptID string,
	checkpointID string,
) (Request, error) {
	if ctx == nil {
		return Request{}, errors.New("注册 Approval 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return Request{}, fmt.Errorf("注册 Approval 被取消: %w", err)
	}
	if err := info.Validate(); err != nil {
		return Request{}, err
	}
	interruptID = strings.TrimSpace(interruptID)
	checkpointID = strings.TrimSpace(checkpointID)
	if interruptID == "" || checkpointID == "" {
		return Request{}, errors.New("注册 Approval 失败: interruptID/checkpointID 不能为空")
	}

	m.mu.RLock()
	timeout := m.timeout
	m.mu.RUnlock()

	now := time.Now().UTC()
	request := Request{
		ID:           info.ApprovalID,
		RequestID:    info.RequestID,
		RunID:        info.RunID,
		SessionID:    info.SessionID,
		AgentID:      info.AgentID,
		ToolName:     info.ToolName,
		Risk:         info.Risk,
		Presentation: info.Presentation,
		Status:       StatusPending,
		CreatedAt:    now,
		ExpiresAt:    now.Add(timeout),
		interruptID:  interruptID,
		checkpointID: checkpointID,
		identity:     info.Identity,
	}

	m.mu.Lock()
	if existing, exists := m.requests[request.ID]; exists && existing.Status == StatusPending {
		m.mu.Unlock()
		return Request{}, fmt.Errorf("Approval %q 已存在待处理请求", request.ID)
	}
	m.requests[request.ID] = request
	m.mu.Unlock()

	m.logger.Info(
		ctx,
		"Tool 调用正在等待用户审批",
		"operation", "approval.request.register",
		"request_id", request.RequestID,
		"run_id", request.RunID,
		"session_id", request.SessionID,
		"agent_id", request.AgentID,
		"approval_id", request.ID,
		"tool", request.ToolName,
		"risk", request.Risk,
	)
	return request, nil
}

// Resolve 原子接受用户决策并返回 Runtime 恢复参数。
//
// 对 allow_session/allow_agent，会先创建 Allow Rule；deny_agent 会先创建持久化 Deny Rule。
// 任何规则落盘失败时，本次审批恢复 Pending，用户可以重试，绝不会出现 UI 已显示长期结论
// 但 Permission Policy 实际没有保存的不一致。
func (m *Manager) Resolve(ctx context.Context, approvalID string, decision Decision) (Resolution, error) {
	if ctx == nil {
		return Resolution{}, errors.New("处理 Approval 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return Resolution{}, fmt.Errorf("处理 Approval 被取消: %w", err)
	}
	if err := decision.Validate(); err != nil {
		return Resolution{}, err
	}
	approvalID = strings.TrimSpace(approvalID)

	m.mu.Lock()
	request, exists := m.requests[approvalID]
	if !exists {
		m.mu.Unlock()
		return Resolution{}, fmt.Errorf("%w: %s", ErrNotFound, approvalID)
	}
	if request.Status != StatusPending {
		m.mu.Unlock()
		return Resolution{}, fmt.Errorf("%w: %s status=%s", ErrNotPending, approvalID, request.Status)
	}
	if time.Now().UTC().After(request.ExpiresAt) {
		request.Status = StatusExpired
		m.requests[approvalID] = request
		m.mu.Unlock()
		return Resolution{}, fmt.Errorf("%w: %s status=%s", ErrNotPending, approvalID, StatusExpired)
	}
	request.Status = StatusResolving
	m.requests[approvalID] = request
	m.mu.Unlock()

	approved := decision != DecisionDeny && decision != DecisionDenyAgent
	grant := permission.ApprovalGrant{
		Scope: permission.GrantOnce,
		Request: InterruptInfo{
			RequestID: request.RequestID,
			RunID:     request.RunID,
			SessionID: request.SessionID,
			AgentID:   request.AgentID,
			ToolName:  request.ToolName,
			Risk:      request.Risk,
			Identity:  request.identity,
		}.PermissionRequest(),
	}

	switch decision {
	case DecisionAllowSession:
		grant.Scope = permission.GrantSession
	case DecisionAllowAgent:
		grant.Scope = permission.GrantAgent
	case DecisionDenyAgent:
		grant.Scope = permission.GrantAgent
		if _, err := m.permissions.DenyAgent(ctx, grant); err != nil {
			m.restorePending(approvalID)
			return Resolution{}, fmt.Errorf("保存 Approval 长期拒绝失败: %w", err)
		}
	}

	if approved {
		if _, err := m.permissions.Grant(ctx, grant); err != nil {
			m.restorePending(approvalID)
			return Resolution{}, fmt.Errorf("保存 Approval 授权失败: %w", err)
		}
	}

	resumeJSON, err := EncodeResumeData(approved)
	if err != nil {
		m.restorePending(approvalID)
		return Resolution{}, err
	}

	m.logger.Info(
		ctx,
		"用户已处理 Tool Approval",
		"operation", "approval.request.resolve",
		"request_id", request.RequestID,
		"run_id", request.RunID,
		"session_id", request.SessionID,
		"agent_id", request.AgentID,
		"approval_id", request.ID,
		"tool", request.ToolName,
		"decision", decision,
	)
	return Resolution{Request: request, Approved: approved, ResumeJSON: resumeJSON}, nil
}

// Complete 在 Runtime 完成用户主动 Resume（无论最终成功或失败）后把 Resolving 请求
// 标记为 Resolved。已经 Expired/Cancelled 的请求保持原终态，便于 UI 与审计区分原因。
func (m *Manager) Complete(approvalID string) {
	approvalID = strings.TrimSpace(approvalID)
	m.mu.Lock()
	request, ok := m.requests[approvalID]
	if ok && request.Status == StatusResolving {
		request.Status = StatusResolved
		m.requests[approvalID] = request
	}
	m.mu.Unlock()
}

// Cancel 在用户取消 Turn 或应用关闭时把仍待处理/恢复中的请求标记为 Cancelled。
//
// 已经 Resolved/Expired 的终态不会被覆盖，避免“先超时、后收到取消信号”把真实终态
// 改写成 Cancelled。Resolving 仍可取消，因为此时 Runtime 还没有真正恢复 Tool。
func (m *Manager) Cancel(approvalID string) {
	approvalID = strings.TrimSpace(approvalID)
	m.mu.Lock()
	request, ok := m.requests[approvalID]
	if ok && (request.Status == StatusPending || request.Status == StatusResolving) {
		request.Status = StatusCancelled
		m.requests[approvalID] = request
	}
	m.mu.Unlock()
}

// Forget 释放已经进入终态的 Approval Runtime State。
//
// Approval 不属于持久化事实；前端已经通过 runtime event 收到终态后，Runtime 应调用本方法
// 删除进程内对象，避免长时间运行的桌面应用因为大量审批累积无界内存。Pending/Resolving 请求
// 不允许删除，防止仍可恢复的 checkpoint 失去控制状态。
func (m *Manager) Forget(approvalID string) {
	approvalID = strings.TrimSpace(approvalID)
	m.mu.Lock()
	request, ok := m.requests[approvalID]
	if ok && request.Status != StatusPending && request.Status != StatusResolving {
		delete(m.requests, approvalID)
	}
	m.mu.Unlock()
}

// Expire 把仍 Pending 的请求原子标记为 Expired，并生成 Approved=false 的恢复参数。
//
// 超时不是简单取消整个 Turn：Runtime 会像“用户拒绝本次调用”一样恢复 Tool，让模型有机会
// 解释未执行原因或选择只读替代方案。只有真正抢到 Pending 状态的超时任务会得到 ok=true。
func (m *Manager) Expire(approvalID string) (resolution Resolution, ok bool, err error) {
	approvalID = strings.TrimSpace(approvalID)
	m.mu.Lock()
	request, exists := m.requests[approvalID]
	if !exists || request.Status != StatusPending {
		m.mu.Unlock()
		return Resolution{}, false, nil
	}
	request.Status = StatusExpired
	m.requests[approvalID] = request
	m.mu.Unlock()

	resumeJSON, err := EncodeResumeData(false)
	if err != nil {
		return Resolution{}, false, err
	}
	return Resolution{Request: request, Approved: false, ResumeJSON: resumeJSON}, true, nil
}

// Get 返回安全的 Request Snapshot。
func (m *Manager) Get(approvalID string) (Request, bool) {
	m.mu.RLock()
	request, ok := m.requests[strings.TrimSpace(approvalID)]
	m.mu.RUnlock()
	return request, ok
}

// ListPending 返回设置/恢复 UI 可使用的待审批快照，按创建时间稳定排序。
func (m *Manager) ListPending() []Request {
	m.mu.RLock()
	result := make([]Request, 0, len(m.requests))
	for _, request := range m.requests {
		if request.Status == StatusPending {
			result = append(result, request)
		}
	}
	m.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

func (m *Manager) restorePending(approvalID string) {
	m.mu.Lock()
	request, ok := m.requests[approvalID]
	if ok && request.Status == StatusResolving {
		request.Status = StatusPending
		m.requests[approvalID] = request
	}
	m.mu.Unlock()
}
