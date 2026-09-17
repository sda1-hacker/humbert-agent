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
	defaultMaxReadableFileBytes int64 = 2 * 1024 * 1024
	defaultMaxWritableFileBytes int64 = 2 * 1024 * 1024
	defaultMaxReadOutputBytes         = 128 * 1024
	defaultReadLines                  = 200
	defaultMaxReadLines               = 1000
	defaultMaxListEntries             = 1000

	defaultWebSearchProvider       = "auto"
	defaultWebSearchTimeoutSeconds = 15
	defaultWebSearchResults        = 10
	defaultWebSearchMaxResults     = 20

	defaultWebFetchTimeoutSeconds = 15
	defaultWebFetchMaxBodyBytes   = 5 * 1024 * 1024
	defaultWebFetchMaxChars       = 12_000
	defaultWebFetchAbsoluteChars  = 50_000
	defaultWebFetchMaxRedirects   = 5

	defaultCommandTimeoutSeconds    = 120
	defaultCommandMaxTimeoutSeconds = 600
	defaultCommandMaxOutputBytes    = 128 * 1024
	defaultCommandMaxArgs           = 128
	defaultCommandMaxArgBytes       = 16 * 1024

	// 下面这些值是 Humbert 的绝对安全上限，而不是普通默认配置。
	// 即使用户通过 config.yaml 或环境变量设置更大的值，也不会允许突破。
	absoluteMaxReadableFileBytes int64 = 64 * 1024 * 1024
	absoluteMaxWritableFileBytes int64 = 64 * 1024 * 1024
	absoluteMaxReadOutputBytes         = 2 * 1024 * 1024
	absoluteMaxReadLines               = 10000
	absoluteMaxListEntries             = 10000

	absoluteMaxWebSearchTimeoutSeconds = 60
	absoluteMaxWebSearchResults        = 100

	absoluteMaxWebFetchTimeoutSeconds       = 60
	absoluteMaxWebFetchBodyBytes      int64 = 16 * 1024 * 1024
	absoluteMaxWebFetchChars                = 200_000
	absoluteMaxWebFetchRedirects            = 10

	absoluteMaxCommandTimeoutSeconds = 3600
	absoluteMaxCommandOutputBytes    = 2 * 1024 * 1024
	absoluteMaxCommandArgs           = 512
	absoluteMaxCommandArgBytes       = 64 * 1024
)

// ToolConfig 是 Humbert Tool Runtime 的集中配置。
//
// 本结构只描述“Tool 如何运行”，不保存动态业务数据，也不保存 API Key、Token
// 等敏感信息。所有配置都由 config package 使用 Viper 集中加载；Builtin Tool
// 不允许自行读取环境变量或 config.yaml。
//
// 当前配置分为四个领域：
//
//   - Files：Workspace 文件读取、写入与编辑限制；
//   - WebSearch：实时网页发现、Provider 策略与结果数量；
//   - WebFetch：读取指定公开 URL 的正文及 SSRF/体积限制；
//   - Command：本地命令执行开关、白名单和资源限制。
//
// Command.Enabled 和 AllowedCommands 继续读取既有的
// security.shell_enabled / security.shell_allowed_commands，以保持现有配置兼容。
type ToolConfig struct {
	Files     FileToolConfig
	WebSearch WebSearchToolConfig
	WebFetch  WebFetchToolConfig
	Command   CommandToolConfig
}

// FileToolConfig 是 Workspace Builtin File Tool 的配置。
type FileToolConfig struct {
	// Enabled 决定是否注册 list_files、read_file、write_file、edit_file。
	Enabled bool

	// MaxReadableFileBytes 是 read_file 允许读取的最大物理文件大小。
	MaxReadableFileBytes int64

	// MaxWritableFileBytes 是 write_file / edit_file 允许生成的最大文件大小。
	MaxWritableFileBytes int64

	// MaxReadOutputBytes 是 read_file 单次最多返回给模型的文本字节数。
	MaxReadOutputBytes int

	// DefaultReadLines 是没有显式 line_count 时的默认读取行数。
	DefaultReadLines int

	// MaxReadLines 是模型单次 read_file 可以请求的最大行数。
	MaxReadLines int

	// MaxListEntries 是 list_files 一次最多返回的目录项数量。
	MaxListEntries int
}

