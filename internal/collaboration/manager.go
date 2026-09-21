package collaboration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const maxTaskChars = 32000

type AgentCatalog interface {
	List(ctx context.Context) ([]agents.AgentInfo, error)
	Get(ctx context.Context, id string) (agents.AgentInfo, error)
}

type Manager struct {
	agents  AgentCatalog
	store   *Store
	builder AgentBuilder
	mu      sync.Mutex
	active  map[string]Run
}

func NewManager(agentService AgentCatalog, store *Store, builder AgentBuilder) (*Manager, error) {
	if agentService == nil || store == nil || builder == nil {
		return nil, errors.New("创建 Collaboration Manager 失败: 依赖不完整")
	}
	return &Manager{agents: agentService, store: store, builder: builder, active: make(map[string]Run)}, nil
}

func (m *Manager) ListAgents(ctx context.Context, parentAgentID string) ([]AgentSummary, error) {
	values, err := m.agents.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取可用 Agent 失败: %w", err)
	}
	result := make([]AgentSummary, 0, len(values))
	for _, value := range values {
		if value.Agent.ID == parentAgentID || !value.Agent.SubagentEnabled {
			continue
		}
		description := strings.TrimSpace(value.Agent.Instruction)
		if len([]rune(description)) > 240 {
			description = string([]rune(description)[:240]) + "…"
		}
		result = append(result, AgentSummary{ID: value.Agent.ID, Name: value.Agent.Name, Description: description})
	}
	return result, nil
}

func (m *Manager) RunAgent(ctx context.Context, input RunInput) (RunOutput, error) {
	input.ChildAgentID = strings.TrimSpace(input.ChildAgentID)
	input.Task = strings.TrimSpace(input.Task)
	if input.ChildAgentID == "" || input.Task == "" {
		return RunOutput{}, errors.New("run_agent 需要 child_agent_id 和自包含 task")
	}
	if input.ChildAgentID == input.ParentScope.AgentID {
		return RunOutput{}, errors.New("run_agent 不能调用当前 Agent 自身")
	}
	target, err := m.agents.Get(ctx, input.ChildAgentID)
	if err != nil {
		return RunOutput{}, fmt.Errorf("读取子 Agent 失败: %w", err)
	}
	if !target.Agent.SubagentEnabled {
		return RunOutput{}, errors.New("目标 Agent 未启用“允许作为子 Agent 调用”")
	}
	if len([]rune(input.Task)) > maxTaskChars {
		return RunOutput{}, fmt.Errorf("run_agent task 不能超过 %d 个字符", maxTaskChars)
	}
	call, ok := humberttools.CallContextFrom(ctx)
	if !ok {
		return RunOutput{}, errors.New("run_agent 缺少 ToolCall 上下文")
	}
	runID := stableRunID(input.ParentScope.RequestID, call.ID)
	now := time.Now().UTC()
	record, found, err := m.store.Load(context.WithoutCancel(ctx), input.ParentScope.SessionID, runID)
	if err != nil {
		return RunOutput{}, err
	}
	if !found {
		record = Run{
			ID: runID, ParentAgentID: input.ParentScope.AgentID, ParentSessionID: input.ParentScope.SessionID,
			ParentRequestID: input.ParentScope.RequestID, ParentRunID: input.ParentScope.RunID,
			ToolCallID: call.ID, ChildAgentID: input.ChildAgentID, Task: input.Task,
			Status: StatusRunning, StartedAt: now, UpdatedAt: now,
		}
	} else if record.ParentAgentID != input.ParentScope.AgentID ||
		record.ParentSessionID != input.ParentScope.SessionID ||
		record.ParentRequestID != input.ParentScope.RequestID ||
		record.ParentRunID != input.ParentScope.RunID ||
		record.ToolCallID != call.ID ||
		record.ChildAgentID != input.ChildAgentID || record.Task != input.Task {
		return RunOutput{}, errors.New("审批恢复后的 run_agent 参数与原调用不一致")
	} else if record.Status == StatusSucceeded {
		return RunOutput{
			RunID: record.ID, ChildAgentID: record.ChildAgentID, ChildAgentName: record.ChildAgentName,
			Status: record.Status, Result: record.Result,
		}, nil
	} else if record.Status == StatusFailed {
		return RunOutput{}, fmt.Errorf("此前相同的 run_agent 调用已经失败: %s", record.Error)
	} else {
		record.Status = StatusRunning
		record.UpdatedAt = now
		record.Error = ""
	}
	if err := m.store.Save(context.WithoutCancel(ctx), record); err != nil {
		return RunOutput{}, err
	}
	m.track(record)

	built, err := m.builder.BuildChildAgent(ctx, BuildAgentInput{ChildAgentID: input.ChildAgentID, ParentScope: input.ParentScope})
	if err != nil {
		return RunOutput{}, m.fail(ctx, record, err)
	}
	record.ChildAgentName = built.AgentName
	if err := m.store.Save(context.WithoutCancel(ctx), record); err != nil {
		return RunOutput{}, m.fail(ctx, record, err)
	}
	m.track(record)

	agentTool := adk.NewAgentTool(ctx, built.Agent)
	invokable, ok := agentTool.(einotool.InvokableTool)
	if !ok {
		return RunOutput{}, m.fail(ctx, record, errors.New("Eino AgentTool 不支持 InvokableRun"))
	}
	arguments, err := json.Marshal(map[string]string{"request": input.Task})
	if err != nil {
		return RunOutput{}, m.fail(ctx, record, err)
	}
	result, err := invokable.InvokableRun(ctx, string(arguments))
	if err != nil {
		var interrupt *adk.InterruptSignal
		if errors.As(err, &interrupt) {
			record.Status = StatusWaitingApproval
			record.UpdatedAt = time.Now().UTC()
			_ = m.store.Save(context.WithoutCancel(ctx), record)
			return RunOutput{}, err
		}
		return RunOutput{}, m.fail(ctx, record, err)
	}

	finished := time.Now().UTC()
	record.Status = StatusSucceeded
	record.Result = strings.TrimSpace(result)
	record.Error = ""
	record.UpdatedAt = finished
	record.FinishedAt = &finished
	if err := m.store.Save(context.WithoutCancel(ctx), record); err != nil {
		return RunOutput{}, m.fail(ctx, record, err)
	}
	m.untrack(record.ID)
	return RunOutput{RunID: runID, ChildAgentID: built.AgentID, ChildAgentName: built.AgentName, Status: StatusSucceeded, Result: record.Result}, nil
}

