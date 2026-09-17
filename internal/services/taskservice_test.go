package services

import (
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/tasks"
)

func TestTaskRunSessionIDsReturnsUniqueNonEmptyIDs(t *testing.T) {
	runs := []tasks.Run{
		{SessionID: " session-1 "},
		{SessionID: ""},
		{SessionID: "session-1"},
		{SessionID: "session-2"},
	}
	got := taskRunSessionIDs(runs)
	if len(got) != 2 || got[0] != "session-1" || got[1] != "session-2" {
		t.Fatalf("taskRunSessionIDs() = %#v", got)
	}
}

func TestScheduleFromDTOParsesOnceWallClockInSelectedTimeZone(t *testing.T) {
	value, err := scheduleFromDTO(TaskScheduleDTO{
		Type:          "once",
		TimeZone:      "Asia/Shanghai",
		RunAt:         "2026-09-17T09:30",
		MisfirePolicy: "run_once",
		OverlapPolicy: "skip",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.September, 17, 1, 30, 0, 0, time.UTC)
	if value.RunAt == nil || !value.RunAt.Equal(want) {
		t.Fatalf("run_at=%v want=%v", value.RunAt, want)
	}
}

func TestScheduleFromDTOStillAcceptsRFC3339(t *testing.T) {
	value, err := scheduleFromDTO(TaskScheduleDTO{
		Type:          "once",
		TimeZone:      "Asia/Shanghai",
		RunAt:         "2026-09-17T09:30:00+09:00",
		MisfirePolicy: "run_once",
		OverlapPolicy: "skip",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.September, 17, 0, 30, 0, 0, time.UTC)
	if value.RunAt == nil || !value.RunAt.Equal(want) {
		t.Fatalf("run_at=%v want=%v", value.RunAt, want)
	}
}

func TestScheduleFromDTORejectsNonexistentDSTWallClock(t *testing.T) {
	_, err := scheduleFromDTO(TaskScheduleDTO{
		Type:          "once",
		TimeZone:      "America/New_York",
		RunAt:         "2026-03-08T02:30",
		MisfirePolicy: "run_once",
		OverlapPolicy: "skip",
	})
	if err == nil {
		t.Fatal("expected nonexistent wall-clock to be rejected")
	}
}
