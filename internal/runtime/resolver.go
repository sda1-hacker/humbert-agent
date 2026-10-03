package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/collaboration"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/multimodal"
	"github.com/sda1-hacker/humbert-agent/internal/preferences"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// modelSnapshotResolver 是 Runtime 与 ModelRegistry 的边界。
type modelSnapshotResolver interface {
	ResolveSnapshot(ctx context.Context, id string) (models.RuntimeSnapshot, error)
	MultimediaConfig(ctx context.Context) (models.MultimediaConfig, error)
}

// BuildChildAgent 为 run_agent 构建一次性的独立 Eino Runtime。
//
// 子 Agent 不读取父 Session Transcript，也不创建 Humbert Session；输入只有父 Agent
// 明确写入 task 的内容。它使用自己的模型、指令和能力选择，但 Workspace/Sandbox 与
// Permission 身份受父 Runtime 上限制约，不能借协作越权。
func (r *Resolver) BuildChildAgent(ctx context.Context, input collaboration.BuildAgentInput) (collaboration.BuiltAgent, error) {
	if ctx == nil {
		return collaboration.BuiltAgent{}, errors.New("构建子 Agent 失败: context.Context 不能为空")
	}
	childInfo, err := r.agents.Get(ctx, strings.TrimSpace(input.ChildAgentID))
	if err != nil {
		return collaboration.BuiltAgent{}, fmt.Errorf("读取子 Agent Profile 失败: %w", err)
	}
	if !childInfo.Agent.SubagentEnabled {
		return collaboration.BuiltAgent{}, errors.New("目标 Agent 未启用“允许作为子 Agent 调用”")
	}
	roles, err := r.resolveModelRoles(ctx, childInfo.Agent, turnInputRequirements{})
	if err != nil {
		return collaboration.BuiltAgent{}, err
	}
	modelSnapshot := roles.chat

	// 能力来自子 Agent Profile；文件、网络和审批身份受父 Scope 约束。
	capabilities, err := r.capabilities.resolve(ctx, childInfo.Agent, humberttools.Scope{
		RequestID: input.ParentScope.RequestID, RunID: input.ParentScope.RunID, SessionID: input.ParentScope.SessionID,
		AgentID: input.ParentScope.AgentID, Workspace: input.ParentScope.Workspace, Sandbox: input.ParentScope.Sandbox,
		DisabledBuiltinTools: []string{collaboration.ListAgentsToolName, collaboration.RunAgentToolName, "session_history", "install_skill", "schedule_task"},
		ToolResultMaxChars:   toolResultMaxCharsForContext(modelSnapshot.ContextWindow),
	}, false)
	if err != nil {
		return collaboration.BuiltAgent{}, err
	}
	resolvedTools, skillSnapshot := capabilities.tools, capabilities.skills
	descriptors, toolScope := capabilities.descriptors, capabilities.scope
	exposedNames := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		exposedNames = append(exposedNames, descriptor.Name)
	}
	if err := validateToolCapability(modelSnapshot, modelRoleChat, exposedNames); err != nil {
		return collaboration.BuiltAgent{}, err
	}

	instruction := buildRuntimeInstruction(childInfo.Agent.Name, childInfo.Agent.Instruction, input.ParentScope.Workspace, descriptors, time.Now())
	instruction, err = r.withPersonalMemory(ctx, instruction)
	if err != nil {
		return collaboration.BuiltAgent{}, err
	}
	instruction, err = r.withResponseLanguage(ctx, instruction)
	if err != nil {
		return collaboration.BuiltAgent{}, err
	}
	instruction = strings.TrimSpace(instruction + `

## 子 Agent 协作约束
你正在作为主 Agent 调用的一次性专业子 Agent 运行。你看不到父会话历史；当前用户消息就是完整任务。
独立完成调查或操作后，返回清晰、可核验的结果给主 Agent。不要假装直接向最终用户说话，也不要继续委派其它 Agent。`)
	if skillSnapshot.Enabled() {
		instruction = strings.TrimSpace(instruction + "\n\n" + skillSnapshot.Instruction)
	}

	childTools, estimateTools := capabilities.executable, capabilities.schemas
	toolTokens, err := r.contextEngine.EstimateTools(ctx, estimateTools)
	if err != nil {
		return collaboration.BuiltAgent{}, fmt.Errorf("估算子 Agent Tool Context 占用失败: %w", err)
	}
	budget, err := r.contextEngine.BudgetForModel(modelSnapshot.ContextWindow, modelSnapshot.MaxOutputTokens)
	if err != nil {
		return collaboration.BuiltAgent{}, fmt.Errorf("计算子 Agent Context Budget 失败: %w", err)
	}
	budget = contextengine.ResolveBudgetForFixedContext(budget, toolTokens+r.contextEngine.EstimateMessages([]*schema.Message{schema.SystemMessage(instruction)}), 0)
	eventSnapshot := &Snapshot{
		RequestID: input.ParentScope.RequestID, RunID: input.ParentScope.RunID,
		SessionID: input.ParentScope.SessionID, AgentID: childInfo.Agent.ID, AgentName: childInfo.Agent.Name,
		ModelID: modelSnapshot.ModelConfigID, ModelRevision: modelSnapshot.Revision,
		ToolRevision: resolvedTools.Revision, EventReporter: r.eventReporter,
		ModelRole: modelRoleChat, limitState: limitStateFromContext(ctx),
	}
	eventSnapshot.Model = modelSnapshot.Instance
	eventSnapshot.Instruction = instruction
	eventSnapshot.Tools = childTools
	eventSnapshot.MaxIterations = r.maxIterations
	handlers, _, err := r.buildAgentHandlers(ctx, eventSnapshot, roles, skillSnapshot, toolScope,
		exposedNames, budget, toolTokens,
		input.ParentScope.SessionID+"/subagent/"+childInfo.Agent.ID, instruction, modelSnapshot.ProviderType)
	if err != nil {
		return collaboration.BuiltAgent{}, err
	}
	eventSnapshot.AgentHandlers = handlers
	child, err := buildChatModelAgent(ctx, eventSnapshot, "专业子 Agent："+strings.TrimSpace(childInfo.Agent.Instruction))
	if err != nil {
		return collaboration.BuiltAgent{}, fmt.Errorf("创建子 Eino ChatModelAgent 失败: %w", err)
	}
	return collaboration.BuiltAgent{Agent: child, AgentID: childInfo.Agent.ID, AgentName: childInfo.Agent.Name}, nil
}

