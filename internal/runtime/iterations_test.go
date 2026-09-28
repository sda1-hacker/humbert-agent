package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/approval"
)

type iterativeTestModel struct {
	calls atomic.Int64
	steps int64
}

func (m *iterativeTestModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	call := m.calls.Add(1)
	if call <= m.steps {
		return schema.AssistantMessage("", []schema.ToolCall{{ID: fmt.Sprint(call), Type: "function", Function: schema.FunctionCall{Name: "budget_tool", Arguments: `{}`}}}), nil
	}
	return schema.AssistantMessage("检查完成", nil), nil
}
func (m *iterativeTestModel) Stream(ctx context.Context, messages []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, messages, opts...)
	return schema.StreamReaderFromArray([]*schema.Message{message}), err
}
func (m *iterativeTestModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

// 使用真实 Eino Runner 跑完 25 次工具调用，验证不是只改了配置数字或错误文案。
func TestRunnerIterationBudgetSupportsLongTasks(t *testing.T) {
	for _, limit := range []int{0, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			ctx := context.Background()
			model, tool := &iterativeTestModel{steps: 25}, &budgetApprovalTool{}
			snapshot := &Snapshot{AgentName: "iteration_test", Model: model, MaxIterations: limit, Tools: []einotool.BaseTool{tool}}
			runner, err := buildRunner(ctx, snapshot, approval.NewCheckpointStore())
			if err != nil {
				t.Fatal(err)
			}
			events := runner.Run(ctx, []*schema.Message{schema.UserMessage("检查项目")})
			var runErr error
			for {
				event, ok := events.Next()
				if !ok {
					break
				}
				if event.Err != nil {
					runErr = event.Err
					continue
				}
				if event.Output != nil && event.Output.MessageOutput != nil {
					if _, err := materializeMessageOutput(ctx, event.Output.MessageOutput); err != nil {
						t.Fatal(err)
					}
				}
			}
			if limit == 0 {
				if runErr != nil || tool.calls.Load() != 25 || model.calls.Load() != 26 {
					t.Fatalf("长任务仍被提前截断: model=%d tool=%d err=%v", model.calls.Load(), tool.calls.Load(), runErr)
				}
			} else if !errors.Is(runErr, adk.ErrExceedMaxIterations) || model.calls.Load() != int64(limit) {
				t.Fatalf("未遵守显式上限: calls=%d error=%v", model.calls.Load(), runErr)
			}
		})
	}
}
