package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const modelConfigSchemaVersion = 1

type providersDocument struct {
	SchemaVersion int        `json:"schema_version"`
	Providers     []Provider `json:"providers"`
}

type modelsDocument struct {
	SchemaVersion int     `json:"schema_version"`
	Models        []Model `json:"models"`
}

// Store 负责 Provider 与 Model 的文件持久化。
//
// 文件布局：
//
//	~/.humbert-agent/config/providers.json
//	~/.humbert-agent/config/models.json
//
// Store 不读取 Credential、不创建 Eino Model，也不维护 Registry revision。
// 这些职责继续属于 Registry。
//
// 并发模型：Provider/Model 配置修改频率很低，并且两份文件之间存在引用关系，
// 因此 Store 使用一个进程内 RWMutex 保护完整“读取-校验-原子写回”流程。这样会
// 串行化配置编辑，但不会影响不同 Session 的 JSONL 并发写入，也不会重新形成
// SQLite 那种所有 Runtime 写操作竞争同一个 Writer 的问题。
//
// 每次写入都由 atomicfile 使用临时文件 + fsync + rename 提交，进程崩溃时旧版
// 完整配置仍然存在，不会留下半截 JSON。
type Store struct {
	providersFile string
	modelsFile    string

	mu sync.RWMutex
}

// NewStore 创建模型配置 Store，并确保 providers.json / models.json 已初始化。
//
// Model Store 只维护当前 Provider/Model 配置，不依赖 Session/Transcript。历史消息会
// 自己保存实际使用的 Provider/Model 元数据，因此模型配置的生命周期与历史记录解耦。
func NewStore(
	ctx context.Context,
	providersFile string,
	modelsFile string,
) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("初始化 Model Store 被取消: %w", err)
	}

	providersFile = strings.TrimSpace(providersFile)
	modelsFile = strings.TrimSpace(modelsFile)
	if providersFile == "" {
		return nil, errors.New("providers.json 路径不能为空")
	}
	if modelsFile == "" {
		return nil, errors.New("models.json 路径不能为空")
	}
	store := &Store{
		providersFile: providersFile,
		modelsFile:    modelsFile,
	}

	if err := store.ensureDocuments(ctx); err != nil {
		return nil, err
	}

	// 启动时立即完整读取一次，避免配置损坏直到第一次点击设置页才暴露。
	if _, err := store.listProvidersLocked(ctx); err != nil {
		return nil, fmt.Errorf("校验 providers.json 失败: %w", err)
	}
	if _, err := store.listModelsRawLocked(ctx); err != nil {
		return nil, fmt.Errorf("校验 models.json 失败: %w", err)
	}

	return store, nil
}

// ListProviders 返回全部 Provider，按名称和 ID 稳定排序。
func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	values, err := s.listProvidersLocked(ctx)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].ID < values[j].ID
	})
	return values, nil
}

// GetProvider 根据 ID 查询 Provider。
func (s *Store) GetProvider(ctx context.Context, id string) (Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getProviderLocked(ctx, id)
}

// CreateProvider 持久化一个新的 Provider。
func (s *Store) CreateProvider(ctx context.Context, provider Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	values, err := s.listProvidersLocked(ctx)
	if err != nil {
		return err
	}

	for _, existing := range values {
		if existing.ID == provider.ID {
			return fmt.Errorf("Provider ID 已存在: %s", provider.ID)
		}
	}

	values = append(values, provider)
	return s.writeProvidersLocked(ctx, values)
}

// UpdateProvider 更新 Provider 元数据。
func (s *Store) UpdateProvider(ctx context.Context, provider Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	values, err := s.listProvidersLocked(ctx)
	if err != nil {
		return err
	}

	found := false
	for index := range values {
		if values[index].ID != provider.ID {
			continue
		}
		values[index] = provider
		found = true
		break
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, provider.ID)
	}

	return s.writeProvidersLocked(ctx, values)
}

// DeleteProvider 删除 Provider。
//
// Provider 是否仍被 Model 引用由 Registry 在调用本方法前检查。本方法仍会再次
// 检查 ID 是否存在，避免并发或错误调用造成静默成功。
func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	values, err := s.listProvidersLocked(ctx)
	if err != nil {
		return err
	}

	result := make([]Provider, 0, len(values))
	found := false
	for _, value := range values {
		if value.ID == id {
			found = true
			continue
		}
		result = append(result, value)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}

	return s.writeProvidersLocked(ctx, result)
}

// CountModelsByProvider 返回指定 Provider 下的模型数量。
func (s *Store) CountModelsByProvider(ctx context.Context, providerID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	values, err := s.listModelsRawLocked(ctx)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, value := range values {
		if value.ProviderID == providerID {
			count++
		}
	}
	return count, nil
}

