package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// SkillSource 是 Runtime 消费的最小能力接口；无需依赖具体的安装器、Store 或 Manager。
// Snapshot 仍保留 Eino Skill middleware 与脚本身份，替换实现不能丢失这两者的一致性。
type SkillSource interface {
	ResolveRuntimeSnapshot(context.Context, []string) (skills.RuntimeSnapshot, error)
}

// MCPSource 区分只读 Schema 预览与真实连接。预览不得调用远程工具或初始化服务。
// Available 保留单个 Server 不可用时的降级规则和现有审计字段。
type MCPSource interface {
	ResolveRuntimeSnapshotBestEffort(context.Context, []humbertmcp.ToolSelection, humberttools.Scope) (humbertmcp.RuntimeSnapshot, error)
	ResolveRuntimeSnapshotAvailable(context.Context, []humbertmcp.ToolSelection, humberttools.Scope) (humbertmcp.RuntimeSnapshot, error)
}

// capabilitySet 是本轮冻结的能力装配结果。主/子 Agent 和 Context 查询共用它，
// 模型实际使用的工具与用于 Token 估算的 Schema 分开，保留 Skills 渐进披露语义。
type capabilitySet struct {
	tools       humberttools.ResolvedTools
	skills      skills.RuntimeSnapshot
	mcp         humbertmcp.RuntimeSnapshot
	scope       humberttools.Scope
	descriptors []humberttools.Descriptor
	executable  []einotool.BaseTool
	schemas     []einotool.BaseTool
	summaries   []CapabilitySummary
}

// capabilityAssembler 只协调已有能力来源，不重写它们的安装、连接或授权规则。
// Skills 必须先冻结，run_skill_script 和 Skill middleware 才能共用相同内容身份。
type capabilityAssembler struct {
	tools      *humberttools.Registry
	skills     SkillSource
	mcp        MCPSource
	extensions extensionRegistry
}

func (a *capabilityAssembler) validate() error {
	if a == nil || a.tools == nil {
		return errors.New("RuntimeResolver ToolRegistry 不能为空")
	}
	return nil
}

// resolve 在相同的已验证 Scope 内组装能力；preview 只使用 MCP 本地 Schema 投影。
func (a *capabilityAssembler) resolve(ctx context.Context, profile agents.Agent, toolScope humberttools.Scope, bestEffortMCP bool) (capabilitySet, error) {
	// Skill Snapshot 先于 Builtin Tool Resolve 冻结。run_skill_script 依赖当前 Turn 的
	// enabled_skills + revision，必须与 Eino Skill Middleware 使用同一份内容身份。
	skillSnapshot, err := a.resolveSkills(ctx, profile.EnabledSkills)
	if err != nil {
		return capabilitySet{}, fmt.Errorf("解析 Agent Skill Snapshot 失败: %w", err)
	}

	toolScope.EnabledBuiltinTools = cloneOptionalStrings(profile.EnabledBuiltinTools)
	toolScope.EnabledMCPTools = mcpSelectionMap(profile.EnabledMCPTools)
	toolScope.EnabledSkills = append([]string(nil), skillSnapshot.Names...)
	toolScope.SkillRevision = skillSnapshot.Revision
	toolScope.SkillIdentities = skillSnapshot.PackageIdentities()
	toolScope.SkillScriptCommands = skillSnapshot.ScriptRuntimeCommands()

	resolvedTools, err := a.tools.Resolve(ctx, toolScope)
	if err != nil {
		return capabilitySet{}, fmt.Errorf("解析 Agent Tool Snapshot 失败: %w", err)
	}

	mcpSnapshot, err := a.resolveMCP(ctx, profile.EnabledMCPTools, toolScope, bestEffortMCP)
	if err != nil {
		return capabilitySet{}, fmt.Errorf("解析 Agent MCP Tool Snapshot 失败: %w", err)
	}

	// 先在 Humbert Capability 层完成所有模型侧 Tool Name 冲突检查，再做 Schema Token
	// 估算。Skill Middleware 会动态注入固定名称 `skill`，因此它也必须参与同一套 fail-closed
	// 规则，不能依赖“Builtin 目前恰好没有同名 Tool”的隐含前提。
	descriptors, err := mergeRuntimeDescriptors(resolvedTools.Descriptors, mcpSnapshot.Descriptors)
	if err != nil {
		return capabilitySet{}, err
	}
	if skillSnapshot.Enabled() {
		descriptors, err = mergeRuntimeDescriptors(
			descriptors,
			[]humberttools.Descriptor{{Name: skills.SkillToolName, Risk: humberttools.RiskRead}},
		)
		if err != nil {
			return capabilitySet{}, err
		}
	}

	// Eino Skill Middleware 在 Agent 创建阶段动态注入 skill 工具，因此它不属于 Humbert
	// 全局 Tool Registry。Context 预算仍必须把这个 schema 算进去，否则启用 Skill 后 UI
	// 和自动压缩阈值会低估真实 Provider Input。
	estimateTools := mergeRuntimeTools(resolvedTools.Tools, mcpSnapshot.Tools)
	if skillSnapshot.Enabled() {
		estimateTools = append(estimateTools, skillSnapshot.ToolDefinition())
	}

	extraDescriptors, extraTools, summaries, err := a.extensions.resolve(ctx, profile.ID, toolScope, bestEffortMCP, a.tools)
	if err != nil {
		return capabilitySet{}, err
	}
	descriptors, err = mergeRuntimeDescriptors(descriptors, extraDescriptors)
	if err != nil {
		return capabilitySet{}, err
	}
	estimateTools = mergeRuntimeTools(estimateTools, extraTools)
	executable := mergeRuntimeTools(mergeRuntimeTools(resolvedTools.Tools, mcpSnapshot.Tools), extraTools)

	return capabilitySet{
		tools: resolvedTools, skills: skillSnapshot, mcp: mcpSnapshot, scope: toolScope,
		descriptors: descriptors, executable: executable, schemas: estimateTools, summaries: summaries,
	}, nil
}

