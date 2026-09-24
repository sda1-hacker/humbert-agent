package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
)

const TopicEvent = "tasks.event"

type Event struct {
	Type       string `json:"type"`
	TaskID     string `json:"taskID"`
	RunID      string `json:"runID,omitempty"`
	Task       *Task  `json:"task,omitempty"`
	Run        *Run   `json:"run,omitempty"`
	OccurredAt string `json:"occurredAt"`
}

type Manager struct {
	store    *Store
	agents   *agents.Service
	sessions *sessions.Service
	runtime  *agentruntime.Service
	events   *eventbus.Bus
	logger   *logging.Logger

	notifications *notifications.Service

	rootCtx     context.Context
	cancel      context.CancelFunc
	unsubscribe func()

	mu              sync.Mutex
	closed          bool
	activeBySession map[string]string
	activeByRun     map[string]string
	activeAgents    map[string]int
	deletingAgents  map[string]int
	cancelPending   map[string]bool

	cycleMu       sync.Mutex
	wg            sync.WaitGroup
	maxConcurrent int
	tickInterval  time.Duration
}

func NewManager(store *Store, agentService *agents.Service, sessionService *sessions.Service, runtimeService *agentruntime.Service, events *eventbus.Bus, logger *logging.Logger) (*Manager, error) {
	if store == nil || agentService == nil || sessionService == nil || runtimeService == nil || events == nil || logger == nil {
		return nil, errors.New("Task Manager 依赖不完整")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		store: store, agents: agentService, sessions: sessionService, runtime: runtimeService,
		events: events, logger: logger, rootCtx: ctx, cancel: cancel,
		activeBySession: make(map[string]string), activeByRun: make(map[string]string),
		activeAgents: make(map[string]int), deletingAgents: make(map[string]int),
		cancelPending: make(map[string]bool),
		maxConcurrent: 2, tickInterval: 5 * time.Second,
	}, nil
}

func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	now := time.Now()
	interrupted, err := m.store.ReconcileInterrupted(ctx, now)
	if err != nil {
		return fmt.Errorf("恢复 TaskRun 状态失败: %w", err)
	}
	queuedAutomation, err := m.store.ReconcileQueuedAutomationRuns(ctx, now)
	if err != nil {
		return fmt.Errorf("恢复主动助手运行状态失败: %w", err)
	}
	for _, run := range interrupted {
		m.logger.Warn(ctx, "TaskRun 已在启动恢复时标记为 interrupted", "operation", "task.run.reconcile", "task_id", run.TaskID, "run_id", run.ID)
	}
	for _, run := range queuedAutomation {
		m.logger.Warn(ctx, "未启动的内部自动运行已在恢复时标记为 interrupted", "operation", "task.automation.reconcile", "task_id", run.TaskID, "run_id", run.ID)
	}
	if err := m.recoverRetries(ctx); err != nil {
		return fmt.Errorf("恢复 TaskRun 重试队列失败: %w", err)
	}
	unsubscribe, err := m.events.Subscribe(agentruntime.TopicEvent, m.handleRuntimePayload)
	if err != nil {
		return fmt.Errorf("订阅 Runtime Event 失败: %w", err)
	}
	m.unsubscribe = unsubscribe
	m.wg.Add(1)
	go m.schedulerLoop()
	return nil
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	requests := make([]string, 0, len(m.activeByRun))
	runIDs := make([]string, 0, len(m.activeByRun))
	for runID, requestID := range m.activeByRun {
		runIDs = append(runIDs, runID)
		requests = append(requests, requestID)
	}
	m.mu.Unlock()

	if m.unsubscribe != nil {
		m.unsubscribe()
	}
	m.cancel()
	now := time.Now().UTC()
	for _, runID := range runIDs {
		if run, err := m.store.GetRun(context.Background(), runID); err == nil && !run.Status.Terminal() {
			run.Status = RunInterrupted
			run.Error = "应用关闭时任务仍在运行；为避免重放工具副作用，本次运行已中断。"
			run.Approval = nil
			run.FinishedAt = &now
			_ = m.store.UpdateRun(context.Background(), run)
		}
	}
	for _, requestID := range requests {
		_ = m.runtime.CancelTurn(requestID)
	}

	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) List(ctx context.Context) ([]Task, error) {
	values, err := m.store.ListTasks(ctx, false)
	if err != nil {
		return nil, err
	}
	result := make([]Task, 0, len(values))
	for _, value := range values {
		if value.Internal {
			continue
		}
		result = append(result, value)
	}
	return result, nil
}

func (m *Manager) Runs(ctx context.Context, taskID string) ([]Run, error) {
	return m.store.ListRuns(ctx, taskID)
}

func (m *Manager) Run(ctx context.Context, runID string) (Run, error) {
	return m.store.GetRun(ctx, runID)
}

// ActiveRunForSession 用于主动助手区分普通聊天审批与 TaskRun 审批，避免同一
// approval 同时产生两条通知。它只暴露当前活动映射，不改变 Task 生命周期。
func (m *Manager) ActiveRunForSession(ctx context.Context, sessionID string) (Run, bool) {
	m.mu.Lock()
	runID := m.activeBySession[strings.TrimSpace(sessionID)]
	m.mu.Unlock()
	if runID == "" {
		return Run{}, false
	}
	run, err := m.store.GetRun(ctx, runID)
	if err != nil {
		return Run{}, false
	}
	return run, true
}

// SetNotificationService 在 Start 前注入通知服务，供通知型任务使用。
func (m *Manager) SetNotificationService(service *notifications.Service) {
	m.mu.Lock()
	m.notifications = service
	m.mu.Unlock()
}

// SuspendAgent 阻止删除过程中新建 Task/TaskRun。release 必须调用；cycleMu
// 保持到删除结束，避免 Agent 清理期间被并发写入重新创建。
func (m *Manager) SuspendAgent(agentID string) func() {
	m.cycleMu.Lock()
	m.mu.Lock()
	m.deletingAgents[agentID]++
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.deletingAgents[agentID]--
			if m.deletingAgents[agentID] <= 0 {
				delete(m.deletingAgents, agentID)
			}
			m.mu.Unlock()
			m.cycleMu.Unlock()
		})
	}
}

func (m *Manager) agentSuspended(agentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed || m.deletingAgents[agentID] > 0
}

func (m *Manager) publish(event Event) {
	event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.events.Publish(context.Background(), TopicEvent, event); err != nil && !errors.Is(err, eventbus.ErrClosed) {
		m.logger.Warn(context.Background(), "发布 Task Event 失败", "error", err)
	}
}
