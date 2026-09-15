package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	einotool "github.com/cloudwego/eino/components/tool"
)

// Registry 管理 Humbert 当前可用 Tool Definition。
//
// Registry 保存的是 Factory，不是某个正在运行的 Tool Instance。
//
// 生命周期：
//
//	Application Bootstrap
//	    ↓
//	Register Builtin Factories
//	    ↓
//	未来 Register Skill/MCP/Plugin Factories
//	    ↓
//	RuntimeResolver.Resolve()
//	    ↓
//	复制当前 Factory Snapshot + Revision
//	    ↓
//	为本 Turn Build Tool Instances
//
// 这保证：
//
//	Agent 本体稳定，能力是动态的。
//
// 而正在执行的 Turn 永远使用它创建时冻结的 ToolSet。
type Registry struct {
	mu sync.RWMutex

	factories map[string]Factory

	revision uint64

	authorizer Authorizer
}

// NewRegistry 创建 Tool Registry。
//
// Revision 从 1 开始。
//
// 后续每一次真正改变 Registry Definition 的 Register/Unregister
// 都会递增 Revision。
func NewRegistry(
	authorizer Authorizer,
) (*Registry, error) {
	if authorizer == nil {
		return nil, errors.New(
			"Tool Registry Authorizer 不能为空",
		)
	}

	return &Registry{
		factories: make(
			map[string]Factory,
		),

		revision: 1,

		authorizer: authorizer,
	}, nil
}

// Revision 返回当前 Tool Registry Revision。
func (r *Registry) Revision() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.revision
}

// Register 注册 Tool Factory。
//
// 重复名称不会覆盖已有 Tool，防止一个动态 Skill/MCP
// 静默替换 Builtin Tool。
func (r *Registry) Register(
	factory Factory,
) error {
	if factory == nil {
		return fmt.Errorf(
			"%w: Factory 不能为空",
			ErrInvalidTool,
		)
	}

	descriptor :=
		factory.Descriptor()

	if err :=
		descriptor.Validate(); err != nil {

		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists :=
		r.factories[descriptor.Name]; exists {

		return fmt.Errorf(
			"%w: %s",
			ErrToolExists,
			descriptor.Name,
		)
	}

	r.factories[descriptor.Name] = factory

	r.revision++

	return nil
}

// Unregister 删除一个 Tool Definition。
//
// 已经创建出来的 RuntimeSnapshot 不受影响。
func (r *Registry) Unregister(
	name string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists :=
		r.factories[name]; !exists {

		return fmt.Errorf(
			"%w: %s",
			ErrToolNotFound,
			name,
		)
	}

	delete(
		r.factories,
		name,
	)

	r.revision++

	return nil
}

// List 返回当前 Tool Descriptor Snapshot。
//
// 返回结果按 Tool Name 排序，保证 UI、日志和测试具有稳定顺序。
func (r *Registry) List() []Descriptor {
	r.mu.RLock()

	result :=
		make(
			[]Descriptor,
			0,
			len(r.factories),
		)

	for _, factory := range r.factories {

		result = append(
			result,
			factory.Descriptor(),
		)
	}

	r.mu.RUnlock()

	sort.Slice(
		result,
		func(
			i int,
			j int,
		) bool {
			return result[i].Name <
				result[j].Name
		},
	)

	return result
}

// Resolve 为一个 RuntimeSnapshot 创建不可变 ToolSet。
//
// 关键步骤：
//
//  1. 在读锁下复制 Factory Snapshot 和 Revision；
//  2. 立即释放 Registry 锁；
//  3. 在锁外执行 Build；
//  4. 每个 Tool 包装 Permission Guard；
//  5. 返回冻结的 []tool.BaseTool。
//
// Build 不在 Registry Lock 内执行，避免未来 MCP/Skill Tool
// 初始化耗时导致整个 Registry 长时间阻塞。
func (r *Registry) Resolve(
	ctx context.Context,
	scope Scope,
) (
	ResolvedTools,
	error,
) {
	if err := ctx.Err(); err != nil {
		return ResolvedTools{},
			fmt.Errorf(
				"解析 Tool Snapshot 被取消: %w",
				err,
			)
	}

	if err :=
		scope.Validate(); err != nil {

		return ResolvedTools{},
			fmt.Errorf(
				"Tool Scope 无效: %w",
				err,
			)
	}

	r.mu.RLock()

	revision :=
		r.revision

	factories :=
		make(
			[]Factory,
			0,
			len(r.factories),
		)

	for _, factory := range r.factories {

		factories = append(
			factories,
			factory,
		)
	}

	r.mu.RUnlock()

	sort.Slice(
		factories,
		func(
			i int,
			j int,
		) bool {
			return factories[i].
				Descriptor().
				Name <
				factories[j].
					Descriptor().
					Name
		},
	)

	selected := map[string]struct{}{}
	if scope.EnabledBuiltinTools != nil {
		known := make(map[string]struct{}, len(factories))
		for _, factory := range factories {
			known[factory.Descriptor().Name] = struct{}{}
		}
		for _, name := range scope.EnabledBuiltinTools {
			if _, ok := known[name]; !ok {
				return ResolvedTools{}, fmt.Errorf("%w: Agent 选择了不存在的 Builtin Tool %q", ErrToolNotFound, name)
			}
			selected[name] = struct{}{}
		}
	}

	resolved := make([]einotool.BaseTool, 0, len(factories))
	resolvedDescriptors := make([]Descriptor, 0, len(factories))
	resolvedNames := make([]string, 0, len(factories))

	for _, factory := range factories {

		if err := ctx.Err(); err != nil {
			return ResolvedTools{},
				fmt.Errorf(
					"解析 Tool Snapshot 被取消: %w",
					err,
				)
		}

		descriptor := factory.Descriptor()
		if scope.EnabledBuiltinTools != nil {
			if _, ok := selected[descriptor.Name]; !ok {
				continue
			}
		}

		instance, err :=
			factory.Build(
				ctx,
				scope,
			)

		if err != nil {
			return ResolvedTools{},
				fmt.Errorf(
					"构建 Tool %q 失败: %w",
					descriptor.Name,
					err,
				)
		}

		guarded, err := GuardInvokableTool(
			ctx,
			r.authorizer,
			descriptor,
			scope,
			instance,
		)
		if err != nil {
			return ResolvedTools{},
				fmt.Errorf(
					"保护 Tool %q 失败: %w",
					descriptor.Name,
					err,
				)
		}

		resolved = append(resolved, guarded)
		resolvedDescriptors = append(resolvedDescriptors, descriptor)
		resolvedNames = append(resolvedNames, descriptor.Name)
	}

	return ResolvedTools{
		Tools:       resolved,
		Descriptors: resolvedDescriptors,
		ToolNames:   resolvedNames,
		Revision:    revision,
	}, nil
}
