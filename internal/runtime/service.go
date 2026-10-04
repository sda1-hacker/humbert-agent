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
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
)

// activeRun 描述一个可能经历多次 Human Approval 暂停/恢复的 User Turn。
//
// Snapshot、Context 和 CheckpointStore 在整个 Turn 生命周期内保持不变。等待审批时没有
// Executor goroutine 在运行，但 Session reservation 与 activeRun 必须继续保留；否则用户可
// 在同一 Session 启动第二个 Turn，破坏 checkpoint 对应的 ActiveBranch。
type activeRun struct {
	RequestID       string
	RunID           string
	SessionID       string
	ctx             context.Context
	cancel          context.CancelFunc
	snapshot        *Snapshot
	checkpointStore *approval.CheckpointStore
	startedAt       time.Time
	phase           RunPhase
	finishOnce      sync.Once

	// waitingApprovalID 非空表示当前没有 Executor worker，Run 正停在该审批点。字段只在
	// Service.mu 下访问。approvalDone 用来停止该请求对应的 timeout worker。
	waitingApprovalID string
	approvalDone      chan struct{}
	// 保存失败只唤醒原超时 worker；缓冲保留先于 worker 等待发生的状态变更。
	approvalRetry chan struct{}
}

// RunLifecycleObserver 观察父 Turn 的最终清理，用于收敛依附于该 Turn 的轻量运行状态。
type RunLifecycleObserver interface {
	ParentRunFinished(ctx context.Context, requestID string)
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
	resolver   *Resolver
	executor   *Executor
	sessions   *sessions.Service
	events     *eventbus.Bus
	logger     *logging.Logger
	approvals  *approval.Manager
	rootCtx    context.Context
	rootCancel context.CancelFunc
	mu         sync.Mutex
	closed     bool

	// activeBySession 保证同一个 Session 同时最多一个用户 Turn。
	activeBySession map[string]string

	// activeByRequest 支持 CancelTurn(requestID)。
	activeByRequest map[string]*activeRun

	// reservationAgents 记录 Session 占用所属 Agent，使删除 Agent 可以在同一把锁下
	// 拒绝已有操作。deletingAgents 则阻止删除期间启动新的 Turn、压缩或 Session 删除。
	reservationAgents  map[string]string
	deletingAgents     map[string]string
	lifecycleObservers []RunLifecycleObserver
	wg                 sync.WaitGroup
	closeDone          chan struct{}
}

// AddRunLifecycleObserver 只在 Application Bootstrap 阶段调用。
func (s *Service) AddRunLifecycleObserver(observer RunLifecycleObserver) {
	if observer == nil {
		return
	}
	s.mu.Lock()
	s.lifecycleObservers = append(s.lifecycleObservers, observer)
	s.mu.Unlock()
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
	if !input.Limits.Deadline.IsZero() {
		var cancelDeadline context.CancelFunc
		ctx, cancelDeadline = context.WithDeadline(ctx, input.Limits.Deadline)
		defer cancelDeadline()
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
			s.notifyRunFinished(requestID)
			s.releaseReservation(sessionID, requestID)
		}
	}()

	userMessage, err := s.sessions.PrepareUserMessage(ctx, sessionID, input.Input, input.RetryUserMessageID)
	if err != nil {
		return StartTurnResult{}, fmt.Errorf("持久化 UserMessage 失败: %w", err)
	}
	receipt := StartTurnResult{RequestID: requestID, RunID: runID, SessionID: sessionID, UserMessageID: userMessage.EntryID}

	limitState, err := prepareExecutionLimitState(input.Limits)
	if err != nil {
		receipt.StartError = runtimeUserVisibleError(err)
		return receipt, err
	}
	snapshot, err := s.resolver.ResolveTurn(ctx, requestID, runID, sessionID, ResolveTurnOptions{limitState: limitState})
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
	if err := configureExecutionLimits(snapshot, input.Limits, limitState); err != nil {
		receipt.StartError = runtimeUserVisibleError(err)
		return receipt, err
	}

	var runCtx context.Context
	var cancel context.CancelFunc
	if !input.Limits.Deadline.IsZero() {
		runCtx, cancel = context.WithDeadline(s.rootCtx, input.Limits.Deadline)
	} else if input.Limits.MaxDuration > 0 {
		runCtx, cancel = context.WithTimeout(s.rootCtx, input.Limits.MaxDuration)
	} else {
		runCtx, cancel = context.WithCancel(s.rootCtx)
	}
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
func (s *Service) ManualCompact(ctx context.Context, sessionID string) (ManualCompactionResult, error) {
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
	result, err := s.resolver.ManualCompact(ctx, sessionID)
	if err != nil {
		return ManualCompactionResult{}, err
	}
	s.logger.Info(
		ctx,
		"用户手动 Context 压缩完成",
		"operation", "runtime.context.manual_compact",
		"session_id", sessionID,
		"compacted", result.Compaction.Compacted,
		logging.Duration(startedAt),
	)
	return result, nil
}
