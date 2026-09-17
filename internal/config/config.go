package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/viper"
)

const (
	defaultAppName = "Humbert"

	defaultMaxFileBytes = int64(1024 * 1024)

	defaultContextSmallWindowThreshold = 64 * 1024
	defaultContextMinReserveTokens     = 16 * 1024
	defaultContextLargeReserveRatio    = 0.10
	defaultContextSmallReserveRatio    = 0.20
	defaultContextKeepRecentRatio      = 0.20
	defaultContextKeepRecentMinTokens  = 8 * 1024
	defaultContextKeepRecentMaxTokens  = 32 * 1024
	defaultContextSerializerMaxChars   = 4000
	defaultMemoryTurnInterval          = 10
	defaultMemoryTokenInterval         = 12 * 1024
	defaultContextOperationTimeoutMS   = 120000

	defaultPermissionApprovalTimeoutMS = 30 * 60 * 1000

	defaultSkillMaxDefinitionBytes = int64(512 * 1024)
	defaultSkillMaxAssetBytes      = int64(4 * 1024 * 1024)
	defaultSkillMaxPackageBytes    = int64(16 * 1024 * 1024)
	defaultSkillMaxFiles           = 256
	defaultSkillMaxDownloadBytes   = int64(32 * 1024 * 1024)
	defaultSkillDownloadTimeoutMS  = 120000
	defaultSkillMaxRedirects       = 5

	defaultMCPConnectTimeoutMS            = 15000
	defaultMCPMaxToolsPerServer           = 64
	defaultMCPMaxToolPages                = 20
	defaultMCPMaxToolDescriptionChars     = 4000
	defaultMCPMaxToolResultChars          = 100000
	defaultMCPPreserveToolResultTailChars = 4000
	defaultMCPCatalogTTLMS                = 60000

	maxAllowedFileBytes = int64(64 * 1024 * 1024)
)

const defaultConfigYAML = `app:
  name: Humbert

logging:
  level: info
  format: json

runtime:
  context:
    auto_compaction: true
    small_window_threshold: 65536
    min_reserve_tokens: 16384
    large_reserve_ratio: 0.10
    small_reserve_ratio: 0.20
    keep_recent_ratio: 0.20
    keep_recent_min_tokens: 8192
    keep_recent_max_tokens: 32768
    serializer_max_chars: 4000
    memory_turn_interval: 10
    memory_token_interval: 12288
    operation_timeout_ms: 120000
  skills:
    max_definition_bytes: 524288
    max_asset_bytes: 4194304
    max_package_bytes: 16777216
    max_files: 256
    max_download_bytes: 33554432
    download_timeout_ms: 120000
    max_redirects: 5
  mcp:
    connect_timeout_ms: 15000
    max_tools_per_server: 64
    max_tool_pages: 20
    max_tool_description_chars: 4000
    max_tool_result_chars: 100000
    preserve_tool_result_tail_chars: 4000
    catalog_ttl_ms: 60000

security:
  max_file_bytes: 1048576
  shell_enabled: true
  sandbox:
    default_profile: standard
    default_network_mode: public
    native_mode: preferred
    command_grace_period_ms: 1500
  permissions:
    enabled: true
    read_action: allow
    write_action: ask
    exec_action: ask
    approval_timeout_ms: 1800000
  shell_allowed_commands:
    - go
    - git
    - node
    - npm
    - npx
    - python
    - python3
	- bash
`

const defaultPreferencesJSON = `{
  "schema_version": 1,
  "user": {
    "name": "你"
  }
}
`

// Config 保存 Humbert 启动阶段使用的应用级配置。
//
// Config 只承载启动时需要且相对稳定的运行参数，例如日志级别、Runtime
// 限制和基础安全策略。Provider、Model、Agent、Session 等动态业务状态不进入
// config.yaml，而是分别由 config/*.json、agents/*/config.json 和 Session JSONL
// 管理。
//
// 这样设计的目的有两个：
//   - Viper 只负责“应用如何启动”，避免动态业务状态与环境变量覆盖机制混在一起；
//   - 文件级业务状态可以按领域独立加锁，不再受单一 SQLite Writer 限制。
//
// API Key、OAuth Token 等敏感数据仍不进入 Config，由 credential.Store 存放在
// secrets/ 下。业务代码禁止绕过本 package 直接读取环境变量或 config.yaml。
type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Logging  LoggingConfig  `mapstructure:"logging"`
	Runtime  RuntimeConfig  `mapstructure:"runtime"`
	Security SecurityConfig `mapstructure:"security"`

	// Paths 是根据当前用户 Home 目录推导出的固定路径。
	// 用户不能通过 config.yaml 修改这些路径，避免内部状态被重定向到任意目录。
	Paths Paths `mapstructure:"-"`
}

