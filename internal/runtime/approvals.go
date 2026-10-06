package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
)

// 本文件负责 Eino checkpoint 暂停、审批等待及恢复。等待审批期间必须继续保留
// Session reservation；恢复只使用原 Turn 的 Snapshot，不重新解析能力或扩大授权。
// ResolveApproval 接受 Vue 对待审批 Tool 的决策，并异步恢复原 Eino Checkpoint。
//
// 方法只等待 Permission Rule（如果有）保存和 Resume worker 启动，不等待后续 Tool/模型完成。
// 同一个 Approval 通过 Manager 的 Pending→Resolving 原子迁移保证最多恢复一次。
func (s *Service) ResolveApproval(ctx context.Context, input ResolveApprovalInput) (ResolveApprovalResult, error) {
	if ctx == nil {
		return ResolveApprovalResult{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return ResolveApprovalResult{}, fmt.Errorf("处理 Approval 被取消: %w", err)
	}
	approvalID := strings.TrimSpace(input.ApprovalID)
	if approvalID == "" {
		return ResolveApprovalResult{}, errors.New("Approval ID 不能为空")
	}
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return ResolveApprovalResult{}, err
	}
	defer finish()

	return s.resolveApproval(ctx, approvalID, input.Decision)
}

// resolveApproval 处理已登记的 Service 操作；权限保存与超时收尾共用同一份 ActiveRun。
func (s *Service) resolveApproval(ctx context.Context, approvalID string, decision approval.Decision) (ResolveApprovalResult, error) {
	request, exists := s.approvals.Get(approvalID)
	if !exists {
		return ResolveApprovalResult{}, fmt.Errorf("%w: %s", approval.ErrNotFound, approvalID)
	}

	// 在创建长期 Rule 前先确认这个 Approval 仍对应当前 ActiveRun，并且 Eino checkpoint
	// 仍然真实存在。这样即使 checkpoint 意外丢失，也不会出现“用户已经授权但 Tool 无法恢复”
	// 的半成功状态，更不会在无法执行本次调用时写入长期 Allow Rule。
	s.mu.Lock()
	active, activeExists := s.activeByRequest[request.RequestID]
	validActive := activeExists && active.waitingApprovalID == approvalID
	s.mu.Unlock()
	if !validActive {
		return ResolveApprovalResult{}, fmt.Errorf("%w: Approval 已不属于活动 Turn", approval.ErrNotPending)
	}
	checkpointExists, err := active.checkpointStore.Has(ctx, active.RunID)
	if err != nil {
		return ResolveApprovalResult{}, fmt.Errorf("检查待恢复 Runtime Checkpoint 失败: %w", err)
	}
	if !checkpointExists {
		return ResolveApprovalResult{}, fmt.Errorf("%w: Runtime Checkpoint 已失效，请重新发送本次请求", approval.ErrNotPending)
	}

	resolution, err := s.approvals.Resolve(ctx, approvalID, decision)
	if err != nil {
		// Resolve 可能在保存失败后从 Resolving 恢复 Pending。即使已过期，也由原
		// worker 重新领取超时责任；不能直接返回并让会话永久停在等待审批。
		s.mu.Lock()
		if current := s.activeByRequest[request.RequestID]; current == active && active.waitingApprovalID == approvalID {
			select {
			case active.approvalRetry <- struct{}{}:
			default:
			}
		}
		s.mu.Unlock()
		return ResolveApprovalResult{}, err
	}

	s.mu.Lock()
	active, activeExists = s.activeByRequest[request.RequestID]
	if s.closed || !activeExists || active.waitingApprovalID != approvalID {
		s.mu.Unlock()
		// Resolve 已经抢到状态，但 Runtime 在极窄窗口内被取消。不能再启动 Tool；把审批标记
		// Cancelled。Agent Scope Rule 若刚被用户明确创建则保留，这是用户主动的长期选择。
		s.approvals.Cancel(approvalID)
		return ResolveApprovalResult{}, fmt.Errorf("%w: Turn 已结束", ErrRunNotFound)
	}
	signalApprovalDoneLocked(active)
	active.waitingApprovalID = ""
	active.phase = RunPhaseRunning
	s.wg.Add(1)
	s.mu.Unlock()

	resolvedRequest := resolution.Request
	resolvedRequest.Status = approval.StatusResolving
	s.publishApprovalEvent(active, EventApprovalResolved, resolvedRequest, decision)

	go s.resumeTurn(active, resolution)
	return ResolveApprovalResult{Approval: resolvedRequest}, nil
}

