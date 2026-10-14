package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// fixedToolDefinitions 在第一次模型请求前冻结并排序 Schema。运行中加载 Skill 正文、
// 审批恢复或模块返回不同顺序时，不重新排列或增加 tools，保持缓存前缀稳定。
type fixedToolDefinitions struct {
	adk.BaseChatModelAgentMiddleware
	infos []*schema.ToolInfo
}

func (h *fixedToolDefinitions) BeforeAgent(ctx context.Context, agent *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	if agent == nil {
		return ctx, agent, errors.New("工具快照需要 Agent Context")
	}
	infos := make([]*schema.ToolInfo, 0, len(agent.Tools))
	for _, tool := range agent.Tools {
		info, err := tool.Info(ctx)
		if err != nil {
			return ctx, agent, err
		}
		if info == nil {
			return ctx, agent, errors.New("工具 Schema 不能为空")
		}
		infos = append(infos, info)
	}
	// 一次深复制完整定义，避免模块修改原 Schema 而污染后续请求。
	data, err := json.Marshal(infos)
	if err != nil {
		return ctx, agent, err
	}
	var frozen []*schema.ToolInfo
	if err := json.Unmarshal(data, &frozen); err != nil {
		return ctx, agent, err
	}
	sort.Slice(frozen, func(i, j int) bool { return frozen[i].Name < frozen[j].Name })
	h.infos = frozen
	return ctx, agent, nil
}

func (h *fixedToolDefinitions) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state != nil {
		state.ToolInfos = append([]*schema.ToolInfo(nil), h.infos...)
	}
	return ctx, state, nil
}
