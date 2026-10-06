package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

type catalogReadCounter struct{ reads int }

func (f *catalogReadCounter) Descriptor() humberttools.Descriptor {
	f.reads++
	return humberttools.Descriptor{Name: "read_file", Risk: humberttools.RiskRead}
}

func (*catalogReadCounter) Build(context.Context, humberttools.Scope) (einotool.InvokableTool, error) {
	return nil, errors.New("配置列表不能创建工具执行实例")
}

type catalogAuthorizer struct{}

func (catalogAuthorizer) Evaluate(context.Context, permission.Request) (permission.Decision, error) {
	return permission.Decision{}, errors.New("配置列表不能执行授权")
}

// 断言实际 Registry 读取次数不随 Agent 数量增长，并防止共享展示目录变成共享可写 DTO。
// 还检查下一次请求会读取新策略，不能用常驻缓存把设置更新隐藏掉。
func TestAgentListSharesRequestCatalogButNotMutableDTOs(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	transcripts, err := transcript.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := agents.NewStore(ctx, root, transcripts)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := humberttools.NewRegistry(catalogAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	factory := &catalogReadCounter{}
	if err := registry.Register(factory); err != nil {
		t.Fatal(err)
	}
	policy := sandbox.Config{DefaultProfile: sandbox.ProfileStandard, CommandGracePeriod: time.Second}
	manager, err := sandbox.NewManager(policy, root)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAgentService(AgentDependencies{
		Agents: agents.NewService(store, nil, logging.NewBootstrap()),
		Tools:  registry, Sandbox: manager, Config: &config.Config{},
	})
	factory.reads = 0
	values, err := service.ListAgents()
	if err != nil || values == nil || len(values) != 0 || factory.reads != 0 {
		t.Fatalf("空列表不应生成目录: values=%v reads=%d err=%v", values, factory.reads, err)
	}
	for i := 1; i <= 3; i++ {
		if err := store.Create(ctx, agents.Agent{ID: fmt.Sprintf("agent-%d", i), Name: "Agent", WorkspaceMode: workspace.ModeCustom, WorkspacePath: root}); err != nil {
			t.Fatal(err)
		}
		factory.reads = 0
		values, err = service.ListAgents()
		if err != nil {
			t.Fatal(err)
		}
		if len(values) != i || factory.reads > 2 {
			t.Fatalf("列表重复生成共享目录: agents=%d reads=%d", len(values), factory.reads)
		}
	}
	values[0].AvailableBuiltinTools[0].Label = "modified"
	if values[1].AvailableBuiltinTools[0].Label == "modified" {
		t.Fatal("不同 Agent 的 DTO 共享可写工具切片")
	}
	policy.DefaultProfile = sandbox.ProfileWorkspaceOnly
	if err := manager.UpdateConfig(policy); err != nil {
		t.Fatal(err)
	}
	values, err = service.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.SandboxStatus.DefaultProfile != string(sandbox.ProfileWorkspaceOnly) {
			t.Fatal("新请求仍显示旧 Sandbox 默认策略")
		}
	}
}