// AppConfig 保存应用自身配置。
type AppConfig struct {
	Name string `mapstructure:"name"`
}

// LoggingConfig 保存统一日志配置。
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// RuntimeConfig 保存 Agent Runtime 的基础限制。
type RuntimeConfig struct {
	Context ContextConfig `mapstructure:"context"`

	// Skills 描述本地 Skill Package 的文件安全上限。动态的已安装 Skill 与 Agent 启用关系
	// 不属于应用配置，分别保存在 skills/ 目录和 Agent Profile 中。
	Skills SkillConfig `mapstructure:"skills"`

	// MCP 描述 Eino officialmcp Adapter 的控制面限制。Server/Agent 动态配置不进入
	// config.yaml，分别保存在 mcp/servers.json 与 Agent Profile。
	MCP MCPConfig `mapstructure:"mcp"`
}

// ContextConfig 保存 ContextEngine 与 Session Memory 的应用级策略。
//
// 这些值通过 Viper 统一加载，可由 config.yaml 和 HUMBERT_RUNTIME_CONTEXT_* 环境变量
// 覆盖。模型自身的 ContextWindow/MaxOutputTokens 不在这里，因为它们属于每个 Model
// 的动态配置。ContextConfig 只描述“何时压缩、保留多少近期历史、何时刷新 Session
// Memory”等全局策略。
//
// 所有比例均使用 0-1 小数；Token 数均为逻辑预算而不是字节数。
type ContextConfig struct {
	// AutoCompaction 控制是否在下一次模型调用前自动执行压缩。手动压缩不受此开关影响。
	AutoCompaction bool `mapstructure:"auto_compaction"`

	// SmallWindowThreshold 区分小上下文模型与大上下文模型。小窗口使用更高百分比的
	// 安全余量，避免固定 16k reserve 对 16k/32k 模型过于激进。
	SmallWindowThreshold int `mapstructure:"small_window_threshold"`

	// MinReserveTokens 是大窗口模型至少保留的输入/输出安全空间。
	MinReserveTokens int `mapstructure:"min_reserve_tokens"`

	LargeReserveRatio float64 `mapstructure:"large_reserve_ratio"`
	SmallReserveRatio float64 `mapstructure:"small_reserve_ratio"`

	// KeepRecent* 决定每次 Compaction 后仍以原始消息形式保留的近期历史预算。
	KeepRecentRatio     float64 `mapstructure:"keep_recent_ratio"`
	KeepRecentMinTokens int     `mapstructure:"keep_recent_min_tokens"`
	KeepRecentMaxTokens int     `mapstructure:"keep_recent_max_tokens"`

	// SerializerMaxChars 限制单条消息块送给压缩/Memory 模型的字符数；实现还会基于
	// 该值和模型 Context Window 施加全局输入上限。原始 Transcript 不会被截断。
	SerializerMaxChars int `mapstructure:"serializer_max_chars"`

	// MemoryTurnInterval/MemoryTokenInterval 控制 Session Memory 的增量刷新节奏。
	MemoryTurnInterval  int `mapstructure:"memory_turn_interval"`
	MemoryTokenInterval int `mapstructure:"memory_token_interval"`

	// OperationTimeoutMS 是手动压缩、自动压缩和 Memory 更新的单次上限。所有调用仍
	// 继承上层 Context，因此应用关闭或 Turn 取消时能够及时退出。
	OperationTimeoutMS int `mapstructure:"operation_timeout_ms"`
}

