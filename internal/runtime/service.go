package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
)

// activeRun 描述一个可能经历多次 Human Approval 暂停/恢复的 User Turn。
//
// Snapshot、Context 和 CheckpointStore 在整个 Turn 生命周期内保持不变。等待审批时没有
// Executor goroutine 在运行，但 Session reservation 与 activeRun 必须继续保留；否则用户可
// 在同一 Session 启动第二个 Turn，破坏 checkpoint 对应的 ActiveBranch。
type activeRun struct {
	RequestID string
	RunID     string
	SessionID string

	ctx    context.Context
	cancel context.CancelFunc

	snapshot        *Snapshot
	checkpointStore *approval.CheckpointStore
	startedAt       time.Time
	phase           RunPhase

	// waitingApprovalID 非空表示当前没有 Executor worker，Run 正停在该审批点。字段只在
	// Service.mu 下访问。approvalDone 用来停止该请求对应的 timeout worker。
	waitingApprovalID string
	approvalDone      chan struct{}
}

// Service 是 Humbert 唯一的 Agent Runtime Service。
//
// 它负责把一次用户操作串成清晰的运行链：
//
//	AppendUserMessage
//	       ↓
//	Resolve immutable Snapshot from current Tree branch
//	       ↓
//	Executor
//	       ├─ realtime EventBus（只给 UI）
//	       └─ SessionManager（只写完整终态消息）
//
// turn.started / turn.completed / tool.started 等事件属于实时 UI 协议，不进入 Session
// JSONL；执行耗时、失败原因等运行审计进入统一结构化日志，不再维护 runstore/runtrace。
type Service struct {
	resolver *Resolver

	executor *Executor

	sessions *sessions.Service

	events *eventbus.Bus

	logger *logging.Logger

	approvals *approval.Manager

	rootCtx context.Context

	rootCancel context.CancelFunc

	mu sync.Mutex

	closed bool

	// activeBySession 保证同一个 Session 同时最多一个用户 Turn。
	activeBySession map[string]string

	// activeByRequest 支持 CancelTurn(requestID)。
	activeByRequest map[string]*activeRun

	// reservationAgents 记录 Session 占用所属 Agent，使删除 Agent 可以在同一把锁下
	// 拒绝已有操作。deletingAgents 则阻止删除期间启动新的 Turn、压缩或 Session 删除。
	reservationAgents map[string]string
	deletingAgents    map[string]string

	wg        sync.WaitGroup
	closeDone chan struct{}
}

// NewService 创建 RuntimeService。
func NewService(
	resolver *Resolver,
	executor *Executor,
	sessionService *sessions.Service,
	events *eventbus.Bus,
	logger *logging.Logger,
	approvalManager *approval.Manager,
) *Service {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &Service{
		resolver:          resolver,
		executor:          executor,
		sessions:          sessionService,
		events:            events,
		logger:            logger,
		approvals:         approvalManager,
		rootCtx:           rootCtx,
		rootCancel:        rootCancel,
		activeBySession:   make(map[string]string),
		activeByRequest:   make(map[string]*activeRun),
		reservationAgents: make(map[string]string),
		deletingAgents:    make(map[string]string),
		closeDone:         make(chan struct{}),
	}
}

