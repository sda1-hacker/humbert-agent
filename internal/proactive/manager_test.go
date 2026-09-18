package proactive

import (
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/tasks"
)

func TestProactiveEventFromTaskSkipsNotificationRun(t *testing.T) {
	run := tasks.Run{ID: "run-1", TaskID: "task-1", AgentID: "agent-1", Execution: tasks.ExecutionNotification, Status: tasks.RunSucceeded}
	if _, ok := proactiveEventFromTask(tasks.Event{Type: "run.succeeded", Run: &run}); ok {
		t.Fatal("notification-only task should not produce a second proactive success notification")
	}
}

func TestProactiveEventFromTaskMarksLongRunningAndUsesTaskName(t *testing.T) {
	started := time.Now().UTC().Add(-6 * time.Minute)
	finished := time.Now().UTC()
	run := tasks.Run{
		ID: "run-1", TaskID: "task-1", AgentID: "agent-1",
		Execution: tasks.ExecutionAgent, Status: tasks.RunSucceeded,
		StartedAt: &started, FinishedAt: &finished,
	}
	task := tasks.Task{ID: "task-1", Name: "每日汇总"}
	event, ok := proactiveEventFromTask(tasks.Event{Type: "run.succeeded", Task: &task, Run: &run})
	if !ok {
		t.Fatal("expected proactive event")
	}
	if event.Kind != EventTaskLongRunningDone {
		t.Fatalf("event kind = %q, want %q", event.Kind, EventTaskLongRunningDone)
	}
	if !strings.Contains(event.Title, "每日汇总") {
		t.Fatalf("event title = %q, expected task name", event.Title)
	}
}
