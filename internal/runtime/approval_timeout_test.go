package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
)

func TestExpiredClickDoesNotStrandWaitingRun(t *testing.T) {
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	log := logging.NewBootstrap()
	ps, err := permission.NewStore(ctx, filepath.Join(t.TempDir(), "permissions.json"))
	must(err)
	pe, err := permission.NewEngine(config.PermissionConfig{Enabled: true, ReadAction: "allow", WriteAction: "ask", ExecAction: "ask", ApprovalTimeoutMS: 1000}, ps, log)
	must(err)
	am, err := approval.NewManager(time.Nanosecond, pe, log)
	must(err)
	req, err := am.Register(ctx, approval.InterruptInfo{ApprovalID: "approval", RequestID: "request", RunID: "run", SessionID: "session", AgentID: "agent", ToolName: "write_file", Risk: permission.RiskWrite, Identity: permission.CapabilityIdentity{Version: permission.CapabilityIdentityVersion, Kind: permission.CapabilityBuiltin, Tool: "write_file", Risk: permission.RiskWrite, SandboxFingerprint: "sbx1:test"}}, "interrupt", "run")
	must(err)
	if time.Now().Before(req.ExpiresAt) {
		t.Fatal("fixture not expired")
	}
	s := NewService(nil, nil, nil, eventbus.New(), log, am)
	defer s.rootCancel()
	runCtx, cancel := context.WithCancel(s.rootCtx)
	defer cancel()
	// Resume 的输入校验失败也必须收尾，测试不连接模型。
	cp := approval.NewCheckpointStore()
	must(cp.Set(ctx, "run", []byte("checkpoint")))
	active := &activeRun{RequestID: "request", RunID: "run", SessionID: "session", snapshot: &Snapshot{RequestID: "request", RunID: "run", SessionID: "session", AgentID: "agent"}, startedAt: time.Now(), ctx: runCtx, cancel: cancel, phase: RunPhaseWaitingApproval, waitingApprovalID: req.ID, approvalDone: make(chan struct{}), checkpointStore: cp}
	s.activeByRequest["request"] = active
	s.activeBySession["session"] = "request"
	_, err = s.ResolveApproval(ctx, ResolveApprovalInput{ApprovalID: req.ID, Decision: approval.DecisionAllowOnce})
	if !errors.Is(err, approval.ErrNotPending) {
		t.Fatalf("expected expired click rejection, got %v", err)
	}
	s.wg.Add(1)
	s.awaitApproval(active, req, active.approvalDone, active.approvalRetry)
	after, _ := am.Get(req.ID)
	if s.activeBySession["session"] == "request" {
		t.Fatalf("approval=%s, timeout worker exited, session still reserved with phase=%s", after.Status, active.phase)
	}
}

// 在真实 Permission Engine 开始保存时暂停，固定“保存跨过审批截止时间”的顺序。
// 屏障不替换权限/审批实现，实际失败仍由临时目录中的无效策略文件触发。
type approvalSaveBarrier struct {
	context.Context
	manager          *approval.Manager
	id               string
	entered, release chan struct{}
	once             sync.Once
}