// StartTurn 持久化用户消息并异步启动一次 Agent Turn。
//
// 顺序固定为：reserve session -> append UserMessage -> resolve Snapshot -> register run ->
// start controlled worker。UserMessage 必须先落盘，因为 Resolver 只从 Session Active
// Branch 构造给 Eino 的上下文，不再额外拼接 pending user content。
func (s *Service) StartTurn(ctx context.Context, input StartTurnInput) (StartTurnResult, error) {
	if ctx == nil {
		return StartTurnResult{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return StartTurnResult{}, fmt.Errorf("启动 Agent Turn 被取消: %w", err)
	}
	if s.resolver == nil || s.executor == nil || s.sessions == nil || s.events == nil || s.logger == nil || s.approvals == nil {
		return StartTurnResult{}, errors.New("RuntimeService 尚未正确初始化")
	}

	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return StartTurnResult{}, errors.New("Session ID 不能为空")
	}
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return StartTurnResult{}, err
	}
	defer finish()

	requestID := uuid.NewString()
	runID := uuid.NewString()

	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return StartTurnResult{}, err
	}
	if err := s.reserveAgentSession(sessionID, session.AgentID, requestID); err != nil {
		return StartTurnResult{}, err
	}
	reserved := true
	defer func() {
		if reserved {
			s.releaseReservation(sessionID, requestID)
		}
	}()

	userMessage, err := s.sessions.PrepareUserMessage(ctx, sessionID, input.Input, input.RetryUserMessageID)
	if err != nil {
		return StartTurnResult{}, fmt.Errorf("持久化 UserMessage 失败: %w", err)
	}
	receipt := StartTurnResult{RequestID: requestID, RunID: runID, SessionID: sessionID, UserMessageID: userMessage.EntryID}

	snapshot, err := s.resolver.ResolveTurn(ctx, requestID, runID, sessionID)
	if err != nil {
		s.logger.Warn(
			ctx,
			"Agent Turn Snapshot 解析失败",
			"operation", "runtime.turn.resolve",
			"request_id", requestID,
			"run_id", runID,
			"session_id", sessionID,
			"user_entry_id", userMessage.EntryID,
			"error", err,
		)
		receipt.StartError = runtimeUserVisibleError(err)
		return receipt, fmt.Errorf("解析 Agent Runtime Snapshot 失败: %w", err)
	}

	runCtx, cancel := context.WithCancel(s.rootCtx)
	active := &activeRun{
		RequestID:       requestID,
		RunID:           runID,
		SessionID:       sessionID,
		ctx:             runCtx,
		cancel:          cancel,
		snapshot:        snapshot,
		checkpointStore: approval.NewCheckpointStore(),
		startedAt:       time.Now(),
		phase:           RunPhaseRunning,
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		receipt.StartError = "应用正在关闭，消息已保存。"
		return receipt, ErrClosed
	}
	s.activeByRequest[requestID] = active
	s.wg.Add(1)
	s.mu.Unlock()

	go s.executeTurn(active)
	reserved = false

	s.logger.Info(
		ctx,
		"Agent Turn 已启动",
		"operation", "runtime.turn.start",
		"request_id", requestID,
		"run_id", runID,
		"session_id", sessionID,
		"agent_id", snapshot.AgentID,
		"model_id", snapshot.ModelID,
		"model_revision", snapshot.ModelRevision,
		"tool_revision", snapshot.ToolRevision,
		"builtin_tools", snapshot.BuiltinToolNames,
		"sandbox_profile", snapshot.Sandbox.Profile,
		"sandbox_network_mode", snapshot.Sandbox.NetworkMode,
		"sandbox_native_backend", snapshot.Sandbox.Capability.Backend,
		"skill_revision", snapshot.SkillRevision,
		"skill_names", snapshot.SkillNames,
		"mcp_revision", snapshot.MCPRevision,
		"mcp_servers", snapshot.MCPServers,
		"mcp_unavailable", snapshot.MCPUnavailable,
		"mcp_tools", snapshot.MCPTools,
		"mcp_tool_names", snapshot.MCPToolNames,
		"user_entry_id", userMessage.EntryID,
	)

	return StartTurnResult{
		RequestID:       requestID,
		RunID:           runID,
		SessionID:       sessionID,
		UserMessageID:   userMessage.EntryID,
		ContextUsage:    snapshot.ContextUsage,
		ContextAssembly: snapshot.ContextAssembly,
		Runtime:         snapshot.Manifest,
	}, nil
}

