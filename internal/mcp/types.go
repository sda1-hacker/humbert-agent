package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/http/httpguts"

	einotool "github.com/cloudwego/eino/components/tool"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	MaxServerKeyLength   = 24
	MaxRawToolNameLength = 256
	MaxStdioEnvVars      = 32
	MaxHTTPHeaders       = 16
)

// Transport 描述 MCP Server 的连接方式。
//
// MCP-01 只固化领域模型；真正创建 stdio / Streamable HTTP Session 在 MCP-02/03 实现。
type Transport string

const (
	TransportStdio          Transport = "stdio"
	TransportStreamableHTTP Transport = "streamable_http"
)

// StdioConfig 保存本地 MCP Server 的进程启动配置。
//
// Command 与 Args 永远分开保存。后续 Transport 必须使用 exec.CommandContext(command, args...)
// 启动，禁止拼接成 shell 字符串。
type StdioConfig struct {
	Command          string               `json:"command"`
	Args             []string             `json:"args,omitempty"`
	WorkingDirectory string               `json:"working_directory,omitempty"`
	Env              []StdioEnvCredential `json:"env,omitempty"`
}

// StdioEnvCredential 保存 stdio 子进程环境变量名与 CredentialStore 引用。
// Value 永远不进入 servers.json。
type StdioEnvCredential struct {
	Name         string `json:"name"`
	CredentialID string `json:"credential_id"`
}

// HTTPConfig 保存远程 Streamable HTTP MCP Endpoint。
//
// 这里只保存 CredentialStore 引用与非敏感 Header 名称；Bearer Token / Header Value 永远不进入 servers.json。
type HTTPConfig struct {
	Endpoint           string                 `json:"endpoint"`
	BearerCredentialID string                 `json:"bearer_credential_id,omitempty"`
	Headers            []HTTPHeaderCredential `json:"headers,omitempty"`
}

// HTTPHeaderCredential 保存一个自定义 HTTP Header 的名称与 CredentialStore 引用。
// Header Value 永远不进入 servers.json。
type HTTPHeaderCredential struct {
	Name         string `json:"name"`
	CredentialID string `json:"credential_id"`
}

// CredentialReader 是 MCP Runtime 读取敏感凭据所需的最小接口。
// credential.Store 满足该接口，MCP 领域层不依赖具体秘密存储实现。
type CredentialReader interface {
	Get(ctx context.Context, id string) (string, error)
}

