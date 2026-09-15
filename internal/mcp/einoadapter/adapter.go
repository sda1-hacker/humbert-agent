package einoadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/cloudwego/eino-ext/components/tool/mcp/officialmcp"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// Adapter 是 Humbert 与 Eino-Ext officialmcp 的唯一转换边界。
//
// Humbert 不自行实现 MCP JSON-RPC / Tool Schema 转换。Session 由 MCP-02 SessionPool 提供，
// Adapter 只负责：raw tool selection -> officialmcp.GetTools -> Humbert Permission Guard。
type Adapter struct {
	config     config.MCPConfig
	authorizer humberttools.Authorizer
}

func New(cfg config.MCPConfig, authorizer humberttools.Authorizer) (*Adapter, error) {
	if authorizer == nil {
		return nil, errors.New("MCP Eino Adapter Authorizer 不能为空")
	}
	return &Adapter{config: cfg, authorizer: authorizer}, nil
}

// BuildTools 把一个已初始化 MCP ClientSession 的显式 Tool 子集转换成受 Humbert Permission
// Guard 保护的 Eino BaseTool。Server annotation 不参与 Risk 决策；MCP V1 默认全部 RiskWrite。
func (a *Adapter) BuildTools(
	ctx context.Context,
	server humbertmcp.Server,
	rawToolNames []string,
	session officialmcp.ClientSession,
	scope humberttools.Scope,
	observeInvocation func(error),
) (humbertmcp.RuntimeSnapshot, error) {
	if ctx == nil {
		return humbertmcp.RuntimeSnapshot{}, errors.New("构建 MCP Eino Tools 失败: context.Context 不能为空")
	}
	if session == nil {
		return humbertmcp.RuntimeSnapshot{}, errors.New("构建 MCP Eino Tools 失败: ClientSession 不能为空")
	}
	if err := humbertmcp.ValidateServerForRuntime(server); err != nil {
		return humbertmcp.RuntimeSnapshot{}, err
	}
	if err := scope.Validate(); err != nil {
		return humbertmcp.RuntimeSnapshot{}, err
	}

	selected := make([]string, 0, len(rawToolNames))
	seen := make(map[string]struct{}, len(rawToolNames))
	for _, raw := range rawToolNames {
		name, err := humbertmcp.NormalizeRawToolName(raw)
		if err != nil {
			return humbertmcp.RuntimeSnapshot{}, err
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		selected = append(selected, name)
	}
	if len(selected) == 0 {
		// officialmcp 的 ToolNameList 为空意味着“获取全部 Tool”。Humbert 的 MCP
		// 能力必须显式选择，因此空选择必须在进入 Adapter 前就收敛为空 Snapshot。
		return humbertmcp.RuntimeSnapshot{}, nil
	}
	sort.Strings(selected)
	if a.config.MaxToolsPerServer > 0 && len(selected) > a.config.MaxToolsPerServer {
		return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("MCP Server %s Tool 数量超过上限 %d", server.Key, a.config.MaxToolsPerServer)
	}

	baseTools, err := officialmcp.GetTools(ctx, &officialmcp.Config{
		Cli:           session,
		ServerName:    server.Key,
		ToolNameList:  selected,
		ListToolsMode: officialmcp.ListToolsAllPages,
		MaxToolPages:  a.config.MaxToolPages,
		MetadataMode:  officialmcp.MetadataBasic,
		ToolNameMapper: func(_ context.Context, input officialmcp.ToolNameMapperInput) (officialmcp.ToolNameMapperOutput, error) {
			name, err := humbertmcp.NameExposedTool(server.Key, input.Tool.Name)
			if err != nil {
				return officialmcp.ToolNameMapperOutput{}, err
			}
			return officialmcp.ToolNameMapperOutput{ExposedName: name}, nil
		},
		DescriptionPolicy: &officialmcp.DescriptionPolicy{MaxChars: a.config.MaxToolDescriptionChars},
		ResultPolicy: &officialmcp.ResultPolicy{
			MaxChars:                 a.config.MaxToolResultChars,
			PreserveTailChars:        a.config.PreserveToolResultTailChars,
			IncludeStructuredContent: true,
		},
	})
	if err != nil {
		return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("通过 Eino officialmcp 获取 Server %q Tools 失败: %w", server.Key, err)
	}
	if len(baseTools) != len(selected) {
		return humbertmcp.RuntimeSnapshot{}, fmt.Errorf(
			"MCP Server %q 已选择 %d 个 Tool，但只解析到 %d 个；Catalog 可能已发生变化",
			server.Key,
			len(selected),
			len(baseTools),
		)
	}

	// Server 的 tools/list 顺序不是 Humbert 的持久化语义。先读取 exposed name，再按名称稳定
	// 排序；这样 Context 估算、调试输出和测试不随 Server 返回顺序漂移。
	type resolvedTool struct {
		name       string
		tool       einotool.BaseTool
		descriptor humberttools.Descriptor
	}
	items := make([]resolvedTool, 0, len(baseTools))
	for _, base := range baseTools {
		invokable, ok := base.(einotool.InvokableTool)
		if !ok {
			return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("MCP Server %q 返回了非 Invokable Tool", server.Key)
		}
		info, err := invokable.Info(ctx)
		if err != nil {
			return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("读取 MCP Tool Info 失败: %w", err)
		}
		if info == nil {
			return humbertmcp.RuntimeSnapshot{}, errors.New("MCP Tool Info 不能为空")
		}
		rawName := ""
		if info.Extra != nil {
			if value, ok := info.Extra[officialmcp.ExtraMCPRawToolName].(string); ok {
				rawName = value
			}
		}
		if rawName == "" {
			return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("MCP Tool %q 缺少 raw tool identity", info.Name)
		}
		descriptor := humberttools.Descriptor{
			Name: info.Name,
			Risk: humbertmcp.ToolRisk(server, rawName),
			MCPOrigin: &humberttools.MCPOrigin{
				ServerID:          server.ID,
				ServerName:        server.Name,
				ServerFingerprint: humbertmcp.ServerFingerprint(server),
				RawToolName:       rawName,
			},
		}
		observed := einotool.InvokableTool(invokable)
		if observeInvocation != nil {
			observed = &observedInvokableTool{tool: invokable, observe: observeInvocation}
		}
		guarded, err := humberttools.GuardInvokableTool(ctx, a.authorizer, descriptor, scope, observed)
		if err != nil {
			return humbertmcp.RuntimeSnapshot{}, fmt.Errorf("保护 MCP Tool %q 失败: %w", info.Name, err)
		}
		items = append(items, resolvedTool{
			name:       info.Name,
			tool:       guarded,
			descriptor: descriptor,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })

	resolved := make([]einotool.BaseTool, 0, len(items))
	descriptors := make([]humberttools.Descriptor, 0, len(items))
	toolNames := make([]string, 0, len(items))
	for _, item := range items {
		resolved = append(resolved, item.tool)
		descriptors = append(descriptors, item.descriptor)
		toolNames = append(toolNames, item.name)
	}

	return humbertmcp.RuntimeSnapshot{
		Tools:       resolved,
		Descriptors: descriptors,
		ToolNames:   toolNames,
	}, nil
}

// observedInvokableTool 只观察真实 MCP Tool 执行结果。Permission Ask/Deny 发生在外层
// Guard，因此只有真正到达 MCP Server 的调用才会更新 Session 的 LastUsed/健康状态。
type observedInvokableTool struct {
	tool    einotool.InvokableTool
	observe func(error)
}

func (t *observedInvokableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.tool.Info(ctx)
}

func (t *observedInvokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, options ...einotool.Option) (string, error) {
	result, err := t.tool.InvokableRun(ctx, argumentsInJSON, options...)
	if t.observe != nil {
		t.observe(err)
	}
	return result, err
}

// DiscoverTools 通过 Eino officialmcp 读取 Server 当前 Tool Catalog。
// 它只返回 Humbert UI / Agent Selection 所需投影，不保存 Server annotation 风险结论。
func (a *Adapter) DiscoverTools(
	ctx context.Context,
	server humbertmcp.Server,
	session officialmcp.ClientSession,
) ([]humbertmcp.ToolCatalogItem, error) {
	if ctx == nil {
		return nil, errors.New("发现 MCP Tools 失败: context.Context 不能为空")
	}
	if session == nil {
		return nil, errors.New("发现 MCP Tools 失败: ClientSession 不能为空")
	}
	if err := humbertmcp.ValidateServerForRuntime(server); err != nil {
		return nil, err
	}

	baseTools, err := officialmcp.GetTools(ctx, &officialmcp.Config{
		Cli:           session,
		ServerName:    server.Key,
		ListToolsMode: officialmcp.ListToolsAllPages,
		MaxToolPages:  a.config.MaxToolPages,
		MetadataMode:  officialmcp.MetadataBasic,
		ToolNameMapper: func(_ context.Context, input officialmcp.ToolNameMapperInput) (officialmcp.ToolNameMapperOutput, error) {
			name, err := humbertmcp.NameExposedTool(server.Key, input.Tool.Name)
			if err != nil {
				return officialmcp.ToolNameMapperOutput{}, err
			}
			return officialmcp.ToolNameMapperOutput{ExposedName: name}, nil
		},
		DescriptionPolicy: &officialmcp.DescriptionPolicy{MaxChars: a.config.MaxToolDescriptionChars},
	})
	if err != nil {
		return nil, fmt.Errorf("通过 Eino officialmcp 发现 Server %q Tools 失败: %w", server.Key, err)
	}
	result := make([]humbertmcp.ToolCatalogItem, 0, len(baseTools))
	for _, base := range baseTools {
		info, err := base.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("读取 MCP Tool Info 失败: %w", err)
		}
		if info == nil {
			return nil, errors.New("MCP Tool Info 不能为空")
		}
		rawName := ""
		annotations := map[string]any(nil)
		if info.Extra != nil {
			if value, ok := info.Extra[officialmcp.ExtraMCPRawToolName].(string); ok {
				rawName = value
			}
			if value, ok := info.Extra[officialmcp.ExtraMCPAnnotations].(map[string]any); ok {
				annotations = value
			}
		}
		if rawName == "" {
			return nil, fmt.Errorf("MCP Tool %q 缺少 raw tool identity", info.Name)
		}
		_, overridden := server.ToolRisks[rawName]
		result = append(result, humbertmcp.ToolCatalogItem{
			RawName:        rawName,
			ExposedName:    info.Name,
			Description:    info.Desc,
			InputSchema:    projectInputSchema(info.ParamsOneOf),
			Annotations:    annotations,
			Risk:           humbertmcp.ToolRisk(server, rawName),
			RiskOverridden: overridden,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RawName < result[j].RawName })
	return result, nil
}

// projectInputSchema 只投影 Eino ToolInfo 已经解析好的参数定义，不直接接触底层 MCP SDK。
// 结果用于 Desktop 展示；无法序列化时返回 nil，不影响 Tool 的真实运行时 schema。
func projectInputSchema(value any) map[string]any {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || string(encoded) == "null" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	if object, ok := decoded.(map[string]any); ok {
		return object
	}
	return map[string]any{"schema": decoded}
}