// ContextStatus 返回当前 Session 的实时 Context 使用情况。
//
// 该查询只读取配置、Tool schema、Transcript 和 Session Memory，不执行模型调用，也不会
// 自动触发压缩。允许在 Turn 运行期间读取；TranscriptStore 自身会提供安全的文件级并发
// 读取语义。
func (s *Service) ContextStatus(ctx context.Context, sessionID string) (contextengine.Usage, error) {
	if ctx == nil {
		return contextengine.Usage{}, errors.New("context.Context 不能为空")
	}
	if s.resolver == nil {
		return contextengine.Usage{}, errors.New("RuntimeService 尚未正确初始化")
	}
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return contextengine.Usage{}, err
	}
	defer finish()
	usage, err := s.resolver.ContextStatus(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return contextengine.Usage{}, fmt.Errorf("读取 Session Context 状态失败: %w", err)
	}
	return usage, nil
}

// ContextOverview 返回 Context Usage 与下一次 Runtime Resolve 的能力清单。
//
// 这是只读状态接口，不占用 Session Turn reservation；运行中的 Turn 可以继续使用已经冻结的
// Snapshot，而 Overview 始终描述“如果现在开始下一 Turn”会使用的当前配置。
func (s *Service) ContextOverview(ctx context.Context, sessionID string) (ContextOverview, error) {
	if ctx == nil {
		return ContextOverview{}, errors.New("context.Context 不能为空")
	}
	if s.resolver == nil {
		return ContextOverview{}, errors.New("RuntimeService 尚未正确初始化")
	}
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return ContextOverview{}, err
	}
	defer finish()
	sessionID = strings.TrimSpace(sessionID)
	overview, err := s.resolver.ContextOverview(ctx, sessionID)
	if err != nil {
		return ContextOverview{}, fmt.Errorf("读取 Session Context Overview 失败: %w", err)
	}
	overview.Active = s.activeRunStatus(sessionID)
	return overview, nil
}

// ManualCompact 串行执行用户主动触发的 Context 压缩。
//
// 手动压缩和正常 Turn 共用 activeBySession 互斥语义：压缩期间拒绝启动新的 User Turn，
// 运行中的 Turn 也不允许被手动压缩，从而保证摘要生成和 JSONL Commit 基于稳定分支。
func (s *Service) ManualCompact(
	ctx context.Context,
	sessionID string,
	updateMemory bool,
) (ManualCompactionResult, error) {
	if ctx == nil {
		return ManualCompactionResult{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return ManualCompactionResult{}, fmt.Errorf("手动压缩被取消: %w", err)
	}
	if s.resolver == nil {
		return ManualCompactionResult{}, errors.New("RuntimeService 尚未正确初始化")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ManualCompactionResult{}, errors.New("Session ID 不能为空")
	}

	requestID := "manual-compaction:" + uuid.NewString()
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return ManualCompactionResult{}, err
	}
	defer finish()
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return ManualCompactionResult{}, err
	}
	if err := s.reserveAgentSession(sessionID, session.AgentID, requestID); err != nil {
		return ManualCompactionResult{}, err
	}
	defer s.releaseReservation(sessionID, requestID)

	startedAt := time.Now()
	result, err := s.resolver.ManualCompact(ctx, sessionID, updateMemory)
	if err != nil {
		return ManualCompactionResult{}, err
	}
	s.logger.Info(
		ctx,
		"用户手动 Context 压缩完成",
		"operation", "runtime.context.manual_compact",
		"session_id", sessionID,
		"update_memory", updateMemory,
		"compacted", result.Compaction.Compacted,
		"memory_updated", result.Memory.Updated,
		logging.Duration(startedAt),
	)
	return result, nil
}

// DeleteSession 与 Turn/手动压缩共用占用锁，后端拒绝删除正在使用的会话。
// 前端的运行状态只用于交互提示，不能作为删除历史的并发保护。
func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer finish()
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("Session ID 不能为空")
	}
	requestID := "delete:" + uuid.NewString()
	if err := s.ensureSessionUnreserved(sessionID); err != nil {
		return err
	}
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := s.reserveAgentSession(sessionID, session.AgentID, requestID); err != nil {
		return err
	}
	defer s.releaseReservation(sessionID, requestID)
	return s.sessions.Delete(ctx, sessionID)
}