// SkillConfig 保存本地 Skill Package 的应用级安全限制。
//
// 这些值只控制 Humbert 读取/安装 Skill 时允许的文件规模，不决定某个 Agent 启用哪些 Skill。
// 所有配置统一经 Viper 加载，可使用 HUMBERT_RUNTIME_SKILLS_* 环境变量覆盖。
type SkillConfig struct {
	// MaxDefinitionBytes 限制单个 SKILL.md。Skill 指令会进入模型 Context，过大的入口文件既
	// 浪费 Token，也容易让单个包挤占全部上下文。
	MaxDefinitionBytes int64 `mapstructure:"max_definition_bytes"`

	// MaxAssetBytes 限制 references/scripts 等单个资源文件。Skill Tool 当前只把 UTF-8 文本
	// 返回给模型，二进制资源即使存在也不会直接注入 Context。
	MaxAssetBytes int64 `mapstructure:"max_asset_bytes"`

	// MaxPackageBytes 与 MaxFiles 限制最终安装后的单个 Skill Package，防止用户误选大型
	// 仓库或恶意目录导致扫描/哈希耗尽本地资源。
	MaxPackageBytes int64 `mapstructure:"max_package_bytes"`
	MaxFiles        int   `mapstructure:"max_files"`

	// MaxDownloadBytes 限制远程 ZIP 的压缩体积。远程 Skill 属于不可信输入，下载阶段必须
	// 在解压前先做硬上限，避免一个超大响应占满内存或磁盘。
	MaxDownloadBytes int64 `mapstructure:"max_download_bytes"`

	// DownloadTimeoutMS 是远程 Skill 下载的总超时。Tool 调用仍继承上层 Turn Context，因此
	// 用户取消 Turn 或应用关闭时会比该超时更早退出。
	DownloadTimeoutMS int `mapstructure:"download_timeout_ms"`

	// MaxRedirects 限制远程 Skill 下载重定向次数；每一跳都会重新执行 HTTPS 与公网地址
	// 校验，避免重定向绕过 SSRF 边界。
	MaxRedirects int `mapstructure:"max_redirects"`
}

// MCPConfig 保存 Eino officialmcp Adapter 的应用级安全/上下文上限。
//
// MCP Server、Agent Tool Selection 与 Credential 都属于动态状态，不进入本结构。
type MCPConfig struct {
	// ConnectTimeoutMS 限制 stdio / Streamable HTTP MCP Server 初始化握手等待时间。
	ConnectTimeoutMS int `mapstructure:"connect_timeout_ms"`

	// MaxToolsPerServer 限制单个 Agent 从同一 MCP Server 显式启用的 Tool 数量。
	MaxToolsPerServer int `mapstructure:"max_tools_per_server"`

	// MaxToolPages 限制 officialmcp tools/list 自动翻页次数。
	MaxToolPages int `mapstructure:"max_tool_pages"`

	// MaxToolDescriptionChars 防止异常长 Tool 描述大量占用模型输入。
	MaxToolDescriptionChars int `mapstructure:"max_tool_description_chars"`

	// MaxToolResultChars 限制 officialmcp 返回给 Agent 的单次 ToolResult 字符数。
	MaxToolResultChars int `mapstructure:"max_tool_result_chars"`

	// PreserveToolResultTailChars 在裁剪超长结果时保留尾部，便于错误摘要/分页信息不丢失。
	PreserveToolResultTailChars int `mapstructure:"preserve_tool_result_tail_chars"`

	// CatalogTTLMS 控制 Desktop 控制面 tools/list Catalog 缓存时间。Runtime 在真正构建
	// Agent Tool 时仍由 Eino officialmcp 校验当前 Server Catalog，不依赖该缓存。
	CatalogTTLMS int `mapstructure:"catalog_ttl_ms"`
}

// SecurityConfig 保存本地 Tool 的基础安全策略。
//
// Shell 默认关闭。即使启用，也只能运行 ShellAllowedCommands 明确允许的程序。
// 后续 Permission/Approval 模块仍应在本配置之上增加运行时授权，而不是把这里
// 当成最终安全边界。
type SecurityConfig struct {
	MaxFileBytes int64 `mapstructure:"max_file_bytes"`

	ShellEnabled bool `mapstructure:"shell_enabled"`

	ShellAllowedCommands []string `mapstructure:"shell_allowed_commands"`

	// Sandbox 描述 Permission 之下的强制执行边界。Permission Allow 永远不能扩大它。
	Sandbox SandboxConfig `mapstructure:"sandbox"`

	// Permissions 描述 Tool/Capability 的运行时授权策略。它与 ShellEnabled 的职责不同：
	// ShellEnabled 决定 run_command 能力是否存在；Permissions 决定一个已经存在的能力
	// 在具体 Agent/Session 中是否允许立即执行、拒绝或需要用户确认。
	Permissions PermissionConfig `mapstructure:"permissions"`
}

