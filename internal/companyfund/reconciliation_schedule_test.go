package companyfund

import (
	"testing"
	"time"
)

func TestReconciliationDailySchedule_DefaultsToSingaporeUTCPlus8AndBuildsIndependentDailyWindows(t *testing.T) {
	schedule, err := NewReconciliationDailySchedule(ReconciliationDailyScheduleConfig{})
	if err != nil {
		t.Fatalf("NewReconciliationDailySchedule() error = %v", err)
	}
	if schedule.LocationName() != DefaultCompanyFundReconciliationTimeZone || schedule.DailyTime() != "03:00" {
		t.Fatalf("default schedule = timezone %q time %q", schedule.LocationName(), schedule.DailyTime())
	}
	now := time.Date(2026, time.July, 10, 3, 0, 0, 0, schedule.location)
	windows, err := schedule.DueWindows(now)
	if err != nil {
		t.Fatalf("DueWindows() error = %v", err)
	}
	if len(windows) != 7 || windows[0].Key != "2026-07-03" || windows[len(windows)-1].Key != "2026-07-09" {
		t.Fatalf("due window keys = %#v", windows)
	}
	latest := windows[len(windows)-1]
	if want := time.Date(2026, time.July, 8, 16, 0, 0, 0, time.UTC); !latest.Start.Equal(want) {
		t.Fatalf("latest start = %s, want %s", latest.Start, want)
	}
	if want := time.Date(2026, time.July, 9, 16, 0, 0, 0, time.UTC); !latest.End.Equal(want) {
		t.Fatalf("latest end = %s, want %s", latest.End, want)
	}
	inputs, err := schedule.SyncRunInputs(now, ChannelSafeheron, "DAILY_RECONCILIATION")
	if err != nil || len(inputs) != len(windows) || inputs[len(inputs)-1].WindowKey != latest.Key || !inputs[len(inputs)-1].WindowEnd.Equal(latest.End) {
		t.Fatalf("SyncRunInputs() = %#v, %v", inputs, err)
	}
}

func TestReconciliationDailySchedule_BeforeLocalRunDoesNotBypassConfiguredTime(t *testing.T) {
	schedule, err := NewReconciliationDailySchedule(ReconciliationDailyScheduleConfig{CatchUpDays: 1})
	if err != nil {
		t.Fatalf("NewReconciliationDailySchedule() error = %v", err)
	}
	beforeRun := time.Date(2026, time.July, 10, 2, 59, 59, 0, schedule.location)
	windows, err := schedule.DueWindows(beforeRun)
	if err != nil || len(windows) != 1 || windows[0].Key != "2026-07-08" {
		t.Fatalf("DueWindows(before configured time) = %#v, %v", windows, err)
	}
}

func TestReconciliationDailySchedule_NextTriggerAtUsesConfiguredLocalDailyTime(t *testing.T) {
	schedule, err := NewReconciliationDailySchedule(ReconciliationDailyScheduleConfig{})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, time.July, 10, 2, 0, 0, 0, schedule.location)
	got, err := schedule.NextTriggerAt(before)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.July, 10, 3, 0, 0, 0, schedule.location).UTC()
	if !got.Equal(want) {
		t.Fatalf("NextTriggerAt(before) = %s, want %s", got, want)
	}
	after := time.Date(2026, time.July, 10, 3, 0, 0, 0, schedule.location)
	got, err = schedule.NextTriggerAt(after)
	if err != nil {
		t.Fatal(err)
	}
	want = time.Date(2026, time.July, 11, 3, 0, 0, 0, schedule.location).UTC()
	if !got.Equal(want) {
		t.Fatalf("NextTriggerAt(after) = %s, want %s", got, want)
	}
}

func TestReconciliationDailySchedule_LateStatusOverlapUsesStableVersionedLocalDateKeys(t *testing.T) {
	schedule, err := NewReconciliationDailySchedule(ReconciliationDailyScheduleConfig{CatchUpDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 10, 3, 1, 0, 0, schedule.location)
	windows, err := schedule.LateStatusWindows(now, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || windows[0].Key != "late-status:v1:2026-07-09" {
		t.Fatalf("late-status windows = %#v", windows)
	}
	if !windows[0].Start.Equal(time.Date(2026, time.July, 6, 16, 0, 0, 0, time.UTC)) ||
		!windows[0].End.Equal(time.Date(2026, time.July, 9, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("late-status UTC boundaries = %#v", windows)
	}
	nextDay, err := schedule.LateStatusWindows(now.AddDate(0, 0, 1), 3)
	if err != nil || len(nextDay) != 1 || nextDay[0].Key != "late-status:v1:2026-07-10" ||
		!nextDay[0].Start.Equal(time.Date(2026, time.July, 7, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("next late-status overlap = %#v, %v", nextDay, err)
	}
	if disabled, err := schedule.LateStatusWindows(now, 0); err != nil || len(disabled) != 0 {
		t.Fatalf("LateStatusWindows(disabled) = %#v, %v", disabled, err)
	}
}

func TestReconciliationDailySchedule_StrictlyRejectsInvalidTimeZoneTimeAndCatchup(t *testing.T) {
	for _, config := range []ReconciliationDailyScheduleConfig{
		{TimeZone: "Local"},
		{TimeZone: "UTC+8"},
		{TimeZone: "Mars/Olympus"},
		{DailyTime: "3:00"},
		{DailyTime: "24:00"},
		{DailyTime: "03:60"},
		{CatchUpDays: -1},
		{CatchUpDays: maxCompanyFundReconciliationCatchUp + 1},
	} {
		if _, err := NewReconciliationDailySchedule(config); err == nil {
			t.Fatalf("invalid schedule config %#v unexpectedly accepted", config)
		}
	}
}