// DeleteAgent 协调 Runtime 占用与 Agent 的可恢复删除状态机。
//
// 删除标记设置与 Session reservation 使用同一把锁：已有操作会阻止删除，删除中的 Agent
// 也不会接受新操作。真正的持久化删除由 deleteAgent 回调完成。
func (s *Service) DeleteAgent(
	ctx context.Context,
	agentID string,
	deleteAgent func(context.Context) error,
) ([]string, error) {
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()

	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("Agent ID 不能为空")
	}
	if deleteAgent == nil {
		return nil, errors.New("Agent 删除回调不能为空")
	}

	requestID := "delete-agent:" + uuid.NewString()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if existing, exists := s.deletingAgents[agentID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: request_id=%s", ErrAgentDeleting, existing)
	}
	for sessionID, reservedAgentID := range s.reservationAgents {
		if reservedAgentID == agentID {
			existing := s.activeBySession[sessionID]
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: session_id=%s request_id=%s", ErrSessionBusy, sessionID, existing)
		}
	}
	s.deletingAgents[agentID] = requestID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if current := s.deletingAgents[agentID]; current == requestID {
			delete(s.deletingAgents, agentID)
		}
		s.mu.Unlock()
	}()

	ids, err := s.sessions.ListIDs(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if err := deleteAgent(ctx); err != nil {
		return ids, err
	}
	return ids, nil
}

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
	s.finishCancelledWaitingRun(active)
	return nil
}