// ListModels 返回全部模型以及其 Provider 展示信息。
func (s *Store) ListModels(ctx context.Context) ([]ModelInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	models, err := s.listModelsRawLocked(ctx)
	if err != nil {
		return nil, err
	}
	providers, err := s.listProvidersLocked(ctx)
	if err != nil {
		return nil, err
	}

	providerByID := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		providerByID[provider.ID] = provider
	}

	result := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		provider, exists := providerByID[model.ProviderID]
		if !exists {
			return nil, fmt.Errorf(
				"模型 %s 引用了不存在的 Provider %s",
				model.ID,
				model.ProviderID,
			)
		}

		result = append(result, ModelInfo{
			Model:        model,
			ProviderName: provider.Name,
			ProviderType: provider.Type,
		})
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Model.DisplayName != result[j].Model.DisplayName {
			return result[i].Model.DisplayName < result[j].Model.DisplayName
		}
		return result[i].Model.ID < result[j].Model.ID
	})
	return result, nil
}

// GetModel 返回指定模型。
func (s *Store) GetModel(ctx context.Context, id string) (Model, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getModelLocked(ctx, id)
}

// ResolveModel 在同一个读锁快照内读取 Model + Provider。
//
// 文件存储没有 SQL JOIN，但在一个 Store RWMutex 读临界区内读取两份配置文件，
// 可以保证本进程中的配置写操作不会插入到中间，从而得到逻辑上一致的运行快照。
func (s *Store) ResolveModel(ctx context.Context, id string) (ResolvedModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	model, err := s.getModelLocked(ctx, id)
	if err != nil {
		return ResolvedModel{}, err
	}
	provider, err := s.getProviderLocked(ctx, model.ProviderID)
	if err != nil {
		return ResolvedModel{}, fmt.Errorf("解析模型 Provider 失败: %w", err)
	}

	return ResolvedModel{Provider: provider, Model: model}, nil
}

// CreateModel 创建模型，并在写入前检查同一 Provider 下 ModelName 唯一性。
func (s *Store) CreateModel(ctx context.Context, value Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	models, err := s.listModelsRawLocked(ctx)
	if err != nil {
		return err
	}

	for _, existing := range models {
		if existing.ID == value.ID {
			return fmt.Errorf("Model ID 已存在: %s", value.ID)
		}
		if existing.ProviderID == value.ProviderID && existing.ModelName == value.ModelName {
			return ErrModelConflict
		}
	}

	providers, err := s.listProvidersLocked(ctx)
	if err != nil {
		return err
	}
	if !providerExists(providers, value.ProviderID) {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, value.ProviderID)
	}

	models = append(models, value)
	return s.writeModelsLocked(ctx, models)
}

// UpdateModel 更新模型配置。
func (s *Store) UpdateModel(ctx context.Context, value Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	models, err := s.listModelsRawLocked(ctx)
	if err != nil {
		return err
	}
	providers, err := s.listProvidersLocked(ctx)
	if err != nil {
		return err
	}
	if !providerExists(providers, value.ProviderID) {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, value.ProviderID)
	}

	found := false
	for index, existing := range models {
		if existing.ID != value.ID &&
			existing.ProviderID == value.ProviderID &&
			existing.ModelName == value.ModelName {
			return ErrModelConflict
		}

		if existing.ID == value.ID {
			models[index] = value
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrModelNotFound, value.ID)
	}

	return s.writeModelsLocked(ctx, models)
}

// DeleteModel 删除模型配置。
//
// Session 历史不依赖该配置继续存在。历史 AssistantMessage 会保存实际使用的
// Provider/Model 元数据，因此历史引用检查属于展示数据与当前配置之间不必要的耦合。
func (s *Store) DeleteModel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	models, err := s.listModelsRawLocked(ctx)
	if err != nil {
		return err
	}

	result := make([]Model, 0, len(models))
	found := false
	for _, model := range models {
		if model.ID == id {
			found = true
			continue
		}
		result = append(result, model)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrModelNotFound, id)
	}

	return s.writeModelsLocked(ctx, result)
}

