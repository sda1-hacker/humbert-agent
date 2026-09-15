package models

import (
	"context"
	"fmt"

	einomodel "github.com/cloudwego/eino/components/model"
)

// RuntimeSnapshot 是 ModelRegistry 在一个配置 Revision 下冻结的完整模型运行信息。
//
// Runtime 不应该只拿到 Eino Model Instance，因为 Session JSONL 的 AssistantMessage 还
// 需要记录当时真正使用的 Provider 与 Model 名称。把这些值和 Instance/Revision 在同
// 一个 Registry Lock 范围内解析，可以避免审计元数据与真实模型实例来自不同配置时刻。
type RuntimeSnapshot struct {
	Instance einomodel.ToolCallingChatModel

	Revision uint64

	ModelConfigID string

	ProviderID string

	ProviderName string

	ProviderType ProviderType

	API string

	ModelName string

	ModelDisplayName string

	// ContextWindow/MaxOutputTokens 来自模型配置快照，与模型实例使用同一 Revision。
	// Runtime 依赖这两个值计算上下文预算，因此不能在 Resolver 中再次读取 models.json。
	ContextWindow int

	MaxOutputTokens int
}

// ResolveSnapshot 在同一个 Registry 锁范围内同时解析模型实例、配置描述与 Revision。
func (r *Registry) ResolveSnapshot(ctx context.Context, id string) (RuntimeSnapshot, error) {
	if ctx == nil {
		return RuntimeSnapshot{}, fmt.Errorf("解析 Model Snapshot 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("解析 Model Snapshot 被取消: %w", err)
	}

	// 第一阶段尝试读缓存。mutation 必须取得 Registry 写锁，因此持有 RLock 时
	// Provider/Model 配置不会通过 Registry 被修改。
	r.mu.RLock()
	if cached, exists := r.cache[id]; exists {
		resolved, err := r.store.ResolveModel(ctx, id)
		if err != nil {
			r.mu.RUnlock()
			return RuntimeSnapshot{}, err
		}
		revision := r.revision.Load()
		r.mu.RUnlock()
		return runtimeSnapshotFromResolved(cached, revision, resolved), nil
	}
	r.mu.RUnlock()

	// Cache miss 使用写锁 double-check，保证并发首次 Resolve 只创建一个基础实例。
	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, exists := r.cache[id]; exists {
		resolved, err := r.store.ResolveModel(ctx, id)
		if err != nil {
			return RuntimeSnapshot{}, err
		}
		return runtimeSnapshotFromResolved(cached, r.revision.Load(), resolved), nil
	}

	resolved, err := r.store.ResolveModel(ctx, id)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	if !resolved.Model.Enabled {
		return RuntimeSnapshot{}, fmt.Errorf("%w: %s", ErrModelDisabled, id)
	}

	instance, err := r.factory.Create(ctx, resolved)
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("创建模型运行实例失败: %w", err)
	}
	r.cache[id] = instance

	return runtimeSnapshotFromResolved(instance, r.revision.Load(), resolved), nil
}

func runtimeSnapshotFromResolved(
	instance einomodel.ToolCallingChatModel,
	revision uint64,
	resolved ResolvedModel,
) RuntimeSnapshot {
	return RuntimeSnapshot{
		Instance:         instance,
		Revision:         revision,
		ModelConfigID:    resolved.Model.ID,
		ProviderID:       resolved.Provider.ID,
		ProviderName:     resolved.Provider.Name,
		ProviderType:     resolved.Provider.Type,
		API:              providerRuntimeAPI(resolved.Provider.Type),
		ModelName:        resolved.Model.ModelName,
		ModelDisplayName: resolved.Model.DisplayName,
		ContextWindow:    resolved.Model.ContextWindow,
		MaxOutputTokens:  resolved.Model.MaxOutputTokens,
	}
}

// providerRuntimeAPI 返回写入 Session JSONL 的稳定 API 家族名称。
//
// 该字段只用于历史解释与 UI，不参与实际网络请求路由。
func providerRuntimeAPI(providerType ProviderType) string {
	switch providerType {
	case ProviderTypeOpenAI, ProviderTypeOpenAICompatible:
		return "openai-completions"
	case ProviderTypeOllama:
		return "ollama"
	default:
		return string(providerType)
	}
}
