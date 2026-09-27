package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	htools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

type budgetAskAuthorizer struct{}

func (budgetAskAuthorizer) Evaluate(_ context.Context, req permission.Request) (permission.Decision, error) {
	return permission.Decision{Action: permission.ActionAsk, ApprovalID: "approval", Identity: req.Identity}, nil
}

type budgetApprovalTool struct{ calls atomic.Int64 }

func (*budgetApprovalTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "budget_tool", Desc: "审批预算回归测试"}, nil
}
func (t *budgetApprovalTool) InvokableRun(context.Context, string, ...einotool.Option) (string, error) {
	t.calls.Add(1)
	return "done", nil
}

func approvalBudgetGraph(t *testing.T, snapshot *Snapshot, inner *budgetApprovalTool) compose.Runnable[*schema.Message, []*schema.Message] {
	t.Helper()
	ctx := context.Background()
	guarded, err := htools.GuardInvokableTool(ctx, budgetAskAuthorizer{}, htools.Descriptor{Name: "budget_tool", Risk: htools.RiskRead}, htools.Scope{
		RequestID: "request", RunID: "run", SessionID: "session", AgentID: "agent",
		Workspace: workspace.Workspace{AgentID: "agent", RootDir: t.TempDir()},
	}, inner)
	if err != nil {
		t.Fatal(err)
	}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:               []einotool.BaseTool{guarded},
		ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: buildToolLifecycleMiddleware(snapshot)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := compose.NewGraph[*schema.Message, []*schema.Message]()
	if err := graph.AddToolsNode("tools", node); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddEdge(compose.START, "tools"); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddEdge("tools", compose.END); err != nil {
		t.Fatal(err)
	}
	compiled, err := graph.Compile(ctx, compose.WithCheckPointStore(approval.NewCheckpointStore()))
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func approvalBudgetInput(ids ...string) *schema.Message {
	input := &schema.Message{Role: schema.Assistant}
	for _, id := range ids {
		input.ToolCalls = append(input.ToolCalls, schema.ToolCall{ID: id, Function: schema.FunctionCall{Name: "budget_tool", Arguments: `{}`}})
	}
	return input
}

func TestApprovalResumePreservesToolBudget(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		approved, exhaustTokens bool
		wantCalls               int64
	}{
		{name: "approved", approved: true, wantCalls: 1},
		{name: "denied"},
		{name: "token_limit_still_applies", approved: true, exhaustTokens: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			inner, snapshot := &budgetApprovalTool{}, &Snapshot{}
			if err := configureExecutionLimits(snapshot, ExecutionLimits{MaxToolCalls: 1, MaxTotalTokens: 1000}); err != nil {
				t.Fatal(err)
			}
			graph := approvalBudgetGraph(t, snapshot, inner)
			input := approvalBudgetInput("call-1")
			_, err := graph.Invoke(ctx, input, compose.WithCheckPointID("approval"))
			interrupted, ok := compose.ExtractInterruptInfo(err)
			if !ok || interrupted == nil || len(interrupted.InterruptContexts) != 1 {
				t.Fatalf("want approval interrupt, got %v", err)
			}
			if inner.calls.Load() != 0 {
				t.Fatal("tool executed before approval")
			}
			if tc.exhaustTokens {
				snapshot.limitState.addTokens(1000)
			}
			data, err := approval.EncodeResumeData(tc.approved)
			if err != nil {
				t.Fatal(err)
			}
			_, err = graph.Invoke(compose.ResumeWithData(ctx, interrupted.InterruptContexts[0].ID, data), input, compose.WithCheckPointID("approval"))
			if tc.exhaustTokens {
				if !errors.Is(err, ErrExecutionLimitExceeded) {
					t.Fatalf("want token limit, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("resumed call failed: %v", err)
			}
			if inner.calls.Load() != tc.wantCalls || snapshot.limitState.toolCalls.Load() != 1 {
				t.Fatalf("executed=%d budget=%d", inner.calls.Load(), snapshot.limitState.toolCalls.Load())
			}
			// 恢复不扣第二次预算，但下一次真正的新调用仍必须被上限阻止。
			_, err = graph.Invoke(ctx, approvalBudgetInput("call-2"), compose.WithCheckPointID("next"))
			if !errors.Is(err, ErrExecutionLimitExceeded) {
				t.Fatalf("new call bypassed budget: %v", err)
			}
		})
	}
}

func TestParallelApprovalsDoNotRechargeWaitingSibling(t *testing.T) {
	ctx := context.Background()
	inner, snapshot := &budgetApprovalTool{}, &Snapshot{}
	if err := configureExecutionLimits(snapshot, ExecutionLimits{MaxToolCalls: 2}); err != nil {
		t.Fatal(err)
	}
	graph := approvalBudgetGraph(t, snapshot, inner)
	input := approvalBudgetInput("first", "second")
	_, err := graph.Invoke(ctx, input, compose.WithCheckPointID("parallel"))
	for pending := 2; pending > 0; pending-- {
		interrupted, ok := compose.ExtractInterruptInfo(err)
		if !ok || interrupted == nil || len(interrupted.InterruptContexts) != pending {
			t.Fatalf("want %d approvals, got %v", pending, err)
		}
		data, encodeErr := approval.EncodeResumeData(true)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		// 每次只批准一个；另一个工具会被 Eino 再次调度并继续等待审批。
		_, err = graph.Invoke(compose.ResumeWithData(ctx, interrupted.InterruptContexts[0].ID, data), input, compose.WithCheckPointID("parallel"))
	}
	if err != nil || inner.calls.Load() != 2 || snapshot.limitState.toolCalls.Load() != 2 {
		t.Fatalf("err=%v executed=%d budget=%d", err, inner.calls.Load(), snapshot.limitState.toolCalls.Load())
	}
}