// Server 是 Humbert 持久化的 MCP Server 配置。
//
// Key 是模型侧 Tool Namespace 的稳定前缀，因此创建后不可修改；Name 只是 UI 展示名称。
type Server struct {
	ID        string       `json:"id"`
	Key       string       `json:"key"`
	Name      string       `json:"name"`
	Enabled   bool         `json:"enabled"`
	Transport Transport    `json:"transport"`
	Stdio     *StdioConfig `json:"stdio,omitempty"`
	HTTP      *HTTPConfig  `json:"http,omitempty"`

	// ToolRisks 保存用户对 raw MCP Tool 的 Humbert Risk Override。
	// 缺省值永远是 write；Server annotation 只作为 UI hint，绝不自动改变本字段。
	ToolRisks map[string]humberttools.RiskLevel `json:"tool_risks,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UnmarshalJSON 为升级前没有 enabled 字段的 servers.json 提供向后兼容。
// 旧 Server 必须继续保持启用，避免升级后所有 Connector 被意外关闭。
func (s *Server) UnmarshalJSON(data []byte) error {
	type alias Server
	value := alias{Enabled: true}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = Server(value)
	return nil
}

// CreateServerInput 创建一个 MCP Server。
type CreateServerInput struct {
	Key       string
	Name      string
	Transport Transport
	Stdio     *StdioConfig
	HTTP      *HTTPConfig
}

// UpdateServerInput 修改 MCP Server 的展示信息与连接配置。
//
// Key 不在 UpdateInput 中，确保已经写入 Agent / Permission / Snapshot 的 Tool Namespace 稳定。
type UpdateServerInput struct {
	Name      string
	Transport Transport
	Stdio     *StdioConfig
	HTTP      *HTTPConfig
}

// ToolSelection 是 Agent 对一个 MCP Server 的显式 Tool 选择。
//
// Agent Profile 只保存 ServerID + raw MCP tool names。模型真正看到的 exposed name 由
// NameExposedTool 在 Runtime Resolve 时确定，不持久化派生名称。
type ToolSelection struct {
	ServerID string   `json:"server_id"`
	Tools    []string `json:"tools"`
}

// RuntimeSnapshot 是一次 User Turn 冻结的 MCP ToolSet。
//
// Revision 来自 MCP Manager 控制面版本。当前 Turn 创建后，即使 Server 配置或 Agent 选择
// 发生变化，本 Snapshot 也不会改变。
type RuntimeServerSnapshot struct {
	ServerID    string `json:"serverID"`
	ServerKey   string `json:"serverKey"`
	ServerName  string `json:"serverName"`
	Fingerprint string `json:"fingerprint"`
	Transport   string `json:"transport"`
}

// RuntimeToolSnapshot 是 Runtime Audit 使用的最小 MCP Tool 身份，不包含参数或结果。
type RuntimeToolSnapshot struct {
	ServerID    string                 `json:"serverID"`
	RawName     string                 `json:"rawName"`
	ExposedName string                 `json:"exposedName"`
	Risk        humberttools.RiskLevel `json:"risk"`
}

// RuntimeServerFailure 表示某个已启用 MCP Server 在当前 Turn 构建真实 Tool 时不可用。
//
// Failure 只属于本 Turn 的能力降级诊断，不会被当成 Runtime 失败终态。Error 已经过
// logging.RedactText 处理，允许安全暴露给 Desktop UI；真正成功进入 Snapshot 的 Server
// 仍只存在 Servers 中。
type RuntimeServerFailure struct {
	ServerID   string `json:"serverID"`
	ServerKey  string `json:"serverKey"`
	ServerName string `json:"serverName"`
	Transport  string `json:"transport"`
	Error      string `json:"error"`
}

// RuntimeSnapshot 是一次 User Turn 冻结的 MCP ToolSet 与安全身份快照。
type RuntimeSnapshot struct {
	Revision    uint64
	Servers     []RuntimeServerSnapshot
	Failures    []RuntimeServerFailure
	AuditTools  []RuntimeToolSnapshot
	Tools       []einotool.BaseTool
	Descriptors []humberttools.Descriptor
	ToolNames   []string
}

// Enabled 返回当前 Snapshot 是否真正包含 MCP Tool。
func (s RuntimeSnapshot) Enabled() bool {
	return len(s.Tools) > 0
}

// ResolveRequest 是 Manager 交给真正 MCP Runtime Backend 的冻结输入。
//
// MCP-01 只定义边界；MCP-02 的 SessionPool 会实现 RuntimeBackend。
type ResolveRequest struct {
	Revision   uint64
	Servers    []Server
	Selections []ToolSelection
	Scope      humberttools.Scope
}

// RuntimeBackend 把已验证的 MCP Server + Agent Tool Selection 转成 Eino Tools。
type RuntimeBackend interface {
	Resolve(ctx context.Context, request ResolveRequest) (RuntimeSnapshot, error)
}

// ServerReferenceChecker 用于删除 MCP Server 前检查 Agent Profile 引用。
//
// mcp package 不依赖 agents package；Application 在 Bootstrap 后注入 AgentService 实现。
type ServerReferenceChecker interface {
	CountAgentsUsingMCPServer(ctx context.Context, serverID string) (int, error)
}

func validateServer(value Server) error {
	if strings.TrimSpace(value.ID) == "" {
		return fmt.Errorf("%w: ID 不能为空", ErrInvalidServer)
	}
	if err := validateServerKey(value.Key); err != nil {
		return err
	}
	if strings.TrimSpace(value.Name) == "" {
		return fmt.Errorf("%w: Name 不能为空", ErrInvalidServer)
	}
	if len([]rune(value.Name)) > 100 {
		return fmt.Errorf("%w: Name 长度不能超过 100", ErrInvalidServer)
	}
	if value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: CreatedAt/UpdatedAt 不能为空", ErrInvalidServer)
	}
	for rawName, risk := range value.ToolRisks {
		if _, err := normalizeRawToolName(rawName); err != nil {
			return fmt.Errorf("%w: Tool Risk Override 名称无效: %v", ErrInvalidServer, err)
		}
		if risk != humberttools.RiskRead && risk != humberttools.RiskWrite && risk != humberttools.RiskExec {
			return fmt.Errorf("%w: Tool %q Risk Override %q 无效", ErrInvalidServer, rawName, risk)
		}
	}
	return validateTransportConfig(value.Transport, value.Stdio, value.HTTP)
}

func validateServerKey(raw string) error {
	key := strings.TrimSpace(raw)
	if key == "" {
		return fmt.Errorf("%w: Key 不能为空", ErrInvalidServer)
	}
	if len(key) > MaxServerKeyLength {
		return fmt.Errorf("%w: Key 长度不能超过 %d", ErrInvalidServer, MaxServerKeyLength)
	}
	for index, char := range key {
		valid := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_'
		if !valid {
			return fmt.Errorf("%w: Key %q 只能包含小写字母、数字和下划线", ErrInvalidServer, key)
		}
		if index == 0 && char >= '0' && char <= '9' {
			return fmt.Errorf("%w: Key 不能以数字开头", ErrInvalidServer)
		}
	}
	return nil
}

func validateTransportConfig(transport Transport, stdio *StdioConfig, http *HTTPConfig) error {
	switch transport {
	case TransportStdio:
		if stdio == nil {
			return fmt.Errorf("%w: stdio transport 缺少 stdio 配置", ErrInvalidServer)
		}
		if http != nil {
			return fmt.Errorf("%w: stdio transport 不能同时保存 http 配置", ErrInvalidServer)
		}
		if strings.TrimSpace(stdio.Command) == "" {
			return fmt.Errorf("%w: stdio command 不能为空", ErrInvalidServer)
		}
		if strings.ContainsRune(stdio.Command, 0) {
			return fmt.Errorf("%w: stdio command 不能包含 NUL", ErrInvalidServer)
		}
		for _, arg := range stdio.Args {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("%w: stdio args 不能包含 NUL", ErrInvalidServer)
			}
		}
		if strings.ContainsRune(stdio.WorkingDirectory, 0) {
			return fmt.Errorf("%w: working_directory 不能包含 NUL", ErrInvalidServer)
		}
		if len(stdio.Env) > MaxStdioEnvVars {
			return fmt.Errorf("%w: stdio 环境变量数量不能超过 %d", ErrInvalidServer, MaxStdioEnvVars)
		}
		seenEnv := make(map[string]struct{}, len(stdio.Env))
		for _, item := range stdio.Env {
			name := strings.TrimSpace(item.Name)
			credentialID := strings.TrimSpace(item.CredentialID)
			if !validStdioEnvName(name) {
				return fmt.Errorf("%w: stdio 环境变量名 %q 无效", ErrInvalidServer, name)
			}
			if credentialID == "" || strings.ContainsRune(credentialID, 0) {
				return fmt.Errorf("%w: stdio 环境变量 %q 缺少有效 Credential 引用", ErrInvalidServer, name)
			}
			key := strings.ToLower(name)
			if _, exists := seenEnv[key]; exists {
				return fmt.Errorf("%w: stdio 环境变量 %q 重复", ErrInvalidServer, name)
			}
			seenEnv[key] = struct{}{}
		}
		return nil

	case TransportStreamableHTTP:
		if http == nil {
			return fmt.Errorf("%w: streamable_http transport 缺少 http 配置", ErrInvalidServer)
		}
		if stdio != nil {
			return fmt.Errorf("%w: streamable_http transport 不能同时保存 stdio 配置", ErrInvalidServer)
		}
		endpoint := strings.TrimSpace(http.Endpoint)
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("%w: MCP HTTP endpoint 无效", ErrInvalidServer)
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return fmt.Errorf("%w: MCP HTTP endpoint 只支持 http/https", ErrInvalidServer)
		}
		if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
			return fmt.Errorf("%w: 非本机 MCP HTTP endpoint 必须使用 https", ErrInvalidServer)
		}
		if parsed.User != nil {
			return fmt.Errorf("%w: MCP HTTP endpoint 不能在 URL 中携带 userinfo", ErrInvalidServer)
		}
		if parsed.RawQuery != "" {
			return fmt.Errorf("%w: MCP HTTP endpoint 不能在 query 中保存参数或凭据", ErrInvalidServer)
		}
		if parsed.Fragment != "" {
			return fmt.Errorf("%w: MCP HTTP endpoint 不能包含 fragment", ErrInvalidServer)
		}
		if strings.ContainsRune(http.BearerCredentialID, 0) {
			return fmt.Errorf("%w: Bearer Credential ID 不能包含 NUL", ErrInvalidServer)
		}
		if len(http.Headers) > MaxHTTPHeaders {
			return fmt.Errorf("%w: 自定义 HTTP Header 数量不能超过 %d", ErrInvalidServer, MaxHTTPHeaders)
		}
		seenHeaders := make(map[string]struct{}, len(http.Headers))
		for _, header := range http.Headers {
			name := strings.TrimSpace(header.Name)
			credentialID := strings.TrimSpace(header.CredentialID)
			if !httpguts.ValidHeaderFieldName(name) {
				return fmt.Errorf("%w: 自定义 HTTP Header 名称 %q 无效", ErrInvalidServer, name)
			}
			if isReservedMCPHeader(name) {
				return fmt.Errorf("%w: HTTP Header %q 由 Humbert/MCP Transport 管理，不能覆盖", ErrInvalidServer, name)
			}
			if strings.EqualFold(name, "Authorization") && strings.TrimSpace(http.BearerCredentialID) != "" {
				return fmt.Errorf("%w: Bearer Token 与自定义 Authorization Header 不能同时配置", ErrInvalidServer)
			}
			if credentialID == "" || strings.ContainsRune(credentialID, 0) {
				return fmt.Errorf("%w: HTTP Header %q 缺少有效 Credential 引用", ErrInvalidServer, name)
			}
			key := strings.ToLower(name)
			if _, exists := seenHeaders[key]; exists {
				return fmt.Errorf("%w: 自定义 HTTP Header %q 重复", ErrInvalidServer, name)
			}
			seenHeaders[key] = struct{}{}
		}
		return nil

	default:
		return fmt.Errorf("%w: Transport %q 不支持", ErrInvalidServer, transport)
	}
}

func validStdioEnvName(name string) bool {
	if name == "" {
		return false
	}
	for index, char := range name {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || char == '_' {
			continue
		}
		if index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isReservedMCPHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "proxy-authorization",
		"proxy-connection",
		"accept",
		"content-type",
		"content-length",
		"host",
		"connection",
		"transfer-encoding",
		"upgrade",
		"mcp-session-id",
		"mcp-protocol-version",
		"last-event-id":
		return true
	default:
		return false
	}
}

func normalizeRawToolName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("MCP Tool 名称不能为空")
	}
	if len([]rune(name)) > MaxRawToolNameLength {
		return "", fmt.Errorf("MCP Tool 名称长度不能超过 %d", MaxRawToolNameLength)
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return "", errors.New("MCP Tool 名称不能包含控制字符")
		}
	}
	return name, nil
}

func cloneServer(value Server) Server {
	result := value
	if value.Stdio != nil {
		copyValue := *value.Stdio
		copyValue.Args = append([]string(nil), value.Stdio.Args...)
		copyValue.Env = append([]StdioEnvCredential(nil), value.Stdio.Env...)
		result.Stdio = &copyValue
	}
	if value.HTTP != nil {
		copyValue := *value.HTTP
		copyValue.Headers = append([]HTTPHeaderCredential(nil), value.HTTP.Headers...)
		result.HTTP = &copyValue
	}
	if value.ToolRisks != nil {
		result.ToolRisks = make(map[string]humberttools.RiskLevel, len(value.ToolRisks))
		for name, risk := range value.ToolRisks {
			result.ToolRisks[name] = risk
		}
	}
	return result
}

func cloneSelections(values []ToolSelection) []ToolSelection {
	result := make([]ToolSelection, len(values))
	for i, value := range values {
		result[i] = ToolSelection{
			ServerID: value.ServerID,
			Tools:    append([]string(nil), value.Tools...),
		}
	}
	return result
}

// ToolRisk 返回 raw MCP Tool 的 Humbert Risk。Server annotation 不参与此判断。
// 未配置 Override 时固定返回 write。
func ToolRisk(server Server, rawToolName string) humberttools.RiskLevel {
	name := strings.TrimSpace(rawToolName)
	if server.ToolRisks != nil {
		if risk, ok := server.ToolRisks[name]; ok {
			switch risk {
			case humberttools.RiskRead, humberttools.RiskWrite, humberttools.RiskExec:
				return risk
			}
		}
	}
	return humberttools.RiskWrite
}

// ValidateStdioConfig 校验 stdio 连接配置，不要求构造完整 Server。
func ValidateStdioConfig(value *StdioConfig) error {
	return validateTransportConfig(TransportStdio, value, nil)
}

// ValidateHTTPConfig 校验 Streamable HTTP 连接配置，不要求构造完整 Server。
func ValidateHTTPConfig(value *HTTPConfig) error {
	return validateTransportConfig(TransportStreamableHTTP, nil, value)
}

// ValidateHTTPHeaderName 校验用户配置的自定义 MCP HTTP Header 名称。
// MCP/HTTP transport 自己管理的保留 Header 不允许覆盖；Authorization 可作为自定义认证方式，
// 但不能与 HTTPConfig.BearerCredentialID 同时存在。
func ValidateHTTPHeaderName(name string) error {
	name = strings.TrimSpace(name)
	if !httpguts.ValidHeaderFieldName(name) {
		return fmt.Errorf("%w: 自定义 HTTP Header 名称 %q 无效", ErrInvalidServer, name)
	}
	if isReservedMCPHeader(name) {
		return fmt.Errorf("%w: HTTP Header %q 由 Humbert/MCP Transport 管理，不能覆盖", ErrInvalidServer, name)
	}
	return nil
}

// ValidateServerForRuntime 校验一个已经持久化/解析出的 Server 是否可以进入 Runtime。
// 该窄接口供 Eino Adapter 子包复用核心领域规则。
func ValidateServerForRuntime(value Server) error {
	return validateServer(value)
}

// NormalizeRawToolName 校验并规范化 Agent Profile 中持久化的 raw MCP Tool 名称。
// exposed name 的生成仍统一由 NameExposedTool 完成。
func NormalizeRawToolName(raw string) (string, error) {
	return normalizeRawToolName(raw)
}
