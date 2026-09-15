package skills

import (
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/sda1-hacker/humbert-agent/internal/config"
)

const (
	// SkillDefinitionFileName 是一个 Skill Package 的入口文件。Humbert 遵循 Agent Skills
	// 常见约定：每个 Skill 一个目录，目录根部必须存在 SKILL.md。
	SkillDefinitionFileName = "SKILL.md"

	// SkillToolName 是注入 Eino ChatModelAgent 的渐进式披露工具名。它不进入 Humbert
	// 全局 Tool Registry，因为它只读取当前 Agent 明确启用的本地 Skill Snapshot，且不会
	// 产生文件写入/命令执行等副作用；真正的写入与执行仍必须经过 Permission + Approval。
	SkillToolName = "skill"
)

// RuntimeStatus 描述 Skill Package 是否能被当前 Humbert + Eino Runtime 激活。
// 安装成功与运行兼容是两个阶段：Unsupported Skill 仍可以保留、查看、更新，
// 只是不能加入 Agent.enabled_skills。
type RuntimeStatus string

const (
	RuntimeStatusReady       RuntimeStatus = "ready"
	RuntimeStatusNeedsSetup  RuntimeStatus = "needs_setup"
	RuntimeStatusUnsupported RuntimeStatus = "unsupported"
)

// SpecStatus 描述 Package 与开放 Agent Skills 规范的兼容程度。
//
// Humbert 早期版本允许下划线名称与更长 description。为避免升级后让现有 Agent Profile
// 失效，这类包仍可读取，但会标记为 legacy；新安装的标准社区 Skill 通常应为 standard。
type SpecStatus string

const (
	SpecStatusStandard SpecStatus = "standard"
	SpecStatusLegacy   SpecStatus = "legacy"
)

// DiagnosticLevel 是 Skill 兼容诊断的严重级别。
type DiagnosticLevel string

const (
	DiagnosticInfo    DiagnosticLevel = "info"
	DiagnosticWarning DiagnosticLevel = "warning"
	DiagnosticError   DiagnosticLevel = "error"
)

// Diagnostic 是安装后可重复计算的 Skill 环境/兼容提示。
//
// Diagnostic 只做说明和启用前判断，不会自动安装系统依赖、修改 PATH、下载运行时或绕过
// Permission/Sandbox。
type Diagnostic struct {
	Code string `json:"code"`

	Level DiagnosticLevel `json:"level"`

	Message string `json:"message"`
}

// ScriptRuntime 描述 scripts/ 下一个脚本的宿主运行时探测结果。
// Available 只表示 Humbert 进程当前 PATH 能找到对应解释器，不代表脚本依赖已经齐全。
type ScriptRuntime struct {
	Path string `json:"path"`

	Language string `json:"language,omitempty"`

	Command string `json:"command,omitempty"`

	Available bool `json:"available"`

	Supported bool `json:"supported"`

	Message string `json:"message,omitempty"`
}

// DiscoveryCandidate 是从本地仓库或远程归档中发现的一个 Skill Package。
// Path 使用来源根目录内的 `/` 分隔相对路径；单 Skill 根目录用 "."。
type DiscoveryCandidate struct {
	Path string `json:"path"`

	Info Info `json:"info"`

	Installed bool `json:"installed"`
}

// DiscoveryResult 是一次 Skill Source 扫描结果。扫描不会修改 Skills Root。
type DiscoveryResult struct {
	SourceKind string `json:"sourceKind"`

	Provider string `json:"provider,omitempty"`

	DisplaySource string `json:"displaySource,omitempty"`

	Candidates []DiscoveryCandidate `json:"candidates"`
}

// FileInfo 是安装时冻结的 Skill 包文件身份。
//
// SHA256 不用于安全认证，而用于保证 Runtime Snapshot 一致性：如果用户在 Agent 正在运行
// 时修改了 references/foo.md，当前 Turn 再读取该文件会得到 ErrSkillSnapshotStale，而不是
// 静默读取与模型启动时不同的内容。下一 Turn 会重新扫描并获得新 Snapshot。
type FileInfo struct {
	Path string `json:"path"`

	SizeBytes int64 `json:"sizeBytes"`

	SHA256 string `json:"sha256"`

	Text bool `json:"text"`
}

