package builtin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/tasks"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

type fakeTaskScheduler struct {
	input  tasks.CreateInput
	active bool
}

func (f *fakeTaskScheduler) Create(_ context.Context, input tasks.CreateInput) (tasks.Task, error) {
	f.input = input
	next := time.Now().Add(time.Hour)
	return tasks.Task{ID: "123e4567-e89b-12d3-a456-426614174000", Name: input.Name, Status: tasks.TaskStatusActive, NextRunAt: &next}, nil
}
func (f *fakeTaskScheduler) ActiveRunForSession(context.Context, string) (tasks.Run, bool) {
	return tasks.Run{}, f.active
}

func TestScheduleTaskBindsCurrentAgentAndChatOrigin(t *testing.T) {
	scheduler := &fakeTaskScheduler{}
	factory, err := NewScheduleTaskFactory(scheduler)
	if err != nil {
		t.Fatal(err)
	}
	scope := humberttools.Scope{AgentID: "agent-one", SessionID: "session-one"}
	if _, err := factory.Build(context.Background(), scope); err != nil {
		t.Fatalf("Eino tool schema: %v", err)
	}
	input := &ScheduleTaskInput{Name: "晨间提醒", Prompt: "喝水", Execution: "notification", ScheduleType: "once", TimeZone: "UTC", RunAt: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)}
	output, err := factory.run(context.Background(), scope, input)
	if err != nil {
		t.Fatal(err)
	}
	if output.TaskID == "" || scheduler.input.AgentID != scope.AgentID || scheduler.input.Origin != "chat" || scheduler.input.OriginRef != scope.SessionID || scheduler.input.Execution != tasks.ExecutionNotification {
		t.Fatalf("unexpected task: %#v output=%#v", scheduler.input, output)
	}
	scheduler.active = true
	if _, err := factory.run(context.Background(), scope, input); err == nil {
		t.Fatal("background task must not schedule another task")
	}
}

func TestScheduleTaskRejectsPastAndMismatchedTimeZone(t *testing.T) {
	base := ScheduleTaskInput{ScheduleType: "once", TimeZone: "Asia/Shanghai", RunAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}
	if _, err := scheduleFromToolInput(base); err == nil || !strings.Contains(err.Error(), "时区") {
		t.Fatalf("mismatch error=%v", err)
	}
	base.TimeZone = "UTC"
	base.RunAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := scheduleFromToolInput(base); err == nil {
		t.Fatal("past run time accepted")
	}
}
