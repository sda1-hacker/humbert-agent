package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/multimodal"
	"github.com/sda1-hacker/humbert-agent/internal/preferences"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// modelSnapshotResolver 是 Runtime 与 ModelRegistry 的边界。
type modelSnapshotResolver interface {
	ResolveSnapshot(ctx context.Context, id string) (models.RuntimeSnapshot, error)
	MultimediaConfig(ctx context.Context) (models.MultimediaConfig, error)
}

// resolvedContextBase 保存构建 Context 所需但尚未投影 Session Transcript 的依赖。
//
// 它只在 Resolver 单次调用内存在，不是持久化对象。把 Agent/Model/Workspace/Tools 的解析
// 收口到一个私有结构，可以让 StartTurn、ContextStatus 与 ManualCompact 使用完全相同的
// Model/Tool 预算语义，避免 UI 显示的 Context Usage 与真正发给模型的请求不一致。
type resolvedContextBase struct {
	// 能力与授权 Scope 只保存一份，由 capabilityAssembler 冻结。
	// 嵌入已有集合后，Context 查询、Manifest 和执行都读取同一份 tools/skills/mcp，
	// 不再同时维护一组内容相同、后续可能漏同步的平行字段。
	capabilitySet
	session           sessions.Session
	agentInfo         agents.AgentInfo
	model             models.RuntimeSnapshot
	modelRoles        resolvedModelRoles
	baseInstruction   string
	toolTokenEstimate int
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
		Tools:             base.executable,
		Messages:          providerMessages,
		Workspace:         base.scope.Workspace,
		Sandbox:           base.scope.Sandbox,
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
	skillSnapshot, mcpSnapshot := capabilities.skills, capabilities.mcp
	descriptors, estimateTools := capabilities.descriptors, capabilities.schemas
	toolTokens, err := r.contextEngine.EstimateTools(ctx, estimateTools)
	if err != nil {
		return resolvedContextBase{}, fmt.Errorf("估算 Tool/Skill Schema Context 占用失败: %w", err)
	}
	instruction := buildRuntimeInstruction(agentInfo.Agent.Name, agentInfo.Agent.Instruction, agentWorkspace, descriptors, time.Now())
	instruction, err = r.withUserPreferences(ctx, instruction)
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
		capabilitySet:     capabilities,
		session:           session,
		agentInfo:         agentInfo,
		model:             modelSnapshot,
		modelRoles:        modelRoles,
		baseInstruction:   instruction,
		toolTokenEstimate: toolTokens,
	}, nil
}

func runtimeManifestFromBase(base resolvedContextBase) RuntimeManifest {
	imageModelID := base.modelRoles.imageModelID
	return RuntimeManifest{
		Extensions:        base.summaries,
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
		ExposedToolNames: base.toolNames(),
		Workspace: RuntimeWorkspaceManifest{
			Mode:    string(base.scope.Workspace.Mode),
			RootDir: base.scope.Workspace.RootDir,
		},
		Sandbox: RuntimeSandboxManifest{
			Profile:       string(base.scope.Sandbox.Profile),
			NetworkMode:   string(base.scope.Sandbox.NetworkMode),
			NativeMode:    string(base.scope.Sandbox.NativeMode),
			NativeBackend: base.scope.Sandbox.Capability.Backend,
			NativeReady:   base.scope.Sandbox.Capability.Available,
		},
	}
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
