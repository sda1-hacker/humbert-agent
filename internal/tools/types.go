package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// RiskLevel 复用 Permission Domain 的风险枚举。
//
// Tool Registry 只负责为 Capability 标注风险；最终 Allow/Deny/Ask 由 permission.Engine
// 结合 Agent、Session、参数条件和用户规则计算。使用类型别名避免 Tool 与 Permission 各维护
// 一套可能漂移的枚举。
type RiskLevel = permission.RiskLevel

const (
	RiskRead  = permission.RiskRead
	RiskWrite = permission.RiskWrite
	RiskExec  = permission.RiskExec
)

// Descriptor 是 Humbert 对 Tool 的稳定描述。
//
// Eino 的 schema.ToolInfo 负责告诉模型：
//
//   - Tool Name；
//   - Description；
//   - JSON Schema。
//
// Descriptor 则负责 Humbert 自己的：
//
//   - Registry 身份；
//   - Permission Risk。
//
// 两者职责不同，不应该混成同一个结构。
type Descriptor struct {
	Name string

	Risk RiskLevel

	// MCPOrigin 仅在 Tool 来自 MCP Server 时提供。它不会暴露给模型；Guard 会把
	// ServerID/Fingerprint 传给 PermissionEngine，用于 Approval 展示和长期 Rule 绑定。
	MCPOrigin *MCPOrigin

	// Internal 表示 Humbert 为运行时可靠性提供的内部只读能力（例如历史检索）。
	// Internal Tool 不受 Agent 的 enabled_builtin_tools 选择影响，也不在普通能力设置中展示。
	Internal bool
}

// MCPOrigin 描述一个 MCP Tool 的稳定来源身份。
//
// RawToolName 是 Server 原始 Tool 名；ServerFingerprint 来自会改变连接安全身份的配置，
// 因此 endpoint/command/credential 引用变化后旧长期授权不会继续命中。
type MCPOrigin struct {
	ServerID          string
	ServerName        string
	ServerFingerprint string
	RawToolName       string
}

// Validate 校验 Tool Descriptor。
func (d Descriptor) Validate() error {
	name :=
		strings.TrimSpace(
			d.Name,
		)

	if name == "" {
		return fmt.Errorf(
			"%w: Tool Name 不能为空",
			ErrInvalidTool,
		)
	}

	if len(name) > 64 {
		return fmt.Errorf(
			"%w: Tool Name 长度不能超过 64",
			ErrInvalidTool,
		)
	}

	// Tool 名称采用稳定 snake_case。
	//
	// 这样将来：
	//
	//	read_file
	//	mcp.github.search_issues
	//
	// 等命名空间规则可以统一扩展。
	//
	// 当前 Builtin Tool 只允许简单 snake_case；
	// MCP Namespace 后续会使用单独的注册校验规则扩展。
	for index, char := range name {

		valid :=
			(char >= 'a' &&
				char <= 'z') ||
				(char >= '0' &&
					char <= '9') ||
				char == '_'

		if !valid {
			return fmt.Errorf(
				"%w: Tool Name %q 包含非法字符 %q",
				ErrInvalidTool,
				name,
				char,
			)
		}

		if index == 0 &&
			char >= '0' &&
			char <= '9' {

			return fmt.Errorf(
				"%w: Tool Name 不能以数字开头",
				ErrInvalidTool,
			)
		}
	}

	switch d.Risk {
	case RiskRead, RiskWrite, RiskExec:
	default:
		return fmt.Errorf(
			"%w: Tool %q RiskLevel %q 不合法",
			ErrInvalidTool,
			name,
			d.Risk,
		)
	}

	if d.MCPOrigin != nil {
		origin := d.MCPOrigin
		if strings.TrimSpace(origin.ServerID) == "" ||
			strings.TrimSpace(origin.ServerFingerprint) == "" ||
			strings.TrimSpace(origin.RawToolName) == "" {
			return fmt.Errorf("%w: MCP Tool %q 缺少 ServerID/Fingerprint/RawToolName", ErrInvalidTool, name)
		}
	}
	return nil
}