// resolvedContextBase 保存构建 Context 所需但尚未投影 Session Transcript 的依赖。
//
// 它只在 Resolver 单次调用内存在，不是持久化对象。把 Agent/Model/Workspace/Tools 的解析
// 收口到一个私有结构，可以让 StartTurn、ContextStatus 与 ManualCompact 使用完全相同的
// Model/Tool 预算语义，避免 UI 显示的 Context Usage 与真正发给模型的请求不一致。
type resolvedContextBase struct {
	capabilities capabilitySet
	session      sessions.Session
	agentInfo    agents.AgentInfo
	model        models.RuntimeSnapshot
	modelRoles   resolvedModelRoles
	workspace    workspace.Workspace
	sandbox      sandbox.EffectivePolicy
	tools        humberttools.ResolvedTools

	// skills 是当前 Agent 在本 Turn 冻结的 Skill Snapshot。它与 Tool Snapshot 一样
	// 在 Resolve 后保持不变，避免用户在 Settings 中修改 Skill 时让正在执行的 Turn 漂移。
	skills skills.RuntimeSnapshot

	// mcp 是当前 Agent 的 MCP Tool Snapshot。MCP-01 在没有选择时为空；MCP-02 注入
	// RuntimeBackend 后会在这里冻结真正的 Eino MCP Tools。
	mcp               humbertmcp.RuntimeSnapshot
	baseInstruction   string
	toolTokenEstimate int
	scope             humberttools.Scope
}

// Resolver 创建不可变 Runtime Snapshot，并协调 Context 与 Compaction。
//
// Resolver 不自行解析 Transcript Wire Message；ContextEngine 是唯一的 ActiveBranch ->
// Model Context 投影层。Runtime 只负责解析当前 Agent、Model、Workspace、Tool Snapshot，
// 然后把冻结后的预算信息交给 ContextEngine。
type Resolver struct {
	agents         *agents.Service
	sessions       *sessions.Service
	models         modelSnapshotResolver
	workspaces     *workspace.Manager
	sandbox        *sandbox.Manager
	tools          *humberttools.Registry
	capabilities   *capabilityAssembler
	contextEngine  *contextengine.Engine
	personalMemory *preferences.Store
	eventReporter  EventReporter
	maxIterations  int
}

