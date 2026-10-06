package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/collaboration"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// buildChatModelAgent 是主/子 Agent 共用的 Eino 构造入口。
// 顺序执行、工具生命周期、模型计账和最大迭代数在这里统一，继续由 Eino 完成推理循环。
func buildChatModelAgent(ctx context.Context, snapshot *Snapshot, description string) (*adk.ChatModelAgent, error) {
	iterations := snapshot.MaxIterations
	if iterations <= 0 {
		iterations = defaultAgentMaxIterations
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		MaxIterations: iterations,
		Name:          snapshot.AgentName, Description: description,
		Instruction: snapshot.Instruction,
		Model:       trackModel(snapshot.Model, snapshot),
		Handlers:    append([]adk.ChatModelAgentMiddleware(nil), snapshot.AgentHandlers...),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: snapshot.Tools, ExecuteSequentially: true, ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: buildToolLifecycleMiddleware(snapshot)}}},
		},
	})
}

// buildAgentHandlers 固定“Skills → Eino Reduction → Context Compaction”的处理顺序。
// 子 Agent 使用独立的 Context 标识，但辅助模型调用仍计入父运行预算。
func (r *Resolver) buildAgentHandlers(ctx context.Context, accounting *Snapshot, roles resolvedModelRoles,
	skillSnapshot skills.RuntimeSnapshot, scope humberttools.Scope, names []string, budget contextengine.Budget,
	toolTokens int, contextID, instruction string, providerType models.ProviderType,
) ([]adk.ChatModelAgentMiddleware, *contextengine.MidRunCompactor, error) {
	compactModel := compactionModel(roles)
	handler, err := r.contextEngine.NewMidRunHandler(contextID, instruction,
		trackAuxiliaryModel(compactModel, modelRoleUtility, accounting), compactModel.ContextWindow,
		compactModel.MaxOutputTokens, budget, toolTokens, reasoningReplayPolicyForProvider(providerType))
	if err != nil {
		return nil, nil, fmt.Errorf("创建 MidRun Context Middleware 失败: %w", err)
	}
	handlers := make([]adk.ChatModelAgentMiddleware, 0, 3)
	if skillSnapshot.Enabled() {
		handlers = append(handlers, skillSnapshot.Middleware())
	}
	reduction, err := r.capabilities.tools.Reduction(ctx, scope, names, budget.SoftThresholdTokens*3/4, handler.Count)
	if err != nil {
		return nil, nil, err
	}
	return append(handlers, reduction, handler), handler, nil
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
	exposedNames := capabilities.toolNames()
	if err := validateToolCapability(modelSnapshot, modelRoleChat, exposedNames); err != nil {
		return collaboration.BuiltAgent{}, err
	}

	instruction := buildRuntimeInstruction(childInfo.Agent.Name, childInfo.Agent.Instruction, input.ParentScope.Workspace, descriptors, time.Now())
	instruction, err = r.withUserPreferences(ctx, instruction)
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

// buildToolLifecycleMiddleware 负责实时 Tool 生命周期，并把可恢复的 Tool 失败转换成标准
// ToolResult 交回模型。只有 Interrupt 与整个 Turn 的 context 取消继续向 Eino 上抛。
//
// 这种语义让 web_fetch 网络超时、远程服务失败、模型参数错误等局部能力故障不会直接
// 杀死 Agent Turn；模型仍能读取失败原因并选择 install_skill、web_search 或其它替代策略。
func buildToolLifecycleMiddleware(snapshot *Snapshot) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			if input == nil {
				return next(ctx, input)
			}
			if snapshot != nil && snapshot.limitState != nil {
				// Eino 按执行地址识别恢复调用，包括等待兄弟工具审批而再次暂停的调用。
				// 这些调用已在首次进入时预留预算；恢复只检查 Token 上限，不重复扣次数。
				wasInterrupted, _, _ := einotool.GetInterruptState[any](ctx)
				err := snapshot.limitState.checkTokens()
				if !wasInterrupted {
					err = snapshot.limitState.beforeToolCall()
				}
				if err != nil {
					return nil, err
				}
			}

			startedAt := time.Now()
			// Tool Arguments 属于不可信且可能包含密钥/文件正文的模型生成数据。实时事件只用于
			// UI Trace，不参与真正 Tool 执行，因此在离开 Runtime 边界前统一脱敏并限制长度；
			// checkpoint 与 guarded Tool 仍持有原始参数，审批恢复不会使用这里的展示字符串。
			reportToolLifecycleEvent(ctx, snapshot, Event{
				Type:          EventToolStarted,
				ToolCallID:    input.CallID,
				ToolName:      input.Name,
				ToolArguments: logging.RedactText(input.Arguments, 4096),
				OccurredAt:    startedAt.UTC().Format(time.RFC3339Nano),
			})

			// 将 ToolCall 身份放进 context。run_agent 会用它生成稳定的子运行 ID；
			// Eino 从审批 checkpoint 恢复时仍使用同一个 CallID，因此不会重复创建子运行。
			callCtx := ctx
			// 子 Agent 经由工具创建，沿调用上下文继承同一预算，审批恢复也复用原状态。
			if snapshot != nil {
				callCtx = context.WithValue(ctx, executionSnapshotContextKey{}, snapshot)
			}
			output, err := next(callCtx, input)
			duration := time.Since(startedAt).Milliseconds()
			if err != nil {
				// Eino Interrupt 是正常的 Human-in-the-loop 暂停信号，不是 Tool 失败。
				// 必须原样上抛，让 Runner 保存 checkpoint 并由 RuntimeService 发布审批事件。
				var interruptSignal *adk.InterruptSignal
				if errors.As(err, &interruptSignal) {
					return nil, err
				}

				// 整个 Turn 已经被取消/超时属于 Runtime 终止条件，不能伪装成普通 ToolResult。
				// 反过来，web_fetch 自己的 HTTP Client timeout、远程安装失败、模型参数错误等
				// 都只是“某个能力本次调用失败”，应该把错误交还给模型，让 ReAct 循环有机会
				// 调整策略，而不是因为一个外部网站超时直接终止整次聊天。
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, err
				}
				if errors.Is(err, ErrExecutionLimitExceeded) {
					return nil, err
				}

				reportToolLifecycleEvent(context.WithoutCancel(ctx), snapshot, Event{
					Type:       EventToolFailed,
					ToolCallID: input.CallID,
					ToolName:   input.Name,
					DurationMS: duration,
					Error:      logging.SafeErrorText(err, 2048),
					OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
				})

				// ToolOutput 以成功的 Eino transport 结果返回，但正文明确标记失败。这样 Eino
				// 会生成标准 Tool Message 并继续下一轮模型推理；持久化层通过固定前缀把它
				// 标记成 IsError=true，不会把失败误记为成功。
				return &compose.ToolOutput{Result: formatRecoverableToolError(err)}, nil
			}

			reportToolLifecycleEvent(ctx, snapshot, Event{
				Type:       EventToolCompleted,
				ToolCallID: input.CallID,
				ToolName:   input.Name,
				DurationMS: duration,
				OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
			})
			return output, nil
		}
	}
}

func reportToolLifecycleEvent(ctx context.Context, snapshot *Snapshot, event Event) {
	if snapshot == nil || snapshot.EventReporter == nil {
		return
	}

	event.RequestID = snapshot.RequestID
	event.RunID = snapshot.RunID
	event.SessionID = snapshot.SessionID
	event.AgentID = snapshot.AgentID
	event.ModelID = snapshot.ModelID
	event.ModelRevision = snapshot.ModelRevision
	event.ToolRevision = snapshot.ToolRevision
	snapshot.EventReporter.Report(ctx, event)
}