// Scope 描述一次 ToolSet Resolve 所属的不可变 Runtime 范围。
//
// ToolFactory 会捕获这个 Scope，因此创建出来的 Tool 与当前
// RuntimeSnapshot 绑定。
//
// 当前 Turn 运行期间即使用户切换 Agent Workspace，已经 Resolve
// 出来的 Tool 也不会改变工作目录。
type Scope struct {
	RequestID string

	RunID string

	SessionID string

	AgentID string

	Workspace workspace.Workspace

	// Sandbox 是本 Turn 冻结的强制安全边界。真实 Runtime 由 Sandbox Manager 注入；
	// SandboxPolicy() 对纯 Tool 测试/局部作用域提供安全的 Workspace-only 默认值。
	Sandbox sandbox.EffectivePolicy

	// EnabledBuiltinTools nil 表示升级前 Agent，使用 Registry 当前全部 Builtin；
	// 非 nil（包括空 slice）表示 Agent 的显式选择。
	EnabledBuiltinTools []string

	// DisabledBuiltinTools 是当前 Runtime 在 Agent Profile 选择之上施加的能力上限。
	// 子 Agent 使用它移除会读取父会话、修改 Agent 配置或继续递归创建子 Agent 的工具。
	DisabledBuiltinTools []string

	// EnabledMCPTools 冻结 Agent 在本 Turn 选择的 ServerID -> raw tool names。
	// 使用基础类型避免 Tool Domain 反向依赖 MCP Domain。
	EnabledMCPTools map[string][]string

	// EnabledSkills/SkillRevision 是本 Turn 已冻结的 Skill 身份摘要。只有依赖 Skill
	// Snapshot 的 Builtin（例如 run_skill_script）会读取它们；普通 Tool 不需要知道 Skill。
	EnabledSkills []string
	SkillRevision string

	// SkillIdentities 是当前 Turn 冻结的 Skill Name -> Package Identity。Permission v2
	// 用它绑定 run_skill_script 的长期授权；它必须与 SkillRevision 来自同一 Snapshot。
	SkillIdentities map[string]string

	// SkillScriptCommands 是当前 Turn 冻结的 Skill -> Script -> Interpreter Command。
	// 与 SkillIdentities 一起用于构建 run_skill_script 的 Permission CapabilityIdentity。
	SkillScriptCommands map[string]map[string]string

	// ToolResultMaxChars 是当前模型工作窗口允许单个 ToolResult 直接进入上下文的字符上限。
	// 超过上限的完整结果由 ResultArchiver 保存，模型只收到首尾摘录和引用编号。
	ToolResultMaxChars int

	// ToolResultBudget is shared by every tool in this turn, including MCP tools.
	// It bounds the total result content kept directly in model context.
	ToolResultBudget *ResultBudget
}

// SandboxPolicy 返回当前 Scope 的有效 Sandbox；未显式注入时使用安全的 Workspace-only 默认值。
func (s Scope) SandboxPolicy() sandbox.EffectivePolicy {
	if strings.TrimSpace(s.Sandbox.WorkspaceRoot) != "" {
		return s.Sandbox
	}
	return sandbox.WorkspaceOnlyPolicy(s.Workspace.RootDir)
}

// Validate 校验 Runtime Tool Scope。
func (s Scope) Validate() error {
	if strings.TrimSpace(
		s.AgentID,
	) == "" {

		return errors.New(
			"Tool Scope AgentID 不能为空",
		)
	}

	if strings.TrimSpace(
		s.Workspace.RootDir,
	) == "" {

		return errors.New(
			"Tool Scope Workspace RootDir 不能为空",
		)
	}

	if s.Workspace.AgentID != "" &&
		s.Workspace.AgentID !=
			s.AgentID {

		return errors.New(
			"Tool Scope AgentID 与 Workspace AgentID 不一致",
		)
	}

	return nil
}

// Factory 根据当前不可变 Runtime Scope 创建一个 Tool Instance。
//
// Factory 本身应该保持无状态或只持有线程安全的长期依赖，例如：
//
//	WorkspaceManager
//	HTTP Client
//
// 真正与当前 Turn 相关的数据必须来自 Scope。
type Factory interface {
	// Descriptor 返回 Tool 的 Humbert Registry 描述。
	Descriptor() Descriptor

	// Build 为当前 Runtime Snapshot 创建可执行 Tool。
	Build(
		ctx context.Context,
		scope Scope,
	) (
		einotool.InvokableTool,
		error,
	)
}

// ResultArchiver 是 Tool 层与 Session Context Artifact 存储的最小边界。
type ResultArchiver interface {
	Archive(ctx context.Context, sessionID string, toolName string, content string) (string, error)
}

// ResolvedTools 是一次 RuntimeSnapshot 真正冻结的 ToolSet。
type ResolvedTools struct {
	// Tools 可以直接交给 Eino ChatModelAgent ToolsConfig。
	Tools []einotool.BaseTool

	// Descriptors/ToolNames 是本 Turn 真正暴露的 Builtin Capability Audit Snapshot。
	Descriptors []Descriptor
	ToolNames   []string

	// Revision 是 Resolve 时 Registry 的配置版本。
	//
	// 当前 Turn 创建以后，即使 Registry revision 继续增加，
	// 本对象中的 Tools 和 Revision 都保持不变。
	Revision uint64
}
