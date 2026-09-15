package services

import (
	"context"
	"fmt"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/models"
)

// ProviderDTO 是返回给 Vue 的 Provider 数据。
//
// 不包含 credential_id，也绝不会返回 API Key。
type ProviderDTO struct {
	ID string `json:"id"`

	Name string `json:"name"`

	Type string `json:"type"`

	BaseURL string `json:"baseURL"`

	HasCredential bool `json:"hasCredential"`

	CreatedAt string `json:"createdAt"`

	UpdatedAt string `json:"updatedAt"`
}

// ModelDTO 是返回给 Vue 的 Model 数据。
type ModelDTO struct {
	ID string `json:"id"`

	ProviderID string `json:"providerID"`

	ProviderName string `json:"providerName"`

	ProviderType string `json:"providerType"`

	ModelName string `json:"modelName"`

	DisplayName string `json:"displayName"`

	TimeoutMS int `json:"timeoutMS"`

	ContextWindow int `json:"contextWindow"`

	MaxOutputTokens int `json:"maxOutputTokens"`

	Enabled bool `json:"enabled"`

	CreatedAt string `json:"createdAt"`

	UpdatedAt string `json:"updatedAt"`
}

// ModelSettingsState 用一次 Bridge 调用返回完整模型设置状态。
type ModelSettingsState struct {
	Revision uint64 `json:"revision"`

	Providers []ProviderDTO `json:"providers"`

	Models []ModelDTO `json:"models"`
}

// CreateProviderRequest 描述前端创建 Provider 请求。
type CreateProviderRequest struct {
	Name string `json:"name"`

	Type string `json:"type"`

	BaseURL string `json:"baseURL"`

	APIKey string `json:"apiKey"`
}

// UpdateProviderRequest 描述前端编辑 Provider 请求。
type UpdateProviderRequest struct {
	Name string `json:"name"`

	Type string `json:"type"`

	BaseURL string `json:"baseURL"`

	APIKey string `json:"apiKey"`

	UpdateAPIKey bool `json:"updateAPIKey"`
}

// SaveModelRequest 同时用于新增和编辑 Model。
type SaveModelRequest struct {
	ProviderID string `json:"providerID"`

	ModelName string `json:"modelName"`

	DisplayName string `json:"displayName"`

	TimeoutMS int `json:"timeoutMS"`

	ContextWindow int `json:"contextWindow"`

	MaxOutputTokens int `json:"maxOutputTokens"`

	Enabled bool `json:"enabled"`
}

// TestModelResponse 返回真实连接测试结果。
type TestModelResponse struct {
	Success bool `json:"success"`

	DurationMS int64 `json:"durationMS"`

	ResponsePreview string `json:"responsePreview"`
}

// ModelService 是 Model Registry 的 Wails Adapter。
//
// 所有真正业务逻辑位于 models.Registry，
// 这里仅负责 DTO 转换与 Wails 调用超时。
type ModelService struct {
	core *coreapp.Application
}

// NewModelService 创建 ModelService。
func NewModelService(
	core *coreapp.Application,
) *ModelService {
	return &ModelService{
		core: core,
	}
}

// State 返回 Provider + Model 完整状态。
func (s *ModelService) State() (
	ModelSettingsState,
	error,
) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
	defer cancel()

	providers, err :=
		s.core.Models().
			ListProviders(ctx)
	if err != nil {
		return ModelSettingsState{}, fmt.Errorf(
			"读取 Provider 列表失败: %w",
			err,
		)
	}

	modelList, err :=
		s.core.Models().
			ListModels(ctx)
	if err != nil {
		return ModelSettingsState{}, fmt.Errorf(
			"读取 Model 列表失败: %w",
			err,
		)
	}

	return ModelSettingsState{
		Revision: s.core.Models().
			Revision(),

		Providers: toProviderDTOs(
			providers,
		),

		Models: toModelDTOs(
			modelList,
		),
	}, nil
}

// CreateProvider 创建 Provider。
func (s *ModelService) CreateProvider(
	request CreateProviderRequest,
) (ProviderDTO, error) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
	defer cancel()

	value, err :=
		s.core.Models().
			CreateProvider(
				ctx,
				models.CreateProviderInput{
					Name: request.Name,

					Type: models.ProviderType(
						request.Type,
					),

					BaseURL: request.BaseURL,

					APIKey: request.APIKey,
				},
			)
	if err != nil {
		return ProviderDTO{}, fmt.Errorf(
			"创建 Provider 失败: %w",
			err,
		)
	}

	return toProviderDTO(
		value,
	), nil
}

// UpdateProvider 修改 Provider。
func (s *ModelService) UpdateProvider(
	id string,
	request UpdateProviderRequest,
) (ProviderDTO, error) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
	defer cancel()

	value, err :=
		s.core.Models().
			UpdateProvider(
				ctx,
				id,
				models.UpdateProviderInput{
					Name: request.Name,

					Type: models.ProviderType(
						request.Type,
					),

					BaseURL: request.BaseURL,

					APIKey: request.APIKey,

					UpdateAPIKey: request.UpdateAPIKey,
				},
			)
	if err != nil {
		return ProviderDTO{}, fmt.Errorf(
			"修改 Provider 失败: %w",
			err,
		)
	}

	return toProviderDTO(
		value,
	), nil
}

