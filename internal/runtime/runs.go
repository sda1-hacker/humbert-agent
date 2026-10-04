package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
)

// 本文件串联执行、恢复、取消和最终清理。Service 是唯一运行状态所有者，
// 每次终态清理只发生一次，关闭流程必须等待初始化及所有受控 worker 结束。
// CancelTurn 请求取消一个运行或等待审批中的 Turn。
//
// 执行阶段只发送 Context cancel，由 Executor worker 负责统一终态；等待审批阶段没有执行
// worker，因此本方法需要主动清理 Approval/Checkpoint/Session reservation 并发布 cancelled。
func (s *Service) CancelTurn(requestID string) error {
	_, finish, err := s.beginOperation(context.Background())
	if err != nil {
		return err
	}
	defer finish()
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return errors.New("Request ID 不能为空")
	}

	s.mu.Lock()
	active, exists := s.activeByRequest[requestID]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, requestID)
	}
	waitingApprovalID := active.waitingApprovalID
	active.phase = RunPhaseCancelling
	if waitingApprovalID != "" {
		signalApprovalDoneLocked(active)
		active.waitingApprovalID = ""
	}
	s.mu.Unlock()

	active.cancel()
	if waitingApprovalID == "" {
		return nil
	}

	s.approvals.Cancel(waitingApprovalID)
	request, _ := s.approvals.Get(waitingApprovalID)
	s.publishEvent(Event{
		Type:             EventApprovalResolved,
		RequestID:        active.RequestID,
		RunID:            active.RunID,
		SessionID:        active.SessionID,
		AgentID:          active.snapshot.AgentID,
		ModelID:          active.snapshot.ModelID,
		ModelRevision:    active.snapshot.ModelRevision,
		ToolRevision:     active.snapshot.ToolRevision,
		Approval:         &request,
		ApprovalDecision: approval.DecisionDeny,
		OccurredAt:       time.Now().UTC().Format(time.RFC3339Nano),
	})
	s.approvals.Forget(waitingApprovalID)
	s.finishRun(active, ExecutionResult{}, context.Canceled)
	return nil
}

// executeTurn 是新 User Turn 的首次 Eino worker。
func (s *Service) executeTurn(active *activeRun) {
	defer s.wg.Done()

	snapshot := active.snapshot
	runtimeManifest := snapshot.Manifest
	s.publishEvent(Event{
		Type:                 EventTurnStarted,
		RequestID:            snapshot.RequestID,
		RunID:                snapshot.RunID,
		SessionID:            snapshot.SessionID,
		AgentID:              snapshot.AgentID,
		ModelID:              snapshot.ModelID,
		ModelRevision:        snapshot.ModelRevision,
		ToolRevision:         snapshot.ToolRevision,
		BuiltinToolNames:     append([]string(nil), snapshot.BuiltinToolNames...),
		SandboxProfile:       string(snapshot.Sandbox.Profile),
		SandboxNetworkMode:   string(snapshot.Sandbox.NetworkMode),
		SandboxNativeBackend: snapshot.Sandbox.Capability.Backend,
		SkillRevision:        snapshot.SkillRevision,
		SkillNames:           append([]string(nil), snapshot.SkillNames...),
		MCPRevision:          snapshot.MCPRevision,
		MCPServers:           append([]humbertmcp.RuntimeServerSnapshot(nil), snapshot.MCPServers...),
		MCPTools:             append([]humbertmcp.RuntimeToolSnapshot(nil), snapshot.MCPTools...),
		MCPToolNames:         append([]string(nil), snapshot.MCPToolNames...),
		Runtime:              &runtimeManifest,
		OccurredAt:           time.Now().UTC().Format(time.RFC3339Nano),
	})

	result, err := s.executor.Execute(active.ctx, snapshot, active.checkpointStore, s.deltaEmitter(snapshot))
	s.handleExecutionOutcome(active, result, err, "")
}