// SandboxConfig 保存跨平台 Sandbox 的应用级默认策略。
// Agent 可以进一步收紧或显式增加额外目录，但 Permission Approval 不能修改本配置。
type SandboxConfig struct {
	DefaultProfile       string `mapstructure:"default_profile"`
	DefaultNetworkMode   string `mapstructure:"default_network_mode"`
	NativeMode           string `mapstructure:"native_mode"`
	CommandGracePeriodMS int    `mapstructure:"command_grace_period_ms"`
}

// PermissionConfig 保存 Permission + Approval 的应用级策略。
//
// 该配置只定义“没有用户规则命中时”的默认风险动作以及人工审批超时，不保存任何用户
// 授权结果。用户选择“Agent 始终允许”产生的规则单独写入 config/permissions.json，
// Session 授权只存在内存中，避免临时权限跨应用重启继续生效。
//
// Action 只允许 allow / deny / ask。生产默认值为：read=allow、write=ask、exec=ask。
// 这样常规读取不会频繁打断 Agent，而修改文件和执行本地程序都需要明确授权。
type PermissionConfig struct {
	Enabled bool `mapstructure:"enabled"`

	ReadAction  string `mapstructure:"read_action"`
	WriteAction string `mapstructure:"write_action"`
	ExecAction  string `mapstructure:"exec_action"`

	// ApprovalTimeoutMS 是一次待审批请求允许保持暂停状态的最长时间。Runtime 使用受
	// rootCtx 控制的 Timer 实现超时，应用关闭或用户取消 Turn 时会提前回收，不创建
	// 无法控制生命周期的后台任务。
	ApprovalTimeoutMS int `mapstructure:"approval_timeout_ms"`
}

// Paths 描述 Humbert Local-first 数据目录布局。
//
// 所有持久化数据统一位于 ~/.humbert-agent。动态状态按领域拆分：
//
//	~/.humbert-agent/
//	├── config.yaml
//	├── config/
//	│   ├── providers.json
//	│   ├── models.json
//	│   └── preferences.json
//	├── secrets/
//	├── agents/<agent-id>/config.json
//	├── agents/<agent-id>/sessions/<session-id>/
//	│   ├── config.json
//	│   └── session.jsonl
//	└── workspaces/
//
// Paths 只描述路径，不负责读取 Provider/Model/Agent 的业务内容。各领域 Store
// 自己拥有对应文件的 Schema、并发策略和错误语义。
type Paths struct {
	HomeDir string

	ConfigFile string
	ConfigDir  string

	ProvidersFile   string
	ModelsFile      string
	PreferencesFile string
	PermissionsFile string

	MCPServersFile string

	LogsDir string
	LogFile string

	SecretsDir    string
	AgentsDir     string
	WorkspacesDir string
	SkillsDir     string
	MCPDir        string
	CacheDir      string
	TempDir       string
}

// Load 从 ~/.humbert-agent/config.yaml 加载配置。
//
// 加载优先级：
//
//	内置默认值
//	    ↓
//	~/.humbert-agent/config.yaml
//	    ↓
//	HUMBERT_* 环境变量
//
// 例如：
//
//	HUMBERT_LOGGING_LEVEL=debug
//	HUMBERT_SECURITY_SHELL_ENABLED=true
//
// 环境变量读取只允许发生在 config package 内。
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("获取当前用户 Home 目录失败: %w", err)
	}

	return loadFromHome(home)
}

