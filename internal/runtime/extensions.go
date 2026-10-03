package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"

	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/sda1-hacker/humbert-agent/internal/component"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// CapabilitySummary 是公共能力面板的稳定投影，不含模块配置、秘密或可执行对象。
type CapabilitySummary struct {
	ID        string   `json:"id"`
	Revision  string   `json:"revision,omitempty"`
	ToolNames []string `json:"toolNames"`
}

// extensionRegistry 只在 Bootstrap 注册，首次装配后封闭注册。
// 不提供服务查找或热卸载，避免关闭正在被 Turn/checkpoint 使用的连接。
type extensionRegistry struct {
	mu        sync.Mutex
	providers map[string]component.Provider
	sealed    bool
}

type registeredProvider struct {
	id       string
	provider component.Provider
}

func (r *extensionRegistry) register(provider component.Provider) error {
	if provider == nil {
		return errors.New("能力提供者不能为空")
	}
	id := strings.TrimSpace(provider.ID())
	if len(id) > 24 || id != provider.ID() {
		return errors.New("能力提供者 ID 必须为不超过 24 字符的稳定名称")
	}
	if err := (humberttools.Descriptor{Name: id, Risk: humberttools.RiskRead}).Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return errors.New("Runtime 能力注册已完成，请在 Bootstrap 中注册模块")
	}
	if r.providers == nil {
		r.providers = make(map[string]component.Provider)
	}
	if _, exists := r.providers[id]; exists {
		return fmt.Errorf("重复的能力提供者 %q", id)
	}
	r.providers[id] = provider
	return nil
}

func (r *extensionRegistry) snapshot() []registeredProvider {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealed = true
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]registeredProvider, 0, len(ids))
	for _, id := range ids {
		result = append(result, registeredProvider{id: id, provider: r.providers[id]})
	}
	return result
}

// RegisterCapabilityProvider 在应用构造期间接入模块，不改变既有 Builtin 默认选择语义。
func (r *Resolver) RegisterCapabilityProvider(provider component.Provider) error {
	if r == nil || r.capabilities == nil {
		return errors.New("Runtime 能力装配器未初始化")
	}
	return r.capabilities.extensions.register(provider)
}

func (r *extensionRegistry) resolve(ctx context.Context, agentID string, scope humberttools.Scope, preview bool,
	registry *humberttools.Registry,
) ([]humberttools.Descriptor, []einotool.BaseTool, []CapabilitySummary, error) {
	var descriptors []humberttools.Descriptor
	var tools []einotool.BaseTool
	var summaries []CapabilitySummary
	for _, registered := range r.snapshot() {
		provider, id := registered.provider, registered.id
		if provider.ID() != id {
			return nil, nil, nil, fmt.Errorf("模块 %q 在注册后改变了能力 ID", id)
		}
		names, err := provider.Selection(ctx, agentID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("读取模块 %q 的 Agent 绑定失败: %w", id, err)
		}
		names = append([]string(nil), names...)
		if len(names) == 0 {
			continue
		}
		selected := make(map[string]bool, len(names))
		for _, name := range names {
			if strings.TrimSpace(name) != name || !strings.HasPrefix(name, id+"_") || selected[name] {
				return nil, nil, nil, fmt.Errorf("模块 %q 的工具选择 %q 缺少模块前缀或重复", id, name)
			}
			selected[name] = true
		}
		request := component.Request{AgentID: agentID, Scope: cloneCapabilityScope(scope), ToolNames: slices.Clone(names)}
		var contribution component.Contribution
		if preview {
			contribution, err = provider.Describe(ctx, request)
		} else {
			contribution, err = provider.Resolve(ctx, request)
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("装配模块 %q 能力失败: %w", id, err)
		}
		revision := strings.TrimSpace(contribution.Revision)
		if revision == "" {
			return nil, nil, nil, fmt.Errorf("模块 %q 缺少冻结版本", id)
		}
		for _, capability := range contribution.Tools {
			descriptor := capability.Descriptor
			if !selected[descriptor.Name] || capability.Tool == nil || descriptor.Internal || descriptor.MCPOrigin != nil {
				return nil, nil, nil, fmt.Errorf("模块 %q 返回了未选择或非法的工具 %q", id, descriptor.Name)
			}
			delete(selected, descriptor.Name)
			// 来源由宿主设置，长期 Allow 精确绑定模块及配置版本，旧授权不能匹配新内容。
			descriptor.ModuleOrigin = &humberttools.ModuleOrigin{ID: id, Revision: revision}
			if err := descriptor.Validate(); err != nil {
				return nil, nil, nil, err
			}
			info, err := capability.Tool.Info(ctx)
			if err != nil {
				return nil, nil, nil, err
			}
			if info == nil || info.Name != descriptor.Name {
				return nil, nil, nil, fmt.Errorf("模块工具 %q 的 Schema 名称不一致", descriptor.Name)
			}
			tool := capability.Tool
			if !preview {
				invokable, ok := tool.(einotool.InvokableTool)
				if !ok {
					return nil, nil, nil, fmt.Errorf("模块工具 %q 不可执行", descriptor.Name)
				}
				tool, err = registry.Guard(ctx, descriptor, scope, invokable)
				if err != nil {
					return nil, nil, nil, err
				}
			}
			descriptors = append(descriptors, descriptor)
			tools = append(tools, tool)
		}
		if len(selected) != 0 {
			return nil, nil, nil, fmt.Errorf("模块 %q 未返回全部已选择工具", id)
		}
		sort.Strings(names)
		summaries = append(summaries, CapabilitySummary{ID: id, Revision: revision, ToolNames: names})
	}
	return descriptors, tools, summaries, nil
}

// cloneCapabilityScope 隔离模块边界上的可变集合。Guard 始终持有宿主原来的 Scope，
// 一个模块误改请求里的选择或 PathRules 时，不会改变其他工具或父运行的安全身份。
// 复用标准库 maps/slices，只对含集合的字段做复制，不引入通用序列化或反射复制器。
func cloneCapabilityScope(scope humberttools.Scope) humberttools.Scope {
	scope.Sandbox.PathRules = slices.Clone(scope.Sandbox.PathRules)
	scope.EnabledBuiltinTools = slices.Clone(scope.EnabledBuiltinTools)
	scope.DisabledBuiltinTools = slices.Clone(scope.DisabledBuiltinTools)
	scope.EnabledSkills = slices.Clone(scope.EnabledSkills)
	scope.SkillIdentities = maps.Clone(scope.SkillIdentities)
	scope.EnabledMCPTools = maps.Clone(scope.EnabledMCPTools)
	for id, names := range scope.EnabledMCPTools {
		scope.EnabledMCPTools[id] = slices.Clone(names)
	}
	scope.SkillScriptCommands = maps.Clone(scope.SkillScriptCommands)
	for name, commands := range scope.SkillScriptCommands {
		scope.SkillScriptCommands[name] = maps.Clone(commands)
	}
	return scope
}