// Info 是 Skill 设置页与 Agent Skill Selector 需要的稳定元数据。
//
// Valid=false 的条目通常来自用户手工复制到 ~/.humbert-agent/skills 的非法目录。List 不会
// 因一个坏包让整个设置页失败，而是把错误作为条目展示；Runtime 只允许 Valid Skill 被启用。
type Info struct {
	Name string `json:"name"`

	Description string `json:"description"`

	SpecStatus SpecStatus `json:"specStatus"`

	SpecMessage string `json:"specMessage,omitempty"`

	License string `json:"license,omitempty"`

	Compatibility string `json:"compatibility,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	AllowedTools string `json:"allowedTools,omitempty"`

	// EinoContext/EinoAgent/EinoModel 是社区 Skill 可声明的 Eino 扩展。它们不会在
	// 安装阶段被拒绝；RuntimeStatus 决定当前 Humbert 是否允许激活。
	EinoContext string `json:"einoContext,omitempty"`

	EinoAgent string `json:"einoAgent,omitempty"`

	EinoModel string `json:"einoModel,omitempty"`

	RuntimeStatus RuntimeStatus `json:"runtimeStatus"`

	RuntimeMessage string `json:"runtimeMessage,omitempty"`

	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`

	ScriptRuntimes []ScriptRuntime `json:"scriptRuntimes,omitempty"`

	DirectoryName string `json:"directoryName"`

	RootDir string `json:"rootDir"`

	Identity string `json:"identity"`

	Valid bool `json:"valid"`

	Error string `json:"error,omitempty"`

	FileCount int `json:"fileCount"`

	SizeBytes int64 `json:"sizeBytes"`

	HasReferences bool `json:"hasReferences"`

	HasScripts bool `json:"hasScripts"`

	HasAssets bool `json:"hasAssets"`

	UpdatedAt time.Time `json:"updatedAt"`
}

// Package 是一个已经完整验证的 Skill Package。
//
// Content 只保存 SKILL.md frontmatter 之后的正文。包内 reference/script 等文件不在这里
// 预加载，避免每个 Turn 都复制数 MB 资源；Snapshot 只冻结它们的 FileInfo 哈希，真正读取
// 时按哈希复核。
type Package struct {
	Info Info

	Content string

	Files []FileInfo
}

// RuntimeSnapshot 是一次 Agent Turn 冻结的 Skill 能力集合。
//
// Snapshot 的生命周期与 runtime.Snapshot 相同。创建后不再读取 Agent Profile；即使用户在
// Settings 中修改启用 Skill，也只影响下一 Turn。SKILL.md 正文直接保存在内存 Backend，
// references/scripts 则通过文件哈希保证“本 Turn 不漂移”。
type RuntimeSnapshot struct {
	Revision string

	Names []string

	Instruction string

	ToolDescription string

	packages map[string]Package

	toolDefinition einotool.BaseTool

	middleware adk.ChatModelAgentMiddleware

	config config.SkillConfig
}

// Enabled 表示当前 Turn 是否真正启用了至少一个 Skill。
func (s RuntimeSnapshot) Enabled() bool {
	return len(s.Names) > 0 && s.middleware != nil
}

// Middleware 返回已经绑定当前 Snapshot Backend 的 Eino Skill Middleware。
//
// 返回值只允许追加到当前 Runtime Snapshot 的 Handlers，不应该缓存到 Application 级别，
// 否则后续 Agent Skill 配置变化会污染正在执行或未来 Turn。
func (s RuntimeSnapshot) Middleware() adk.ChatModelAgentMiddleware {
	return s.middleware
}

// ToolDefinition 返回与 Eino Middleware 实际注入的 skill Tool 等价的只读 ToolInfo。
// ContextEngine 使用它估算 Tool Schema Token；它本身不可执行。
func (s RuntimeSnapshot) ToolDefinition() einotool.BaseTool {
	return s.toolDefinition
}

// PackageNames 返回 Snapshot 中的 Skill 名称副本，避免调用方修改内部 slice。
func (s RuntimeSnapshot) PackageNames() []string {
	return append([]string(nil), s.Names...)
}

// PackageIdentities 返回当前 Turn 冻结的 Skill Name -> Package Identity 副本。
// Permission v2 使用它把 run_skill_script 的可复用授权绑定到具体 Skill 内容，而不是只绑定
// Tool 名称。调用方得到的是新 map，不能修改 RuntimeSnapshot 内部 packages。
func (s RuntimeSnapshot) PackageIdentities() map[string]string {
	result := make(map[string]string, len(s.packages))
	for name, pkg := range s.packages {
		result[name] = pkg.Info.Identity
	}
	return result
}

// ScriptRuntimeCommands 返回当前 Turn 冻结的 Skill Script -> Interpreter Command 映射。
// Permission v2 用它把长期脚本授权绑定到解释器身份；只暴露已经被 Skill 诊断识别且支持的脚本。
func (s RuntimeSnapshot) ScriptRuntimeCommands() map[string]map[string]string {
	result := make(map[string]map[string]string, len(s.packages))
	for name, pkg := range s.packages {
		commands := make(map[string]string)
		for _, item := range pkg.Info.ScriptRuntimes {
			if !item.Supported || strings.TrimSpace(item.Command) == "" || strings.TrimSpace(item.Path) == "" {
				continue
			}
			commands[strings.TrimSpace(strings.ReplaceAll(item.Path, "\\", "/"))] = strings.TrimSpace(item.Command)
		}
		result[name] = commands
	}
	return result
}