// loadFromHome 是 Load 的内部实现，同时允许单元测试使用临时 Home，避免测试
// 污染真实 ~/.humbert-agent。
func loadFromHome(home string) (*Config, error) {
	paths, err := resolvePaths(home)
	if err != nil {
		return nil, fmt.Errorf("解析 Humbert 应用目录失败: %w", err)
	}

	if err := ensureLayout(paths); err != nil {
		return nil, fmt.Errorf("创建 Humbert 目录结构失败: %w", err)
	}

	if err := ensureDefaultConfig(paths.ConfigFile); err != nil {
		return nil, fmt.Errorf("初始化默认配置文件失败: %w", err)
	}

	// preferences.json 保存用户身份与界面偏好；具体校验和后续写入由
	// preferences.Store 负责。Provider/Model 文件仍由各自领域 Store 初始化。
	if err := ensureDefaultTextFile(paths.PreferencesFile, defaultPreferencesJSON); err != nil {
		return nil, fmt.Errorf("初始化 preferences.json 失败: %w", err)
	}

	v := viper.New()
	setDefaults(v)

	v.SetConfigFile(paths.ConfigFile)
	v.SetConfigType("yaml")
	v.SetEnvPrefix("HUMBERT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", paths.ConfigFile, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析应用配置失败: %w", err)
	}

	cfg.Paths = paths
	normalize(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("校验应用配置失败: %w", err)
	}

	return &cfg, nil
}

func resolvePaths(home string) (Paths, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return Paths{}, errors.New("用户 Home 目录不能为空")
	}

	root := filepath.Join(home, ".humbert-agent")
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("解析应用根目录绝对路径失败: %w", err)
	}
	root = filepath.Clean(absoluteRoot)

	configDir := filepath.Join(root, "config")
	logsDir := filepath.Join(root, "logs")

	return Paths{
		HomeDir: root,

		ConfigFile: filepath.Join(root, "config.yaml"),
		ConfigDir:  configDir,

		ProvidersFile:   filepath.Join(configDir, "providers.json"),
		ModelsFile:      filepath.Join(configDir, "models.json"),
		PreferencesFile: filepath.Join(configDir, "preferences.json"),
		PermissionsFile: filepath.Join(configDir, "permissions.json"),
		MCPServersFile:  filepath.Join(root, "mcp", "servers.json"),

		LogsDir: logsDir,
		LogFile: filepath.Join(logsDir, "humbert.log"),

		SecretsDir:    filepath.Join(root, "secrets"),
		AgentsDir:     filepath.Join(root, "agents"),
		WorkspacesDir: filepath.Join(root, "workspaces"),
		SkillsDir:     filepath.Join(root, "skills"),
		MCPDir:        filepath.Join(root, "mcp"),
		CacheDir:      filepath.Join(root, "cache"),
		TempDir:       filepath.Join(root, "tmp"),
	}, nil
}

// ensureLayout 在应用真正启动前创建完整目录布局。
//
// Unix 系统使用 0700，避免其他本机用户遍历 Humbert 数据。Windows 的实际
// 访问控制由用户目录 ACL 负责。
func ensureLayout(paths Paths) error {
	directories := []string{
		paths.HomeDir,
		paths.ConfigDir,
		paths.LogsDir,
		paths.SecretsDir,
		paths.AgentsDir,
		paths.WorkspacesDir,
		paths.SkillsDir,
		paths.MCPDir,
		paths.CacheDir,
		paths.TempDir,
	}

	// 目录按照父目录在前的顺序创建，因此可以使用 Mkdir 而不是 MkdirAll。
	// 这样如果 ~/.humbert-agent 或其中任何受控子目录已经被替换成符号链接，
	// Humbert 会明确拒绝启动，而不是跟随链接把配置、凭据或 Transcript 写到
	// 应用数据根目录之外。
	for _, directory := range directories {
		if err := ensurePrivateDirectory(directory); err != nil {
			return fmt.Errorf("准备目录 %s 失败: %w", directory, err)
		}
	}

	return nil
}

// ensurePrivateDirectory 创建或验证一个 Humbert 自有目录。
//
// 已存在的符号链接和普通文件都会被拒绝。Unix 下每次启动都会把目录权限收紧为
// 0700；Windows 则依赖用户目录 ACL。调用方必须保证父目录已经存在。
func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("受控目录不能是符号链接")
		}
		if !info.IsDir() {
			return errors.New("受控路径不是目录")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
	} else {
		return fmt.Errorf("读取目录状态失败: %w", err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("设置目录权限失败: %w", err)
		}
	}

	return nil
}