// DeleteProvider 删除 Provider。
func (s *ModelService) DeleteProvider(
	id string,
) error {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
	defer cancel()

	if err :=
		s.core.Models().
			DeleteProvider(
				ctx,
				id,
			); err != nil {

		return fmt.Errorf(
			"删除 Provider 失败: %w",
			err,
		)
	}

	return nil
}

// CreateModel 创建 Model。
func (s *ModelService) CreateModel(
	request SaveModelRequest,
) (ModelDTO, error) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
	defer cancel()

	value, err :=
		s.core.Models().
			CreateModel(
				ctx,
				models.CreateModelInput{
					ProviderID: request.ProviderID,

					ModelName: request.ModelName,

					DisplayName: request.DisplayName,

					TimeoutMS: request.TimeoutMS,

					ContextWindow: request.ContextWindow,

					MaxOutputTokens: request.MaxOutputTokens,

					Enabled: request.Enabled,
				},
			)
	if err != nil {
		return ModelDTO{}, fmt.Errorf(
			"创建 Model 失败: %w",
			err,
		)
	}

	return s.modelDTOByID(
		ctx,
		value.ID,
	)
}

// UpdateModel 修改 Model。
func (s *ModelService) UpdateModel(
	id string,
	request SaveModelRequest,
) (ModelDTO, error) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
	defer cancel()

	value, err :=
		s.core.Models().
			UpdateModel(
				ctx,
				id,
				models.UpdateModelInput{
					ProviderID: request.ProviderID,

					ModelName: request.ModelName,

					DisplayName: request.DisplayName,

					TimeoutMS: request.TimeoutMS,

					ContextWindow: request.ContextWindow,

					MaxOutputTokens: request.MaxOutputTokens,

					Enabled: request.Enabled,
				},
			)
	if err != nil {
		return ModelDTO{}, fmt.Errorf(
			"修改 Model 失败: %w",
			err,
		)
	}

	return s.modelDTOByID(
		ctx,
		value.ID,
	)
}

// DeleteModel 删除尚未进入 Run Audit 的模型。
func (s *ModelService) DeleteModel(
	id string,
) error {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
	defer cancel()

	if err :=
		s.core.Models().
			DeleteModel(
				ctx,
				id,
			); err != nil {

		return fmt.Errorf(
			"删除 Model 失败: %w",
			err,
		)
	}

	return nil
}

// TestModel 发起一次真实模型 API 请求。
func (s *ModelService) TestModel(
	id string,
) (TestModelResponse, error) {
	// 外部模型请求允许最长 5 分钟。
	// 实际模型自身 Timeout 通常会更早终止。
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			5*time.Minute,
		)
	defer cancel()

	result, err :=
		s.core.Models().
			TestModel(
				ctx,
				id,
			)
	if err != nil {
		return TestModelResponse{}, fmt.Errorf(
			"测试 Model 失败: %w",
			err,
		)
	}

	return TestModelResponse{
		Success: result.Success,

		DurationMS: result.DurationMS,

		ResponsePreview: result.ResponsePreview,
	}, nil
}

func (s *ModelService) modelDTOByID(
	ctx context.Context,
	id string,
) (ModelDTO, error) {
	values, err :=
		s.core.Models().
			ListModels(ctx)
	if err != nil {
		return ModelDTO{}, err
	}

	for _, value := range values {
		if value.Model.ID == id {
			return toModelDTO(
				value,
			), nil
		}
	}

	return ModelDTO{}, fmt.Errorf(
		"保存后的模型 %s 未找到",
		id,
	)
}

func toProviderDTOs(
	values []models.Provider,
) []ProviderDTO {
	result := make(
		[]ProviderDTO,
		0,
		len(values),
	)

	for _, value := range values {
		result = append(
			result,
			toProviderDTO(value),
		)
	}

	return result
}

func toProviderDTO(
	value models.Provider,
) ProviderDTO {
	return ProviderDTO{
		ID: value.ID,

		Name: value.Name,

		Type: string(value.Type),

		BaseURL: value.BaseURL,

		HasCredential: value.CredentialID != "",

		CreatedAt: value.CreatedAt.Format(
			time.RFC3339,
		),

		UpdatedAt: value.UpdatedAt.Format(
			time.RFC3339,
		),
	}
}

func toModelDTOs(
	values []models.ModelInfo,
) []ModelDTO {
	result := make(
		[]ModelDTO,
		0,
		len(values),
	)

	for _, value := range values {
		result = append(
			result,
			toModelDTO(value),
		)
	}

	return result
}

func toModelDTO(
	value models.ModelInfo,
) ModelDTO {
	return ModelDTO{
		ID: value.Model.ID,

		ProviderID: value.Model.ProviderID,

		ProviderName: value.ProviderName,

		ProviderType: string(
			value.ProviderType,
		),

		ModelName: value.Model.ModelName,

		DisplayName: value.Model.DisplayName,

		TimeoutMS: value.Model.TimeoutMS,

		ContextWindow: value.Model.ContextWindow,

		MaxOutputTokens: value.Model.MaxOutputTokens,

		Enabled: value.Model.Enabled,

		CreatedAt: value.Model.CreatedAt.Format(
			time.RFC3339,
		),

		UpdatedAt: value.Model.UpdatedAt.Format(
			time.RFC3339,
		),
	}
}
