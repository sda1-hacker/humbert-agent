package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
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

// ModelCapabilityConfigDTO 是用户对模型能力的三态覆盖配置。
// 空值在领域层按 auto 处理；Desktop 始终返回规范化后的 auto/enabled/disabled。
type ModelCapabilityConfigDTO struct {
	Tools     string `json:"tools"`
	Vision    string `json:"vision"`
	Files     string `json:"files"`
	Reasoning string `json:"reasoning"`
	JSON      string `json:"json"`
	Audio     string `json:"audio"`
}

// ModelCapabilitiesDTO 是 Runtime 经过 Auto 推断 + Override 后的有效能力。
type ModelCapabilitiesDTO struct {
	Tools     bool `json:"tools"`
	Vision    bool `json:"vision"`
	Files     bool `json:"files"`
	Reasoning bool `json:"reasoning"`
	JSON      bool `json:"json"`
	Audio     bool `json:"audio"`
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

	CapabilityConfig ModelCapabilityConfigDTO `json:"capabilityConfig"`

	Capabilities ModelCapabilitiesDTO `json:"capabilities"`

	Enabled bool `json:"enabled"`

	CreatedAt string `json:"createdAt"`

	UpdatedAt string `json:"updatedAt"`
}

// MultimediaConfigDTO 是设置页维护的应用级多媒体模型路由。
type MultimediaConfigDTO struct {
	ImageModelID string `json:"imageModelID"`
}

// ModelSettingsState 用一次 Bridge 调用返回完整模型设置状态。
type ModelSettingsState struct {
	Revision uint64 `json:"revision"`

	Providers []ProviderDTO `json:"providers"`

	Models []ModelDTO `json:"models"`

	Multimedia MultimediaConfigDTO `json:"multimedia"`
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

	CapabilityConfig ModelCapabilityConfigDTO `json:"capabilityConfig"`

	Enabled bool `json:"enabled"`
}

// TestModelResponse 返回真实连接测试结果。
type TestModelResponse struct {
	Success bool `json:"success"`

	DurationMS int64 `json:"durationMS"`

	ResponsePreview string `json:"responsePreview"`
}

// ModelDiagnostic 是供首次引导和设置页展示的可操作诊断结果。
// 不包含底层响应正文或错误原文，避免把服务端回显的凭据带入 UI。
type ModelDiagnostic struct {
	Success        bool   `json:"success"`
	Category       string `json:"category"`
	Summary        string `json:"summary"`
	Action         string `json:"action"`
	DurationMS     int64  `json:"durationMS"`
	ToolsSupported bool   `json:"toolsSupported"`
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

	multimedia, err := s.core.Models().MultimediaConfig(ctx)
	if err != nil {
		return ModelSettingsState{}, fmt.Errorf("读取多媒体模型配置失败: %w", err)
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

		Multimedia: multimediaConfigDTO(multimedia),
	}, nil
}

// UpdateMultimediaConfig 修改应用级多媒体模型路由。
func (s *ModelService) UpdateMultimediaConfig(request MultimediaConfigDTO) (MultimediaConfigDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	value, err := s.core.Models().SetMultimediaConfig(ctx, models.MultimediaConfig{
		ImageModelID: request.ImageModelID,
	})
	if err != nil {
		return MultimediaConfigDTO{}, fmt.Errorf("更新多媒体模型配置失败: %w", err)
	}
	return multimediaConfigDTO(value), nil
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

					Capabilities: capabilityConfigFromDTO(request.CapabilityConfig),

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

					Capabilities: capabilityConfigFromDTO(request.CapabilityConfig),

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

// DeleteModel 删除不再被当前 Agent 任一模型角色引用的模型。
// 历史 Session 不阻止删除模型配置。
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

// DiagnoseModel 发送最小请求，并把常见连接故障转换成可处理的提示。
func (s *ModelService) DiagnoseModel(id string) (ModelDiagnostic, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	model, err := s.modelDTOByID(ctx, id)
	if err != nil {
		return ModelDiagnostic{}, fmt.Errorf("读取待诊断模型失败: %w", err)
	}
	result := ModelDiagnostic{ToolsSupported: model.Capabilities.Tools}
	if !model.Enabled {
		result.Category, result.Summary, result.Action = "model_disabled", "模型未启用", "在模型设置中启用该模型后重试。"
		return result, nil
	}
	providers, err := s.core.Models().ListProviders(ctx)
	if err != nil {
		return ModelDiagnostic{}, fmt.Errorf("读取供应商失败: %w", err)
	}
	for _, provider := range providers {
		if provider.ID == model.ProviderID && provider.Type == models.ProviderTypeOpenAI && provider.CredentialID == "" {
			result.Category, result.Summary, result.Action = "credential", "尚未配置 API Key", "在供应商设置中填写 API Key 后重试。"
			return result, nil
		}
	}

	started := time.Now()
	test, err := s.core.Models().TestModel(ctx, id)
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Category, result.Summary, result.Action = classifyModelDiagnostic(err)
		return result, nil
	}
	result.Success = test.Success
	result.DurationMS = test.DurationMS
	result.Category = "ok"
	result.Summary = "模型连接成功"
	if model.Capabilities.Tools {
		result.Action = "文本对话已验证；工具调用能力依据模型配置推断，尚未实际测试。"
	} else {
		result.Action = "文本对话已验证。当前未启用工具调用能力；需要工具时请确认模型支持并在模型设置中开启。"
	}
	return result, nil
}