// ensureDefaultConfig 在第一次启动时创建 config.yaml。
func ensureDefaultConfig(path string) error {
	return ensureDefaultTextFile(path, defaultConfigYAML)
}

// ensureDefaultTextFile 使用 O_EXCL 创建首次启动默认文件，不覆盖用户已有配置。
//
// 同一台机器上即使两个启动流程同时进入该函数，也只有一个能够成功创建；
// 另一个看到 os.ErrExist 后直接继续，因此不会发生互相覆盖。
func ensureDefaultTextFile(path string, content string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := os.Lstat(path)
			if statErr != nil {
				return fmt.Errorf("检查已有默认文件 %s 失败: %w", path, statErr)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("默认文件不能是符号链接: %s", path)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("默认文件路径不是普通文件: %s", path)
			}
			return nil
		}
		return fmt.Errorf("创建默认文件 %s 失败: %w", path, err)
	}

	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()

	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("写入默认文件 %s 失败: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步默认文件 %s 失败: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭默认文件 %s 失败: %w", path, err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("设置默认文件 %s 权限失败: %w", path, err)
		}
	}

	success = true
	return nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.name", defaultAppName)
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("runtime.context.auto_compaction", true)
	v.SetDefault("runtime.context.small_window_threshold", defaultContextSmallWindowThreshold)
	v.SetDefault("runtime.context.min_reserve_tokens", defaultContextMinReserveTokens)
	v.SetDefault("runtime.context.large_reserve_ratio", defaultContextLargeReserveRatio)
	v.SetDefault("runtime.context.small_reserve_ratio", defaultContextSmallReserveRatio)
	v.SetDefault("runtime.context.keep_recent_ratio", defaultContextKeepRecentRatio)
	v.SetDefault("runtime.context.keep_recent_min_tokens", defaultContextKeepRecentMinTokens)
	v.SetDefault("runtime.context.keep_recent_max_tokens", defaultContextKeepRecentMaxTokens)
	v.SetDefault("runtime.context.serializer_max_chars", defaultContextSerializerMaxChars)
	v.SetDefault("runtime.context.memory_turn_interval", defaultMemoryTurnInterval)
	v.SetDefault("runtime.context.memory_token_interval", defaultMemoryTokenInterval)
	v.SetDefault("runtime.context.operation_timeout_ms", defaultContextOperationTimeoutMS)
	v.SetDefault("runtime.skills.max_definition_bytes", defaultSkillMaxDefinitionBytes)
	v.SetDefault("runtime.skills.max_asset_bytes", defaultSkillMaxAssetBytes)
	v.SetDefault("runtime.skills.max_package_bytes", defaultSkillMaxPackageBytes)
	v.SetDefault("runtime.skills.max_files", defaultSkillMaxFiles)
	v.SetDefault("runtime.skills.max_download_bytes", defaultSkillMaxDownloadBytes)
	v.SetDefault("runtime.skills.download_timeout_ms", defaultSkillDownloadTimeoutMS)
	v.SetDefault("runtime.skills.max_redirects", defaultSkillMaxRedirects)
	v.SetDefault("runtime.mcp.connect_timeout_ms", defaultMCPConnectTimeoutMS)
	v.SetDefault("runtime.mcp.max_tools_per_server", defaultMCPMaxToolsPerServer)
	v.SetDefault("runtime.mcp.max_tool_pages", defaultMCPMaxToolPages)
	v.SetDefault("runtime.mcp.max_tool_description_chars", defaultMCPMaxToolDescriptionChars)
	v.SetDefault("runtime.mcp.max_tool_result_chars", defaultMCPMaxToolResultChars)
	v.SetDefault("runtime.mcp.preserve_tool_result_tail_chars", defaultMCPPreserveToolResultTailChars)
	v.SetDefault("runtime.mcp.catalog_ttl_ms", defaultMCPCatalogTTLMS)
	v.SetDefault("security.max_file_bytes", defaultMaxFileBytes)
	v.SetDefault("security.permissions.enabled", true)
	v.SetDefault("security.permissions.read_action", "allow")
	v.SetDefault("security.permissions.write_action", "ask")
	v.SetDefault("security.permissions.exec_action", "ask")
	v.SetDefault("security.permissions.approval_timeout_ms", defaultPermissionApprovalTimeoutMS)
	v.SetDefault("security.shell_enabled", false)
	v.SetDefault("security.sandbox.default_profile", "standard")
	v.SetDefault("security.sandbox.default_network_mode", "public")
	v.SetDefault("security.sandbox.native_mode", "preferred")
	v.SetDefault("security.sandbox.command_grace_period_ms", 1500)
	v.SetDefault("security.shell_allowed_commands", []string{
		"go",
		"git",
		"node",
		"npm",
		"npx",
		"python",
		"python3",
	})
}