// WebSearchToolConfig 是 web_search 的运行参数。
//
// Provider 默认使用 auto。auto 会把“搜索 Provider 选择与fallback”
// 封装在一次 Tool 调用内部：优先使用已配置的 API Provider，然后使用
// AnySearch 匿名免费 API，最后才回退到 Bing / DuckDuckGo HTML 搜索。
//
// 这样模型无需为了同一个查询连续调用多个搜索工具，也不会因为某一个免费搜索页
// 临时返回低质量结果就立刻把错误摘要当成事实。
type WebSearchToolConfig struct {
	Enabled bool

	// Provider 支持 auto、anysearch_free、anysearch、tavily、brave、serper、bing、duckduckgo。
	Provider string

	TimeoutSeconds int

	DefaultResults int

	MaxResults int
}

// WebFetchToolConfig 是 web_fetch 的运行参数。
//
// web_fetch 专门负责“已经知道 URL 后读取原页面”。它与 web_search 职责分离：
// web_search 用于发现页面；web_fetch 用于读取并核对页面正文。对于排行榜、公告、
// 官方文档等需要精确原文的信息，这种两步策略比只使用搜索摘要可靠得多。
type WebFetchToolConfig struct {
	Enabled bool

	TimeoutSeconds int

	// MaxBodyBytes 是 HTTP 原始响应体允许读取的最大字节数。
	MaxBodyBytes int64

	// DefaultMaxChars 是模型没有指定 max_chars 时默认返回的文本字符预算。
	DefaultMaxChars int

	// MaxChars 是单次调用允许模型请求的最大字符数。
	MaxChars int

	// MaxRedirects 是逐跳校验 SSRF 后允许跟随的最大重定向次数。
	MaxRedirects int
}

// CommandToolConfig 是 run_command 的安全和资源配置。
//
// run_command 不执行 shell command string，而是通过 exec.CommandContext 直接
// 传递 executable + argv，因此不会解释 &&、|、$() 等 Shell 语法。
//
// 但本地程序依然具有真实系统权限，所以该能力默认关闭，只有用户显式设置
// security.shell_enabled=true 后才会注册。
type CommandToolConfig struct {
	Enabled bool

	AllowedCommands []string

	DefaultTimeoutSeconds int

	MaxTimeoutSeconds int

	MaxOutputBytes int

	MaxArgs int

	MaxArgBytes int
}