func classifyModelDiagnostic(err error) (category, summary, action string) {
	var urlErr *url.Error
	var netErr net.Error
	lower := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout(), strings.Contains(lower, "timeout"):
		return "timeout", "模型请求超时", "检查网络和服务状态，或适当增加模型请求超时。"
	case strings.Contains(lower, "401"), strings.Contains(lower, "403"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "invalid api key"), strings.Contains(lower, "incorrect api key"), strings.Contains(lower, "credential"), strings.Contains(lower, "api key"):
		return "credential", "凭据被服务拒绝", "检查 API Key、账户权限与供应商地址。"
	case strings.Contains(lower, "404"), strings.Contains(lower, "model not found"), strings.Contains(lower, "model_not_found"):
		return "endpoint", "模型或接口未找到", "检查模型标识和 Base URL；兼容接口通常需要正确的 /v1 路径。"
	case strings.Contains(lower, "429"), strings.Contains(lower, "rate limit"), strings.Contains(lower, "quota"):
		return "quota", "请求受到额度或频率限制", "检查账户额度和频率限制，稍后重试。"
	case strings.Contains(lower, "tool"), strings.Contains(lower, "function call"), strings.Contains(lower, "unsupported parameter"):
		return "capability", "模型能力或请求参数不匹配", "核对模型实际支持的能力及参数，在模型设置中调整能力配置。"
	case strings.Contains(lower, "unsupported protocol scheme"), strings.Contains(lower, "invalid url"), strings.Contains(lower, "no host"):
		return "endpoint", "供应商地址无效", "检查 Base URL 的协议、主机和 API 路径。"
	case errors.As(err, &urlErr), strings.Contains(lower, "connection refused"), strings.Contains(lower, "no such host"), strings.Contains(lower, "certificate"), strings.Contains(lower, "tls"):
		return "network", "无法连接模型服务", "检查 Base URL、网络、代理及本地服务是否运行。"
	default:
		return "unknown", "模型请求失败", "检查供应商日志、模型标识和连接配置后重试。"
	}
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

		CapabilityConfig: capabilityConfigDTO(value.Model.Capabilities),

		Capabilities: capabilitiesDTO(models.EffectiveCapabilities(
			value.ProviderType,
			value.Model.ModelName,
			value.Model.Capabilities,
		)),

		Enabled: value.Model.Enabled,

		CreatedAt: value.Model.CreatedAt.Format(
			time.RFC3339,
		),

		UpdatedAt: value.Model.UpdatedAt.Format(
			time.RFC3339,
		),
	}
}

func capabilityConfigFromDTO(value ModelCapabilityConfigDTO) models.CapabilityConfig {
	return models.CapabilityConfig{
		Tools: models.CapabilityMode(value.Tools), Vision: models.CapabilityMode(value.Vision),
		Files: models.CapabilityMode(value.Files), Reasoning: models.CapabilityMode(value.Reasoning),
		JSON: models.CapabilityMode(value.JSON), Audio: models.CapabilityMode(value.Audio),
	}
}

func capabilityConfigDTO(value models.CapabilityConfig) ModelCapabilityConfigDTO {
	normalize := func(mode models.CapabilityMode) string {
		if mode == "" {
			return string(models.CapabilityAuto)
		}
		return string(mode)
	}
	return ModelCapabilityConfigDTO{
		Tools: normalize(value.Tools), Vision: normalize(value.Vision), Files: normalize(value.Files),
		Reasoning: normalize(value.Reasoning), JSON: normalize(value.JSON), Audio: normalize(value.Audio),
	}
}

func capabilitiesDTO(value models.Capabilities) ModelCapabilitiesDTO {
	return ModelCapabilitiesDTO{
		Tools: value.Tools, Vision: value.Vision, Files: value.Files,
		Reasoning: value.Reasoning, JSON: value.JSON, Audio: value.Audio,
	}
}

func multimediaConfigDTO(value models.MultimediaConfig) MultimediaConfigDTO {
	return MultimediaConfigDTO{ImageModelID: value.ImageModelID}
}