// ResolveApproval 接受 Vue 对待审批 Tool 的决策，并异步恢复原 Eino Checkpoint。
//
// 方法只等待 Permission Rule（如果有）保存和 Resume worker 启动，不等待后续 Tool/模型完成。
// 同一个 Approval 通过 Manager 的 Pending→Resolving 原子迁移保证最多恢复一次。
func (s *Service) ResolveApproval(
	ctx context.Context,
	input ResolveApprovalInput,
) (ResolveApprovalResult, error) {
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

	resolution, err := s.approvals.Resolve(ctx, approvalID, input.Decision)
	if err != nil {
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
	s.publishEvent(Event{
		Type:             EventApprovalResolved,
		RequestID:        active.RequestID,
		RunID:            active.RunID,
		SessionID:        active.SessionID,
		AgentID:          active.snapshot.AgentID,
		ModelID:          active.snapshot.ModelID,
		ModelRevision:    active.snapshot.ModelRevision,
		ToolRevision:     active.snapshot.ToolRevision,
		Approval:         &resolvedRequest,
		ApprovalDecision: input.Decision,
		OccurredAt:       time.Now().UTC().Format(time.RFC3339Nano),
	})

	go s.resumeTurn(active, resolution)
	return ResolveApprovalResult{Approval: resolvedRequest}, nil
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

	result, err := s.executor.Execute(
		active.ctx,
		snapshot,
		active.checkpointStore,
		s.deltaEmitter(snapshot),
	)
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
func (s *Service) handleExecutionOutcome(
	active *activeRun,
	result ExecutionResult,
	runErr error,
	previousApprovalID string,
) {
	// 前一个审批已经通过 resolved/expired event 对前端可见；本次 Resume 产生结果后即可
	// 释放其进程内状态。若再次发生新的 Approval，新的请求会拥有独立 ID。
	if previousApprovalID != "" {
		defer s.approvals.Forget(previousApprovalID)
	}
	if runErr != nil {
		s.handleExecutionError(active.snapshot, result, runErr, active.startedAt)
		s.cleanupRun(active)
		return
	}
	if result.Interrupted != nil {
		if err := s.registerInterruptedRun(active, result.Interrupted); err != nil {
			s.handleExecutionError(
				active.snapshot,
				result,
				fmt.Errorf("注册 Tool Approval 失败: %w", err),
				active.startedAt,
			)
			s.cleanupRun(active)
		}
		return
	}

	s.completeTurn(active, result)
}

func (s *Service) registerInterruptedRun(active *activeRun, interrupted *InterruptedExecution) error {
	if interrupted == nil {
		return errors.New("InterruptedExecution 不能为空")
	}
	info := interrupted.Info
	if info.RequestID != active.RequestID || info.RunID != active.RunID ||
		info.SessionID != active.SessionID || info.AgentID != active.snapshot.AgentID {
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

	request, err := s.approvals.Register(
		active.ctx,
		info,
		interrupted.InterruptID,
		active.RunID,
	)
	if err != nil {
		return err
	}

	done := make(chan struct{})
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
	active.phase = RunPhaseWaitingApproval
	s.wg.Add(1)
	s.mu.Unlock()
	s.publishEvent(Event{
		Type:          EventApprovalRequested,
		RequestID:     active.RequestID,
		RunID:         active.RunID,
		SessionID:     active.SessionID,
		AgentID:       active.snapshot.AgentID,
		ModelID:       active.snapshot.ModelID,
		ModelRevision: active.snapshot.ModelRevision,
		ToolRevision:  active.snapshot.ToolRevision,
		Approval:      &request,
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})

	go s.awaitApproval(active, request, done)
	return nil
}

// awaitApproval 是每个 Pending Approval 唯一的受控超时 worker。
//
// 它只等待三个事件：用户已经处理、Run Context 取消、到达 ExpiresAt。所有分支都退出并由
// Service.wg 回收，不会创建悬挂 goroutine。超时会以 Approved=false 恢复 Agent，而不是
// 把整个 Turn 直接判为失败。
func (s *Service) awaitApproval(active *activeRun, request approval.Request, done <-chan struct{}) {
	defer s.wg.Done()

	delay := time.Until(request.ExpiresAt)
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-done:
		return
	case <-active.ctx.Done():
		s.finalizeWaitingCancellation(active, request.ID)
		return
	case <-timer.C:
	}

	checkpointExists, checkpointErr := active.checkpointStore.Has(context.WithoutCancel(active.ctx), active.RunID)
	if checkpointErr != nil || !checkpointExists {
		if checkpointErr == nil {
			checkpointErr = errors.New("Runtime Checkpoint 已失效")
		}
		s.approvals.Cancel(request.ID)
		s.approvals.Forget(request.ID)
		s.handleExecutionError(
			active.snapshot,
			ExecutionResult{},
			fmt.Errorf("Approval 超时前检查 Runtime Checkpoint 失败: %w", checkpointErr),
			active.startedAt,
		)
		s.cleanupRun(active)
		return
	}

	resolution, expired, err := s.approvals.Expire(request.ID)
	if err != nil {
		s.logger.Error(
			context.Background(),
			"生成 Approval 超时恢复参数失败",
			"operation", "approval.expire",
			"request_id", active.RequestID,
			"run_id", active.RunID,
			"session_id", active.SessionID,
			"approval_id", request.ID,
			"error", err,
		)
		active.cancel()
		s.finalizeWaitingCancellation(active, request.ID)
		return
	}
	if !expired {
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
	active.phase = RunPhaseRunning
	s.mu.Unlock()

	expiredRequest, _ := s.approvals.Get(request.ID)
	s.publishEvent(Event{
		Type:          EventApprovalExpired,
		RequestID:     active.RequestID,
		RunID:         active.RunID,
		SessionID:     active.SessionID,
		AgentID:       active.snapshot.AgentID,
		ModelID:       active.snapshot.ModelID,
		ModelRevision: active.snapshot.ModelRevision,
		ToolRevision:  active.snapshot.ToolRevision,
		Approval:      &expiredRequest,
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})

	// 当前 timeout worker 继续承担 Resume 工作，不再创建额外 goroutine。
	result, runErr := s.executor.Resume(
		active.ctx,
		active.snapshot,
		active.checkpointStore,
		resolution.Request.InterruptID(),
		resolution.ResumeJSON,
		s.deltaEmitter(active.snapshot),
	)
	s.handleExecutionOutcome(active, result, runErr, request.ID)
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
			"Turn 完成后的 Context/Memory 维护失败",
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
		s.handleExecutionError(snapshot, result, err, active.startedAt)
		s.cleanupRun(active)
		return
	}

	s.publishEvent(Event{
		Type:          EventTurnCompleted,
		RequestID:     snapshot.RequestID,
		RunID:         snapshot.RunID,
		SessionID:     snapshot.SessionID,
		AgentID:       snapshot.AgentID,
		ModelID:       snapshot.ModelID,
		ModelRevision: snapshot.ModelRevision,
		ToolRevision:  snapshot.ToolRevision,
		MessageID:     result.MessageID,
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})

	s.logger.Info(
		context.Background(),
		"Agent Turn 已完成",
		"operation", "runtime.turn.complete",
		"request_id", snapshot.RequestID,
		"run_id", snapshot.RunID,
		"session_id", snapshot.SessionID,
		"agent_id", snapshot.AgentID,
		"message_id", result.MessageID,
		logging.Duration(active.startedAt),
	)
	s.cleanupRun(active)
}

func (s *Service) deltaEmitter(snapshot *Snapshot) DeltaEmitter {
	return func(eventType EventType, delta string) {
		if delta == "" {
			return
		}
		s.publishEvent(Event{
			Type:          eventType,
			RequestID:     snapshot.RequestID,
			RunID:         snapshot.RunID,
			SessionID:     snapshot.SessionID,
			AgentID:       snapshot.AgentID,
			ModelID:       snapshot.ModelID,
			ModelRevision: snapshot.ModelRevision,
			ToolRevision:  snapshot.ToolRevision,
			Delta:         delta,
			OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
}

// handleExecutionError 只把执行失败投影成实时终态 Event 与结构化日志。
//
// 失败期间已经生成的 Assistant partial content 由 Executor 以 error/aborted
// AssistantMessage 保存，因此这里严禁再次 AppendAssistantMessage，避免双写。
func (s *Service) handleExecutionError(
	snapshot *Snapshot,
	result ExecutionResult,
	runErr error,
	startedAt time.Time,
) {
	cancelled := errors.Is(runErr, context.Canceled)
	eventType := EventTurnFailed
	statusText := "failed"
	if cancelled {
		eventType = EventTurnCancelled
		statusText = "cancelled"
	}

	userVisibleError := runtimeUserVisibleError(runErr)
	s.publishEvent(Event{
		Type:          eventType,
		RequestID:     snapshot.RequestID,
		RunID:         snapshot.RunID,
		SessionID:     snapshot.SessionID,
		AgentID:       snapshot.AgentID,
		ModelID:       snapshot.ModelID,
		ModelRevision: snapshot.ModelRevision,
		ToolRevision:  snapshot.ToolRevision,
		MessageID:     result.MessageID,
		Error:         userVisibleError,
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})

	if cancelled {
		s.logger.Warn(
			context.Background(),
			"Agent Turn 已取消",
			"operation", "runtime.turn.cancelled",
			"request_id", snapshot.RequestID,
			"run_id", snapshot.RunID,
			"session_id", snapshot.SessionID,
			"agent_id", snapshot.AgentID,
			"message_id", result.MessageID,
			"status", statusText,
			"error", runErr,
			logging.Duration(startedAt),
		)
		return
	}

	s.logger.Error(
		context.Background(),
		"Agent Turn 执行失败",
		"operation", "runtime.turn.failed",
		"request_id", snapshot.RequestID,
		"run_id", snapshot.RunID,
		"session_id", snapshot.SessionID,
		"agent_id", snapshot.AgentID,
		"message_id", result.MessageID,
		"status", statusText,
		"error", runErr,
		logging.Duration(startedAt),
	)
}

// runtimeUserVisibleError 把底层 Provider/Eino/HTTP 错误转换成适合直接展示给用户的文本。
//
// 结构化日志仍然记录原始 error，便于开发排查；Runtime Event 不直接把第三方 SDK 的
// 实现细节、长错误链或潜在响应片段暴露到 UI。
func runtimeUserVisibleError(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, context.Canceled) {
		return "本次生成已取消。"
	}

	if errors.Is(err, contextengine.ErrContextBudgetExceeded) {
		return "当前对话上下文已达到模型安全预算，并且没有更多历史可以安全压缩。请手动精简当前请求、增大该模型的 Context Window，或新建会话继续。"
	}

	lower := strings.ToLower(err.Error())

	if errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(lower, "client.timeout") ||
		strings.Contains(lower, "context deadline exceeded") {
		return "模型服务在等待响应时超时。本次已经生成的内容已尽量保留，可以直接重试。"
	}

	if strings.Contains(lower, "inappropriat") ||
		strings.Contains(lower, "output data may contain") ||
		strings.Contains(lower, "content filter") ||
		strings.Contains(lower, "content_filter") ||
		strings.Contains(lower, "safety policy") ||
		strings.Contains(lower, "sensitive content") {
		return "模型服务因内容安全策略中止了本次生成。此前已经成功生成的内容已尽量保留。"
	}

	return "模型生成过程中发生错误。本次已经生成的内容已尽量保留，请稍后重试。"
}

func (s *Service) activeRunStatus(sessionID string) *ActiveRunStatus {
	s.mu.Lock()
	requestID, exists := s.activeBySession[sessionID]
	if !exists {
		s.mu.Unlock()
		return nil
	}
	active, exists := s.activeByRequest[requestID]
	if !exists || active == nil || active.snapshot == nil {
		// reserveSession 与 activeByRequest 注册之间存在很短的初始化窗口。该窗口由
		// StartTurn 调用方自己的 starting 状态表达，Overview 不伪造一个未冻结 Runtime。
		s.mu.Unlock()
		return nil
	}
	status := &ActiveRunStatus{
		RequestID:         active.RequestID,
		RunID:             active.RunID,
		SessionID:         active.SessionID,
		Phase:             active.phase,
		StartedAt:         active.startedAt.UTC().Format(time.RFC3339Nano),
		WaitingApprovalID: active.waitingApprovalID,
		Runtime:           cloneRuntimeManifest(active.snapshot.Manifest),
	}
	waitingApprovalID := active.waitingApprovalID
	s.mu.Unlock()

	if waitingApprovalID != "" && s.approvals != nil {
		if request, ok := s.approvals.Get(waitingApprovalID); ok {
			status.Approval = &request
		}
	}
	return status
}

func cloneRuntimeManifest(value RuntimeManifest) RuntimeManifest {
	clone := value
	clone.BuiltinToolNames = append([]string(nil), value.BuiltinToolNames...)
	clone.SkillNames = append([]string(nil), value.SkillNames...)
	clone.MCPServers = append([]humbertmcp.RuntimeServerSnapshot(nil), value.MCPServers...)
	clone.MCPUnavailable = append([]humbertmcp.RuntimeServerFailure(nil), value.MCPUnavailable...)
	clone.MCPTools = append([]humbertmcp.RuntimeToolSnapshot(nil), value.MCPTools...)
	clone.MCPToolNames = append([]string(nil), value.MCPToolNames...)
	clone.ExposedToolNames = append([]string(nil), value.ExposedToolNames...)
	return clone
}

// beginOperation 将初始化、同步 IO 和审批处理纳入与 Worker 相同的关闭边界。
// 必须在释放生命周期锁前计数；关闭会取消派生 Context，随后等待调用方执行 finish。
func (s *Service) beginOperation(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, ErrClosed
	}
	s.wg.Add(1)
	operationCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.rootCtx, cancel)
	return operationCtx, func() {
		stop()
		cancel()
		s.wg.Done()
	}, nil
}

func (s *Service) reserveSession(sessionID string, requestID string) error {
	return s.reserveAgentSession(sessionID, "", requestID)
}

func (s *Service) ensureSessionUnreserved(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if existing, exists := s.activeBySession[sessionID]; exists {
		return fmt.Errorf("%w: request_id=%s", ErrSessionBusy, existing)
	}
	return nil
}

func (s *Service) reserveAgentSession(sessionID string, agentID string, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrClosed
	}
	if existing, exists := s.activeBySession[sessionID]; exists {
		return fmt.Errorf("%w: request_id=%s", ErrSessionBusy, existing)
	}
	if agentID != "" {
		if existing, exists := s.deletingAgents[agentID]; exists {
			return fmt.Errorf("%w: request_id=%s", ErrAgentDeleting, existing)
		}
	}
	s.activeBySession[sessionID] = requestID
	if agentID != "" {
		s.reservationAgents[sessionID] = agentID
	}
	return nil
}

func (s *Service) releaseReservation(sessionID string, requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.activeBySession[sessionID]
	if !exists || current != requestID {
		return
	}
	delete(s.activeBySession, sessionID)
	delete(s.reservationAgents, sessionID)
}

func (s *Service) cleanupRun(active *activeRun) {
	if active == nil {
		return
	}
	active.cancel()
	_ = active.checkpointStore.Delete(context.Background(), active.RunID)

	s.mu.Lock()
	if current, exists := s.activeByRequest[active.RequestID]; exists && current == active {
		signalApprovalDoneLocked(active)
		delete(s.activeByRequest, active.RequestID)
	}
	s.mu.Unlock()

	s.mu.Lock()
	if current, exists := s.activeBySession[active.SessionID]; exists && current == active.RequestID {
		delete(s.activeBySession, active.SessionID)
		delete(s.reservationAgents, active.SessionID)
	}
	s.mu.Unlock()
}

func signalApprovalDoneLocked(active *activeRun) {
	if active == nil || active.approvalDone == nil {
		return
	}
	close(active.approvalDone)
	active.approvalDone = nil
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
	s.finishCancelledWaitingRun(active)
}

func (s *Service) finishCancelledWaitingRun(active *activeRun) {
	snapshot := active.snapshot
	s.publishEvent(Event{
		Type:          EventTurnCancelled,
		RequestID:     active.RequestID,
		RunID:         active.RunID,
		SessionID:     active.SessionID,
		AgentID:       snapshot.AgentID,
		ModelID:       snapshot.ModelID,
		ModelRevision: snapshot.ModelRevision,
		ToolRevision:  snapshot.ToolRevision,
		Error:         "本次生成已取消。",
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	s.logger.Warn(
		context.Background(),
		"等待 Approval 的 Agent Turn 已取消",
		"operation", "runtime.turn.cancelled",
		"request_id", active.RequestID,
		"run_id", active.RunID,
		"session_id", active.SessionID,
		"agent_id", snapshot.AgentID,
		logging.Duration(active.startedAt),
	)
	s.cleanupRun(active)
}

// publishEvent 发布 Runtime 瞬时 UI Event。
//
// Event delivery failure 不改变 Agent 执行结果。例如用户恰好关闭窗口，Session JSONL
// 仍应由 Executor 正常完成写入。
func (s *Service) publishEvent(event Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.events.Publish(ctx, TopicEvent, event); err != nil {
		s.logger.Warn(
			context.Background(),
			"发布 Runtime Event 失败",
			"operation", "runtime.event.publish",
			"event_type", string(event.Type),
			"request_id", event.RequestID,
			"run_id", event.RunID,
			"session_id", event.SessionID,
			"error", err,
		)
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
