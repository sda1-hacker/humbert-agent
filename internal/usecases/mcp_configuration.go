package usecases

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
)

// MCPConfigurationRequest 是应用用例输入；Wails DTO 在入口显式转换。
// Secret 只用于本次写入，返回值仍是领域 Server 的非敏感凭据引用。
type MCPCredentialInput struct {
	Name   string
	Secret string
}
type MCPConfigurationRequest struct {
	Key, Name, Transport, Command string
	Args                          []string
	WorkingDirectory              string
	Env                           []MCPCredentialInput
	Endpoint                      string
	BearerEnabled                 bool
	BearerToken                   string
	Headers                       []MCPCredentialInput
}

// MCPConfiguration 保留配置更新与 CredentialStore 之间的提交/清理顺序。
// Manager 管理连接及 Server，CredentialStore 管理秘密；此用例只协调两者，供任意入口复用。
type MCPConfiguration struct {
	manager     *humbertmcp.Manager
	credentials MCPCredentials
	logger      *logging.Logger
}

// MCPCredentials 仅声明此用例需要的操作，不暴露秘密读取能力或具体存储方式。
type MCPCredentials interface {
	Put(context.Context, string, string) error
	Delete(context.Context, string) error
}

func NewMCPConfiguration(manager *humbertmcp.Manager, credentials MCPCredentials, logger *logging.Logger) *MCPConfiguration {
	return &MCPConfiguration{manager: manager, credentials: credentials, logger: logger}
}

// Create 先取得稳定 Server ID，再写秘密引用；后续任一步失败都会清理本次创建的内容。
func (s *MCPConfiguration) Create(ctx context.Context, request MCPConfigurationRequest) (humbertmcp.Server, error) {
	transport := humbertmcp.Transport(request.Transport)
	value, err := s.manager.Create(ctx, humbertmcp.CreateServerInput{
		Key:       request.Key,
		Name:      request.Name,
		Transport: transport,
		Stdio:     requestStdioWithoutCredentials(request),
		HTTP:      requestHTTPWithoutCredentials(request),
	})
	if err != nil {
		return humbertmcp.Server{}, fmt.Errorf("创建 MCP Server 失败: %w", err)
	}

	var createdCredentialIDs []string
	cleanupCreatedServer := func(cause error) (humbertmcp.Server, error) {
		s.cleanupCredentials(ctx, createdCredentialIDs)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_ = s.manager.Delete(cleanupCtx, value.ID)
		return humbertmcp.Server{}, cause
	}

	switch transport {
	case humbertmcp.TransportStdio:
		if len(request.Env) > 0 {
			configured, created, configureErr := s.prepareStdioConfig(ctx, value.ID, nil, request)
			createdCredentialIDs = created
			if configureErr != nil {
				return cleanupCreatedServer(fmt.Errorf("创建 MCP stdio Credential 失败: %w", configureErr))
			}
			value, err = s.manager.Update(ctx, value.ID, humbertmcp.UpdateServerInput{
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
		value, err = s.manager.Update(ctx, value.ID, humbertmcp.UpdateServerInput{
			Name:      request.Name,
			Transport: transport,
			HTTP:      configured,
		})
		if err != nil {
			return cleanupCreatedServer(fmt.Errorf("保存 MCP HTTP 配置失败: %w", err))
		}
	}

	return value, nil
}

// Update 把空 Secret 解释为保留原凭据；新配置提交成功之后才清理失去引用的旧凭据。
func (s *MCPConfiguration) Update(ctx context.Context, id string, request MCPConfigurationRequest) (humbertmcp.Server, error) {
	existing, err := s.manager.Get(ctx, id)
	if err != nil {
		return humbertmcp.Server{}, fmt.Errorf("读取 MCP Server 失败: %w", err)
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
			return humbertmcp.Server{}, fmt.Errorf("准备 MCP stdio Credential 失败: %w", err)
		}

	case humbertmcp.TransportStreamableHTTP:
		var previous *humbertmcp.HTTPConfig
		if existing.Transport == humbertmcp.TransportStreamableHTTP {
			previous = existing.HTTP
		}
		httpConfig, createdCredentialIDs, err = s.prepareHTTPConfig(ctx, existing.ID, previous, request)
		if err != nil {
			return humbertmcp.Server{}, fmt.Errorf("准备 MCP HTTP Credential 失败: %w", err)
		}
	}

	value, err := s.manager.Update(ctx, id, humbertmcp.UpdateServerInput{
		Name:      request.Name,
		Transport: transport,
		Stdio:     stdioConfig,
		HTTP:      httpConfig,
	})
	if err != nil {
		s.cleanupCredentials(ctx, createdCredentialIDs)
		return humbertmcp.Server{}, fmt.Errorf("更新 MCP Server 失败: %w", err)
	}

	// Server 已成功指向新 Credential 后再删除旧引用；清理失败不会回滚已提交配置。
	s.cleanupCredentials(ctx, obsoleteServerCredentialIDs(existing, value))
	return value, nil
}

// Delete 先删除 Server，再清理其凭据，避免清理失败留下引用已不存在秘密的活动 Server。
func (s *MCPConfiguration) Delete(ctx context.Context, id string) error {
	existing, err := s.manager.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("读取 MCP Server 失败: %w", err)
	}
	if err := s.manager.Delete(ctx, id); err != nil {
		return fmt.Errorf("删除 MCP Server 失败: %w", err)
	}
	s.cleanupCredentials(ctx, serverCredentialIDs(existing))
	return nil
}

func (s *MCPConfiguration) prepareStdioConfig(
	ctx context.Context,
	serverID string,
	existing *humbertmcp.StdioConfig,
	request MCPConfigurationRequest,
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
			if err := s.credentials.Put(ctx, credentialID, secret); err != nil {
				return cleanupOnError(fmt.Errorf("保存 stdio 环境变量 %q Credential 失败: %w", name, err))
			}
			created = append(created, credentialID)
		}
		result.Env = append(result.Env, humbertmcp.StdioEnvCredential{Name: name, CredentialID: credentialID})
	}
	if err := humbertmcp.ValidateStdioConfig(result); err != nil {
		return cleanupOnError(err)
	}
	return result, created, nil
}