func normalize(cfg *Config) {
	cfg.App.Name = strings.TrimSpace(cfg.App.Name)
	cfg.Logging.Level = strings.ToLower(strings.TrimSpace(cfg.Logging.Level))
	cfg.Logging.Format = strings.ToLower(strings.TrimSpace(cfg.Logging.Format))
	cfg.Security.ShellAllowedCommands = normalizeCommands(cfg.Security.ShellAllowedCommands)
	cfg.Security.Sandbox.DefaultProfile = strings.ToLower(strings.TrimSpace(cfg.Security.Sandbox.DefaultProfile))
	cfg.Security.Sandbox.DefaultNetworkMode = strings.ToLower(strings.TrimSpace(cfg.Security.Sandbox.DefaultNetworkMode))
	cfg.Security.Sandbox.NativeMode = strings.ToLower(strings.TrimSpace(cfg.Security.Sandbox.NativeMode))
	cfg.Security.Permissions = NormalizePermissionConfig(cfg.Security.Permissions)
}

func normalizeCommands(commands []string) []string {
	result := make([]string, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))

	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}

		if runtime.GOOS == "windows" {
			command = strings.ToLower(command)
		}

		if _, exists := seen[command]; exists {
			continue
		}

		seen[command] = struct{}{}
		result = append(result, command)
	}

	return result
}