func (m *Manager) fail(ctx context.Context, record Run, cause error) error {
	finished := time.Now().UTC()
	record.Status = StatusFailed
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		record.Status = StatusInterrupted
	}
	record.Error = logging.SafeErrorText(cause, 4096)
	record.UpdatedAt = finished
	record.FinishedAt = &finished
	saveErr := m.store.Save(context.WithoutCancel(ctx), record)
	m.untrack(record.ID)
	if saveErr != nil {
		return errors.Join(cause, saveErr)
	}
	return cause
}

func (m *Manager) track(record Run) {
	m.mu.Lock()
	m.active[record.ID] = record
	m.mu.Unlock()
}

func (m *Manager) untrack(runID string) {
	m.mu.Lock()
	delete(m.active, runID)
	m.mu.Unlock()
}

// ParentRunFinished 清理因父 Turn 取消、关闭或审批超时而没有机会自行收敛的子运行审计。
func (m *Manager) ParentRunFinished(ctx context.Context, requestID string) {
	m.mu.Lock()
	values := make([]Run, 0)
	for runID, record := range m.active {
		if record.ParentRequestID == requestID {
			values = append(values, record)
			delete(m.active, runID)
		}
	}
	m.mu.Unlock()

	for _, record := range values {
		finished := time.Now().UTC()
		record.Status = StatusInterrupted
		record.Error = "父 Agent Turn 已结束"
		record.UpdatedAt = finished
		record.FinishedAt = &finished
		_ = m.store.Save(context.WithoutCancel(ctx), record)
	}
}

func stableRunID(requestID string, callID string) string {
	sum := sha256.Sum256([]byte(requestID + "\x00" + callID))
	return "subrun_" + hex.EncodeToString(sum[:12])
}
