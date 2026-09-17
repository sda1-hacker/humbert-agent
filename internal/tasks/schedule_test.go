package tasks

import (
	"testing"
	"time"
)

func TestNormalizeTaskInputAppliesSafeDefaults(t *testing.T) {
	_, _, status, schedule, limits, err := normalizeTaskInput(
		" daily review ",
		" summarize ",
		"",
		Schedule{},
		Limits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if status != TaskStatusActive || schedule.Type != ScheduleManual {
		t.Fatalf("unexpected defaults: status=%q schedule=%q", status, schedule.Type)
	}
	if limits.MaxDurationSeconds != defaultMaxDurationSeconds || limits.MaxModelCalls != defaultMaxModelCalls || limits.MaxToolCalls != defaultMaxToolCalls || limits.MaxAttempts != 1 || limits.RetryDelaySeconds != defaultRetryDelaySeconds {
		t.Fatalf("unexpected limits: %+v", limits)
	}
}

func TestNextOccurrenceDailyUsesConfiguredTimeZone(t *testing.T) {
	after := time.Date(2026, time.September, 17, 0, 30, 0, 0, time.UTC)
	next, err := nextOccurrence(Schedule{
		Type:      ScheduleDaily,
		TimeZone:  "Asia/Shanghai",
		TimeOfDay: "09:00",
	}, after)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.September, 17, 1, 0, 0, 0, time.UTC)
	if next == nil || !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestNextOccurrenceWeeklySelectsNextConfiguredDay(t *testing.T) {
	// 2026-09-17 is Thursday. Monday=1 and Friday=5, so Friday is next.
	after := time.Date(2026, time.September, 17, 12, 0, 0, 0, time.UTC)
	next, err := nextOccurrence(Schedule{
		Type:      ScheduleWeekly,
		TimeZone:  "UTC",
		TimeOfDay: "08:15",
		Weekdays:  []int{1, 5},
	}, after)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.September, 18, 8, 15, 0, 0, time.UTC)
	if next == nil || !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestAdvanceIntervalSkipsHistoricSlots(t *testing.T) {
	scheduled := time.Date(2026, time.September, 17, 10, 0, 0, 0, time.UTC)
	now := scheduled.Add(3*time.Hour + 5*time.Minute)
	next, err := advanceOccurrence(Schedule{
		Type:            ScheduleInterval,
		TimeZone:        "UTC",
		IntervalMinutes: 60,
	}, scheduled, now)
	if err != nil {
		t.Fatal(err)
	}
	want := scheduled.Add(4 * time.Hour)
	if next == nil || !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestNormalizeScheduleRejectsInvalidWeeklySchedule(t *testing.T) {
	_, err := normalizeSchedule(Schedule{
		Type:      ScheduleWeekly,
		TimeZone:  "UTC",
		TimeOfDay: "09:00",
	})
	if err == nil {
		t.Fatal("expected missing weekdays to be rejected")
	}
}

func TestNextOccurrenceDailySkipsNonexistentDSTWallClock(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	// 2026-03-08 jumps from 01:59 to 03:00 in New York, so 02:30 does not exist.
	after := time.Date(2026, time.March, 7, 3, 0, 0, 0, location)
	next, err := nextOccurrence(Schedule{
		Type: ScheduleDaily, TimeZone: "America/New_York", TimeOfDay: "02:30",
	}, after)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.March, 9, 2, 30, 0, 0, location).UTC()
	if next == nil || !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}
