package tools

import "context"

type toolCallContextKey struct{}

// CallContext 是当前 Eino ToolCall 的稳定身份。嵌套 Agent-as-Tool 用它把一次
// run_agent 调用映射到同一个审计记录，并在审批恢复后继续原来的子 Runtime。
type CallContext struct {
	ID   string
	Name string
}

func WithCallContext(ctx context.Context, callID string, name string) context.Context {
	return context.WithValue(ctx, toolCallContextKey{}, CallContext{ID: callID, Name: name})
}

func CallContextFrom(ctx context.Context) (CallContext, bool) {
	if ctx == nil {
		return CallContext{}, false
	}
	value, ok := ctx.Value(toolCallContextKey{}).(CallContext)
	return value, ok && value.ID != ""
}
