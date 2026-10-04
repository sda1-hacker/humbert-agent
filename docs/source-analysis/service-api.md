# 桌面服务接口与 DTO 索引

覆盖 **119 个公开业务方法**，验证了 **117 个不同前端方法调用**均存在相应 Go 声明。该检查只核对方法名称，不验证序列化字段和运行行为。

直接扫描 internal/services 的当前 Go 声明和 frontend/src/api 调用。ServiceName/ServiceStartup/ServiceShutdown 属于 Wails 生命周期，在本表省略；只列当前公开业务方法。表中没有前端 API 调用的方法不等于无用方法，也不代表已开放页面入口。

DTO 代码块保持源码字段与 JSON tag；业务校验、默认值和错误处理仍以章节及链接函数体为准。以下是文档快照，不是另一套协议实现。

## AgentService

[internal/services/agentservice.go](../../internal/services/agentservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `ListAgents` | `无` | `([]AgentDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `GetAgent` | `id string` | `(AgentDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `CreateAgent` | `request CreateAgentRequest` | `(AgentDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `UpdateAgent` | `id string, request UpdateAgentRequest` | `(AgentDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `UpdateAgentProfile` | `id string, request AgentProfileRequest` | `(AgentDTO, error)` | 未在 API 文件发现 |
| `SetAgentModel` | `id string, request AgentModelRequest` | `(AgentDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `SetAgentModelRoles` | `id string, request AgentModelRolesRequest` | `(AgentDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `SetAgentSkills` | `id string, request AgentSkillsRequest` | `(AgentDTO, error)` | 未在 API 文件发现 |
| `ListBuiltinTools` | `无` | `[]BuiltinToolDTO` | [agents.js](../../frontend/src/api/agents.js) |
| `GetSandboxStatus` | `无` | `SandboxStatusDTO` | [agents.js](../../frontend/src/api/agents.js) |
| `UpdateSandboxSettings` | `request SandboxSettingsRequest` | `(SandboxStatusDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `RunSandboxDiagnostics` | `无` | `(SandboxDiagnosticsDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `UpdateAgentSecurity` | `id string, request AgentSecurityRequest` | `(AgentDTO, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `SelectSandboxDirectory` | `currentPath string` | `(string, error)` | [agents.js](../../frontend/src/api/agents.js) |
| `DeleteAgent` | `id string` | `error` | [agents.js](../../frontend/src/api/agents.js) |
| `SelectWorkspaceDirectory` | `currentPath string` | `(string, error)` | [agents.js](../../frontend/src/api/agents.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type AgentDTO struct {
	ID                     string             `json:"id"`
	Name                   string             `json:"name"`
	Avatar                 string             `json:"avatar"`
	SubagentEnabled        bool               `json:"subagentEnabled"`
	Instruction            string             `json:"instruction"`
	ModelID                string             `json:"modelID"`
	ModelDisplayName       string             `json:"modelDisplayName"`
	ModelRoles             AgentModelRolesDTO `json:"modelRoles"`
	EnabledSkills          []string           `json:"enabledSkills"`
	EnabledBuiltinTools    []string           `json:"enabledBuiltinTools"`
	BuiltinToolsConfigured bool               `json:"builtinToolsConfigured"`
	AvailableBuiltinTools  []BuiltinToolDTO   `json:"availableBuiltinTools"`
	Sandbox                SandboxPolicyDTO   `json:"sandbox"`
	SandboxStatus          SandboxStatusDTO   `json:"sandboxStatus"`
	WorkspaceMode          string             `json:"workspaceMode"`
	WorkspacePath          string             `json:"workspacePath"`
	WorkspaceDisplayPath   string             `json:"workspaceDisplayPath"`
	CreatedAt              string             `json:"createdAt"`
	UpdatedAt              string             `json:"updatedAt"`
}

type SandboxPolicyDTO struct {
	Profile              string   `json:"profile"`
	AdditionalWritePaths []string `json:"additionalWritePaths"`
	NetworkMode          string   `json:"networkMode"`
	NativeMode           string   `json:"nativeMode"`
}

type AgentSecurityRequest struct {
	EnabledBuiltinTools []string         `json:"enabledBuiltinTools"`
	Sandbox             SandboxPolicyDTO `json:"sandbox"`
}

type AgentProfileRequest struct {
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	Instruction string `json:"instruction"`
}

type AgentModelRequest struct {
	ModelID string `json:"modelID"`
}

type AgentModelRolesDTO struct {
	UtilityModelID string `json:"utilityModelID"`
}

type AgentModelRolesRequest struct {
	UtilityModelID string `json:"utilityModelID"`
}

type AgentSkillsRequest struct {
	EnabledSkills []string `json:"enabledSkills"`
}

type BuiltinToolDTO struct {
	Name     string `json:"name"`
	Risk     string `json:"risk"`
	Category string `json:"category"`
	Label    string `json:"label"`
}

type SandboxStatusDTO struct {
	Platform             string `json:"platform"`
	Backend              string `json:"backend"`
	Available            bool   `json:"available"`
	Reason               string `json:"reason"`
	Filesystem           bool   `json:"filesystem"`
	ProcessTree          bool   `json:"processTree"`
	Network              bool   `json:"network"`
	DefaultProfile       string `json:"defaultProfile"`
	DefaultNetworkMode   string `json:"defaultNetworkMode"`
	DefaultNativeMode    string `json:"defaultNativeMode"`
	CommandGracePeriodMS int    `json:"commandGracePeriodMS"`
	ShellEnabled         bool   `json:"shellEnabled"`
	ShellRuntimeActive   bool   `json:"shellRuntimeActive"`
}

type SandboxSettingsRequest struct {
	DefaultProfile       string `json:"defaultProfile"`
	DefaultNetworkMode   string `json:"defaultNetworkMode"`
	DefaultNativeMode    string `json:"defaultNativeMode"`
	CommandGracePeriodMS int    `json:"commandGracePeriodMS"`
	ShellEnabled         bool   `json:"shellEnabled"`
}

type SandboxDiagnosticCheckDTO struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type SandboxDiagnosticsDTO struct {
	Summary string                      `json:"summary"`
	Checks  []SandboxDiagnosticCheckDTO `json:"checks"`
}

type CreateAgentRequest struct {
	Name                   string             `json:"name"`
	Avatar                 string             `json:"avatar"`
	SubagentEnabled        bool               `json:"subagentEnabled"`
	Instruction            string             `json:"instruction"`
	ModelID                string             `json:"modelID"`
	ModelRoles             AgentModelRolesDTO `json:"modelRoles"`
	EnabledSkills          []string           `json:"enabledSkills"`
	WorkspaceMode          string             `json:"workspaceMode"`
	WorkspacePath          string             `json:"workspacePath"`
	BuiltinToolsConfigured bool               `json:"builtinToolsConfigured"`
	EnabledBuiltinTools    []string           `json:"enabledBuiltinTools"`
	Sandbox                SandboxPolicyDTO   `json:"sandbox"`
}

type UpdateAgentRequest struct {
	Name                string              `json:"name"`
	Avatar              string              `json:"avatar"`
	SubagentEnabled     bool                `json:"subagentEnabled"`
	Instruction         string              `json:"instruction"`
	ModelID             string              `json:"modelID"`
	ModelRoles          *AgentModelRolesDTO `json:"modelRoles"`
	EnabledSkills       []string            `json:"enabledSkills"`
	WorkspaceMode       string              `json:"workspaceMode"`
	WorkspacePath       string              `json:"workspacePath"`
	EnabledBuiltinTools *[]string           `json:"enabledBuiltinTools"`

	// nil 表示未提交；空对象表示恢复应用默认策略。
	Sandbox *SandboxPolicyDTO `json:"sandbox"`
}
```

</details>

## AppService

[internal/services/appservice.go](../../internal/services/appservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `Status` | `无` | `(AppStatus, error)` | [app.js](../../frontend/src/api/app.js) |
| `ExportBackup` | `passphrase string` | `(string, error)` | [app.js](../../frontend/src/api/app.js) |
| `PendingBackupStatus` | `无` | `(databackup.BackupStatus, error)` | [app.js](../../frontend/src/api/app.js) |
| `CancelPendingBackup` | `无` | `error` | [app.js](../../frontend/src/api/app.js) |
| `PendingRestoreStatus` | `无` | `(databackup.RestoreStatus, error)` | [app.js](../../frontend/src/api/app.js) |
| `CancelPendingRestore` | `无` | `error` | [app.js](../../frontend/src/api/app.js) |
| `ScheduleRestore` | `passphrase string` | `(string, error)` | [app.js](../../frontend/src/api/app.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type AppStatus struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Ready         bool   `json:"ready"`
	StorageReady  bool   `json:"storageReady"`
	DataDir       string `json:"dataDir"`
	ConfigFile    string `json:"configFile"`
	ConfigDir     string `json:"configDir"`
	AgentsDir     string `json:"agentsDir"`
	StartedAt     string `json:"startedAt"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
}
```

</details>

## ChatService

[internal/services/chatservice.go](../../internal/services/chatservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `StartTurn` | `request StartTurnRequest` | `(agentruntime.StartTurnResult, error)` | [chat.js](../../frontend/src/api/chat.js) |
| `ContextStatus` | `sessionID string` | `(contextengine.Usage, error)` | [chat.js](../../frontend/src/api/chat.js) |
| `ContextOverview` | `sessionID string` | `(agentruntime.ContextOverview, error)` | [chat.js](../../frontend/src/api/chat.js) |
| `CompactContext` | `sessionID string` | `(agentruntime.ManualCompactionResult, error)` | [chat.js](../../frontend/src/api/chat.js) |
| `ResolveApproval` | `request ResolveApprovalRequest` | `(agentruntime.ResolveApprovalResult, error)` | [chat.js](../../frontend/src/api/chat.js) |
| `CancelTurn` | `requestID string` | `error` | [chat.js](../../frontend/src/api/chat.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type StartTurnRequest struct {
	SessionID          string              `json:"sessionID"`
	Content            string              `json:"content"`
	Attachments        []AttachmentRequest `json:"attachments,omitempty"`
	RetryUserMessageID string              `json:"retryUserMessageID,omitempty"`
}

type AttachmentRequest struct {
	Name       string `json:"name"`
	MIMEType   string `json:"mimeType"`
	Base64Data string `json:"base64Data"`
}

type ResolveApprovalRequest struct {
	ApprovalID string            `json:"approvalID"`
	Decision   approval.Decision `json:"decision"`
}
```

</details>

## MCPService

[internal/services/mcpservice.go](../../internal/services/mcpservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `ListServers` | `无` | `([]MCPServerDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `CreateServer` | `request MCPServerRequest` | `(MCPServerDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `UpdateServer` | `id string, request MCPServerRequest` | `(MCPServerDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `ExportServersConfig` | `无` | `(string, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `ImportServersConfig` | `payload string` | `([]MCPServerDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `RefreshTools` | `serverID string` | `([]MCPToolDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `SetServerEnabled` | `id string, enabled bool` | `(MCPServerDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `DisconnectServer` | `id string` | `error` | [mcp.js](../../frontend/src/api/mcp.js) |
| `DeleteServer` | `id string` | `error` | [mcp.js](../../frontend/src/api/mcp.js) |
| `TestConnection` | `serverID string` | `(MCPConnectionTestDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `DiscoverTools` | `serverID string` | `([]MCPToolDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `SetToolRisk` | `serverID string, rawToolName string, risk string` | `error` | [mcp.js](../../frontend/src/api/mcp.js) |
| `GetAgentTools` | `agentID string` | `([]MCPToolSelectionDTO, error)` | [mcp.js](../../frontend/src/api/mcp.js) |
| `SetAgentTools` | `agentID string, selections []MCPToolSelectionDTO` | `error` | [mcp.js](../../frontend/src/api/mcp.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type MCPHTTPHeaderDTO struct {
	Name          string `json:"name"`
	HasCredential bool   `json:"hasCredential"`
}

type MCPStdioEnvDTO struct {
	Name          string `json:"name"`
	HasCredential bool   `json:"hasCredential"`
}

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

type MCPHTTPHeaderRequest struct {
	Name   string `json:"name"`
	Secret string `json:"secret"`
}

type MCPStdioEnvRequest struct {
	Name   string `json:"name"`
	Secret string `json:"secret"`
}

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

type MCPToolSelectionDTO struct {
	ServerID string   `json:"serverID"`
	Tools    []string `json:"tools"`
}

type MCPToolDTO struct {
	RawName        string         `json:"rawName"`
	ExposedName    string         `json:"exposedName"`
	Description    string         `json:"description"`
	InputSchema    map[string]any `json:"inputSchema,omitempty"`
	Annotations    map[string]any `json:"annotations,omitempty"`
	Risk           string         `json:"risk"`
	RiskOverridden bool           `json:"riskOverridden"`
}

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

type MCPConnectionTestDTO struct {
	OK         bool  `json:"ok"`
	ToolCount  int   `json:"toolCount"`
	DurationMS int64 `json:"durationMS"`
}
```

</details>

## ModelService

[internal/services/modelservice.go](../../internal/services/modelservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `State` | `无` | `(ModelSettingsState, error)` | [models.js](../../frontend/src/api/models.js) |
| `UpdateMultimediaConfig` | `request MultimediaConfigDTO` | `(MultimediaConfigDTO, error)` | [models.js](../../frontend/src/api/models.js) |
| `CreateProvider` | `request CreateProviderRequest` | `(ProviderDTO, error)` | [models.js](../../frontend/src/api/models.js) |
| `UpdateProvider` | `id string, request UpdateProviderRequest` | `(ProviderDTO, error)` | [models.js](../../frontend/src/api/models.js) |
| `DeleteProvider` | `id string` | `error` | [models.js](../../frontend/src/api/models.js) |
| `CreateModel` | `request SaveModelRequest` | `(ModelDTO, error)` | [models.js](../../frontend/src/api/models.js) |
| `UpdateModel` | `id string, request SaveModelRequest` | `(ModelDTO, error)` | [models.js](../../frontend/src/api/models.js) |
| `DeleteModel` | `id string` | `error` | [models.js](../../frontend/src/api/models.js) |
| `TestModel` | `id string` | `(TestModelResponse, error)` | [models.js](../../frontend/src/api/models.js) |
| `DiagnoseModel` | `id string` | `(ModelDiagnostic, error)` | [models.js](../../frontend/src/api/models.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type ProviderDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	BaseURL       string `json:"baseURL"`
	HasCredential bool   `json:"hasCredential"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type ModelCapabilityConfigDTO struct {
	Tools     string `json:"tools"`
	Vision    string `json:"vision"`
	Files     string `json:"files"`
	Reasoning string `json:"reasoning"`
	JSON      string `json:"json"`
	Audio     string `json:"audio"`
}

type ModelCapabilitiesDTO struct {
	Tools     bool `json:"tools"`
	Vision    bool `json:"vision"`
	Files     bool `json:"files"`
	Reasoning bool `json:"reasoning"`
	JSON      bool `json:"json"`
	Audio     bool `json:"audio"`
}

type ModelDTO struct {
	ID               string                   `json:"id"`
	ProviderID       string                   `json:"providerID"`
	ProviderName     string                   `json:"providerName"`
	ProviderType     string                   `json:"providerType"`
	ModelName        string                   `json:"modelName"`
	DisplayName      string                   `json:"displayName"`
	TimeoutMS        int                      `json:"timeoutMS"`
	ContextWindow    int                      `json:"contextWindow"`
	MaxOutputTokens  int                      `json:"maxOutputTokens"`
	CapabilityConfig ModelCapabilityConfigDTO `json:"capabilityConfig"`
	Capabilities     ModelCapabilitiesDTO     `json:"capabilities"`
	Enabled          bool                     `json:"enabled"`
	CreatedAt        string                   `json:"createdAt"`
	UpdatedAt        string                   `json:"updatedAt"`
}

type MultimediaConfigDTO struct {
	ImageModelID string `json:"imageModelID"`
}

type ModelSettingsState struct {
	Revision   uint64              `json:"revision"`
	Providers  []ProviderDTO       `json:"providers"`
	Models     []ModelDTO          `json:"models"`
	Multimedia MultimediaConfigDTO `json:"multimedia"`
}

type CreateProviderRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

type UpdateProviderRequest struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	BaseURL      string `json:"baseURL"`
	APIKey       string `json:"apiKey"`
	UpdateAPIKey bool   `json:"updateAPIKey"`
}

type SaveModelRequest struct {
	ProviderID       string                   `json:"providerID"`
	ModelName        string                   `json:"modelName"`
	DisplayName      string                   `json:"displayName"`
	TimeoutMS        int                      `json:"timeoutMS"`
	ContextWindow    int                      `json:"contextWindow"`
	MaxOutputTokens  int                      `json:"maxOutputTokens"`
	CapabilityConfig ModelCapabilityConfigDTO `json:"capabilityConfig"`
	Enabled          bool                     `json:"enabled"`
}

type TestModelResponse struct {
	Success         bool   `json:"success"`
	DurationMS      int64  `json:"durationMS"`
	ResponsePreview string `json:"responsePreview"`
}

type ModelDiagnostic struct {
	Success        bool   `json:"success"`
	Category       string `json:"category"`
	Summary        string `json:"summary"`
	Action         string `json:"action"`
	DurationMS     int64  `json:"durationMS"`
	ToolsSupported bool   `json:"toolsSupported"`
}
```

</details>

## PermissionService

[internal/services/permissionservice.go](../../internal/services/permissionservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `Mode` | `无` | `(string, error)` | [permissions.js](../../frontend/src/api/permissions.js) |
| `SetMode` | `mode string` | `(string, error)` | [permissions.js](../../frontend/src/api/permissions.js) |
| `State` | `sessionID string` | `(PermissionStateDTO, error)` | [permissions.js](../../frontend/src/api/permissions.js) |
| `UpdateSettings` | `request UpdatePermissionSettingsRequest` | `error` | [permissions.js](../../frontend/src/api/permissions.js) |
| `DeleteRule` | `ruleID string` | `error` | [permissions.js](../../frontend/src/api/permissions.js) |
| `DeleteSessionRule` | `sessionID string, ruleID string` | `error` | [permissions.js](../../frontend/src/api/permissions.js) |
| `ClearSessionRules` | `sessionID string` | `error` | [permissions.js](../../frontend/src/api/permissions.js) |
| `ClearPersistentAllows` | `无` | `(int, error)` | [permissions.js](../../frontend/src/api/permissions.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type PermissionRuleDTO struct {
	ID                    string `json:"id"`
	AgentID               string `json:"agentID"`
	AgentName             string `json:"agentName"`
	SessionID             string `json:"sessionID,omitempty"`
	ToolName              string `json:"toolName"`
	Action                string `json:"action"`
	Scope                 string `json:"scope"`
	CapabilityKind        string `json:"capabilityKind,omitempty"`
	Command               string `json:"command,omitempty"`
	Executable            string `json:"executable,omitempty"`
	InvocationFingerprint string `json:"invocationFingerprint,omitempty"`
	SkillName             string `json:"skillName,omitempty"`
	SkillIdentity         string `json:"skillIdentity,omitempty"`
	Script                string `json:"script,omitempty"`
	MCPServerID           string `json:"mcpServerID,omitempty"`
	MCPServerName         string `json:"mcpServerName,omitempty"`
	MCPServerFingerprint  string `json:"mcpServerFingerprint,omitempty"`
	MCPTool               string `json:"mcpTool,omitempty"`
	ModuleID              string `json:"moduleID,omitempty"`
	ModuleRevision        string `json:"moduleRevision,omitempty"`
	SandboxFingerprint    string `json:"sandboxFingerprint,omitempty"`
	CreatedAt             string `json:"createdAt"`
}

type PermissionStateDTO struct {
	Mode              string              `json:"mode"`
	Enabled           bool                `json:"enabled"`
	ReadAction        string              `json:"readAction"`
	WriteAction       string              `json:"writeAction"`
	ExecAction        string              `json:"execAction"`
	ApprovalTimeoutMS int                 `json:"approvalTimeoutMS"`
	PersistentRules   []PermissionRuleDTO `json:"persistentRules"`
	SessionRules      []PermissionRuleDTO `json:"sessionRules"`
}

type UpdatePermissionSettingsRequest struct {
	Mode              string `json:"mode"`
	Enabled           bool   `json:"enabled"`
	ReadAction        string `json:"readAction"`
	WriteAction       string `json:"writeAction"`
	ExecAction        string `json:"execAction"`
	ApprovalTimeoutMS int    `json:"approvalTimeoutMS"`
}
```

</details>

## PreferenceService

[internal/services/preferenceservice.go](../../internal/services/preferenceservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `GetUserProfile` | `无` | `(UserProfileDTO, error)` | [preferences.js](../../frontend/src/api/preferences.js) |
| `UpdateUserProfile` | `request UserProfileDTO` | `(UserProfileDTO, error)` | [preferences.js](../../frontend/src/api/preferences.js) |
| `SetLanguage` | `language string` | `(UserProfileDTO, error)` | [preferences.js](../../frontend/src/api/preferences.js) |
| `ListPersonalMemories` | `无` | `([]preferences.PersonalMemory, error)` | [preferences.js](../../frontend/src/api/preferences.js) |
| `AddPersonalMemory` | `text string` | `(preferences.PersonalMemory, error)` | [preferences.js](../../frontend/src/api/preferences.js) |
| `AddPersonalMemoryFromMessage` | `text, sessionID, entryID string` | `(preferences.PersonalMemory, error)` | [preferences.js](../../frontend/src/api/preferences.js) |
| `UpdatePersonalMemory` | `id string, text string` | `(preferences.PersonalMemory, error)` | [preferences.js](../../frontend/src/api/preferences.js) |
| `DeletePersonalMemory` | `id string` | `error` | [preferences.js](../../frontend/src/api/preferences.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type UserProfileDTO struct {
	Name     string `json:"name"`
	Avatar   string `json:"avatar"`
	Language string `json:"language"`
}
```

</details>

## ProactiveService

[internal/services/proactiveservice.go](../../internal/services/proactiveservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `GetSettings` | `无` | `(ProactiveSettingsDTO, error)` | [proactive.js](../../frontend/src/api/proactive.js) |
| `UpdateSettings` | `value ProactiveSettingsDTO` | `(ProactiveSettingsDTO, error)` | [proactive.js](../../frontend/src/api/proactive.js) |
| `Status` | `无` | `(ProactiveStatusDTO, error)` | [proactive.js](../../frontend/src/api/proactive.js) |
| `Notifications` | `limit int` | `([]notifications.Notification, error)` | [proactive.js](../../frontend/src/api/proactive.js) |
| `Records` | `limit int` | `([]proactive.Record, error)` | [proactive.js](../../frontend/src/api/proactive.js) |
| `RunHeartbeat` | `无` | `error` | [proactive.js](../../frontend/src/api/proactive.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type ProactiveQuietHoursDTO struct {
	Enabled  bool   `json:"enabled"`
	Start    string `json:"start"`
	End      string `json:"end"`
	TimeZone string `json:"timeZone"`
}

type ProactiveRuleDTO struct {
	Enabled         bool   `json:"enabled"`
	Action          string `json:"action"`
	AgentID         string `json:"agentID,omitempty"`
	AgentPrompt     string `json:"agentPrompt,omitempty"`
	CooldownSeconds int    `json:"cooldownSeconds,omitempty"`
}

type ProactiveSettingsDTO struct {
	Enabled                  bool                        `json:"enabled"`
	HeartbeatIntervalMinutes int                         `json:"heartbeatIntervalMinutes"`
	QuietHours               ProactiveQuietHoursDTO      `json:"quietHours"`
	Rules                    map[string]ProactiveRuleDTO `json:"rules"`
}

type ProactiveStatusDTO struct {
	Running         bool   `json:"running"`
	LastHeartbeatAt string `json:"lastHeartbeatAt,omitempty"`
	NextHeartbeatAt string `json:"nextHeartbeatAt,omitempty"`
	QueuedEvents    int    `json:"queuedEvents"`
}
```

</details>

## SessionService

[internal/services/sessionservice.go](../../internal/services/sessionservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `Search` | `query string` | `(SessionSearchDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |
| `List` | `agentID string` | `([]SessionDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |
| `Create` | `agentID string, title string` | `(SessionDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |
| `Rename` | `id string, title string` | `(SessionDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |
| `SetArchived` | `id string, archived bool` | `(SessionDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |
| `Delete` | `id string` | `error` | [sessions.js](../../frontend/src/api/sessions.js) |
| `Messages` | `sessionID string, limit int` | `([]MessageDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |
| `MessagePage` | `sessionID string, beforeEntryID string, limit int` | `(MessagePageDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |
| `MessageWindow` | `sessionID, entryID string` | `(MessagePageDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |
| `ReadAttachment` | `sessionID string, attachmentID string` | `(AttachmentContentDTO, error)` | [sessions.js](../../frontend/src/api/sessions.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type SessionDTO struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentID"`
	Title     string `json:"title"`
	Archived  bool   `json:"archived"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type MessageDTO struct {
	MessageNo   int64              `json:"messageNo"`
	ID          string             `json:"id"`
	SessionID   string             `json:"sessionID"`
	Role        string             `json:"role"`
	Content     string             `json:"content"`
	Metadata    MessageMetadataDTO `json:"metadata"`
	Attachments []AttachmentDTO    `json:"attachments,omitempty"`
	CreatedAt   string             `json:"createdAt"`
}

type MessageMetadataDTO struct {
	Reasoning     string            `json:"reasoning_content,omitempty"`
	ToolCalls     []schema.ToolCall `json:"tool_calls,omitempty"`
	ProviderID    string            `json:"provider_id,omitempty"`
	ModelName     string            `json:"model_name,omitempty"`
	ResponseModel string            `json:"response_model,omitempty"`
	ResponseID    string            `json:"response_id,omitempty"`
	StopReason    string            `json:"stop_reason,omitempty"`
	Incomplete    bool              `json:"incomplete,omitempty"`
	ToolCallID    string            `json:"tool_call_id,omitempty"`
	ToolName      string            `json:"tool_name,omitempty"`
	IsError       bool              `json:"is_error,omitempty"`
}

type MessagePageDTO struct {
	Messages     []MessageDTO `json:"messages"`
	HasMore      bool         `json:"hasMore"`
	NextBeforeID string       `json:"nextBeforeID"`
}

type AttachmentDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MIMEType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	Kind      string `json:"kind"`
}

type AttachmentContentDTO struct {
	ID         string `json:"id"`
	Base64Data string `json:"base64Data"`
}

type SessionSearchDTO struct {
	Results  []searchindex.Result `json:"results"`
	Updating bool                 `json:"updating"`
	Error    string               `json:"error,omitempty"`
}
```

</details>

## SkillService

[internal/services/skillservice.go](../../internal/services/skillservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `State` | `无` | `(SkillStateDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `SkillDetail` | `name string` | `(SkillDetailDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `ReadSkillFile` | `name string, relativePath string` | `(SkillFileContentDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `SelectSkillDirectory` | `currentPath string` | `(string, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `InstallSkill` | `sourceDirectory string` | `(SkillDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `InstallSkillFromURL` | `sourceURL string, skillPath string` | `(SkillDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `DiscoverSkillSource` | `sourceURL string, skillPath string` | `(SkillDiscoveryDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `DiscoverLocalSkillSource` | `sourceDirectory string` | `(SkillDiscoveryDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `InstallDiscoveredSkillsFromURL` | `sourceURL string, paths []string` | `([]SkillDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `InstallDiscoveredSkillsFromDirectory` | `sourceDirectory string, paths []string` | `([]SkillDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `CheckSkillUpdate` | `name string` | `(SkillUpdateCheckDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `UpdateSkill` | `name string` | `(SkillUpdateResultDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `ReinstallSkill` | `name string` | `(SkillUpdateResultDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `ReinstallSkillFromURL` | `name string, sourceURL string, skillPath string` | `(SkillUpdateResultDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `ReinstallSkillFromDirectory` | `name string, sourceDirectory string` | `(SkillUpdateResultDTO, error)` | [skills.js](../../frontend/src/api/skills.js) |
| `SetSkillAlias` | `name string, alias string` | `error` | [skills.js](../../frontend/src/api/skills.js) |
| `EnableSkillForAgent` | `skillName string, agentID string` | `error` | [skills.js](../../frontend/src/api/skills.js) |
| `DisableSkillForAgent` | `skillName string, agentID string` | `error` | [skills.js](../../frontend/src/api/skills.js) |
| `DeleteSkill` | `name string` | `error` | [skills.js](../../frontend/src/api/skills.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type SkillAgentDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// EnabledSkills 是该 Agent Profile 当前保存的 canonical Skill 名称副本。
	// Settings → Skills 用它驱动“先选 Agent，再开关 Skill”的唯一控制面；它不是第二份关系存储。
	EnabledSkills []string `json:"enabledSkills"`
}

type SkillFileDTO struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
	Text      bool   `json:"text"`
}

type SkillSourceDTO struct {
	Known            bool   `json:"known"`
	Kind             string `json:"kind,omitempty"`
	Provider         string `json:"provider,omitempty"`
	DisplayURL       string `json:"displayUrl,omitempty"`
	LocalDirectory   string `json:"localDirectory,omitempty"`
	SkillPath        string `json:"skillPath,omitempty"`
	Repository       string `json:"repository,omitempty"`
	Ref              string `json:"ref,omitempty"`
	InstalledAt      string `json:"installedAt,omitempty"`
	UpdatedAt        string `json:"updatedAt,omitempty"`
	RecordedIdentity string `json:"recordedIdentity,omitempty"`
	Drifted          bool   `json:"drifted"`
}

type SkillUpdateCheckDTO struct {
	Name              string `json:"name"`
	CurrentIdentity   string `json:"currentIdentity"`
	CandidateIdentity string `json:"candidateIdentity"`
	UpdateAvailable   bool   `json:"updateAvailable"`
	SourceDrifted     bool   `json:"sourceDrifted"`
	CheckedAt         string `json:"checkedAt"`
}

type SkillUpdateResultDTO struct {
	Name             string         `json:"name"`
	Identity         string         `json:"identity"`
	PreviousIdentity string         `json:"previousIdentity"`
	Changed          bool           `json:"changed"`
	Source           SkillSourceDTO `json:"source"`
}

type SkillDiagnosticDTO struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type SkillScriptRuntimeDTO struct {
	Path      string `json:"path"`
	Language  string `json:"language,omitempty"`
	Command   string `json:"command,omitempty"`
	Available bool   `json:"available"`
	Supported bool   `json:"supported"`
	Message   string `json:"message,omitempty"`
}

type SkillDiscoveryCandidateDTO struct {
	Path           string                  `json:"path"`
	Name           string                  `json:"name"`
	Description    string                  `json:"description"`
	SpecStatus     string                  `json:"specStatus"`
	SpecMessage    string                  `json:"specMessage,omitempty"`
	RuntimeStatus  string                  `json:"runtimeStatus"`
	RuntimeMessage string                  `json:"runtimeMessage,omitempty"`
	Valid          bool                    `json:"valid"`
	Error          string                  `json:"error,omitempty"`
	Installed      bool                    `json:"installed"`
	FileCount      int                     `json:"fileCount"`
	SizeBytes      int64                   `json:"sizeBytes"`
	HasScripts     bool                    `json:"hasScripts"`
	HasReferences  bool                    `json:"hasReferences"`
	HasAssets      bool                    `json:"hasAssets"`
	Diagnostics    []SkillDiagnosticDTO    `json:"diagnostics,omitempty"`
	ScriptRuntimes []SkillScriptRuntimeDTO `json:"scriptRuntimes,omitempty"`
}

type SkillDiscoveryDTO struct {
	SourceKind    string                       `json:"sourceKind"`
	Provider      string                       `json:"provider,omitempty"`
	DisplaySource string                       `json:"displaySource,omitempty"`
	Candidates    []SkillDiscoveryCandidateDTO `json:"candidates"`
}

type SkillDetailDTO struct {
	Name           string                  `json:"name"`
	Alias          string                  `json:"alias,omitempty"`
	Description    string                  `json:"description"`
	SpecStatus     string                  `json:"specStatus"`
	SpecMessage    string                  `json:"specMessage,omitempty"`
	License        string                  `json:"license,omitempty"`
	Compatibility  string                  `json:"compatibility,omitempty"`
	Metadata       map[string]string       `json:"metadata,omitempty"`
	AllowedTools   string                  `json:"allowedTools,omitempty"`
	RuntimeStatus  string                  `json:"runtimeStatus"`
	RuntimeMessage string                  `json:"runtimeMessage,omitempty"`
	Diagnostics    []SkillDiagnosticDTO    `json:"diagnostics,omitempty"`
	ScriptRuntimes []SkillScriptRuntimeDTO `json:"scriptRuntimes,omitempty"`
	HasAssets      bool                    `json:"hasAssets"`
	RootDir        string                  `json:"rootDir"`
	Identity       string                  `json:"identity"`
	FileCount      int                     `json:"fileCount"`
	SizeBytes      int64                   `json:"sizeBytes"`
	UpdatedAt      string                  `json:"updatedAt"`
	Source         SkillSourceDTO          `json:"source"`
	Files          []SkillFileDTO          `json:"files"`
}

type SkillFileContentDTO struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
	Content   string `json:"content"`
}

type SkillDTO struct {
	Name string `json:"name"`

	// Alias 是 Humbert 用户自己的本地展示名称。它不参与 Skill 身份、Agent 引用或 Runtime。
	Alias          string                  `json:"alias,omitempty"`
	Description    string                  `json:"description"`
	SpecStatus     string                  `json:"specStatus"`
	SpecMessage    string                  `json:"specMessage,omitempty"`
	License        string                  `json:"license,omitempty"`
	Compatibility  string                  `json:"compatibility,omitempty"`
	Metadata       map[string]string       `json:"metadata,omitempty"`
	AllowedTools   string                  `json:"allowedTools,omitempty"`
	RuntimeStatus  string                  `json:"runtimeStatus"`
	RuntimeMessage string                  `json:"runtimeMessage,omitempty"`
	Diagnostics    []SkillDiagnosticDTO    `json:"diagnostics,omitempty"`
	ScriptRuntimes []SkillScriptRuntimeDTO `json:"scriptRuntimes,omitempty"`
	DirectoryName  string                  `json:"directoryName"`
	RootDir        string                  `json:"rootDir"`
	Identity       string                  `json:"identity"`
	Valid          bool                    `json:"valid"`
	Error          string                  `json:"error,omitempty"`
	FileCount      int                     `json:"fileCount"`
	SizeBytes      int64                   `json:"sizeBytes"`
	HasReferences  bool                    `json:"hasReferences"`
	HasScripts     bool                    `json:"hasScripts"`
	HasAssets      bool                    `json:"hasAssets"`
	UpdatedAt      string                  `json:"updatedAt"`

	// Source 是 Humbert 记录的安装来源投影。Invalid Skill 也会携带它，
	// 这样详情页可以直接提供“从来源修复”，而不要求 Package 当前可解析。
	Source       SkillSourceDTO  `json:"source"`
	UsedByAgents []SkillAgentDTO `json:"usedByAgents"`
}

type SkillStateDTO struct {
	RootDir string `json:"rootDir"`

	// SourceError 只表示来源元数据不可用，不应让整个 Catalog 失效。Skill 安装、启停和删除
	// 仍然可以继续；更新/修复来源需要用户先处理该警告。
	SourceError     string   `json:"sourceError,omitempty"`
	SourceResolvers []string `json:"sourceResolvers"`

	// Agents 是 Skills 设置页顶部可选择的 Agent 列表，并携带 enabled_skills 只读投影。
	// 开关操作最终仍然只修改 Agent.config.json，不创建第二份 Skill 关系存储。
	Agents []SkillAgentDTO `json:"agents"`
	Skills []SkillDTO      `json:"skills"`
}
```

</details>

## TaskService

[internal/services/taskservice.go](../../internal/services/taskservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `List` | `无` | `([]TaskDTO, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `Runs` | `taskID string` | `([]TaskRunDTO, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `Create` | `request SaveTaskRequest` | `(TaskDTO, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `Update` | `id string, request SaveTaskRequest` | `(TaskDTO, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `SetStatus` | `id string, request TaskStatusRequest` | `(TaskDTO, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `Archive` | `id string` | `error` | [tasks.js](../../frontend/src/api/tasks.js) |
| `Delete` | `id string` | `([]string, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `DeleteRun` | `id string` | `([]string, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `ClearRuns` | `taskID string` | `([]string, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `RunNow` | `id string` | `(TaskRunDTO, error)` | [tasks.js](../../frontend/src/api/tasks.js) |
| `CancelRun` | `id string` | `(TaskRunDTO, error)` | [tasks.js](../../frontend/src/api/tasks.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type TaskScheduleDTO struct {
	Type            string `json:"type"`
	TimeZone        string `json:"timeZone"`
	RunAt           string `json:"runAt,omitempty"`
	IntervalMinutes int    `json:"intervalMinutes,omitempty"`
	TimeOfDay       string `json:"timeOfDay,omitempty"`
	Weekdays        []int  `json:"weekdays,omitempty"`
	MisfirePolicy   string `json:"misfirePolicy"`
	OverlapPolicy   string `json:"overlapPolicy"`
}

type TaskLimitsDTO struct {
	MaxDurationSeconds int `json:"maxDurationSeconds"`
	MaxModelCalls      int `json:"maxModelCalls"`
	MaxToolCalls       int `json:"maxToolCalls"`
	MaxTotalTokens     int `json:"maxTotalTokens"`
	MaxAttempts        int `json:"maxAttempts"`
	RetryDelaySeconds  int `json:"retryDelaySeconds"`
}

type TaskDTO struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentID"`
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"originRef,omitempty"`
	Name      string `json:"name"`
	Prompt    string `json:"prompt"`
	Execution string `json:"execution"`

	// ConversationMode / PersistentSessionID 让桌面端明确展示任务的会话策略。
	// PersistentSessionID 只用于状态展示，不要求前端直接操作 Session 生命周期。
	ConversationMode    string          `json:"conversationMode"`
	PersistentSessionID string          `json:"persistentSessionID,omitempty"`
	Status              string          `json:"status"`
	Schedule            TaskScheduleDTO `json:"schedule"`
	Limits              TaskLimitsDTO   `json:"limits"`
	NextRunAt           string          `json:"nextRunAt,omitempty"`
	CreatedAt           string          `json:"createdAt"`
	UpdatedAt           string          `json:"updatedAt"`
}

type TaskApprovalDTO struct {
	ID           string `json:"id"`
	ToolName     string `json:"toolName"`
	Risk         string `json:"risk"`
	Presentation any    `json:"presentation"`
	CreatedAt    string `json:"createdAt"`
	ExpiresAt    string `json:"expiresAt"`
}

type TaskRunDTO struct {
	ID               string           `json:"id"`
	TaskID           string           `json:"taskID"`
	AgentID          string           `json:"agentID"`
	SessionID        string           `json:"sessionID"`
	SessionAvailable bool             `json:"sessionAvailable"`
	RequestID        string           `json:"requestID,omitempty"`
	RuntimeRunID     string           `json:"runtimeRunID,omitempty"`
	Trigger          string           `json:"trigger"`
	Execution        string           `json:"execution"`
	ParentRunID      string           `json:"parentRunID,omitempty"`
	ScheduledFor     string           `json:"scheduledFor"`
	Attempt          int              `json:"attempt"`
	Status           string           `json:"status"`
	ToolCalls        int              `json:"toolCalls"`
	ModelCalls       int              `json:"modelCalls"`
	InputTokens      int              `json:"inputTokens"`
	OutputTokens     int              `json:"outputTokens"`
	TotalTokens      int              `json:"totalTokens"`
	Approval         *TaskApprovalDTO `json:"approval,omitempty"`
	ResultMessageID  string           `json:"resultMessageID,omitempty"`
	ResultPreview    string           `json:"resultPreview,omitempty"`
	Error            string           `json:"error,omitempty"`
	CreatedAt        string           `json:"createdAt"`
	StartedAt        string           `json:"startedAt,omitempty"`
	FinishedAt       string           `json:"finishedAt,omitempty"`
	DeadlineAt       string           `json:"deadlineAt,omitempty"`
}

type SaveTaskRequest struct {
	AgentID          string          `json:"agentID"`
	Name             string          `json:"name"`
	Prompt           string          `json:"prompt"`
	Execution        string          `json:"execution"`
	ConversationMode string          `json:"conversationMode"`
	Status           string          `json:"status"`
	Schedule         TaskScheduleDTO `json:"schedule"`
	Limits           TaskLimitsDTO   `json:"limits"`
}

type TaskStatusRequest struct {
	Status string `json:"status"`
}
```

</details>

## WorkspaceService

[internal/services/workspaceservice.go](../../internal/services/workspaceservice.go)

| 方法 | 参数 | 返回 | 当前 API 调用 |
| --- | --- | --- | --- |
| `Overview` | `agentID string` | `(WorkspaceOverviewDTO, error)` | [workspace.js](../../frontend/src/api/workspace.js) |
| `ListDirectory` | `agentID, path string` | `(WorkspaceDirectoryDTO, error)` | [workspace.js](../../frontend/src/api/workspace.js) |
| `PreviewFile` | `agentID, path string` | `(WorkspacePreviewDTO, error)` | [workspace.js](../../frontend/src/api/workspace.js) |
| `SearchDocuments` | `agentID, query string` | `(DocumentSearchDTO, error)` | [workspace.js](../../frontend/src/api/workspace.js) |

<details>
<summary>本服务 DTO/请求结构原文</summary>

```go
type WorkspaceOverviewDTO struct {
	AgentID        string `json:"agentID"`
	AgentName      string `json:"agentName"`
	Mode           string `json:"mode"`
	RootDir        string `json:"rootDir"`
	FileCount      int    `json:"fileCount"`
	DirectoryCount int    `json:"directoryCount"`
	TotalBytes     int64  `json:"totalBytes"`
	StatsTruncated bool   `json:"statsTruncated"`
	ScannedAt      string `json:"scannedAt"`
}

type WorkspaceEntryDTO struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	Hidden     bool   `json:"hidden"`
}

type WorkspaceDirectoryDTO struct {
	Path      string              `json:"path"`
	Entries   []WorkspaceEntryDTO `json:"entries"`
	Truncated bool                `json:"truncated"`
}

type WorkspacePreviewDTO struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	MIMEType   string `json:"mimeType"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
	Content    string `json:"content,omitempty"`
	DataURL    string `json:"dataURL,omitempty"`
	Truncated  bool   `json:"truncated"`
}

type DocumentSearchDTO struct {
	Results  []searchindex.DocumentResult `json:"results"`
	Updating bool                         `json:"updating"`
	Error    string                       `json:"error,omitempty"`
}
```

</details>

