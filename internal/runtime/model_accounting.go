package runtime

import (
	"context"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/models"
)

type executionSnapshotContextKey struct{}

func limitStateFromContext(ctx context.Context) *executionLimitState {
	snapshot, _ := ctx.Value(executionSnapshotContextKey{}).(*Snapshot)
	if snapshot == nil {
		return nil
	}
	return snapshot.limitState
}

// TrackAuxiliaryModel 供应用装配的工具辅助模型使用，例如浏览器截图分析。
// 继承工具调用的运行身份和共享预算，不需要让工具包依赖 Runtime。
func TrackAuxiliaryModel(ctx context.Context, model models.RuntimeSnapshot, role string) einomodel.ToolCallingChatModel {
	parent, _ := ctx.Value(executionSnapshotContextKey{}).(*Snapshot)
	if parent == nil {
		return model.Instance
	}
	return trackAuxiliaryModel(model, role, parent)
}

// 在模型边界统计，避免依赖 Agent 中间件顺序；摘要、视觉和子 Agent 同样经过这里。
type trackedChatModel struct {
	inner    einomodel.ToolCallingChatModel
	snapshot *Snapshot
}

func trackModel(model einomodel.ToolCallingChatModel, snapshot *Snapshot) einomodel.ToolCallingChatModel {
	if model == nil {
		return nil
	}
	if tracked, ok := model.(*trackedChatModel); ok && tracked.snapshot == snapshot {
		return model
	}
	return &trackedChatModel{inner: model, snapshot: snapshot}
}

func trackAuxiliaryModel(model models.RuntimeSnapshot, role string, parent *Snapshot) einomodel.ToolCallingChatModel {
	return trackModel(model.Instance, &Snapshot{
		RequestID: parent.RequestID, RunID: parent.RunID, SessionID: parent.SessionID, AgentID: parent.AgentID,
		ModelID: model.ModelConfigID, ModelRevision: model.Revision, ModelRole: role,
		EventReporter: parent.EventReporter, limitState: parent.limitState,
	})
}

func (m *trackedChatModel) WithTools(infos []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, err := m.inner.WithTools(infos)
	if err != nil {
		return nil, err
	}
	return &trackedChatModel{inner: bound, snapshot: m.snapshot}, nil
}

func (m *trackedChatModel) before(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.snapshot.limitState.beforeModelCall(); err != nil {
		return err
	}
	reportToolLifecycleEvent(ctx, m.snapshot, Event{Type: EventModelStarted, ModelRole: m.snapshot.ModelRole, OccurredAt: time.Now().UTC().Format(time.RFC3339Nano)})
	return nil
}

func (m *trackedChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	if err := m.before(ctx); err != nil {
		return nil, err
	}
	message, err := m.inner.Generate(ctx, messages, opts...)
	usage := modelUsageAccumulator{}
	usage.record(ctx, m.snapshot, message)
	return message, err
}

func (m *trackedChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	if err := m.before(ctx); err != nil {
		return nil, err
	}
	stream, err := m.inner.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	usage := modelUsageAccumulator{}
	return schema.StreamReaderWithConvert(stream, func(message *schema.Message) (*schema.Message, error) {
		usage.record(ctx, m.snapshot, message)
		return message, nil
	}), nil
}

type modelUsageAccumulator struct{ input, output, total int }

func (u *modelUsageAccumulator) record(ctx context.Context, snapshot *Snapshot, message *schema.Message) {
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		return
	}
	usage := message.ResponseMeta.Usage
	// Eino 的流式 Usage 是累计快照（ConcatMessages 取各字段最大值），这里只累加差值。
	input, output := max(u.input, usage.PromptTokens), max(u.output, usage.CompletionTokens)
	total := max(u.total, usage.TotalTokens, input+output)
	if input == u.input && output == u.output && total == u.total {
		return
	}
	snapshot.limitState.addTokens(total - u.total)
	reportToolLifecycleEvent(context.WithoutCancel(ctx), snapshot, Event{
		Type: EventModelUsage, ModelRole: snapshot.ModelRole,
		InputTokens: input - u.input, OutputTokens: output - u.output, TotalTokens: total - u.total,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	u.input, u.output, u.total = input, output, total
}
