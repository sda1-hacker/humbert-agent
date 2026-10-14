package tools

import "context"

type runContextKey struct{}

// WithRunContext 保留真实 Turn 的生命周期；Eino 某个工具/审批迭代结束不代表 Turn 结束。
func WithRunContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, runContextKey{}, ctx)
}

func RunContext(ctx context.Context) context.Context {
	if run, ok := ctx.Value(runContextKey{}).(context.Context); ok {
		return run
	}
	return ctx
}
