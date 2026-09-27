package tools_test

import (
	"context"
	"errors"
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
