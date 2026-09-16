package models

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/credential"
	"github.com/sda1-hacker/humbert-agent/internal/logging"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Registry 是 Humbert 模型系统的唯一运行时入口。
//
// 它统一负责：
//
//   - Provider CRUD；
//   - Model CRUD；
//   - Credential 生命周期；
//   - Eino Model 创建；
//   - Model instance cache；
//   - model_revision；
//   - 连通性测试。
//
// Agent Runtime 后续只依赖：
//
//	modelRegistry.Resolve(ctx, modelID)
//
// 而不直接访问底层 JSON 文件、API Key 或具体模型 SDK。
type Registry struct {
	store *Store

	credentials *credential.Store

	factory *Factory

	logger *logging.Logger

	// mu 同时保护 Provider/Model mutation 与 model cache。
	//
	// 配置修改期间禁止新的 Resolve 进入，
	// 可以保证不会出现“配置文件已经更新，但 Credential 还没有完成切换”的
	// 半完成运行时状态。
	mu sync.RWMutex

	cache map[string]einomodel.ToolCallingChatModel

	// referenceChecker 用于删除 Model 前检查 Agent Profile 是否仍引用该模型。
	// 接口定义在 models 包中，agents.Store 通过结构化方法直接实现，避免两个
	// Domain Package 相互 import。该检查只发生在低频配置删除路径。
	referenceChecker ModelReferenceChecker

	revision atomic.Uint64
}

// ModelReferenceChecker 描述 Model Registry 删除模型前需要的 Agent 引用检查。
//
// Registry 不应该知道 Agent Profile 的文件布局，因此只依赖这个最小接口。
// Application 在 Composition Root 中注入 agents.Store；这样 Model Domain 保持
// 独立，同时避免删除仍被 Agent 默认配置引用的模型造成悬空 model_id。
type ModelReferenceChecker interface {
	CountAgentsByModel(ctx context.Context, modelID string) (int, error)
}

// RegistryOption 配置 Model Registry 的可选领域依赖。
type RegistryOption func(*Registry)

// WithModelReferenceChecker 配置删除模型时使用的 Agent 引用检查器。
func WithModelReferenceChecker(checker ModelReferenceChecker) RegistryOption {
	return func(registry *Registry) {
		registry.referenceChecker = checker
	}
}

// NewRegistry 创建模型 Registry。
//
// revision 从 1 开始。
//
// 当前 revision 是进程内运行时版本，不需要跨程序重启持久化，
// 因为重启后所有正在运行的 Runtime Snapshot 和 Model Cache 本身都已经消失。
func NewRegistry(
	store *Store,
	credentials *credential.Store,
	logger *logging.Logger,
	options ...RegistryOption,
) *Registry {
	registry := &Registry{
		store: store,

		credentials: credentials,

		factory: NewFactory(credentials),

		logger: logger,

		cache: make(
			map[string]einomodel.ToolCallingChatModel,
		),
	}

	for _, option := range options {
		if option == nil {
			continue
		}
		option(registry)
	}

	registry.revision.Store(1)

	return registry
}

// Revision 返回当前模型配置版本。
//
// 每次成功的 Provider/Model mutation 都会 +1。
func (r *Registry) Revision() uint64 {
	return r.revision.Load()
}

// ListProviders 返回全部 Provider。
func (r *Registry) ListProviders(
	ctx context.Context,
) ([]Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.store.ListProviders(ctx)
}

// ListModels 返回全部 Model。
func (r *Registry) ListModels(
	ctx context.Context,
) ([]ModelInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.store.ListModels(ctx)
}

