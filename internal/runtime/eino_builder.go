package runtime

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"

	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
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
	reduction, err := r.tools.Reduction(ctx, scope, names, budget.SoftThresholdTokens*3/4, handler.Count)
	if err != nil {
		return nil, nil, err
	}
	return append(handlers, reduction, handler), handler, nil
}
