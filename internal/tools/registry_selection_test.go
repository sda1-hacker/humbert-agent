package tools_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/permission"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/tools/builtin"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

type allowAuthorizer struct{}

func (allowAuthorizer) Evaluate(context.Context, permission.Request) (permission.Decision, error) {
	return permission.Decision{Action: permission.ActionAllow}, nil
}

// nil 继承全部，显式空集合禁用可选能力，禁用名单始终优先；返回名称与描述符必须对齐。
func TestRegistrySelectionKeepsNilEmptyAndDisabledSemantics(t *testing.T) {
	registry, err := humberttools.NewRegistry(allowAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	for _, factory := range []humberttools.Factory{builtin.NewUpdatePlanFactory(), builtin.NewCurrentTimeFactory()} {
		if err := registry.Register(factory); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name                    string
		enabled, disabled, want []string
	}{
		{"继承", nil, nil, []string{"get_current_time", "update_plan"}},
		{"空选择", []string{}, nil, []string{}},
		{"禁用优先", []string{"update_plan", "get_current_time"}, []string{"update_plan"}, []string{"get_current_time"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := registry.Resolve(context.Background(), humberttools.Scope{
				AgentID: "agent", Workspace: workspace.Workspace{AgentID: "agent", RootDir: t.TempDir()},
				EnabledBuiltinTools: test.enabled, DisabledBuiltinTools: test.disabled,
			})
			if err != nil || !reflect.DeepEqual(value.ToolNames, test.want) || len(value.Tools) != len(test.want) {
				t.Fatalf("选择结果错误: %#v err=%v", value, err)
			}
			for index, descriptor := range value.Descriptors {
				if descriptor.Name != value.ToolNames[index] {
					t.Fatal("工具名称与冻结描述符不一致")
				}
			}
		})
	}
}

func TestRegistryRejectsUnknownBuiltinSelection(t *testing.T) {
	registry, err := humberttools.NewRegistry(allowAuthorizer{})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := registry.Register(builtin.NewCurrentTimeFactory()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	_, err = registry.Resolve(context.Background(), humberttools.Scope{
		AgentID:             "agent",
		Workspace:           workspace.Workspace{AgentID: "agent", RootDir: t.TempDir()},
		EnabledBuiltinTools: []string{"get_current_time", "cancel_delegation"},
	})
	if !errors.Is(err, humberttools.ErrToolNotFound) {
		t.Fatalf("unknown tool accepted: %v", err)
	}
}