// NewResolver 创建 Runtime Resolver。
func NewResolver(
	agentService *agents.Service,
	sessionService *sessions.Service,
	modelResolver modelSnapshotResolver,
	workspaceManager *workspace.Manager,
	sandboxManager *sandbox.Manager,
	toolRegistry *humberttools.Registry,
	skillManager SkillSource,
	mcpManager MCPSource,
	contextEngine *contextengine.Engine,
	personalMemory *preferences.Store,
	eventReporter EventReporter,
	iterations ...int,
) *Resolver {
	maxIterations := defaultAgentMaxIterations
	if len(iterations) > 0 && iterations[0] > 0 {
		maxIterations = iterations[0]
	}
	return &Resolver{
		agents:         agentService,
		sessions:       sessionService,
		models:         modelResolver,
		workspaces:     workspaceManager,
		sandbox:        sandboxManager,
		tools:          toolRegistry,
		capabilities:   &capabilityAssembler{tools: toolRegistry, skills: skillManager, mcp: mcpManager},
		contextEngine:  contextEngine,
		personalMemory: personalMemory,
		eventReporter:  eventReporter,
		maxIterations:  maxIterations,
	}
}

// ResolveTurn 从当前 Session ActiveBranch 构造完整 Runtime Snapshot。
//
// UserMessage 必须在调用本方法前持久化。若达到自动压缩阈值，Resolver 会在首次模型调用
// 之前执行一次持久化 Compaction，然后重新 Build Context。正在执行的 ReAct tool loop 则
// 由 Snapshot 中的 MidRun Middleware 做纯内存压缩保护。
func (r *Resolver) ResolveTurn(
	ctx context.Context,
	requestID string,
	runID string,
	sessionID string,
	resolveOptions ...ResolveTurnOptions,
) (*Snapshot, error) {
	requirements, err := r.currentTurnRequirements(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	base, err := r.resolveContextBase(ctx, requestID, runID, sessionID, false, requirements)
	if err != nil {
		return nil, err
	}

	contextSnapshot, err := r.buildContextSnapshot(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("构建 Session Context 失败: %w", err)
	}
	contextSnapshot, err = r.alignModelToContext(ctx, &base, contextSnapshot)
	if err != nil {
		return nil, err
	}

	manifest := runtimeManifestFromBase(base)
	if err := validateToolCapability(base.model, modelRoleChat, manifest.ExposedToolNames); err != nil {
		return nil, err
	}

	var options ResolveTurnOptions
	if len(resolveOptions) > 0 {
		options = resolveOptions[0]
	}
	accounting := &Snapshot{
		RequestID: requestID, RunID: runID, SessionID: sessionID, AgentID: base.agentInfo.Agent.ID,
		EventReporter: r.eventReporter, limitState: options.limitState,
	}
	budget := contextSnapshot.Budget
	handlers, contextHandler, err := r.buildAgentHandlers(ctx, accounting, base.modelRoles, base.skills, base.scope,
		manifest.ExposedToolNames, budget, base.toolTokenEstimate, base.session.ID, contextSnapshot.Instruction, base.model.ProviderType)
	if err != nil {
		return nil, err
	}

	providerMessages, err := r.sessions.HydrateMessages(ctx, base.session.ID, contextSnapshot.Messages)
	if err != nil {
		return nil, fmt.Errorf("恢复 Runtime 附件失败: %w", err)
	}

	// 主聊天模型不支持图片时，图片只交给视觉辅助模型做一次事实观察；观察结果作为
	// 不可信文本注入当前 Provider Context，真正的 Agent 推理、工具调用和最终回答仍由
	// Chat Model 完成。支持 Vision 的 Chat Model 则继续直接接收原图片。
	if requirementsFromMessages(contextSnapshot.Messages).Vision && !base.modelRoles.chat.Capabilities.Vision {
		if base.modelRoles.image == nil {
			return nil, capabilityError(base.modelRoles.chat, modelRoleChat, []string{"Vision"})
		}

		// 为视觉观察分配历史预算的一部分，之后统一由 Reduction/Summarization
		// 决定完整输入是否可用；旧历史已满不应阻止能够通过摘要释放空间的图片请求。
		availableTokens := max(256, min(8192, budget.HistoryBudgetTokens/4))
		beforeBridgeTokens := r.contextEngine.EstimateMessages(providerMessages)
		providerMessages, err = multimodal.BridgeImagesForTextModel(
			ctx,
			trackAuxiliaryModel(*base.modelRoles.image, modelRoleImage, accounting),
			providerMessages,
			availableTokens,
		)
		if err != nil {
			return nil, fmt.Errorf("视觉辅助处理失败: %w", err)
		}
		afterBridgeTokens := r.contextEngine.EstimateMessages(providerMessages)
		if delta := afterBridgeTokens - beforeBridgeTokens; delta > 0 {
			contextSnapshot.Usage.MessageTokens += delta
			contextSnapshot.Usage.UsedTokens += delta
			if contextSnapshot.Usage.ContextWindow > 0 {
				contextSnapshot.Usage.Percent = float64(contextSnapshot.Usage.UsedTokens) * 100 / float64(contextSnapshot.Usage.ContextWindow)
			}
			contextSnapshot.Usage.NeedsCompaction = contextSnapshot.Usage.UsedTokens >= contextSnapshot.Usage.ThresholdTokens
			contextSnapshot.Usage.NeedsSoftCompaction = contextSnapshot.Usage.UsedTokens >= contextSnapshot.Usage.SoftThresholdTokens
		}
	}

	return &Snapshot{
		MaxIterations:     r.maxIterations,
		Manifest:          manifest,
		RequestID:         requestID,
		RunID:             runID,
		SessionID:         base.session.ID,
		AgentID:           manifest.AgentID,
		AgentName:         manifest.AgentName,
		Instruction:       contextSnapshot.Instruction,
		ModelID:           manifest.ModelID,
		ModelRevision:     manifest.ModelRevision,
		ModelRole:         modelRoleChat,
		ModelCapabilities: base.model.Capabilities,
		ToolRevision:      manifest.ToolRevision,
		BuiltinToolNames:  append([]string(nil), manifest.BuiltinToolNames...),
		SkillRevision:     manifest.SkillRevision,
		SkillNames:        append([]string(nil), manifest.SkillNames...),
		MCPRevision:       manifest.MCPRevision,
		MCPServers:        append([]humbertmcp.RuntimeServerSnapshot(nil), manifest.MCPServers...),
		MCPUnavailable:    append([]humbertmcp.RuntimeServerFailure(nil), manifest.MCPUnavailable...),
		MCPTools:          append([]humbertmcp.RuntimeToolSnapshot(nil), manifest.MCPTools...),
		MCPToolNames:      append([]string(nil), manifest.MCPToolNames...),
		ProviderID:        base.model.ProviderID,
		ProviderAPI:       base.model.API,
		ProviderName:      base.model.ProviderName,
		ModelName:         base.model.ModelName,
		ModelDisplayName:  base.model.ModelDisplayName,
		Model:             base.model.Instance,
		ContextWindow:     base.model.ContextWindow,
		MaxOutputTokens:   base.model.MaxOutputTokens,
		ToolTokenEstimate: base.toolTokenEstimate,
		ContextBudget:     contextSnapshot.Budget,
		ContextUsage:      contextSnapshot.Usage,
		ContextAssembly:   contextSnapshot.Assembly,
		ContextHandler:    contextHandler,
		AgentHandlers:     handlers,
		Tools:             base.capabilities.executable,
		Messages:          providerMessages,
		Workspace:         base.workspace,
		Sandbox:           base.sandbox,
		SessionWriter:     r.sessions,
		EventReporter:     r.eventReporter,
	}, nil
}

// buildContextSnapshot 使用当前 base.model 的预算构建本次真实 Provider Context。
func (r *Resolver) buildContextSnapshot(ctx context.Context, base resolvedContextBase) (contextengine.Snapshot, error) {
	return r.contextEngine.Build(ctx, contextengine.BuildRequest{
		SessionID:         base.session.ID,
		Instruction:       base.baseInstruction,
		ContextWindow:     base.model.ContextWindow,
		MaxOutputTokens:   base.model.MaxOutputTokens,
		ToolTokenEstimate: base.toolTokenEstimate,
		ReasoningPolicy:   reasoningReplayPolicyForProvider(base.model.ProviderType),
	})
}

// alignModelToContext 让模型角色与 ContextEngine 最终会发送的消息保持一致。
//
// Chat Model 始终是当前 Turn 的执行模型。这里唯一需要根据最终 Context 补解析的是
// Vision Assistant：当前 User Message 可能只有文字，但紧邻的上一轮图片仍处于重放窗口，
// 因此只有 Build 之后才能准确判断本轮是否需要视觉辅助。
func (r *Resolver) alignModelToContext(ctx context.Context, base *resolvedContextBase, snapshot contextengine.Snapshot) (contextengine.Snapshot, error) {
	if base == nil {
		return contextengine.Snapshot{}, errors.New("Runtime Context Base 不能为空")
	}

	requirements := requirementsFromMessages(snapshot.Messages)
	roles, err := r.resolveModelRoles(ctx, base.agentInfo.Agent, requirements)
	if err != nil {
		return contextengine.Snapshot{}, err
	}
	base.modelRoles = roles
	base.model = roles.chat
	return snapshot, nil
}

// ContextOverview 返回当前 Session 下一次请求的 Context Usage 与 Runtime Manifest。
//
// 该方法不会修改 Transcript、不会自动压缩，也不会为了 MCP Token 估算建立外部连接。
// Manifest 与 Usage 来自同一次 resolveContextBase，避免前端分别读取 Agent/Model/Tool 后
// 得到互相不一致的瞬时状态。
func (r *Resolver) ContextOverview(ctx context.Context, sessionID string) (ContextOverview, error) {
	base, err := r.resolveContextBase(ctx, "context-overview", "", sessionID, true, turnInputRequirements{})
	if err != nil {
		return ContextOverview{}, err
	}
	snapshot, err := r.buildContextSnapshot(ctx, base)
	if err != nil {
		return ContextOverview{}, fmt.Errorf("读取 Context Usage 失败: %w", err)
	}
	snapshot, err = r.alignModelToContext(ctx, &base, snapshot)
	if err != nil {
		return ContextOverview{}, err
	}

	return ContextOverview{Usage: snapshot.Usage, Assembly: snapshot.Assembly, Runtime: runtimeManifestFromBase(base)}, nil
}

// ContextStatus 保留原有轻量接口，供只需要 Token Usage 的调用方使用。
func (r *Resolver) ContextStatus(ctx context.Context, sessionID string) (contextengine.Usage, error) {
	overview, err := r.ContextOverview(ctx, sessionID)
	if err != nil {
		return contextengine.Usage{}, err
	}
	return overview.Usage, nil
}

// ManualCompact 执行用户主动触发的持久化摘要，与自动压缩共用中间件。
func (r *Resolver) ManualCompact(ctx context.Context, sessionID string) (ManualCompactionResult, error) {
	base, err := r.resolveContextBase(ctx, "manual-compaction", uuid.NewString(), sessionID, true, turnInputRequirements{})
	if err != nil {
		return ManualCompactionResult{}, err
	}

	compactModel := compactionModel(base.modelRoles)
	compactResult, compactErr := r.contextEngine.Compact(ctx, contextengine.CompactRequest{
		SessionID:                 base.session.ID,
		Model:                     compactModel.Instance,
		Instruction:               base.baseInstruction,
		ContextWindow:             base.model.ContextWindow,
		MaxOutputTokens:           base.model.MaxOutputTokens,
		ToolTokenEstimate:         base.toolTokenEstimate,
		CompactionContextWindow:   compactModel.ContextWindow,
		CompactionMaxOutputTokens: compactModel.MaxOutputTokens,
		Reason:                    contextengine.CompactionReasonManual,
		ReasoningPolicy:           reasoningReplayPolicyForProvider(base.model.ProviderType),
		Force:                     true,
	})
	if compactErr != nil && !errors.Is(compactErr, contextengine.ErrNothingToCompact) {
		return ManualCompactionResult{}, fmt.Errorf("手动压缩 Context 失败: %w", compactErr)
	}

	usage, err := r.ContextStatus(ctx, base.session.ID)
	if err != nil {
		return ManualCompactionResult{}, err
	}
	return ManualCompactionResult{Compaction: compactResult, ContextUsage: usage}, nil
}

// MaintainAfterTurn 在原始消息落盘后提交运行中的摘要，不再次请求模型。
// 提交失败保留原始历史，由 Runtime 记录告警，不改变已完成回答的终态。
func (r *Resolver) MaintainAfterTurn(ctx context.Context, snapshot *Snapshot) error {
	if snapshot == nil {
		return errors.New("Runtime Snapshot 不能为空")
	}
	// 使用本 Turn 第一次真实模型请求的 Prompt Usage 校准近似 Token 估算。校准失败或
	// Provider 未返回 Usage 都不影响主流程，它只是后续预算的精度优化。
	r.observeInitialPromptUsage(ctx, snapshot)
	if snapshot.ContextHandler != nil {
		return snapshot.ContextHandler.Commit(ctx)
	}
	return nil
}

// observeInitialPromptUsage 用首次中间件处理后的估算校准真实 Provider usage。
// 后续工具循环已经加入新结果，不能再与第一次请求的估算比较。
func (r *Resolver) observeInitialPromptUsage(ctx context.Context, snapshot *Snapshot) {
	if snapshot == nil || snapshot.ContextHandler == nil || snapshot.ContextHandler.FirstInputTokens() <= 0 {
		return
	}
	firstUsage := 0
	seenUser := false
	err := r.sessions.VisitActiveBranchReverse(ctx, snapshot.SessionID, func(entry transcript.Entry) bool {
		if entry.Type != transcript.EntryMessage || entry.Message == nil {
			return true
		}
		if entry.Message.Role == transcript.RoleUser {
			seenUser = true
			return false
		}
		if entry.Message.Role == transcript.RoleAssistant && entry.Message.Usage != nil && entry.Message.Usage.Input > 0 {
			firstUsage = entry.Message.Usage.Input
		}
		return true
	})
	if err == nil && seenUser && firstUsage > 0 {
		r.contextEngine.ObserveSessionPromptUsage(ctx, snapshot.SessionID, snapshot.ContextHandler.FirstInputTokens(), firstUsage)
	}
}

func (r *Resolver) resolveContextBase(
	ctx context.Context,
	requestID string,
	runID string,
	sessionID string,
	bestEffortMCP bool,
	requirements turnInputRequirements,
) (resolvedContextBase, error) {
	if ctx == nil {
		return resolvedContextBase{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return resolvedContextBase{}, fmt.Errorf("解析 Runtime Context 被取消: %w", err)
	}

	session, err := r.sessions.Get(ctx, sessionID)
	if err != nil {
		return resolvedContextBase{}, fmt.Errorf("读取 Session 失败: %w", err)
	}
	agentInfo, err := r.agents.Get(ctx, session.AgentID)
	if err != nil {
		return resolvedContextBase{}, fmt.Errorf("读取 Agent Profile 失败: %w", err)
	}
	modelRoles, err := r.resolveModelRoles(ctx, agentInfo.Agent, requirements)
	if err != nil {
		return resolvedContextBase{}, err
	}
	modelSnapshot := modelRoles.chat

	agentWorkspace, err := r.workspaces.Resolve(ctx, agentInfo.Agent.ID, agentInfo.Agent.WorkspaceMode, agentInfo.Agent.WorkspacePath)
	if err != nil {
		return resolvedContextBase{}, fmt.Errorf("Agent Workspace 当前不可使用: %w", err)
	}
	sandboxPolicy, err := r.sandbox.Resolve(ctx, agentWorkspace.RootDir, agentInfo.Agent.Sandbox)
	if err != nil {
		return resolvedContextBase{}, fmt.Errorf("解析 Agent Sandbox Policy 失败: %w", err)
	}

	capabilities, err := r.capabilities.resolve(ctx, agentInfo.Agent, humberttools.Scope{
		RequestID: requestID, RunID: runID, SessionID: session.ID, AgentID: agentInfo.Agent.ID,
		Workspace: agentWorkspace, Sandbox: sandboxPolicy, ToolResultMaxChars: toolResultMaxCharsForContext(modelSnapshot.ContextWindow),
	}, bestEffortMCP)
	if err != nil {
		return resolvedContextBase{}, err
	}
	resolvedTools, skillSnapshot, mcpSnapshot := capabilities.tools, capabilities.skills, capabilities.mcp
	descriptors, estimateTools, toolScope := capabilities.descriptors, capabilities.schemas, capabilities.scope
	toolTokens, err := r.contextEngine.EstimateTools(ctx, estimateTools)
	if err != nil {
		return resolvedContextBase{}, fmt.Errorf("估算 Tool/Skill Schema Context 占用失败: %w", err)
	}
	instruction := buildRuntimeInstruction(agentInfo.Agent.Name, agentInfo.Agent.Instruction, agentWorkspace, descriptors, time.Now())
	instruction, err = r.withPersonalMemory(ctx, instruction)
	if err != nil {
		return resolvedContextBase{}, err
	}
	instruction, err = r.withResponseLanguage(ctx, instruction)
	if err != nil {
		return resolvedContextBase{}, err
	}
	if len(mcpSnapshot.Failures) > 0 {
		serverNames := make([]string, 0, len(mcpSnapshot.Failures))
		for _, failure := range mcpSnapshot.Failures {
			serverNames = append(serverNames, failure.ServerName)
		}
		instruction = appendMCPUnavailableInstruction(instruction, serverNames)
	}
	if skillSnapshot.Enabled() {
		instruction = strings.TrimSpace(instruction + "\n\n" + skillSnapshot.Instruction)
	}
	return resolvedContextBase{
		capabilities:      capabilities,
		session:           session,
		agentInfo:         agentInfo,
		model:             modelSnapshot,
		modelRoles:        modelRoles,
		workspace:         agentWorkspace,
		sandbox:           sandboxPolicy,
		tools:             resolvedTools,
		skills:            skillSnapshot,
		mcp:               mcpSnapshot,
		baseInstruction:   instruction,
		toolTokenEstimate: toolTokens,
		scope:             toolScope,
	}, nil
}

func runtimeManifestFromBase(base resolvedContextBase) RuntimeManifest {
	exposed := make([]string, 0, len(base.tools.ToolNames)+len(base.mcp.ToolNames)+1)
	exposed = append(exposed, base.tools.ToolNames...)
	exposed = append(exposed, base.mcp.ToolNames...)
	if base.skills.Enabled() {
		exposed = append(exposed, skills.SkillToolName)
	}
	for _, summary := range base.capabilities.summaries {
		exposed = append(exposed, summary.ToolNames...)
	}
	exposed = uniqueSortedStrings(exposed)

	imageModelID := base.modelRoles.imageModelID
	return RuntimeManifest{
		Extensions:        base.capabilities.summaries,
		AgentID:           base.agentInfo.Agent.ID,
		AgentName:         base.agentInfo.Agent.Name,
		ModelID:           base.model.ModelConfigID,
		ModelDisplayName:  base.model.ModelDisplayName,
		ModelRevision:     base.model.Revision,
		ModelRole:         modelRoleChat,
		ModelCapabilities: base.model.Capabilities,
		ModelRoles: RuntimeModelRolesManifest{
			ChatModelID:    base.modelRoles.chat.ModelConfigID,
			UtilityModelID: base.modelRoles.utility.ModelConfigID,
			ImageModelID:   imageModelID,
			ActiveModelID:  base.model.ModelConfigID,
			ActiveRole:     modelRoleChat,
		},
		ToolRevision:     base.tools.Revision,
		BuiltinToolNames: append([]string(nil), base.tools.ToolNames...),
		SkillRevision:    base.skills.Revision,
		SkillNames:       base.skills.PackageNames(),
		MCPRevision:      base.mcp.Revision,
		MCPServers:       append([]humbertmcp.RuntimeServerSnapshot(nil), base.mcp.Servers...),
		MCPUnavailable:   append([]humbertmcp.RuntimeServerFailure(nil), base.mcp.Failures...),
		MCPTools:         append([]humbertmcp.RuntimeToolSnapshot(nil), base.mcp.AuditTools...),
		MCPToolNames:     append([]string(nil), base.mcp.ToolNames...),
		ExposedToolNames: exposed,
		Workspace: RuntimeWorkspaceManifest{
			Mode:    string(base.workspace.Mode),
			RootDir: base.workspace.RootDir,
		},
		Sandbox: RuntimeSandboxManifest{
			Profile:       string(base.sandbox.Profile),
			NetworkMode:   string(base.sandbox.NetworkMode),
			NativeMode:    string(base.sandbox.NativeMode),
			NativeBackend: base.sandbox.Capability.Backend,
			NativeReady:   base.sandbox.Capability.Available,
		},
	}
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

// withPersonalMemory 只注入用户明确保存的短事实。每轮读取一次并冻结在 Snapshot 中；
// 单条和总条数由 Store 控制，避免无界长期记忆挤占 Context。
func (r *Resolver) withPersonalMemory(ctx context.Context, instruction string) (string, error) {
	if r.personalMemory == nil {
		return "", errors.New("个人记忆 Store 未初始化")
	}
	items, err := r.personalMemory.ListMemories(ctx)
	if err != nil {
		return "", fmt.Errorf("读取跨会话个人记忆失败: %w", err)
	}
	if len(items) == 0 {
		return instruction, nil
	}
	var builder strings.Builder
	builder.WriteString(instruction)
	builder.WriteString("\n\n<user_managed_memory>\n以下是用户明确保存、可在设置中修订或删除的跨会话事实。它们可能过时；与当前用户陈述冲突时以当前陈述为准。\n")
	for _, item := range items {
		builder.WriteString("- ")
		builder.WriteString(escapePromptText(item.Text))
		builder.WriteByte('\n')
	}
	builder.WriteString("</user_managed_memory>")
	return builder.String(), nil
}

// withResponseLanguage 只约束面向用户的正文；Provider 的 reasoning_content 属于原始模型数据，
// 不能假装系统设置已经翻译或改写了它。
func (r *Resolver) withResponseLanguage(ctx context.Context, instruction string) (string, error) {
	if r.personalMemory == nil {
		return "", errors.New("Preferences Store 未初始化")
	}
	profile, err := r.personalMemory.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("读取回复语言失败: %w", err)
	}
	name := map[string]string{"zh-CN": "Simplified Chinese", "en-US": "English", "ja-JP": "Japanese", "ko-KR": "Korean"}[profile.Language]
	if name == "" {
		name = "Simplified Chinese"
	}
	return instruction + "\n\n<response_language>\nReply to the user in " + name + " (" + profile.Language + ") unless the user explicitly requests another language. Keep code, paths, URLs, and quoted source text unchanged. Do not reveal internal reasoning.\n</response_language>", nil
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

// OperationTimeout 返回 Context 单次维护任务的最长执行时间。
//
// RuntimeService 使用该值为派生状态维护创建受当前 Turn 控制的 Context，
// 用户取消和应用关闭都会传播到维护过程。
func (r *Resolver) OperationTimeout() time.Duration {
	timeout := time.Duration(r.contextEngine.Config().OperationTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		return 2 * time.Minute
	}
	return timeout
}

// Validate 校验 RuntimeResolver 依赖。
func (r *Resolver) Validate() error {
	if r.agents == nil {
		return errors.New("RuntimeResolver AgentService 不能为空")
	}
	if r.sessions == nil {
		return errors.New("RuntimeResolver SessionService 不能为空")
	}
	if r.models == nil {
		return errors.New("RuntimeResolver ModelResolver 不能为空")
	}
	if r.workspaces == nil {
		return errors.New("RuntimeResolver WorkspaceManager 不能为空")
	}
	if r.sandbox == nil {
		return errors.New("RuntimeResolver SandboxManager 不能为空")
	}
	if r.tools == nil {
		return errors.New("RuntimeResolver ToolRegistry 不能为空")
	}
	if err := r.capabilities.validate(); err != nil {
		return err
	}

	if r.contextEngine == nil {
		return errors.New("RuntimeResolver ContextEngine 不能为空")
	}
	if r.personalMemory == nil {
		return errors.New("RuntimeResolver PersonalMemory Store 不能为空")
	}
	if r.eventReporter == nil {
		return errors.New("RuntimeResolver EventReporter 不能为空")
	}
	return nil
}