// LoadToolConfig 使用 Viper 加载 Tool 配置。
//
// configFile 通常是 ~/.humbert-agent/config.yaml。加载优先级为：
//
//   - 本 package 默认值；
//   - config.yaml；
//   - HUMBERT_* 环境变量。
//
// Builtin Tool 本身不会读取环境变量；所有配置读取都收口在这里。
func LoadToolConfig(configFile string) (ToolConfig, error) {
	v := viper.New()
	v.SetEnvPrefix("HUMBERT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	setToolDefaults(v)

	configFile = strings.TrimSpace(configFile)
	if configFile != "" {
		info, err := os.Stat(configFile)
		switch {
		case err == nil:
			if info.IsDir() {
				return ToolConfig{}, fmt.Errorf(
					"Tool 配置文件路径指向目录: %s",
					configFile,
				)
			}

			v.SetConfigFile(configFile)
			if err := v.ReadInConfig(); err != nil {
				return ToolConfig{}, fmt.Errorf(
					"读取 Tool 配置失败: %w",
					err,
				)
			}

		case errors.Is(err, os.ErrNotExist):
			// 首次启动时 config.yaml 可以尚不存在，此时只使用默认值和环境变量。

		default:
			return ToolConfig{}, fmt.Errorf(
				"检查 Tool 配置文件失败: %w",
				err,
			)
		}
	}

	result := ToolConfig{
		Files: FileToolConfig{
			Enabled: v.GetBool(
				"tools.files.enabled",
			),
			MaxReadableFileBytes: v.GetInt64(
				"tools.files.max_readable_file_bytes",
			),
			MaxWritableFileBytes: v.GetInt64(
				"tools.files.max_writable_file_bytes",
			),
			MaxReadOutputBytes: v.GetInt(
				"tools.files.max_read_output_bytes",
			),
			DefaultReadLines: v.GetInt(
				"tools.files.default_read_lines",
			),
			MaxReadLines: v.GetInt(
				"tools.files.max_read_lines",
			),
			MaxListEntries: v.GetInt(
				"tools.files.max_list_entries",
			),
		},
		WebSearch: WebSearchToolConfig{
			Enabled: v.GetBool(
				"tools.web_search.enabled",
			),
			Provider: strings.ToLower(
				strings.TrimSpace(
					v.GetString(
						"tools.web_search.provider",
					),
				),
			),
			TimeoutSeconds: v.GetInt(
				"tools.web_search.timeout_seconds",
			),
			DefaultResults: v.GetInt(
				"tools.web_search.default_results",
			),
			MaxResults: v.GetInt(
				"tools.web_search.max_results",
			),
		},
		WebFetch: WebFetchToolConfig{
			Enabled: v.GetBool(
				"tools.web_fetch.enabled",
			),
			TimeoutSeconds: v.GetInt(
				"tools.web_fetch.timeout_seconds",
			),
			MaxBodyBytes: v.GetInt64(
				"tools.web_fetch.max_body_bytes",
			),
			DefaultMaxChars: v.GetInt(
				"tools.web_fetch.default_max_chars",
			),
			MaxChars: v.GetInt(
				"tools.web_fetch.max_chars",
			),
			MaxRedirects: v.GetInt(
				"tools.web_fetch.max_redirects",
			),
		},
		Command: CommandToolConfig{
			Enabled: v.GetBool(
				"security.shell_enabled",
			),
			AllowedCommands: normalizeToolCommands(
				v.GetStringSlice(
					"security.shell_allowed_commands",
				),
			),
			DefaultTimeoutSeconds: v.GetInt(
				"tools.command.default_timeout_seconds",
			),
			MaxTimeoutSeconds: v.GetInt(
				"tools.command.max_timeout_seconds",
			),
			MaxOutputBytes: v.GetInt(
				"tools.command.max_output_bytes",
			),
			MaxArgs: v.GetInt(
				"tools.command.max_args",
			),
			MaxArgBytes: v.GetInt(
				"tools.command.max_arg_bytes",
			),
		},
	}

	if err := result.Validate(); err != nil {
		return ToolConfig{}, fmt.Errorf(
			"Tool 配置无效: %w",
			err,
		)
	}

	return result, nil
}

// Validate 校验整个 Tool 配置。
func (c ToolConfig) Validate() error {
	if err := c.Files.Validate(); err != nil {
		return err
	}

	if err := c.WebSearch.Validate(); err != nil {
		return err
	}

	if err := c.WebFetch.Validate(); err != nil {
		return err
	}

	if err := c.Command.Validate(); err != nil {
		return err
	}

	return nil
}

// Validate 校验 File Tool 配置。
func (c FileToolConfig) Validate() error {
	if c.MaxReadableFileBytes <= 0 {
		return errors.New(
			"tools.files.max_readable_file_bytes 必须大于 0",
		)
	}

	if c.MaxReadableFileBytes >
		absoluteMaxReadableFileBytes {
		return fmt.Errorf(
			"tools.files.max_readable_file_bytes 不能超过 %d",
			absoluteMaxReadableFileBytes,
		)
	}

	if c.MaxWritableFileBytes <= 0 {
		return errors.New(
			"tools.files.max_writable_file_bytes 必须大于 0",
		)
	}

	if c.MaxWritableFileBytes >
		absoluteMaxWritableFileBytes {
		return fmt.Errorf(
			"tools.files.max_writable_file_bytes 不能超过 %d",
			absoluteMaxWritableFileBytes,
		)
	}

	if c.MaxReadOutputBytes <= 0 {
		return errors.New(
			"tools.files.max_read_output_bytes 必须大于 0",
		)
	}

	if c.MaxReadOutputBytes >
		absoluteMaxReadOutputBytes {
		return fmt.Errorf(
			"tools.files.max_read_output_bytes 不能超过 %d",
			absoluteMaxReadOutputBytes,
		)
	}

	if int64(
		c.MaxReadOutputBytes,
	) > c.MaxReadableFileBytes {
		return errors.New(
			"tools.files.max_read_output_bytes 不能大于 max_readable_file_bytes",
		)
	}

	if c.DefaultReadLines <= 0 {
		return errors.New(
			"tools.files.default_read_lines 必须大于 0",
		)
	}

	if c.MaxReadLines <= 0 {
		return errors.New(
			"tools.files.max_read_lines 必须大于 0",
		)
	}

	if c.MaxReadLines >
		absoluteMaxReadLines {
		return fmt.Errorf(
			"tools.files.max_read_lines 不能超过 %d",
			absoluteMaxReadLines,
		)
	}

	if c.DefaultReadLines >
		c.MaxReadLines {
		return errors.New(
			"tools.files.default_read_lines 不能大于 max_read_lines",
		)
	}

	if c.MaxListEntries <= 0 {
		return errors.New(
			"tools.files.max_list_entries 必须大于 0",
		)
	}

	if c.MaxListEntries >
		absoluteMaxListEntries {
		return fmt.Errorf(
			"tools.files.max_list_entries 不能超过 %d",
			absoluteMaxListEntries,
		)
	}

	return nil
}

// Validate 校验 Web Search 配置。
func (c WebSearchToolConfig) Validate() error {
	provider :=
		strings.ToLower(
			strings.TrimSpace(
				c.Provider,
			),
		)

	switch provider {
	case "auto",
		"anysearch_free",
		"anysearch",
		"tavily",
		"brave",
		"serper",
		"bing",
		"duckduckgo":
		// 合法 Provider。

	default:
		return fmt.Errorf(
			"tools.web_search.provider 不支持: %q",
			c.Provider,
		)
	}

	if c.TimeoutSeconds <= 0 ||
		c.TimeoutSeconds >
			absoluteMaxWebSearchTimeoutSeconds {
		return fmt.Errorf(
			"tools.web_search.timeout_seconds 必须位于 1-%d 之间",
			absoluteMaxWebSearchTimeoutSeconds,
		)
	}

	if c.DefaultResults <= 0 ||
		c.DefaultResults >
			absoluteMaxWebSearchResults {
		return fmt.Errorf(
			"tools.web_search.default_results 必须位于 1-%d 之间",
			absoluteMaxWebSearchResults,
		)
	}

	if c.MaxResults <= 0 ||
		c.MaxResults >
			absoluteMaxWebSearchResults {
		return fmt.Errorf(
			"tools.web_search.max_results 必须位于 1-%d 之间",
			absoluteMaxWebSearchResults,
		)
	}

	if c.DefaultResults >
		c.MaxResults {
		return errors.New(
			"tools.web_search.default_results 不能大于 max_results",
		)
	}

	return nil
}

// Validate 校验 Web Fetch 配置。
func (c WebFetchToolConfig) Validate() error {
	if c.TimeoutSeconds <= 0 ||
		c.TimeoutSeconds >
			absoluteMaxWebFetchTimeoutSeconds {
		return fmt.Errorf(
			"tools.web_fetch.timeout_seconds 必须位于 1-%d 之间",
			absoluteMaxWebFetchTimeoutSeconds,
		)
	}

	if c.MaxBodyBytes <= 0 ||
		c.MaxBodyBytes >
			absoluteMaxWebFetchBodyBytes {
		return fmt.Errorf(
			"tools.web_fetch.max_body_bytes 必须位于 1-%d 之间",
			absoluteMaxWebFetchBodyBytes,
		)
	}

	if c.DefaultMaxChars <= 0 ||
		c.DefaultMaxChars >
			absoluteMaxWebFetchChars {
		return fmt.Errorf(
			"tools.web_fetch.default_max_chars 必须位于 1-%d 之间",
			absoluteMaxWebFetchChars,
		)
	}

	if c.MaxChars <= 0 ||
		c.MaxChars >
			absoluteMaxWebFetchChars {
		return fmt.Errorf(
			"tools.web_fetch.max_chars 必须位于 1-%d 之间",
			absoluteMaxWebFetchChars,
		)
	}

	if c.DefaultMaxChars >
		c.MaxChars {
		return errors.New(
			"tools.web_fetch.default_max_chars 不能大于 max_chars",
		)
	}

	if c.MaxRedirects < 0 ||
		c.MaxRedirects >
			absoluteMaxWebFetchRedirects {
		return fmt.Errorf(
			"tools.web_fetch.max_redirects 必须位于 0-%d 之间",
			absoluteMaxWebFetchRedirects,
		)
	}

	return nil
}

// Validate 校验本地命令执行配置。
func (c CommandToolConfig) Validate() error {
	if c.DefaultTimeoutSeconds <= 0 {
		return errors.New(
			"tools.command.default_timeout_seconds 必须大于 0",
		)
	}

	if c.MaxTimeoutSeconds <= 0 ||
		c.MaxTimeoutSeconds >
			absoluteMaxCommandTimeoutSeconds {
		return fmt.Errorf(
			"tools.command.max_timeout_seconds 必须位于 1-%d 之间",
			absoluteMaxCommandTimeoutSeconds,
		)
	}

	if c.DefaultTimeoutSeconds >
		c.MaxTimeoutSeconds {
		return errors.New(
			"tools.command.default_timeout_seconds 不能大于 max_timeout_seconds",
		)
	}

	if c.MaxOutputBytes <= 0 ||
		c.MaxOutputBytes >
			absoluteMaxCommandOutputBytes {
		return fmt.Errorf(
			"tools.command.max_output_bytes 必须位于 1-%d 之间",
			absoluteMaxCommandOutputBytes,
		)
	}

	if c.MaxArgs <= 0 ||
		c.MaxArgs >
			absoluteMaxCommandArgs {
		return fmt.Errorf(
			"tools.command.max_args 必须位于 1-%d 之间",
			absoluteMaxCommandArgs,
		)
	}

	if c.MaxArgBytes <= 0 ||
		c.MaxArgBytes >
			absoluteMaxCommandArgBytes {
		return fmt.Errorf(
			"tools.command.max_arg_bytes 必须位于 1-%d 之间",
			absoluteMaxCommandArgBytes,
		)
	}

	for _, command := range c.AllowedCommands {
		if filepath.Base(
			command,
		) != command ||
			strings.ContainsAny(
				command,
				`/\`,
			) {
			return fmt.Errorf(
				"security.shell_allowed_commands 只能包含程序名称，不能包含路径: %q",
				command,
			)
		}
	}

	if c.Enabled &&
		len(c.AllowedCommands) == 0 {
		return errors.New(
			"security.shell_enabled=true 时 shell_allowed_commands 不能为空",
		)
	}

	return nil
}

// SafeCommandEnvironment 返回允许传递给 Agent 本地子进程的环境变量白名单。
//
// 该函数位于 config package，是因为读取宿主环境属于 Application Configuration
// Boundary。Builtin Tool 不允许自行调用 os.Getenv/os.Environ。
func SafeCommandEnvironment() []string {
	keys := []string{
		"PATH",
		"HOME",
		"TMPDIR",
		"LANG",
		"LC_ALL",
		"LC_CTYPE",
		"GOROOT",
		"GOPATH",
		"GOCACHE",
		"GOMODCACHE",
	}

	if runtime.GOOS == "windows" {
		keys = append(
			keys,
			"USERPROFILE",
			"SYSTEMROOT",
			"WINDIR",
			"PATHEXT",
			"TEMP",
			"TMP",
		)
	}

	result :=
		make(
			[]string,
			0,
			len(keys),
		)

	seen :=
		make(
			map[string]struct{},
			len(keys),
		)

	for _, key := range keys {
		lookupKey := key

		if runtime.GOOS == "windows" {
			lookupKey =
				strings.ToUpper(key)
		}

		if _, exists :=
			seen[lookupKey]; exists {
			continue
		}

		value, ok :=
			os.LookupEnv(key)

		if !ok ||
			strings.ContainsRune(
				value,
				'\x00',
			) {
			continue
		}

		seen[lookupKey] =
			struct{}{}

		result =
			append(
				result,
				key+"="+value,
			)
	}

	return result
}

func normalizeToolCommands(
	commands []string,
) []string {
	result :=
		make(
			[]string,
			0,
			len(commands),
		)

	seen :=
		make(
			map[string]struct{},
			len(commands),
		)

	for _, command := range commands {
		command =
			strings.TrimSpace(
				command,
			)

		if command == "" {
			continue
		}

		if runtime.GOOS == "windows" {
			command =
				strings.ToLower(
					command,
				)
		}

		if _, exists :=
			seen[command]; exists {
			continue
		}

		seen[command] =
			struct{}{}

		result =
			append(
				result,
				command,
			)
	}

	return result
}

// setToolDefaults 统一声明 Tool 配置默认值。
func setToolDefaults(
	v *viper.Viper,
) {
	v.SetDefault(
		"tools.files.enabled",
		true,
	)

	v.SetDefault(
		"tools.files.max_readable_file_bytes",
		defaultMaxReadableFileBytes,
	)

	v.SetDefault(
		"tools.files.max_writable_file_bytes",
		defaultMaxWritableFileBytes,
	)

	v.SetDefault(
		"tools.files.max_read_output_bytes",
		defaultMaxReadOutputBytes,
	)

	v.SetDefault(
		"tools.files.default_read_lines",
		defaultReadLines,
	)

	v.SetDefault(
		"tools.files.max_read_lines",
		defaultMaxReadLines,
	)

	v.SetDefault(
		"tools.files.max_list_entries",
		defaultMaxListEntries,
	)

	v.SetDefault(
		"tools.web_search.enabled",
		true,
	)

	v.SetDefault(
		"tools.web_search.provider",
		defaultWebSearchProvider,
	)

	v.SetDefault(
		"tools.web_search.timeout_seconds",
		defaultWebSearchTimeoutSeconds,
	)

	v.SetDefault(
		"tools.web_search.default_results",
		defaultWebSearchResults,
	)

	v.SetDefault(
		"tools.web_search.max_results",
		defaultWebSearchMaxResults,
	)

	v.SetDefault(
		"tools.web_fetch.enabled",
		true,
	)

	v.SetDefault(
		"tools.web_fetch.timeout_seconds",
		defaultWebFetchTimeoutSeconds,
	)

	v.SetDefault(
		"tools.web_fetch.max_body_bytes",
		defaultWebFetchMaxBodyBytes,
	)

	v.SetDefault(
		"tools.web_fetch.default_max_chars",
		defaultWebFetchMaxChars,
	)

	v.SetDefault(
		"tools.web_fetch.max_chars",
		defaultWebFetchAbsoluteChars,
	)

	v.SetDefault(
		"tools.web_fetch.max_redirects",
		defaultWebFetchMaxRedirects,
	)

	v.SetDefault(
		"security.shell_enabled",
		false,
	)

	v.SetDefault(
		"security.shell_allowed_commands",
		[]string{
			"go",
			"git",
			"node",
			"npm",
			"npx",
			"python",
			"python3",
		},
	)

	v.SetDefault(
		"tools.command.default_timeout_seconds",
		defaultCommandTimeoutSeconds,
	)

	v.SetDefault(
		"tools.command.max_timeout_seconds",
		defaultCommandMaxTimeoutSeconds,
	)

	v.SetDefault(
		"tools.command.max_output_bytes",
		defaultCommandMaxOutputBytes,
	)

	v.SetDefault(
		"tools.command.max_args",
		defaultCommandMaxArgs,
	)

	v.SetDefault(
		"tools.command.max_arg_bytes",
		defaultCommandMaxArgBytes,
	)
}
