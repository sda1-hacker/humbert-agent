package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
)

type skillUsersStub struct {
	users []agents.AgentInfo
	err   error
}

func (s *skillUsersStub) AgentsUsingSkill(context.Context, string) ([]agents.AgentInfo, error) {
	return s.users, s.err
}

type skillRemoverStub struct{ removed bool }

func (s *skillRemoverStub) Remove(context.Context, string) error { s.removed = true; return nil }

func TestSkillMaintenanceCannotBypassAgentReferences(t *testing.T) {
	users := &skillUsersStub{users: []agents.AgentInfo{{Agent: agents.Agent{Name: "Demo"}}}}
	remover := &skillRemoverStub{}
	usecase := NewSkillMaintenance(users, remover)
	if err := usecase.Remove(context.Background(), "demo"); err == nil || remover.removed {
		t.Fatal("removed a referenced skill")
	}
	users.users = nil
	users.err = errors.New("reference store unavailable")
	if err := usecase.Remove(context.Background(), "demo"); err == nil || remover.removed {
		t.Fatal("removed a skill with unknown references")
	}
	users.err = nil
	if err := usecase.Remove(context.Background(), "demo"); err != nil || !remover.removed {
		t.Fatalf("unreferenced skill removal failed: %v", err)
	}
}
