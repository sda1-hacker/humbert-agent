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
// Snapshot 仍保留 Eino Skill 工具与脚本身份，替换实现不能丢失这两者的一致性。
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
// 模型工具和预算共用提前准备的定义，Skill 正文继续通过工具结果渐进披露。
type capabilitySet struct {
	tools     humberttools.ResolvedTools
	skills    skills.RuntimeSnapshot
	mcp       humbertmcp.RuntimeSnapshot
	scope     humberttools.Scope
	resolved  resolvedCapabilities
	summaries []CapabilitySummary
}

// capabilityAssembler 只协调已有能力来源，不重写它们的安装、连接或授权规则。
// Skills 必须先冻结，run_skill_script 和 Skill 工具才能共用相同内容身份。
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
	// enabled_skills + revision，必须与 Eino Skill 工具使用同一份内容身份。
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

	var resolved resolvedCapabilities
	if err := resolved.add(ctx, resolvedTools.Descriptors, resolvedTools.Tools); err != nil {
		return capabilitySet{}, err
	}
	if err := resolved.add(ctx, mcpSnapshot.Descriptors, mcpSnapshot.Tools); err != nil {
		return capabilitySet{}, err
	}
	if skillSnapshot.Enabled() {
		if err := resolved.add(ctx, []humberttools.Descriptor{{Name: skills.SkillToolName, Risk: humberttools.RiskRead}}, []einotool.BaseTool{skillSnapshot.ToolDefinition()}); err != nil {
			return capabilitySet{}, err
		}
	}
	extraDescriptors, extraTools, summaries, err := a.extensions.resolve(ctx, profile.ID, toolScope, bestEffortMCP, a.tools)
	if err != nil {
		return capabilitySet{}, err
	}
	if err := resolved.add(ctx, extraDescriptors, extraTools); err != nil {
		return capabilitySet{}, err
	}

	return capabilitySet{
		tools: resolvedTools, skills: skillSnapshot, mcp: mcpSnapshot, scope: toolScope,
		resolved: resolved, summaries: summaries,
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
	names := make([]string, 0, len(c.resolved))
	for _, capability := range c.resolved {
		names = append(names, capability.descriptor.Name)
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

// resolvedCapabilities 是最终工具集合；名称、Schema 预算和实际执行均从这里投影。
// 不依赖不同来源的 slice 顺序相同，先按 Schema 名称关联并拒绝缺项与碰撞。
type resolvedCapability struct {
	descriptor humberttools.Descriptor
	tool       einotool.BaseTool
}
type resolvedCapabilities []resolvedCapability

func (r *resolvedCapabilities) add(ctx context.Context, descriptors []humberttools.Descriptor, tools []einotool.BaseTool) error {
	if len(descriptors) != len(tools) {
		return errors.New("Runtime 工具描述与定义数量不一致")
	}
	seen := make(map[string]bool, len(*r)+len(descriptors))
	for _, item := range *r {
		seen[item.descriptor.Name] = true
	}
	definitions := make(map[string]einotool.BaseTool, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return errors.New("Runtime 工具定义不能为空")
		}
		info, err := tool.Info(ctx)
		if err != nil {
			return err
		}
		if info == nil || info.Name == "" {
			return errors.New("Runtime 工具 Schema 缺少名称")
		}
		if definitions[info.Name] != nil {
			return fmt.Errorf("Runtime Tool Name 冲突: %s", info.Name)
		}
		definitions[info.Name] = tool
	}
	added := make(resolvedCapabilities, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if seen[descriptor.Name] {
			return fmt.Errorf("Runtime Tool Name 冲突: %s", descriptor.Name)
		}
		tool := definitions[descriptor.Name]
		if tool == nil {
			return fmt.Errorf("Runtime 工具 %q 缺少对应 Schema", descriptor.Name)
		}
		seen[descriptor.Name] = true
		added = append(added, resolvedCapability{descriptor: descriptor, tool: tool})
	}
	*r = append(*r, added...)
	return nil
}

func (c capabilitySet) modelTools() []einotool.BaseTool {
	tools := make([]einotool.BaseTool, 0, len(c.resolved))
	for _, item := range c.resolved {
		tools = append(tools, item.tool)
	}
	return tools
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