// CreateProvider 创建 Provider，并安全保存 Credential。
func (r *Registry) CreateProvider(
	ctx context.Context,
	input CreateProviderInput,
) (Provider, error) {
	normalized, err :=
		normalizeCreateProviderInput(input)
	if err != nil {
		return Provider{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()

	provider := Provider{
		ID: uuid.NewString(),

		Name: normalized.Name,

		Type: normalized.Type,

		BaseURL: normalized.BaseURL,

		CreatedAt: now,

		UpdatedAt: now,
	}

	if normalized.APIKey != "" {
		provider.CredentialID =
			"provider-" + provider.ID

		if err := r.credentials.Put(
			ctx,
			provider.CredentialID,
			normalized.APIKey,
		); err != nil {
			return Provider{}, fmt.Errorf(
				"保存 Provider Credential 失败: %w",
				err,
			)
		}
	}

	if err := r.store.CreateProvider(
		ctx,
		provider,
	); err != nil {
		if provider.CredentialID != "" {
			_ = r.credentials.Delete(
				context.Background(),
				provider.CredentialID,
			)
		}

		return Provider{}, err
	}

	r.configurationChangedLocked()

	r.logger.Info(
		ctx,
		"模型 Provider 已创建",
		"provider_id",
		provider.ID,
		"provider_type",
		string(provider.Type),
	)

	return provider, nil
}

// UpdateProvider 更新 Provider。
//
// Credential 更新采用可回滚策略：
//
//  1. 先读取旧 Secret 作为内存备份；
//  2. 写入新 Secret；
//  3. 原子更新 providers.json；
//  4. 文件更新失败时恢复旧 Secret。
//
// API Key 永远不会进入日志。
func (r *Registry) UpdateProvider(
	ctx context.Context,
	id string,
	input UpdateProviderInput,
) (Provider, error) {
	normalized, err :=
		normalizeUpdateProviderInput(input)
	if err != nil {
		return Provider{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	existing, err :=
		r.store.GetProvider(
			ctx,
			id,
		)
	if err != nil {
		return Provider{}, err
	}

	updated := existing

	updated.Name =
		normalized.Name

	updated.Type =
		normalized.Type

	updated.BaseURL =
		normalized.BaseURL

	updated.UpdatedAt =
		time.Now().UTC()

	var backup credentialBackup

	if normalized.UpdateAPIKey {
		backup, err =
			r.backupCredential(
				ctx,
				existing.CredentialID,
			)
		if err != nil {
			return Provider{}, err
		}

		if normalized.APIKey == "" {
			updated.CredentialID = ""
		} else {
			if updated.CredentialID == "" {
				updated.CredentialID =
					"provider-" + updated.ID
			}

			if err := r.credentials.Put(
				ctx,
				updated.CredentialID,
				normalized.APIKey,
			); err != nil {
				return Provider{}, fmt.Errorf(
					"更新 Provider Credential 失败: %w",
					err,
				)
			}
		}
	}

	if err := r.store.UpdateProvider(
		ctx,
		updated,
	); err != nil {
		if normalized.UpdateAPIKey {
			r.rollbackCredential(
				backup,
				updated.CredentialID,
			)
		}

		return Provider{}, err
	}

	if normalized.UpdateAPIKey &&
		existing.CredentialID != "" &&
		existing.CredentialID !=
			updated.CredentialID {

		if err := r.credentials.Delete(
			ctx,
			existing.CredentialID,
		); err != nil {
			r.logger.Warn(
				ctx,
				"Provider 已更新，但旧 Credential 清理失败",
				"provider_id",
				id,
				"error",
				err,
			)
		}
	}

	r.configurationChangedLocked()

	r.logger.Info(
		ctx,
		"模型 Provider 已更新",
		"provider_id",
		id,
		"provider_type",
		string(updated.Type),
	)

	return updated, nil
}

// DeleteProvider 删除未被 Model 使用的 Provider。
func (r *Registry) DeleteProvider(
	ctx context.Context,
	id string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	provider, err :=
		r.store.GetProvider(
			ctx,
			id,
		)
	if err != nil {
		return err
	}

	count, err :=
		r.store.CountModelsByProvider(
			ctx,
			id,
		)
	if err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf(
			"%w: 当前 Provider 下仍有 %d 个模型",
			ErrProviderInUse,
			count,
		)
	}

	backup, err :=
		r.backupCredential(
			ctx,
			provider.CredentialID,
		)
	if err != nil {
		return err
	}

	if provider.CredentialID != "" {
		if err := r.credentials.Delete(
			ctx,
			provider.CredentialID,
		); err != nil {
			return fmt.Errorf(
				"删除 Provider Credential 失败: %w",
				err,
			)
		}
	}

	if err := r.store.DeleteProvider(
		ctx,
		id,
	); err != nil {
		r.restoreCredential(
			backup,
		)

		return err
	}

	r.configurationChangedLocked()

	r.logger.Info(
		ctx,
		"模型 Provider 已删除",
		"provider_id",
		id,
	)

	return nil
}

// CreateModel 创建模型。
func (r *Registry) CreateModel(
	ctx context.Context,
	input CreateModelInput,
) (Model, error) {
	normalized, err :=
		normalizeCreateModelInput(input)
	if err != nil {
		return Model{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, err := r.store.GetProvider(
		ctx,
		normalized.ProviderID,
	); err != nil {
		return Model{}, err
	}

	now := time.Now().UTC()

	value := Model{
		ID: uuid.NewString(),

		ProviderID: normalized.ProviderID,

		ModelName: normalized.ModelName,

		DisplayName: normalized.DisplayName,

		TimeoutMS: normalized.TimeoutMS,

		ContextWindow: normalized.ContextWindow,

		MaxOutputTokens: normalized.MaxOutputTokens,

		Capabilities: normalized.Capabilities,

		Enabled: normalized.Enabled,

		CreatedAt: now,

		UpdatedAt: now,
	}

	if err := r.store.CreateModel(
		ctx,
		value,
	); err != nil {
		return Model{}, err
	}

	r.configurationChangedLocked()

	r.logger.Info(
		ctx,
		"模型已创建",
		"model_id",
		value.ID,
		"provider_id",
		value.ProviderID,
	)

	return value, nil
}

// UpdateModel 更新模型配置。
func (r *Registry) UpdateModel(
	ctx context.Context,
	id string,
	input UpdateModelInput,
) (Model, error) {
	normalized, err :=
		normalizeUpdateModelInput(input)
	if err != nil {
		return Model{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	existing, err :=
		r.store.GetModel(
			ctx,
			id,
		)
	if err != nil {
		return Model{}, err
	}

	if _, err := r.store.GetProvider(
		ctx,
		normalized.ProviderID,
	); err != nil {
		return Model{}, err
	}

	existing.ProviderID =
		normalized.ProviderID

	existing.ModelName =
		normalized.ModelName

	existing.DisplayName =
		normalized.DisplayName

	existing.TimeoutMS =
		normalized.TimeoutMS

	existing.ContextWindow =
		normalized.ContextWindow

	existing.MaxOutputTokens =
		normalized.MaxOutputTokens

	existing.Capabilities =
		normalized.Capabilities

	existing.Enabled =
		normalized.Enabled

	existing.UpdatedAt =
		time.Now().UTC()

	if err := r.store.UpdateModel(
		ctx,
		existing,
	); err != nil {
		return Model{}, err
	}

	r.configurationChangedLocked()

	r.logger.Info(
		ctx,
		"模型配置已更新",
		"model_id",
		id,
	)

	return existing, nil
}

// DeleteModel 删除不再被当前 Agent Profile 的 Chat/Utility/Memory/Vision 角色引用的模型配置。
//
// 历史 Session 不构成删除阻塞条件：AssistantMessage 已经持久化实际 Provider/Model
// 元数据，删除 models.json 中的配置不会破坏历史记录。只有当前 Agent 的任一模型角色
// 仍指向该模型时才拒绝删除，避免产生悬空配置引用。
func (r *Registry) DeleteModel(
	ctx context.Context,
	id string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, err := r.store.GetModel(
		ctx,
		id,
	); err != nil {
		return err
	}

	if r.referenceChecker != nil {
		agentCount, err := r.referenceChecker.CountAgentsByModel(ctx, id)
		if err != nil {
			return fmt.Errorf("检查 Model 的 Agent 引用失败: %w", err)
		}
		if agentCount > 0 {
			return fmt.Errorf(
				"%w: 当前仍有 %d 个 Agent 在 Chat/Utility/Memory/Vision 角色中使用该模型，请先切换相关模型角色",
				ErrModelInUse,
				agentCount,
			)
		}
	}

	if err := r.store.DeleteModel(
		ctx,
		id,
	); err != nil {
		return err
	}

	r.configurationChangedLocked()

	r.logger.Info(
		ctx,
		"模型已删除",
		"model_id",
		id,
	)

	return nil
}

// Resolve 根据 Model ID 返回一个可复用的 Eino ToolCallingChatModel。
//
// Resolve 是后续 RuntimeResolver 唯一应该调用的模型解析接口。
//
// cache 命中时不重新读取 Credential 或创建 HTTP Client。
// 当任何 Provider/Model 配置变化时，整个缓存会被清空，下一次 Resolve
// 自动创建使用最新配置的 Model 实例。
func (r *Registry) Resolve(
	ctx context.Context,
	id string,
) (
	einomodel.ToolCallingChatModel,
	error,
) {
	r.mu.RLock()

	if cached, exists :=
		r.cache[id]; exists {
		r.mu.RUnlock()

		return cached, nil
	}

	r.mu.RUnlock()

	// 使用写锁进行 double-check，
	// 防止多个并发 Turn 同时为同一个 Model 创建多个实例。
	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, exists :=
		r.cache[id]; exists {
		return cached, nil
	}

	resolved, err :=
		r.store.ResolveModel(
			ctx,
			id,
		)
	if err != nil {
		return nil, err
	}

	if !resolved.Model.Enabled {
		return nil, fmt.Errorf(
			"%w: %s",
			ErrModelDisabled,
			id,
		)
	}

	instance, err :=
		r.factory.Create(
			ctx,
			resolved,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建模型运行实例失败: %w",
			err,
		)
	}

	r.cache[id] =
		instance

	return instance, nil
}

// TestModel 真正向模型发送一次最小请求。
//
// 该方法不会使用 Registry cache，确保用户刚刚修改的 Provider/Model
// 配置可以被真实验证。
//
// 测试提示词非常短，但仍可能产生少量 API Token 费用。
func (r *Registry) TestModel(
	ctx context.Context,
	id string,
) (TestResult, error) {
	r.mu.RLock()

	resolved, err :=
		r.store.ResolveModel(
			ctx,
			id,
		)
	if err != nil {
		r.mu.RUnlock()

		return TestResult{}, err
	}

	instance, err :=
		r.factory.Create(
			ctx,
			resolved,
		)

	r.mu.RUnlock()

	if err != nil {
		return TestResult{}, err
	}

	timeout :=
		time.Duration(
			resolved.Model.TimeoutMS,
		) * time.Millisecond

	testCtx, cancel :=
		context.WithTimeout(
			ctx,
			timeout,
		)
	defer cancel()

	started := time.Now()

	response, err :=
		instance.Generate(
			testCtx,
			[]*schema.Message{
				{
					Role: schema.User,

					Content: "Reply with exactly OK.",
				},
			},
		)

	duration :=
		time.Since(
			started,
		)

	if err != nil {
		return TestResult{}, fmt.Errorf(
			"模型连接测试失败: %w",
			err,
		)
	}

	if response == nil {
		return TestResult{}, errors.New(
			"模型请求成功但返回空响应",
		)
	}

	preview :=
		strings.TrimSpace(
			response.Content,
		)

	if preview == "" {
		preview =
			"(请求成功，模型返回空文本)"
	}

	if len(preview) > 200 {
		preview =
			preview[:200]
	}

	r.logger.Info(
		ctx,
		"模型连接测试成功",
		"model_id",
		id,
		logging.Duration(
			started,
		),
	)

	return TestResult{
		Success: true,

		DurationMS: duration.Milliseconds(),

		ResponsePreview: preview,
	}, nil
}

func (r *Registry) configurationChangedLocked() {
	clear(r.cache)

	r.revision.Add(1)
}

type credentialBackup struct {
	ID string

	Value string

	Exists bool
}

func (r *Registry) backupCredential(
	ctx context.Context,
	id string,
) (credentialBackup, error) {
	if id == "" {
		return credentialBackup{}, nil
	}

	value, err :=
		r.credentials.Get(
			ctx,
			id,
		)
	if err != nil {
		if errors.Is(
			err,
			credential.ErrNotFound,
		) {
			return credentialBackup{
				ID: id,
			}, nil
		}

		return credentialBackup{}, fmt.Errorf(
			"备份 Credential 失败: %w",
			err,
		)
	}

	return credentialBackup{
		ID: id,

		Value: value,

		Exists: true,
	}, nil
}

func (r *Registry) rollbackCredential(
	backup credentialBackup,
	newCredentialID string,
) {
	if newCredentialID != "" {
		_ = r.credentials.Delete(
			context.Background(),
			newCredentialID,
		)
	}

	r.restoreCredential(backup)
}

func (r *Registry) restoreCredential(
	backup credentialBackup,
) {
	if !backup.Exists ||
		backup.ID == "" {
		return
	}

	if err := r.credentials.Put(
		context.Background(),
		backup.ID,
		backup.Value,
	); err != nil {
		r.logger.Error(
			context.Background(),
			"恢复 Provider Credential 失败",
			"credential_id",
			backup.ID,
			"error",
			err,
		)
	}
}