func (c *approvalSaveBarrier) Err() error {
	request, _ := c.manager.Get(c.id)
	if request.Status == approval.StatusResolving {
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return c.Context.Err()
}

func TestApprovalSaveAcrossDeadline(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		decision                         approval.Decision
		afterDeadline, saveFails, cancel bool
	}{
		{"allow_failure_before_deadline", approval.DecisionAllowAgent, false, true, false},
		{"allow_failure_after_deadline", approval.DecisionAllowAgent, true, true, false},
		{"deny_failure_after_deadline", approval.DecisionDenyAgent, true, true, false},
		{"success_after_deadline", approval.DecisionAllowAgent, true, false, false},
		{"cancel_during_save", approval.DecisionAllowAgent, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			logger := logging.NewBootstrap()
			policyPath := filepath.Join(t.TempDir(), "permissions.json")
			store, err := permission.NewStore(ctx, policyPath)
			must(err)
			engine, err := permission.NewEngine(config.PermissionConfig{Enabled: true, ReadAction: "allow", WriteAction: "ask", ExecAction: "ask", ApprovalTimeoutMS: 1000}, store, logger)
			must(err)
			manager, err := approval.NewManager(time.Second, engine, logger)
			must(err)
			request, err := manager.Register(ctx, approval.InterruptInfo{ApprovalID: "approval", RequestID: "request", RunID: "run", SessionID: "session", AgentID: "agent", ToolName: "write_file", Risk: permission.RiskWrite, Identity: permission.CapabilityIdentity{Version: permission.CapabilityIdentityVersion, Kind: permission.CapabilityBuiltin, Tool: "write_file", Risk: permission.RiskWrite, SandboxFingerprint: "sbx1:test"}}, "interrupt", "run")
			must(err)
			events := eventbus.New()
			var eventsMu sync.Mutex
			counts := make(map[EventType]int)
			unsubscribe, err := events.Subscribe(TopicEvent, func(_ context.Context, payload any) {
				eventsMu.Lock()
				counts[payload.(Event).Type]++
				eventsMu.Unlock()
			})
			must(err)
			defer unsubscribe()
			s := NewService(nil, nil, nil, events, logger, manager)
			runCtx, cancel := context.WithCancel(s.rootCtx)
			checkpoint := approval.NewCheckpointStore()
			must(checkpoint.Set(ctx, "run", []byte("checkpoint")))
			done, retry := make(chan struct{}), make(chan struct{}, 1)
			active := &activeRun{RequestID: "request", RunID: "run", SessionID: "session", snapshot: &Snapshot{RequestID: "request", RunID: "run", SessionID: "session", AgentID: "agent"}, startedAt: time.Now(), ctx: runCtx, cancel: cancel, phase: RunPhaseWaitingApproval, waitingApprovalID: request.ID, approvalDone: done, approvalRetry: retry, checkpointStore: checkpoint}
			s.activeByRequest["request"] = active
			s.activeBySession["session"] = "request"
			if tc.saveFails {
				must(os.Rename(policyPath, policyPath+".saved"))
				must(os.Mkdir(policyPath, 0700))
			}
			barrier := &approvalSaveBarrier{Context: ctx, manager: manager, id: request.ID, entered: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
			defer func() { release(); cancel(); s.rootCancel() }()
			resolved := make(chan error, 1)
			go func() {
				// 直接进入公共 API 使用的处理函数，保留测试 Context 上的时序屏障；
				// 公共入口的生命周期包装会生成新的 cancelCtx，屏障将不再可见。
				_, err := s.resolveApproval(barrier, request.ID, tc.decision)
				resolved <- err
			}()
			select {
			case <-barrier.entered:
			case err := <-resolved:
				t.Fatalf("审批未进入保存阶段: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("未进入保存阶段")
			}
			workerDone := make(chan struct{})
			s.wg.Add(1)
			go func() { s.awaitApproval(active, request, done, retry); close(workerDone) }()
			if tc.afterDeadline {
				// 到期后原 worker 必须仍在等保存结果，不能误以为已经开始恢复。
				timer := time.NewTimer(time.Until(request.ExpiresAt) + 30*time.Millisecond)
				select {
				case <-workerDone:
					timer.Stop()
					t.Fatal("保存尚未完成，超时 worker 已提前退出")
				case <-timer.C:
				}
			}
			if tc.cancel {
				must(s.CancelTurn(active.RequestID))
			}
			release()
			select {
			case err = <-resolved:
			case <-time.After(3 * time.Second):
				t.Fatal("审批保存没有返回")
			}
			if tc.saveFails && err == nil {
				t.Fatal("预期权限持久化失败")
			}
			if !tc.saveFails {
				must(err)
			}
			if !tc.afterDeadline {
				pending, ok := manager.Get(request.ID)
				if !ok || pending.Status != approval.StatusPending {
					t.Fatalf("到期前保存失败不能立即结束审批: %+v", pending)
				}
			}
			select {
			case <-workerDone:
			case <-time.After(3 * time.Second):
				t.Fatal("超时 worker 未收尾")
			}
			finished := make(chan struct{})
			go func() { s.wg.Wait(); close(finished) }()
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("审批恢复未收尾")
			}
			s.mu.Lock()
			_, reserved := s.activeBySession[active.SessionID]
			s.mu.Unlock()
			if reserved {
				t.Fatal("审批结束后 Session 仍被占用")
			}
			if _, ok := manager.Get(request.ID); ok {
				t.Fatal("审批终态没有释放")
			}
			eventsMu.Lock()
			defer eventsMu.Unlock()
			if counts[EventTurnFailed]+counts[EventTurnCancelled] != 1 {
				t.Fatalf("必须只收尾一次: %v", counts)
			}
			if tc.saveFails && !tc.cancel {
				if counts[EventApprovalExpired] != 1 {
					t.Fatalf("保存失败后没有唯一的超时恢复: %v", counts)
				}
			} else if counts[EventApprovalExpired] != 0 {
				t.Fatalf("成功或取消后不能再次超时恢复: %v", counts)
			}
		})
	}
}