func (s *Store) ensureDocuments(ctx context.Context) error {
	if _, err := os.Lstat(s.providersFile); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("检查 providers.json 失败: %w", err)
		}
		if err := atomicfile.WriteJSON(ctx, s.providersFile, 0o600, providersDocument{
			SchemaVersion: modelConfigSchemaVersion,
			Providers:     []Provider{},
		}); err != nil {
			return fmt.Errorf("初始化 providers.json 失败: %w", err)
		}
	}

	if _, err := os.Lstat(s.modelsFile); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("检查 models.json 失败: %w", err)
		}
		if err := atomicfile.WriteJSON(ctx, s.modelsFile, 0o600, modelsDocument{
			SchemaVersion: modelConfigSchemaVersion,
			Models:        []Model{},
		}); err != nil {
			return fmt.Errorf("初始化 models.json 失败: %w", err)
		}
	}

	return nil
}

func (s *Store) listProvidersLocked(ctx context.Context) ([]Provider, error) {
	var document providersDocument
	if err := atomicfile.ReadJSON(ctx, s.providersFile, &document); err != nil {
		return nil, fmt.Errorf("读取 Provider 配置失败: %w", err)
	}
	if document.SchemaVersion != modelConfigSchemaVersion {
		return nil, fmt.Errorf(
			"不支持的 providers.json schema_version: %d",
			document.SchemaVersion,
		)
	}
	if document.Providers == nil {
		document.Providers = []Provider{}
	}
	return append([]Provider(nil), document.Providers...), nil
}

func (s *Store) listModelsRawLocked(ctx context.Context) ([]Model, error) {
	var document modelsDocument
	if err := atomicfile.ReadJSON(ctx, s.modelsFile, &document); err != nil {
		return nil, fmt.Errorf("读取 Model 配置失败: %w", err)
	}
	if document.SchemaVersion != modelConfigSchemaVersion {
		return nil, fmt.Errorf(
			"不支持的 models.json schema_version: %d",
			document.SchemaVersion,
		)
	}
	if document.Models == nil {
		document.Models = []Model{}
	}

	// schema_version=1 的早期开发文件没有 context_window/max_output_tokens。
	// 这两个字段在 JSON 中缺失时会反序列化为零值。读取阶段只在内存投影中补默认值，
	// 不为了兼容字段在每次启动时主动重写用户配置；下一次用户保存模型时会自然持久化。
	for index := range document.Models {
		document.Models[index] = applyModelDefaults(document.Models[index])
	}

	return append([]Model(nil), document.Models...), nil
}

// applyModelDefaults 为旧配置补齐 ContextEngine 所需的模型预算元数据。
//
// 本函数只处理“字段缺失”的零值，不修复非法非零值。非法值会在模型下一次编辑时由
// normalizeCreateModelInput 拒绝，避免静默篡改用户显式配置。
func applyModelDefaults(value Model) Model {
	if value.ContextWindow == 0 {
		value.ContextWindow = defaultModelContextWindow
	}
	if value.MaxOutputTokens == 0 {
		value.MaxOutputTokens = defaultModelMaxOutputTokens
	}
	return value
}

func (s *Store) getProviderLocked(ctx context.Context, id string) (Provider, error) {
	id = strings.TrimSpace(id)
	values, err := s.listProvidersLocked(ctx)
	if err != nil {
		return Provider{}, err
	}
	for _, value := range values {
		if value.ID == id {
			return value, nil
		}
	}
	return Provider{}, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
}

func (s *Store) getModelLocked(ctx context.Context, id string) (Model, error) {
	id = strings.TrimSpace(id)
	values, err := s.listModelsRawLocked(ctx)
	if err != nil {
		return Model{}, err
	}
	for _, value := range values {
		if value.ID == id {
			return value, nil
		}
	}
	return Model{}, fmt.Errorf("%w: %s", ErrModelNotFound, id)
}

func (s *Store) writeProvidersLocked(ctx context.Context, values []Provider) error {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].ID < values[j].ID
	})
	if err := atomicfile.WriteJSON(ctx, s.providersFile, 0o600, providersDocument{
		SchemaVersion: modelConfigSchemaVersion,
		Providers:     values,
	}); err != nil {
		return fmt.Errorf("写入 providers.json 失败: %w", err)
	}
	return nil
}

func (s *Store) writeModelsLocked(ctx context.Context, values []Model) error {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].DisplayName != values[j].DisplayName {
			return values[i].DisplayName < values[j].DisplayName
		}
		return values[i].ID < values[j].ID
	})
	if err := atomicfile.WriteJSON(ctx, s.modelsFile, 0o600, modelsDocument{
		SchemaVersion: modelConfigSchemaVersion,
		Models:        values,
	}); err != nil {
		return fmt.Errorf("写入 models.json 失败: %w", err)
	}
	return nil
}

func providerExists(values []Provider, providerID string) bool {
	for _, value := range values {
		if value.ID == providerID {
			return true
		}
	}
	return false
}