func (s *MCPConfiguration) prepareHTTPConfig(
	ctx context.Context,
	serverID string,
	existing *humbertmcp.HTTPConfig,
	request MCPConfigurationRequest,
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
			if err := s.credentials.Put(ctx, credentialID, secret); err != nil {
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
			if err := s.credentials.Put(ctx, credentialID, secret); err != nil {
				return cleanupOnError(fmt.Errorf("保存 HTTP Header %q Credential 失败: %w", name, err))
			}
			created = append(created, credentialID)
		}
		result.Headers = append(result.Headers, humbertmcp.HTTPHeaderCredential{Name: name, CredentialID: credentialID})
	}
	if err := humbertmcp.ValidateHTTPConfig(result); err != nil {
		return cleanupOnError(err)
	}
	return result, created, nil
}

func (s *MCPConfiguration) cleanupCredentials(ctx context.Context, ids []string) {
	// 清理不能复用一个已经超时/取消的请求 Context，否则错误路径可能留下孤立 Secret。
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if err := s.credentials.Delete(cleanupCtx, id); err != nil {
			s.logger.Warn(ctx, "清理 MCP Credential 失败", "operation", "mcp.credential.cleanup", "credential_id", id, "error", err)
		}
	}
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

func requestStdioWithoutCredentials(request MCPConfigurationRequest) *humbertmcp.StdioConfig {
	if humbertmcp.Transport(request.Transport) != humbertmcp.TransportStdio {
		return nil
	}
	return &humbertmcp.StdioConfig{
		Command:          request.Command,
		Args:             append([]string(nil), request.Args...),
		WorkingDirectory: request.WorkingDirectory,
	}
}

func requestHTTPWithoutCredentials(request MCPConfigurationRequest) *humbertmcp.HTTPConfig {
	if humbertmcp.Transport(request.Transport) != humbertmcp.TransportStreamableHTTP {
		return nil
	}
	return &humbertmcp.HTTPConfig{Endpoint: strings.TrimSpace(request.Endpoint)}
}