func validate(cfg *Config) error {
	if cfg.App.Name == "" {
		return errors.New("app.name 不能为空")
	}

	switch cfg.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("不支持的 logging.level: %q", cfg.Logging.Level)
	}

	switch cfg.Logging.Format {
	case "json", "text":
	default:
		return fmt.Errorf("不支持的 logging.format: %q", cfg.Logging.Format)
	}

	contextConfig := cfg.Runtime.Context
	if contextConfig.SmallWindowThreshold < 4096 {
		return errors.New("runtime.context.small_window_threshold 不能小于 4096")
	}
	if contextConfig.MinReserveTokens < 0 {
		return errors.New("runtime.context.min_reserve_tokens 不能小于 0")
	}
	if contextConfig.LargeReserveRatio <= 0 || contextConfig.LargeReserveRatio >= 1 {
		return errors.New("runtime.context.large_reserve_ratio 必须位于 0-1 之间")
	}
	if contextConfig.SmallReserveRatio <= 0 || contextConfig.SmallReserveRatio >= 1 {
		return errors.New("runtime.context.small_reserve_ratio 必须位于 0-1 之间")
	}
	if contextConfig.KeepRecentRatio <= 0 || contextConfig.KeepRecentRatio >= 1 {
		return errors.New("runtime.context.keep_recent_ratio 必须位于 0-1 之间")
	}
	if contextConfig.KeepRecentMinTokens < 256 ||
		contextConfig.KeepRecentMaxTokens < contextConfig.KeepRecentMinTokens {
		return errors.New("runtime.context.keep_recent_min_tokens/max_tokens 配置无效")
	}
	if contextConfig.SerializerMaxChars < 256 || contextConfig.SerializerMaxChars > 65536 {
		return errors.New("runtime.context.serializer_max_chars 必须位于 256-65536 之间")
	}
	if contextConfig.MemoryTurnInterval < 1 || contextConfig.MemoryTurnInterval > 1000 {
		return errors.New("runtime.context.memory_turn_interval 必须位于 1-1000 之间")
	}
	if contextConfig.MemoryTokenInterval < 256 {
		return errors.New("runtime.context.memory_token_interval 不能小于 256")
	}
	if contextConfig.OperationTimeoutMS < 1000 || contextConfig.OperationTimeoutMS > 10*60*1000 {
		return errors.New("runtime.context.operation_timeout_ms 必须位于 1000-600000 之间")
	}

	skillConfig := cfg.Runtime.Skills
	if skillConfig.MaxDefinitionBytes < 1024 || skillConfig.MaxDefinitionBytes > 4*1024*1024 {
		return errors.New("runtime.skills.max_definition_bytes 必须位于 1024-4194304 之间")
	}
	if skillConfig.MaxAssetBytes < 1024 || skillConfig.MaxAssetBytes > 64*1024*1024 {
		return errors.New("runtime.skills.max_asset_bytes 必须位于 1024-67108864 之间")
	}
	if skillConfig.MaxPackageBytes < skillConfig.MaxDefinitionBytes ||
		skillConfig.MaxPackageBytes < skillConfig.MaxAssetBytes ||
		skillConfig.MaxPackageBytes > 256*1024*1024 {
		return errors.New("runtime.skills.max_package_bytes 必须不小于单文件上限且不能超过 268435456")
	}
	if skillConfig.MaxFiles < 1 || skillConfig.MaxFiles > 4096 {
		return errors.New("runtime.skills.max_files 必须位于 1-4096 之间")
	}
	if skillConfig.MaxDownloadBytes < 1024 || skillConfig.MaxDownloadBytes > 512*1024*1024 {
		return errors.New("runtime.skills.max_download_bytes 必须位于 1024-536870912 之间")
	}
	if skillConfig.DownloadTimeoutMS < 1000 || skillConfig.DownloadTimeoutMS > 10*60*1000 {
		return errors.New("runtime.skills.download_timeout_ms 必须位于 1000-600000 之间")
	}
	if skillConfig.MaxRedirects < 0 || skillConfig.MaxRedirects > 20 {
		return errors.New("runtime.skills.max_redirects 必须位于 0-20 之间")
	}

	mcpConfig := cfg.Runtime.MCP
	if mcpConfig.ConnectTimeoutMS < 1000 || mcpConfig.ConnectTimeoutMS > 120000 {
		return errors.New("runtime.mcp.connect_timeout_ms 必须位于 1000-120000 之间")
	}
	if mcpConfig.MaxToolsPerServer < 1 || mcpConfig.MaxToolsPerServer > 512 {
		return errors.New("runtime.mcp.max_tools_per_server 必须位于 1-512 之间")
	}
	if mcpConfig.MaxToolPages < 1 || mcpConfig.MaxToolPages > 100 {
		return errors.New("runtime.mcp.max_tool_pages 必须位于 1-100 之间")
	}
	if mcpConfig.MaxToolDescriptionChars < 256 || mcpConfig.MaxToolDescriptionChars > 65536 {
		return errors.New("runtime.mcp.max_tool_description_chars 必须位于 256-65536 之间")
	}
	if mcpConfig.MaxToolResultChars < 1024 || mcpConfig.MaxToolResultChars > 2*1024*1024 {
		return errors.New("runtime.mcp.max_tool_result_chars 必须位于 1024-2097152 之间")
	}
	if mcpConfig.PreserveToolResultTailChars < 0 ||
		mcpConfig.PreserveToolResultTailChars > mcpConfig.MaxToolResultChars {
		return errors.New("runtime.mcp.preserve_tool_result_tail_chars 必须位于 0-max_tool_result_chars 之间")
	}

	if err := ValidateSandboxConfig(cfg.Security.Sandbox); err != nil {
		return err
	}

	if err := ValidatePermissionConfig(cfg.Security.Permissions); err != nil {
		return err
	}

	if cfg.Security.MaxFileBytes <= 0 ||
		cfg.Security.MaxFileBytes > maxAllowedFileBytes {
		return fmt.Errorf(
			"security.max_file_bytes 必须位于 1-%d 之间",
			maxAllowedFileBytes,
		)
	}

	for _, command := range cfg.Security.ShellAllowedCommands {
		if filepath.Base(command) != command {
			return fmt.Errorf(
				"shell_allowed_commands 只能包含程序名称，不能包含路径: %q",
				command,
			)
		}

		if strings.ContainsAny(command, `/\`) {
			return fmt.Errorf(
				"shell_allowed_commands 包含路径分隔符: %q",
				command,
			)
		}
	}

	return nil
}
