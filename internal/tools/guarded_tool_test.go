package tools

import (
	"context"
	"errors"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

type nestedCheckpointTool struct {
	resumed bool
}

func (t *nestedCheckpointTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "nested_checkpoint", Desc: "test nested checkpoint passthrough"}, nil
}

func (t *nestedCheckpointTool) InvokableRun(ctx context.Context, _ string, _ ...einotool.Option) (string, error) {
	wasInterrupted, hasState, state := einotool.GetInterruptState[[]byte](ctx)
	if !wasInterrupted {
		return "", einotool.StatefulInterrupt(ctx, "nested tool interrupted", []byte("nested-state"))
	}
	if !hasState || string(state) != "nested-state" {
		return "", errors.New("nested checkpoint state missing")
	}
	t.resumed = true
	return "resumed", nil
}

type alwaysAllowAuthorizer struct{}

func (alwaysAllowAuthorizer) Evaluate(context.Context, permission.Request) (permission.Decision, error) {
	return permission.Decision{Action: permission.ActionAllow}, nil
}

func TestGuardPassesNestedCheckpointStateToWrappedTool(t *testing.T) {
	ctx := context.Background()
	inner := &nestedCheckpointTool{}
	guarded, err := GuardInvokableTool(ctx, alwaysAllowAuthorizer{}, Descriptor{
		Name: "nested_checkpoint", Risk: RiskRead,
	}, Scope{
		AgentID: "agent", Workspace: workspace.Workspace{AgentID: "agent", RootDir: t.TempDir()},
	}, inner)
	if err != nil {
		t.Fatalf("GuardInvokableTool() error = %v", err)
	}

	toolsNode, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{Tools: []einotool.BaseTool{guarded}})
	if err != nil {
		t.Fatalf("NewToolNode() error = %v", err)
	}
	graph := compose.NewGraph[*schema.Message, []*schema.Message]()
	if err := graph.AddToolsNode("tools", toolsNode); err != nil {
		t.Fatalf("AddToolsNode() error = %v", err)
	}
	if err := graph.AddEdge(compose.START, "tools"); err != nil {
		t.Fatalf("AddEdge(START) error = %v", err)
	}
	if err := graph.AddEdge("tools", compose.END); err != nil {
		t.Fatalf("AddEdge(END) error = %v", err)
	}
	compiled, err := graph.Compile(ctx, compose.WithCheckPointStore(approval.NewCheckpointStore()))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	checkpointID := "guard-nested-checkpoint"
	input := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		ID: "call-1", Function: schema.FunctionCall{Name: "nested_checkpoint", Arguments: `{}`},
	}}}
	_, err = compiled.Invoke(ctx, input, compose.WithCheckPointID(checkpointID))
	interrupt, ok := compose.ExtractInterruptInfo(err)
	if !ok || interrupt == nil || len(interrupt.InterruptContexts) != 1 {
		t.Fatalf("first Invoke() error = %v, want one interrupt", err)
	}

	resumeCtx := compose.Resume(ctx, interrupt.InterruptContexts[0].ID)
	if _, err := compiled.Invoke(resumeCtx, input, compose.WithCheckPointID(checkpointID)); err != nil {
		t.Fatalf("resumed Invoke() error = %v", err)
	}
	if !inner.resumed {
		t.Fatal("wrapped nested tool was not resumed")
	}
}
