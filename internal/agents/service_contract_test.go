package agents

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
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

	profile, err := service.UpdateProfile(ctx, original.ID, "New", "", "new instruction")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Agent.Name != "New" || profile.Agent.Instruction != "new instruction" {
		t.Fatalf("profile not updated: %#v", profile.Agent)
	}
	assertAgentPreserved(t, profile.Agent, original, "profile", map[string]bool{"Name": true, "Avatar": true, "Instruction": true, "UpdatedAt": true})

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

// 完整表单和局部命令必须遵守同一边界，不能通过 UpdateProfile 绕过指令上限。
// 中文名称按字符计数，与前端的 100 字符输入限制一致。
func TestProfileValidationIsSharedByAllWriteCommands(t *testing.T) {
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
	service := NewService(store, nil, logging.NewBootstrap())
	name := strings.Repeat("中", 100)
	created, err := service.Create(ctx, CreateInput{Name: name, Instruction: "original"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Agent.ID
	if _, err := service.Update(ctx, id, UpdateInput{Name: name, Instruction: "original"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateProfile(ctx, id, name, "", "original"); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ name, instruction string }{
		{strings.Repeat("中", 101), "original"},
		{"Changed", strings.Repeat("x", maxInstructionLength+1)},
	} {
		operations := []func() error{
			func() error {
				_, err := service.Create(ctx, CreateInput{Name: input.name, Instruction: input.instruction})
				return err
			},
			func() error {
				_, err := service.Update(ctx, id, UpdateInput{Name: input.name, Instruction: input.instruction})
				return err
			},
			func() error { _, err := service.UpdateProfile(ctx, id, input.name, "", input.instruction); return err },
		}
		for index, operation := range operations {
			if err := operation(); err == nil {
				t.Fatalf("write command %d accepted invalid input", index)
			}
			value, err := store.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if value.Agent.Name != name || value.Agent.Instruction != "original" {
				t.Fatalf("failed validation modified persisted Profile: %#v", value.Agent)
			}
		}
	}
	values, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("failed creation left Profile behind: %d", len(values))
	}
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

// 并发局部保存必须基于锁内最新配置，不能采用调用方预先读取的旧副本。
func TestConcurrentMutationsPreserveEveryUpdate(t *testing.T) {
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
	if err := store.Create(ctx, Agent{ID: "concurrent", Name: "Concurrent"}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 24)
	for i := 0; i < 24; i++ {
		go func() {
			<-start
			_, err := store.Mutate(ctx, "concurrent", func(agent *Agent) error { agent.Instruction += "x"; return nil })
			results <- err
		}()
	}
	close(start)
	for i := 0; i < 24; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	value, err := store.Get(ctx, "concurrent")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Agent.Instruction) != 24 {
		t.Fatalf("lost concurrent writes: %q", value.Agent.Instruction)
	}
}
