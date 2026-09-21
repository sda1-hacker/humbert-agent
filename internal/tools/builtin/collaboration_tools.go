package builtin

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/collaboration"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

type collaborationToolFactory struct {
	name    string
	manager *collaboration.Manager
}

type ListAgentsInput struct{}
type ListAgentsOutput struct {
	Agents []collaboration.AgentSummary `json:"agents"`
}
type RunAgentInput struct {
	ChildAgentID string `json:"child_agent_id" jsonschema:"description=The ID of the configured specialist Agent to call."`
	Task         string `json:"task" jsonschema:"description=A self-contained task including all context constraints and expected output the child needs."`
}

func NewListAgentsFactory(manager *collaboration.Manager) (humberttools.Factory, error) {
	return newCollaborationToolFactory(collaboration.ListAgentsToolName, manager)
}

func NewRunAgentFactory(manager *collaboration.Manager) (humberttools.Factory, error) {
	return newCollaborationToolFactory(collaboration.RunAgentToolName, manager)
}

func newCollaborationToolFactory(name string, manager *collaboration.Manager) (humberttools.Factory, error) {
	if manager == nil {
		return nil, errors.New("Collaboration Manager 不能为空")
	}
	return &collaborationToolFactory{name: name, manager: manager}, nil
}

func (f *collaborationToolFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: f.name, Risk: humberttools.RiskRead}
}

func (f *collaborationToolFactory) Build(_ context.Context, scope humberttools.Scope) (tool.InvokableTool, error) {
	switch f.name {
	case collaboration.ListAgentsToolName:
		return utils.InferTool(f.name, "List other configured specialist Agents that can be called for a focused subtask.", func(ctx context.Context, _ *ListAgentsInput) (*ListAgentsOutput, error) {
			values, err := f.manager.ListAgents(ctx, scope.AgentID)
			if err != nil {
				return nil, err
			}
			return &ListAgentsOutput{Agents: values}, nil
		})
	case collaboration.RunAgentToolName:
		return utils.InferTool(f.name, "Run one configured specialist Agent synchronously with isolated context. Wait for its result, then evaluate and synthesize it before answering the user.", func(ctx context.Context, input *RunAgentInput) (*collaboration.RunOutput, error) {
			if input == nil {
				return nil, errors.New("run_agent 输入不能为空")
			}
			result, err := f.manager.RunAgent(ctx, collaboration.RunInput{ChildAgentID: input.ChildAgentID, Task: input.Task, ParentScope: scope})
			if err != nil {
				return nil, err
			}
			return &result, nil
		})
	default:
		return nil, errors.New("未知 Collaboration Tool")
	}
}
