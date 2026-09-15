package app

import (
	"context"

	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
)

// runtimeEventPublisher 把 Runtime Event 发布到 Humbert Internal EventBus。
// Desktop/Wails Adapter 继续监听同一个 Runtime Topic，因此实时 Tool Lifecycle
// 与持久化方式完全解耦。
type runtimeEventPublisher func(ctx context.Context, event agentruntime.Event)

// runtimeEventReporter 是 Runtime 到实时 UI Event Stream 的桥梁。
//
// Tool started/completed/failed 的主要价值是实时反馈，不再单独持久化。历史 Tool
// 调用可以由 Session Transcript 中的 assistant(tool_calls) + tool(result) 重建，
// 避免同一个 Tool 生命周期重复写多份审计数据。
type runtimeEventReporter struct {
	publish runtimeEventPublisher
}

// newRuntimeEventReporter 创建 Runtime Event Reporter。
func newRuntimeEventReporter(publisher runtimeEventPublisher) *runtimeEventReporter {
	return &runtimeEventReporter{publish: publisher}
}

// Report 把 Runtime Event 立即发送到实时 EventBus。
//
// Report 不进行持久化 IO；即使 UI 已关闭，Event 发布失败也不能改变 Tool 执行结果。
// 调用方 Context 会原样传给 EventBus，使事件生命周期与当前 Run 一致，而不是错误地
// 绑定 Application Bootstrap 的启动 Context。
func (r *runtimeEventReporter) Report(
	ctx context.Context,
	event agentruntime.Event,
) {
	if r == nil || r.publish == nil {
		return
	}
	r.publish(ctx, event)
}