func (s *Service) registerInterruptedRun(active *activeRun, interrupted *InterruptedExecution) error {
	if interrupted == nil {
		return errors.New("InterruptedExecution 不能为空")
	}
	info := interrupted.Info
	// Agent-as-Tool 的审批会携带子 Agent ID，因此不能再强制等于父 Snapshot AgentID。
	// Request/Run/Session 必须严格匹配；AgentID 非空且来自受信 Tool Guard 的冻结 Scope。
	if info.RequestID != active.RequestID || info.RunID != active.RunID ||
		info.SessionID != active.SessionID || strings.TrimSpace(info.AgentID) == "" {
		return errors.New("Approval Interrupt 身份与当前 Runtime Snapshot 不一致")
	}

	// Interrupt Event 只有在 checkpoint 已经成功写入后才能进入可审批暂停态。过去这里隐式
	// 信任 Eino；现在把它提升为明确 Runtime 边界，避免产生无法 Resume 的悬空 Approval。
	checkpointExists, err := active.checkpointStore.Has(active.ctx, active.RunID)
	if err != nil {
		return fmt.Errorf("检查 Eino Runtime Checkpoint 失败: %w", err)
	}
	if !checkpointExists {
		return errors.New("Eino 返回了 Approval Interrupt，但 Runtime Checkpoint 不存在")
	}

	request, err := s.approvals.Register(active.ctx, info, interrupted.InterruptID)
	if err != nil {
		return err
	}

	done := make(chan struct{})
	retry := make(chan struct{}, 1)
	s.mu.Lock()
	current, exists := s.activeByRequest[active.RequestID]
	if !exists || current != active {
		s.mu.Unlock()
		s.approvals.Cancel(request.ID)
		s.approvals.Forget(request.ID)
		return ErrRunNotFound
	}
	active.waitingApprovalID = request.ID
	active.approvalDone = done
	active.approvalRetry = retry
	active.phase = RunPhaseWaitingApproval
	s.wg.Add(1)
	s.mu.Unlock()
	s.publishApprovalEvent(active, EventApprovalRequested, request, "")

	go s.awaitApproval(active, request, done, retry)
	return nil
}

// awaitApproval 是每个 Pending Approval 唯一的受控超时 worker。
//
// 到期时若权限仍在保存，继续等待处理结果：成功由 done 结束等待，失败由 retry 唤醒，
// 取消由 Run Context 收尾。重试沿用原 ExpiresAt，不延长审批期限，也不另开超时 worker。
// 真正领取超时后以 Approved=false 恢复 Agent，让模型处理未获授权的调用。
func (s *Service) awaitApproval(active *activeRun, request approval.Request, done, retry <-chan struct{}) {
	defer s.wg.Done()

	delay := time.Until(request.ExpiresAt)
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	var resolution approval.Resolution
	for {
		select {
		case <-done:
			return
		case <-active.ctx.Done():
			s.finalizeWaitingCancellation(active, request.ID)
			return
		case <-timer.C:
		case <-retry:
		}
		if time.Now().Before(request.ExpiresAt) {
			continue
		}

		var expired bool
		resolution, expired = s.approvals.Expire(request.ID)
		if expired {
			break
		}
		current, exists := s.approvals.Get(request.ID)
		if !exists || (current.Status != approval.StatusPending && current.Status != approval.StatusResolving) {
			return
		}
		// Resolving 只是保存进行中，不代表已有恢复 worker。Pending 也可能是
		// Expire 返回后才恢复的状态；缓冲中的 retry 会让下一轮继续领取。
	}

	checkpointExists, checkpointErr := active.checkpointStore.Has(context.WithoutCancel(active.ctx), active.RunID)
	if checkpointErr != nil || !checkpointExists {
		if checkpointErr == nil {
			checkpointErr = errors.New("Runtime Checkpoint 已失效")
		}
		s.approvals.Cancel(request.ID)
		s.approvals.Forget(request.ID)
		s.finishRun(
			active,
			ExecutionResult{},
			fmt.Errorf("Approval 超时前检查 Runtime Checkpoint 失败: %w", checkpointErr),
		)
		return
	}

	s.mu.Lock()
	current, exists := s.activeByRequest[active.RequestID]
	if !exists || current != active || active.waitingApprovalID != request.ID {
		s.mu.Unlock()
		return
	}
	active.waitingApprovalID = ""
	active.approvalDone = nil
	active.approvalRetry = nil
	active.phase = RunPhaseRunning
	s.mu.Unlock()

	expiredRequest, _ := s.approvals.Get(request.ID)
	s.publishApprovalEvent(active, EventApprovalExpired, expiredRequest, "")

	// 当前 timeout worker 继续承担 Resume 工作，不再创建额外 goroutine。
	result, runErr := s.executor.Resume(
		active.ctx,
		active.Snapshot,
		active.checkpointStore,
		resolution.Request.InterruptID(),
		resolution.ResumeJSON,
		s.deltaEmitter(active.Snapshot),
	)
	s.handleExecutionOutcome(active, result, runErr, request.ID)
}

func signalApprovalDoneLocked(active *activeRun) {
	if active == nil || active.approvalDone == nil {
		return
	}
	close(active.approvalDone)
	active.approvalDone = nil
	active.approvalRetry = nil
}

func (s *Service) finalizeWaitingCancellation(active *activeRun, approvalID string) {
	if active == nil {
		return
	}

	s.mu.Lock()
	current, exists := s.activeByRequest[active.RequestID]
	if !exists || current != active || active.waitingApprovalID != approvalID {
		s.mu.Unlock()
		return
	}
	active.waitingApprovalID = ""
	signalApprovalDoneLocked(active)
	s.mu.Unlock()

	s.approvals.Cancel(approvalID)
	s.approvals.Forget(approvalID)
	s.finishRun(active, ExecutionResult{}, context.Canceled)
}