// 未安装可选组件且没有绑定时返回空能力；已有显式绑定时必须报错，不能静默忽略配置。
func (a *capabilityAssembler) resolveSkills(ctx context.Context, names []string) (skills.RuntimeSnapshot, error) {
	if a.skills == nil {
		if len(names) != 0 {
			return skills.RuntimeSnapshot{}, errors.New("Agent 已绑定 Skills，但未装配 Skills 组件")
		}
		return skills.RuntimeSnapshot{}, nil
	}
	return a.skills.ResolveRuntimeSnapshot(ctx, names)
}

func (a *capabilityAssembler) resolveMCP(ctx context.Context, selection []humbertmcp.ToolSelection, scope humberttools.Scope, preview bool) (humbertmcp.RuntimeSnapshot, error) {
	if a.mcp == nil {
		if len(selection) != 0 {
			return humbertmcp.RuntimeSnapshot{}, errors.New("Agent 已绑定 MCP，但未装配 MCP 组件")
		}
		return humbertmcp.RuntimeSnapshot{}, nil
	}
	if preview {
		return a.mcp.ResolveRuntimeSnapshotBestEffort(ctx, selection, scope)
	}
	return a.mcp.ResolveRuntimeSnapshotAvailable(ctx, selection, scope)
}

// toolNames 从已完成名称冲突检查的最终描述符生成模型可见名称。
// 主 Agent Manifest 与子 Agent 能力校验共用它，新增来源只需进入 resolve 的集合，
// 不必再修改另一份 Builtin/MCP/Skill 名称拼装分支。
func (c capabilitySet) toolNames() []string {
	names := make([]string, 0, len(c.descriptors))
	for _, descriptor := range c.descriptors {
		names = append(names, descriptor.Name)
	}
	return uniqueSortedStrings(names)
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func cloneOptionalStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func mcpSelectionMap(values []humbertmcp.ToolSelection) map[string][]string {
	result := make(map[string][]string, len(values))
	for _, selection := range values {
		result[selection.ServerID] = append([]string(nil), selection.Tools...)
	}
	return result
}

// mergeRuntimeDescriptors 合并不同 Tool Source 的模型侧名称，并在任何碰撞时 fail-closed。
// MCP Tool 使用 mcp_<serverKey>_ 前缀，但仍不依赖命名约定来假设“永远不会冲突”。
func mergeRuntimeDescriptors(base []humberttools.Descriptor, extra []humberttools.Descriptor) ([]humberttools.Descriptor, error) {
	result := make([]humberttools.Descriptor, 0, len(base)+len(extra))
	seen := make(map[string]struct{}, len(base)+len(extra))
	for _, values := range [][]humberttools.Descriptor{base, extra} {
		for _, descriptor := range values {
			if _, exists := seen[descriptor.Name]; exists {
				return nil, fmt.Errorf("Runtime Tool Name 冲突: %s", descriptor.Name)
			}
			seen[descriptor.Name] = struct{}{}
			result = append(result, descriptor)
		}
	}
	return result, nil
}

// mergeRuntimeTools 生成新的 Tool slice，避免把 MCP Tool append 到 Registry Snapshot 的
// backing array。当前 Turn 创建后两个来源都保持冻结。
func mergeRuntimeTools(base []einotool.BaseTool, extra []einotool.BaseTool) []einotool.BaseTool {
	result := make([]einotool.BaseTool, 0, len(base)+len(extra))
	result = append(result, base...)
	result = append(result, extra...)
	return result
}

// toolResultMaxCharsForContext 给单个 ToolResult 设置与模型窗口相关的直接注入上限。
// 完整结果会由 Context Artifact Store 保存，因此这里可以保守限制而不丢失可恢复性。
func toolResultMaxCharsForContext(contextWindow int) int {
	if contextWindow <= 0 {
		return 16000
	}
	// 近似允许单结果占 8% 上下文，按中英文混合保守使用约 2 chars/token；
	// 同时限制在 8K-64K 字符，避免极端大窗口让一次 ToolResult 重新成为上下文炸弹。
	limit := contextWindow * 2 / 12
	if limit < 8192 {
		limit = 8192
	}
	if limit > 65536 {
		limit = 65536
	}
	return limit
}
