package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const mcpServiceTimeout = 30 * time.Second

// MCPHTTPHeaderDTO 是返回给前端的非敏感 Header 配置投影。
// Header Value 与 CredentialID 都不会离开后端。
type MCPHTTPHeaderDTO struct {
	Name          string `json:"name"`
	HasCredential bool   `json:"hasCredential"`
}

// MCPStdioEnvDTO 是返回给前端的非敏感 stdio 环境变量投影。
// Value 与 CredentialID 都不会离开后端。
type MCPStdioEnvDTO struct {
	Name          string `json:"name"`
	HasCredential bool   `json:"hasCredential"`
}

// MCPServerDTO 是 Desktop 层的 MCP Server 配置投影。
// 任何 Secret 都不会通过该 DTO 回显。
type MCPServerDTO struct {
	ID                  string             `json:"id"`
	Key                 string             `json:"key"`
	Name                string             `json:"name"`
	Enabled             bool               `json:"enabled"`
	Transport           string             `json:"transport"`
	Command             string             `json:"command,omitempty"`
	Args                []string           `json:"args,omitempty"`
	WorkingDirectory    string             `json:"workingDirectory,omitempty"`
	Env                 []MCPStdioEnvDTO   `json:"env,omitempty"`
	Endpoint            string             `json:"endpoint,omitempty"`
	HasBearerCredential bool               `json:"hasBearerCredential"`
	Headers             []MCPHTTPHeaderDTO `json:"headers,omitempty"`
	Fingerprint         string             `json:"fingerprint"`
	ConnectionState     string             `json:"connectionState"`
	Connected           bool               `json:"connected"`
	ConnectedAt         string             `json:"connectedAt,omitempty"`
	LastUsedAt          string             `json:"lastUsedAt,omitempty"`
	LastSuccessAt       string             `json:"lastSuccessAt,omitempty"`
	LastError           string             `json:"lastError,omitempty"`
	LastErrorAt         string             `json:"lastErrorAt,omitempty"`
	CreatedAt           string             `json:"createdAt"`
	UpdatedAt           string             `json:"updatedAt"`
}

// MCPHTTPHeaderRequest 是前端提交的自定义 Header。
// Secret 只存在于本次 Wails 调用内存中；保存后进入 CredentialStore。
type MCPHTTPHeaderRequest struct {
	Name   string `json:"name"`
	Secret string `json:"secret"`
}

// MCPStdioEnvRequest 是前端提交的 stdio 子进程环境变量。
// Value 只存在于本次 Wails 调用内存中；保存后进入 CredentialStore。
type MCPStdioEnvRequest struct {
	Name   string `json:"name"`
	Secret string `json:"secret"`
}

// MCPServerRequest 是创建/更新 Server 的 Desktop 输入。
// Update 时 Key 会被忽略；Server Key 一旦创建即保持稳定。
type MCPServerRequest struct {
	Key              string                 `json:"key"`
	Name             string                 `json:"name"`
	Transport        string                 `json:"transport"`
	Command          string                 `json:"command"`
	Args             []string               `json:"args"`
	WorkingDirectory string                 `json:"workingDirectory"`
	Env              []MCPStdioEnvRequest   `json:"env"`
	Endpoint         string                 `json:"endpoint"`
	BearerEnabled    bool                   `json:"bearerEnabled"`
	BearerToken      string                 `json:"bearerToken"`
	Headers          []MCPHTTPHeaderRequest `json:"headers"`
}

// MCPToolSelectionDTO 是 Agent enabled_mcp_tools 的 Desktop 表示。
type MCPToolSelectionDTO struct {
	ServerID string   `json:"serverID"`
	Tools    []string `json:"tools"`
}

// MCPToolDTO 是连接器页面使用的 MCP Tool Catalog 投影。
type MCPToolDTO struct {
	RawName        string         `json:"rawName"`
	ExposedName    string         `json:"exposedName"`
	Description    string         `json:"description"`
	InputSchema    map[string]any `json:"inputSchema,omitempty"`
	Annotations    map[string]any `json:"annotations,omitempty"`
	Risk           string         `json:"risk"`
	RiskOverridden bool           `json:"riskOverridden"`
}