// resumeTurn 从用户审批或超时拒绝产生的 Resolution 恢复同一个 User Turn。
func (s *Service) resumeTurn(active *activeRun, resolution approval.Resolution) {
	defer s.wg.Done()

	result, err := s.executor.Resume(
		active.ctx,
		active.snapshot,
		active.checkpointStore,
		resolution.Request.InterruptID(),
		resolution.ResumeJSON,
		s.deltaEmitter(active.snapshot),
	)
	// 用户主动 Resolve 会从 Resolving 进入 Resolved；超时请求保持 Expired。
	s.approvals.Complete(resolution.Request.ID)
	s.handleExecutionOutcome(active, result, err, resolution.Request.ID)
}

// handleExecutionOutcome 把 Execute/Resume 的三种结果收敛为：失败、再次中断、正常完成。
func (s *Service) handleExecutionOutcome(active *activeRun, result ExecutionResult, runErr error, previousApprovalID string) {
	// 前一个审批已经通过 resolved/expired event 对前端可见；本次 Resume 产生结果后即可
	// 释放其进程内状态。若再次发生新的 Approval，新的请求会拥有独立 ID。
	if previousApprovalID != "" {
		defer s.approvals.Forget(previousApprovalID)
	}
	if runErr != nil {
		s.finishRun(active, result, runErr)
		return
	}
	if result.Interrupted != nil {
		if err := s.registerInterruptedRun(active, result.Interrupted); err != nil {
			s.finishRun(active, result, fmt.Errorf("注册 Tool Approval 失败: %w", err))
		}
		return
	}

	s.completeTurn(active, result)
}

func (s *Service) completeTurn(active *activeRun, result ExecutionResult) {
	snapshot := active.snapshot
	s.mu.Lock()
	if active.phase != RunPhaseCancelling {
		active.phase = RunPhaseMaintaining
	}
	s.mu.Unlock()
	s.publishEvent(Event{
		Type: EventTurnMaintaining, RequestID: active.RequestID, RunID: active.RunID,
		SessionID: active.SessionID, AgentID: snapshot.AgentID, MessageID: result.MessageID,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	maintenanceCtx, maintenanceCancel := context.WithTimeout(active.ctx, s.resolver.OperationTimeout())
	if maintenanceErr := s.resolver.MaintainAfterTurn(maintenanceCtx, snapshot); maintenanceErr != nil {
		s.logger.Warn(
			maintenanceCtx,
			"Turn 完成后的 Context 维护失败",
			"operation", "runtime.turn.maintenance",
			"request_id", snapshot.RequestID,
			"run_id", snapshot.RunID,
			"session_id", snapshot.SessionID,
			"agent_id", snapshot.AgentID,
			"error", maintenanceErr,
		)
	}
	maintenanceCancel()
	if err := active.ctx.Err(); err != nil {
		s.finishRun(active, result, err)
		return
	}

	s.finishRun(active, result, nil)
}

func (s *Service) cleanupRun(active *activeRun) {
	if active == nil {
		return
	}
	active.cancel()
	_ = active.checkpointStore.Delete(context.Background(), active.RunID)

	s.notifyRunFinished(active.RequestID)

	// 必要收尾完成后同时释放两个索引；旧收尾不能删除新运行的 reservation。
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeByRequest[active.RequestID] == active {
		signalApprovalDoneLocked(active)
		delete(s.activeByRequest, active.RequestID)
	}
	if s.activeBySession[active.SessionID] == active.RequestID {
		delete(s.activeBySession, active.SessionID)
		delete(s.reservationAgents, active.SessionID)
	}
}

func (s *Service) notifyRunFinished(requestID string) {
	s.mu.Lock()
	observers := append([]RunLifecycleObserver(nil), s.lifecycleObservers...)
	s.mu.Unlock()
	for _, observer := range observers {
		observer.ParentRunFinished(context.Background(), requestID)
	}
}

// Close 停止接受新 Turn，取消所有运行中的 Turn，并等待受控 Worker 回收。
func (s *Service) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}

	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.rootCancel()
		// 所有 Close 调用共享同一个等待者；首次等待超时不代表 Worker 已经退出。
		go func() {
			s.wg.Wait()
			close(s.closeDone)
		}()
	}
	done := s.closeDone
	s.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等待 RuntimeService 关闭超时: %w", ctx.Err())
	}
}
