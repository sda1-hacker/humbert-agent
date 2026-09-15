package agents

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func TestNarrowAgentCommandsPreserveUnrelatedConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	transcripts, err := transcript.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, root, transcripts)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	original := Agent{
		ID: "agent-contract", Name: "Old", Instruction: "old instruction", ModelID: "old-model",
		EnabledSkills: []string{"skill-a"}, EnabledBuiltinTools: []string{"read_file"},
		Sandbox:       sandbox.AgentPolicy{Profile: sandbox.ProfileStandard},
		WorkspaceMode: workspace.ModeCustom, WorkspacePath: "/legacy/workspace",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(ctx, original); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, nil, nil)

	profile, err := service.UpdateProfile(ctx, original.ID, "New", "new instruction")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Agent.Name != "New" || profile.Agent.Instruction != "new instruction" {
		t.Fatalf("profile not updated: %#v", profile.Agent)
	}
	assertAgentPreserved(t, profile.Agent, original, "profile", map[string]bool{"Name": true, "Instruction": true, "UpdatedAt": true})

	beforeModel := profile.Agent
	model, err := service.SetModel(ctx, original.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if model.Agent.ModelID != "" {
		t.Fatalf("model not cleared: %q", model.Agent.ModelID)
	}
	assertAgentPreserved(t, model.Agent, beforeModel, "model", map[string]bool{"ModelID": true, "UpdatedAt": true})

	beforeSkills := model.Agent
	skills, err := service.SetSkills(ctx, original.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills.Agent.EnabledSkills) != 0 {
		t.Fatalf("skills not cleared: %#v", skills.Agent.EnabledSkills)
	}
	assertAgentPreserved(t, skills.Agent, beforeSkills, "skills", map[string]bool{"EnabledSkills": true, "UpdatedAt": true})

	beforeSecurity := skills.Agent
	policy := sandbox.AgentPolicy{NetworkMode: sandbox.NetworkNone}
	security, err := service.UpdateSecurity(ctx, original.ID, []string{"write_file"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(security.Agent.EnabledBuiltinTools, []string{"write_file"}) {
		t.Fatalf("builtin tools not updated: %#v", security.Agent.EnabledBuiltinTools)
	}
	if security.Agent.Sandbox.NetworkMode != policy.NetworkMode {
		t.Fatalf("sandbox not updated: %#v", security.Agent.Sandbox)
	}
	assertAgentPreserved(t, security.Agent, beforeSecurity, "security", map[string]bool{"EnabledBuiltinTools": true, "Sandbox": true, "UpdatedAt": true})
}

func assertAgentPreserved(t *testing.T, got, before Agent, operation string, allowed map[string]bool) {
	t.Helper()
	gv, bv := reflect.ValueOf(got), reflect.ValueOf(before)
	typ := gv.Type()
	for i := 0; i < gv.NumField(); i++ {
		name := typ.Field(i).Name
		if allowed[name] {
			continue
		}
		if !reflect.DeepEqual(gv.Field(i).Interface(), bv.Field(i).Interface()) {
			t.Fatalf("%s unexpectedly changed %s: got=%#v before=%#v", operation, name, gv.Field(i).Interface(), bv.Field(i).Interface())
		}
	}
}