// MCPPortableServerDTO 是可导出/导入的非敏感 MCP 配置。Credential Value、CredentialID、
// Runtime 状态与内部 ID 均不会进入导出文件。导入后 Server 默认保持停用，用户需要重新
// 填写认证信息并显式启用。
type MCPPortableServerDTO struct {
	Key              string            `json:"key"`
	Name             string            `json:"name"`
	Transport        string            `json:"transport"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	WorkingDirectory string            `json:"workingDirectory,omitempty"`
	Endpoint         string            `json:"endpoint,omitempty"`
	ToolRisks        map[string]string `json:"toolRisks,omitempty"`
	CredentialHints  []string          `json:"credentialHints,omitempty"`
}

type MCPPortableBundleDTO struct {
	Version int                    `json:"version"`
	Servers []MCPPortableServerDTO `json:"servers"`
}

// MCPConnectionTestDTO 表示一次真实 MCP 连接 + tools/list 的结果。
type MCPConnectionTestDTO struct {
	OK         bool  `json:"ok"`
	ToolCount  int   `json:"toolCount"`
	DurationMS int64 `json:"durationMS"`
}

// MCPService 暴露 MCP 控制面 API。
type MCPService struct {
	core *coreapp.Application
}

func NewMCPService(core *coreapp.Application) *MCPService {
	return &MCPService{core: core}
}

func (s *MCPService) ServiceName() string {
	return "MCPService"
}

func (s *MCPService) ListServers() ([]MCPServerDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	values, err := s.core.MCP().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 MCP Server 列表失败: %w", err)
	}
	result := make([]MCPServerDTO, 0, len(values))
	for _, value := range values {
		status, statusErr := s.core.MCP().RuntimeStatus(ctx, value.ID)
		if statusErr != nil {
			return nil, fmt.Errorf("读取 MCP Server 运行状态失败: %w", statusErr)
		}
		result = append(result, toMCPServerDTO(value, status))
	}
	return result, nil
}

func (s *MCPService) CreateServer(request MCPServerRequest) (MCPServerDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()

	transport := humbertmcp.Transport(request.Transport)
	value, err := s.core.MCP().Create(ctx, humbertmcp.CreateServerInput{
		Key:       request.Key,
		Name:      request.Name,
		Transport: transport,
		Stdio:     requestStdioWithoutCredentials(request),
		HTTP:      requestHTTPWithoutCredentials(request),
	})
	if err != nil {
		return MCPServerDTO{}, fmt.Errorf("创建 MCP Server 失败: %w", err)
	}

	var createdCredentialIDs []string
	cleanupCreatedServer := func(cause error) (MCPServerDTO, error) {
		s.cleanupCredentials(ctx, createdCredentialIDs)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_ = s.core.MCP().Delete(cleanupCtx, value.ID)
		return MCPServerDTO{}, cause
	}

	switch transport {
	case humbertmcp.TransportStdio:
		if len(request.Env) > 0 {
			configured, created, configureErr := s.prepareStdioConfig(ctx, value.ID, nil, request)
			createdCredentialIDs = created
			if configureErr != nil {
				return cleanupCreatedServer(fmt.Errorf("创建 MCP stdio Credential 失败: %w", configureErr))
			}
			value, err = s.core.MCP().Update(ctx, value.ID, humbertmcp.UpdateServerInput{
				Name:      request.Name,
				Transport: transport,
				Stdio:     configured,
			})
			if err != nil {
				return cleanupCreatedServer(fmt.Errorf("保存 MCP stdio Credential 配置失败: %w", err))
			}
		}

	case humbertmcp.TransportStreamableHTTP:
		configured, created, configureErr := s.prepareHTTPConfig(ctx, value.ID, nil, request)
		createdCredentialIDs = created
		if configureErr != nil {
			return cleanupCreatedServer(fmt.Errorf("创建 MCP HTTP Credential 失败: %w", configureErr))
		}
		value, err = s.core.MCP().Update(ctx, value.ID, humbertmcp.UpdateServerInput{
			Name:      request.Name,
			Transport: transport,
			HTTP:      configured,
		})
		if err != nil {
			return cleanupCreatedServer(fmt.Errorf("保存 MCP HTTP 配置失败: %w", err))
		}
	}

	return toMCPServerDTO(value, humbertmcp.RuntimeStatus{}), nil
}

func (s *MCPService) UpdateServer(id string, request MCPServerRequest) (MCPServerDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()

	existing, err := s.core.MCP().Get(ctx, id)
	if err != nil {
		return MCPServerDTO{}, fmt.Errorf("读取 MCP Server 失败: %w", err)
	}

	transport := humbertmcp.Transport(request.Transport)
	var stdioConfig *humbertmcp.StdioConfig
	var httpConfig *humbertmcp.HTTPConfig
	var createdCredentialIDs []string

	switch transport {
	case humbertmcp.TransportStdio:
		var previous *humbertmcp.StdioConfig
		if existing.Transport == humbertmcp.TransportStdio {
			previous = existing.Stdio
		}
		stdioConfig, createdCredentialIDs, err = s.prepareStdioConfig(ctx, existing.ID, previous, request)
		if err != nil {
			return MCPServerDTO{}, fmt.Errorf("准备 MCP stdio Credential 失败: %w", err)
		}

	case humbertmcp.TransportStreamableHTTP:
		var previous *humbertmcp.HTTPConfig
		if existing.Transport == humbertmcp.TransportStreamableHTTP {
			previous = existing.HTTP
		}
		httpConfig, createdCredentialIDs, err = s.prepareHTTPConfig(ctx, existing.ID, previous, request)
		if err != nil {
			return MCPServerDTO{}, fmt.Errorf("准备 MCP HTTP Credential 失败: %w", err)
		}
	}

	value, err := s.core.MCP().Update(ctx, id, humbertmcp.UpdateServerInput{
		Name:      request.Name,
		Transport: transport,
		Stdio:     stdioConfig,
		HTTP:      httpConfig,
	})
	if err != nil {
		s.cleanupCredentials(ctx, createdCredentialIDs)
		return MCPServerDTO{}, fmt.Errorf("更新 MCP Server 失败: %w", err)
	}

	// Server 已成功指向新 Credential 后再删除旧引用；清理失败不会回滚已提交配置。
	s.cleanupCredentials(ctx, obsoleteServerCredentialIDs(existing, value))
	return toMCPServerDTO(value, humbertmcp.RuntimeStatus{}), nil
}

// ExportServersConfig 导出不包含 Secret 的可移植 MCP 配置 JSON。
func (s *MCPService) ExportServersConfig() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	values, err := s.core.MCP().List(ctx)
	if err != nil {
		return "", fmt.Errorf("导出 MCP Server 配置失败: %w", err)
	}
	bundle := MCPPortableBundleDTO{Version: 1, Servers: make([]MCPPortableServerDTO, 0, len(values))}
	for _, value := range values {
		item := MCPPortableServerDTO{
			Key: value.Key, Name: value.Name, Transport: string(value.Transport),
			ToolRisks: make(map[string]string, len(value.ToolRisks)),
		}
		for name, risk := range value.ToolRisks {
			item.ToolRisks[name] = string(risk)
		}
		if len(item.ToolRisks) == 0 {
			item.ToolRisks = nil
		}
		switch value.Transport {
		case humbertmcp.TransportStdio:
			if value.Stdio != nil {
				item.Command = value.Stdio.Command
				item.Args = append([]string(nil), value.Stdio.Args...)
				item.WorkingDirectory = value.Stdio.WorkingDirectory
				for _, env := range value.Stdio.Env {
					item.CredentialHints = append(item.CredentialHints, "env:"+env.Name)
				}
			}
		case humbertmcp.TransportStreamableHTTP:
			if value.HTTP != nil {
				item.Endpoint = value.HTTP.Endpoint
				if value.HTTP.BearerCredentialID != "" {
					item.CredentialHints = append(item.CredentialHints, "bearer")
				}
				for _, header := range value.HTTP.Headers {
					item.CredentialHints = append(item.CredentialHints, "header:"+header.Name)
				}
			}
		}
		bundle.Servers = append(bundle.Servers, item)
	}
	encoded, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", fmt.Errorf("编码 MCP 导出配置失败: %w", err)
	}
	return string(encoded), nil
}

// ImportServersConfig 导入 Humbert 非敏感 MCP 配置。为防止缺失 Secret 的配置立即进入
// Runtime，所有导入 Server 都默认 disabled；CredentialHints 仅用于提醒用户重新填写。
func (s *MCPService) ImportServersConfig(payload string) ([]MCPServerDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	var bundle MCPPortableBundleDTO
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &bundle); err != nil {
		return nil, fmt.Errorf("解析 MCP 导入配置失败: %w", err)
	}
	if bundle.Version != 1 {
		return nil, fmt.Errorf("不支持的 MCP 配置版本: %d", bundle.Version)
	}
	if len(bundle.Servers) == 0 {
		return nil, errors.New("导入配置中没有 MCP Server")
	}
	if len(bundle.Servers) > 100 {
		return nil, errors.New("一次最多导入 100 个 MCP Server")
	}
	created := make([]humbertmcp.Server, 0, len(bundle.Servers))
	rollback := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, item := range created {
			_ = s.core.MCP().Delete(cleanupCtx, item.ID)
		}
	}
	for _, item := range bundle.Servers {
		transport := humbertmcp.Transport(item.Transport)
		input := humbertmcp.CreateServerInput{Key: item.Key, Name: item.Name, Transport: transport}
		switch transport {
		case humbertmcp.TransportStdio:
			input.Stdio = &humbertmcp.StdioConfig{Command: item.Command, Args: append([]string(nil), item.Args...), WorkingDirectory: item.WorkingDirectory}
		case humbertmcp.TransportStreamableHTTP:
			input.HTTP = &humbertmcp.HTTPConfig{Endpoint: item.Endpoint}
		default:
			rollback()
			return nil, fmt.Errorf("MCP Server %q 的 transport %q 不支持", item.Name, item.Transport)
		}
		value, err := s.core.MCP().Create(ctx, input)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("导入 MCP Server %q 失败: %w", item.Name, err)
		}
		created = append(created, value)
		for rawName, rawRisk := range item.ToolRisks {
			risk := humberttools.RiskLevel(strings.ToLower(strings.TrimSpace(rawRisk)))
			if _, err := s.core.MCP().SetToolRisk(ctx, value.ID, rawName, risk); err != nil {
				rollback()
				return nil, fmt.Errorf("恢复 MCP Tool Risk 失败: %w", err)
			}
		}
		value, err = s.core.MCP().SetEnabled(ctx, value.ID, false)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("停用新导入 MCP Server 失败: %w", err)
		}
		created[len(created)-1] = value
	}
	result := make([]MCPServerDTO, 0, len(created))
	for _, value := range created {
		result = append(result, toMCPServerDTO(value, humbertmcp.RuntimeStatus{State: humbertmcp.ConnectionDisabled}))
	}
	return result, nil
}

// RefreshTools 强制跳过控制面 Catalog 缓存执行真实 tools/list。
func (s *MCPService) RefreshTools(serverID string) ([]MCPToolDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	values, err := s.core.MCP().DiscoverToolsFresh(ctx, serverID, true)
	if err != nil {
		return nil, fmt.Errorf("刷新 MCP Tools 失败: %w", err)
	}
	result := make([]MCPToolDTO, 0, len(values))
	for _, value := range values {
		result = append(result, MCPToolDTO{RawName: value.RawName, ExposedName: value.ExposedName, Description: value.Description, InputSchema: value.InputSchema, Annotations: value.Annotations, Risk: string(value.Risk), RiskOverridden: value.RiskOverridden})
	}
	return result, nil
}

// SetServerEnabled 只切换 MCP Server 控制面状态；Agent Tool Selection 与 Credential 保留。
func (s *MCPService) SetServerEnabled(id string, enabled bool) (MCPServerDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	value, err := s.core.MCP().SetEnabled(ctx, id, enabled)
	if err != nil {
		return MCPServerDTO{}, fmt.Errorf("更新 MCP Server 启用状态失败: %w", err)
	}
	status, err := s.core.MCP().RuntimeStatus(ctx, value.ID)
	if err != nil {
		return MCPServerDTO{}, fmt.Errorf("读取 MCP Server 运行状态失败: %w", err)
	}
	return toMCPServerDTO(value, status), nil
}

// DisconnectServer 主动释放 MCP Session，不修改任何持久化配置。
func (s *MCPService) DisconnectServer(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	if err := s.core.MCP().Disconnect(ctx, id); err != nil {
		return fmt.Errorf("断开 MCP Server 失败: %w", err)
	}
	return nil
}

func (s *MCPService) DeleteServer(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()

	existing, err := s.core.MCP().Get(ctx, id)
	if err != nil {
		return fmt.Errorf("读取 MCP Server 失败: %w", err)
	}
	if err := s.core.MCP().Delete(ctx, id); err != nil {
		return fmt.Errorf("删除 MCP Server 失败: %w", err)
	}
	s.cleanupCredentials(ctx, serverCredentialIDs(existing))
	return nil
}

// TestConnection 使用与 Runtime 完全相同的 Session Backend 测试连接。
func (s *MCPService) TestConnection(serverID string) (MCPConnectionTestDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	started := time.Now()
	count, err := s.core.MCP().TestConnection(ctx, serverID)
	if err != nil {
		return MCPConnectionTestDTO{}, fmt.Errorf("测试 MCP Server 连接失败: %w", err)
	}
	return MCPConnectionTestDTO{OK: true, ToolCount: count, DurationMS: time.Since(started).Milliseconds()}, nil
}

// DiscoverTools 返回 Server 当前真实 tools/list；它不会自动修改任何 Agent 配置。
func (s *MCPService) DiscoverTools(serverID string) ([]MCPToolDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	values, err := s.core.MCP().DiscoverTools(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("发现 MCP Tools 失败: %w", err)
	}
	result := make([]MCPToolDTO, 0, len(values))
	for _, value := range values {
		result = append(result, MCPToolDTO{
			RawName:        value.RawName,
			ExposedName:    value.ExposedName,
			Description:    value.Description,
			InputSchema:    value.InputSchema,
			Annotations:    value.Annotations,
			Risk:           string(value.Risk),
			RiskOverridden: value.RiskOverridden,
		})
	}
	return result, nil
}

// SetToolRisk 保存用户对一个 MCP Tool 的 Humbert Risk Override。
// Server annotation 只是参考；真正进入 PermissionEngine 的 Risk 只来自这里的显式用户选择。
func (s *MCPService) SetToolRisk(serverID string, rawToolName string, risk string) error {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	level := humberttools.RiskLevel(strings.ToLower(strings.TrimSpace(risk)))
	if _, err := s.core.MCP().SetToolRisk(ctx, serverID, rawToolName, level); err != nil {
		return fmt.Errorf("更新 MCP Tool Risk 失败: %w", err)
	}
	return nil
}

// GetAgentTools 返回指定 Agent 当前持久化的 MCP Tool Selection。
// 连接器 UI 只读取 Agent Profile 这一份事实来源，不在 MCP Server 侧维护反向引用。
func (s *MCPService) GetAgentTools(agentID string) ([]MCPToolSelectionDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	value, err := s.core.Agents().Get(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("读取 Agent MCP Tool 配置失败: %w", err)
	}
	result := make([]MCPToolSelectionDTO, 0, len(value.Agent.EnabledMCPTools))
	for _, selection := range value.Agent.EnabledMCPTools {
		result = append(result, MCPToolSelectionDTO{
			ServerID: selection.ServerID,
			Tools:    append([]string(nil), selection.Tools...),
		})
	}
	return result, nil
}

// SetAgentTools 是“连接器”页面保存 Tool 开关的唯一入口。
func (s *MCPService) SetAgentTools(agentID string, selections []MCPToolSelectionDTO) error {
	ctx, cancel := context.WithTimeout(context.Background(), mcpServiceTimeout)
	defer cancel()
	values := make([]humbertmcp.ToolSelection, 0, len(selections))
	for _, selection := range selections {
		values = append(values, humbertmcp.ToolSelection{
			ServerID: selection.ServerID,
			Tools:    append([]string(nil), selection.Tools...),
		})
	}
	if _, err := s.core.Agents().SetMCPToolsForAgent(ctx, agentID, values); err != nil {
		return fmt.Errorf("保存 Agent MCP Tool 配置失败: %w", err)
	}
	return nil
}

func (s *MCPService) prepareStdioConfig(
	ctx context.Context,
	serverID string,
	existing *humbertmcp.StdioConfig,
	request MCPServerRequest,
) (*humbertmcp.StdioConfig, []string, error) {
	if len(request.Env) > humbertmcp.MaxStdioEnvVars {
		return nil, nil, fmt.Errorf("stdio 环境变量数量不能超过 %d", humbertmcp.MaxStdioEnvVars)
	}

	validationConfig := requestStdioWithoutCredentials(request)
	for _, item := range request.Env {
		validationConfig.Env = append(validationConfig.Env, humbertmcp.StdioEnvCredential{
			Name:         strings.TrimSpace(item.Name),
			CredentialID: "pending",
		})
	}
	if err := humbertmcp.ValidateStdioConfig(validationConfig); err != nil {
		return nil, nil, err
	}

	result := requestStdioWithoutCredentials(request)
	created := make([]string, 0, len(request.Env))
	cleanupOnError := func(err error) (*humbertmcp.StdioConfig, []string, error) {
		s.cleanupCredentials(ctx, created)
		return nil, nil, err
	}

	existingEnv := make(map[string]humbertmcp.StdioEnvCredential)
	if existing != nil {
		for _, item := range existing.Env {
			existingEnv[strings.ToLower(strings.TrimSpace(item.Name))] = item
		}
	}
	for _, item := range request.Env {
		name := strings.TrimSpace(item.Name)
		secret := strings.TrimSpace(item.Secret)
		credentialID := ""
		if secret == "" {
			if previous, ok := existingEnv[strings.ToLower(name)]; ok {
				credentialID = previous.CredentialID
			} else {
				return cleanupOnError(fmt.Errorf("stdio 环境变量 %q 的 Secret 不能为空", name))
			}
		} else {
			credentialID = newMCPCredentialID(serverID, "env")
			if err := s.core.Credentials().Put(ctx, credentialID, secret); err != nil {
				return cleanupOnError(fmt.Errorf("保存 stdio 环境变量 %q Credential 失败: %w", name, err))
			}
			created = append(created, credentialID)
		}
		result.Env = append(result.Env, humbertmcp.StdioEnvCredential{
			Name:         name,
			CredentialID: credentialID,
		})
	}
	if err := humbertmcp.ValidateStdioConfig(result); err != nil {
		return cleanupOnError(err)
	}
	return result, created, nil
}

func (s *MCPService) prepareHTTPConfig(
	ctx context.Context,
	serverID string,
	existing *humbertmcp.HTTPConfig,
	request MCPServerRequest,
) (*humbertmcp.HTTPConfig, []string, error) {
	if len(request.Headers) > humbertmcp.MaxHTTPHeaders {
		return nil, nil, fmt.Errorf("自定义 HTTP Header 数量不能超过 %d", humbertmcp.MaxHTTPHeaders)
	}

	// 在写入任何 Secret 之前先用占位 Credential ID 完成 Endpoint/Header 领域校验。
	validationConfig := &humbertmcp.HTTPConfig{Endpoint: strings.TrimSpace(request.Endpoint)}
	if request.BearerEnabled {
		validationConfig.BearerCredentialID = "pending"
	}
	seen := make(map[string]struct{}, len(request.Headers))
	for _, item := range request.Headers {
		name := http.CanonicalHeaderKey(strings.TrimSpace(item.Name))
		if err := humbertmcp.ValidateHTTPHeaderName(name); err != nil {
			return nil, nil, err
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return nil, nil, fmt.Errorf("自定义 HTTP Header %q 重复", name)
		}
		seen[key] = struct{}{}
		validationConfig.Headers = append(validationConfig.Headers, humbertmcp.HTTPHeaderCredential{
			Name:         name,
			CredentialID: "pending",
		})
	}
	if err := humbertmcp.ValidateHTTPConfig(validationConfig); err != nil {
		return nil, nil, err
	}

	result := &humbertmcp.HTTPConfig{Endpoint: strings.TrimSpace(request.Endpoint)}
	created := make([]string, 0, 1+len(request.Headers))
	cleanupOnError := func(err error) (*humbertmcp.HTTPConfig, []string, error) {
		s.cleanupCredentials(ctx, created)
		return nil, nil, err
	}

	if request.BearerEnabled {
		secret := strings.TrimSpace(request.BearerToken)
		if secret == "" && existing != nil && strings.TrimSpace(existing.BearerCredentialID) != "" {
			result.BearerCredentialID = existing.BearerCredentialID
		} else {
			if secret == "" {
				return cleanupOnError(errors.New("Bearer Token 不能为空"))
			}
			credentialID := newMCPCredentialID(serverID, "bearer")
			if err := s.core.Credentials().Put(ctx, credentialID, secret); err != nil {
				return cleanupOnError(fmt.Errorf("保存 Bearer Token 失败: %w", err))
			}
			created = append(created, credentialID)
			result.BearerCredentialID = credentialID
		}
	}

	existingHeaders := make(map[string]humbertmcp.HTTPHeaderCredential)
	if existing != nil {
		for _, item := range existing.Headers {
			existingHeaders[strings.ToLower(strings.TrimSpace(item.Name))] = item
		}
	}
	for _, item := range request.Headers {
		name := http.CanonicalHeaderKey(strings.TrimSpace(item.Name))
		secret := strings.TrimSpace(item.Secret)
		credentialID := ""
		if secret == "" {
			if previous, ok := existingHeaders[strings.ToLower(name)]; ok {
				credentialID = previous.CredentialID
			} else {
				return cleanupOnError(fmt.Errorf("HTTP Header %q 的 Secret 不能为空", name))
			}
		} else {
			credentialID = newMCPCredentialID(serverID, "header")
			if err := s.core.Credentials().Put(ctx, credentialID, secret); err != nil {
				return cleanupOnError(fmt.Errorf("保存 HTTP Header %q Credential 失败: %w", name, err))
			}
			created = append(created, credentialID)
		}
		result.Headers = append(result.Headers, humbertmcp.HTTPHeaderCredential{
			Name:         name,
			CredentialID: credentialID,
		})
	}
	if err := humbertmcp.ValidateHTTPConfig(result); err != nil {
		return cleanupOnError(err)
	}
	return result, created, nil
}

func (s *MCPService) cleanupCredentials(ctx context.Context, ids []string) {
	// 清理不能复用一个已经超时/取消的请求 Context，否则错误路径可能留下孤立 Secret。
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if err := s.core.Credentials().Delete(cleanupCtx, id); err != nil {
			s.core.Logger().Warn(ctx, "清理 MCP Credential 失败", "operation", "mcp.credential.cleanup", "credential_id", id, "error", err)
		}
	}
}

func toMCPServerDTO(value humbertmcp.Server, status humbertmcp.RuntimeStatus) MCPServerDTO {
	dto := MCPServerDTO{
		ID:              value.ID,
		Key:             value.Key,
		Name:            value.Name,
		Enabled:         value.Enabled,
		Transport:       string(value.Transport),
		Fingerprint:     humbertmcp.ServerFingerprint(value),
		ConnectionState: string(status.State),
		Connected:       status.Connected,
		CreatedAt:       value.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:       value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !status.ConnectedAt.IsZero() {
		dto.ConnectedAt = status.ConnectedAt.UTC().Format(time.RFC3339Nano)
	}
	if !status.LastUsedAt.IsZero() {
		dto.LastUsedAt = status.LastUsedAt.UTC().Format(time.RFC3339Nano)
	}
	if !status.LastSuccessAt.IsZero() {
		dto.LastSuccessAt = status.LastSuccessAt.UTC().Format(time.RFC3339Nano)
	}
	dto.LastError = strings.TrimSpace(status.LastError)
	if !status.LastErrorAt.IsZero() {
		dto.LastErrorAt = status.LastErrorAt.UTC().Format(time.RFC3339Nano)
	}
	if value.Stdio != nil {
		dto.Command = value.Stdio.Command
		dto.Args = append([]string(nil), value.Stdio.Args...)
		dto.WorkingDirectory = value.Stdio.WorkingDirectory
		dto.Env = make([]MCPStdioEnvDTO, 0, len(value.Stdio.Env))
		for _, item := range value.Stdio.Env {
			dto.Env = append(dto.Env, MCPStdioEnvDTO{
				Name:          item.Name,
				HasCredential: strings.TrimSpace(item.CredentialID) != "",
			})
		}
	}
	if value.HTTP != nil {
		dto.Endpoint = value.HTTP.Endpoint
		dto.HasBearerCredential = strings.TrimSpace(value.HTTP.BearerCredentialID) != ""
		dto.Headers = make([]MCPHTTPHeaderDTO, 0, len(value.HTTP.Headers))
		for _, header := range value.HTTP.Headers {
			dto.Headers = append(dto.Headers, MCPHTTPHeaderDTO{
				Name:          header.Name,
				HasCredential: strings.TrimSpace(header.CredentialID) != "",
			})
		}
	}
	return dto
}

func requestStdioWithoutCredentials(request MCPServerRequest) *humbertmcp.StdioConfig {
	if humbertmcp.Transport(request.Transport) != humbertmcp.TransportStdio {
		return nil
	}
	return &humbertmcp.StdioConfig{
		Command:          request.Command,
		Args:             append([]string(nil), request.Args...),
		WorkingDirectory: request.WorkingDirectory,
	}
}

func requestHTTPWithoutCredentials(request MCPServerRequest) *humbertmcp.HTTPConfig {
	if humbertmcp.Transport(request.Transport) != humbertmcp.TransportStreamableHTTP {
		return nil
	}
	return &humbertmcp.HTTPConfig{Endpoint: strings.TrimSpace(request.Endpoint)}
}

func newMCPCredentialID(serverID string, kind string) string {
	return fmt.Sprintf("mcp.%s.%s.%s", strings.TrimSpace(serverID), kind, uuid.NewString())
}

func serverCredentialIDs(server humbertmcp.Server) []string {
	result := make([]string, 0)
	if server.Stdio != nil {
		for _, item := range server.Stdio.Env {
			if id := strings.TrimSpace(item.CredentialID); id != "" {
				result = append(result, id)
			}
		}
	}
	if server.HTTP != nil {
		if id := strings.TrimSpace(server.HTTP.BearerCredentialID); id != "" {
			result = append(result, id)
		}
		for _, header := range server.HTTP.Headers {
			if id := strings.TrimSpace(header.CredentialID); id != "" {
				result = append(result, id)
			}
		}
	}
	return result
}

func obsoleteServerCredentialIDs(previous humbertmcp.Server, next humbertmcp.Server) []string {
	keep := make(map[string]struct{})
	for _, id := range serverCredentialIDs(next) {
		keep[id] = struct{}{}
	}
	result := make([]string, 0)
	for _, id := range serverCredentialIDs(previous) {
		if _, ok := keep[id]; !ok {
			result = append(result, id)
		}
	}
	return result
}
