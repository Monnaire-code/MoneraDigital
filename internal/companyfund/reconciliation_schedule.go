package companyfund

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

const (
	DefaultCompanyFundReconciliationTimeZone = "Asia/Singapore"
	DefaultCompanyFundReconciliationTime     = "03:00"
	DefaultCompanyFundReconciliationCatchUp  = 7
	DefaultCompanyFundLateStatusOverlapDays  = 7
	maxCompanyFundReconciliationCatchUp      = 366
	maxCompanyFundLateStatusOverlapDays      = 366
	companyFundLateStatusWindowKeyPrefix     = "late-status:v1:"
)

var companyFundDailyTimePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// ReconciliationDailyScheduleConfig keeps all local-calendar inputs explicit.
// Empty fields select the documented UTC+8 Singapore 03:00 / seven-day
// defaults; invalid non-empty values are rejected rather than silently
// falling back to the host process time zone.
type ReconciliationDailyScheduleConfig struct {
	TimeZone    string
	DailyTime   string
	CatchUpDays int
}

type ReconciliationDailySchedule struct {
	location     *time.Location
	locationName string
	hour         int
	minute       int
	catchUpDays  int
}

// CompanyFundReconciliationWindow is a UTC [start,end) interval for one
// natural day in the configured local timezone. Key is the local date, so it
// remains stable even when a timezone observes daylight-saving changes.
type CompanyFundReconciliationWindow struct {
	Key   string
	Start time.Time
	End   time.Time
}

func NewReconciliationDailySchedule(config ReconciliationDailyScheduleConfig) (*ReconciliationDailySchedule, error) {
	locationName := config.TimeZone
	if locationName == "" {
		locationName = DefaultCompanyFundReconciliationTimeZone
	}
	if locationName == "Local" {
		return nil, fmt.Errorf("company-fund reconciliation timezone must not use host-local time")
	}
	location, err := time.LoadLocation(locationName)
	if err != nil {
		return nil, fmt.Errorf("invalid company-fund reconciliation timezone %q: %w", locationName, err)
	}

	dailyTime := config.DailyTime
	if dailyTime == "" {
		dailyTime = DefaultCompanyFundReconciliationTime
	}
	matches := companyFundDailyTimePattern.FindStringSubmatch(dailyTime)
	if matches == nil {
		return nil, fmt.Errorf("invalid company-fund reconciliation daily time %q; expected strict HH:MM", dailyTime)
	}
	hour, _ := strconv.Atoi(matches[1])
	minute, _ := strconv.Atoi(matches[2])

	catchUpDays := config.CatchUpDays
	if catchUpDays == 0 {
		catchUpDays = DefaultCompanyFundReconciliationCatchUp
	}
	if catchUpDays < 1 || catchUpDays > maxCompanyFundReconciliationCatchUp {
		return nil, fmt.Errorf("company-fund reconciliation catch-up days must be between 1 and %d", maxCompanyFundReconciliationCatchUp)
	}
	return &ReconciliationDailySchedule{
		location:     location,
		locationName: locationName,
		hour:         hour,
		minute:       minute,
		catchUpDays:  catchUpDays,
	}, nil
}

func (schedule *ReconciliationDailySchedule) LocationName() string {
	if schedule == nil {
		return ""
	}
	return schedule.locationName
}

func (schedule *ReconciliationDailySchedule) DailyTime() string {
	if schedule == nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", schedule.hour, schedule.minute)
}

// DueWindows returns chronological independent daily windows. Before today's
// configured local run time, the newest eligible window is the day before
// yesterday; at/after that time it is yesterday. This prevents an early
// scheduler invocation from silently bypassing the configured trigger.
func (schedule *ReconciliationDailySchedule) DueWindows(now time.Time) ([]CompanyFundReconciliationWindow, error) {
	latestDay, err := schedule.latestEligibleLocalDay(now)
	if err != nil {
		return nil, err
	}

	windows := make([]CompanyFundReconciliationWindow, 0, schedule.catchUpDays)
	for offset := schedule.catchUpDays - 1; offset >= 0; offset-- {
		day := latestDay.AddDate(0, 0, -offset)
		end := day.AddDate(0, 0, 1)
		windows = append(windows, CompanyFundReconciliationWindow{
			Key:   day.Format("2006-01-02"),
			Start: day.UTC(),
			End:   end.UTC(),
		})
	}
	return windows, nil
}

// LateStatusWindows returns one independently keyed rolling overlap after the
// normal daily compensation pass. Its key is stable for the current eligible
// local date rather than by invocation time, so a minute-based runtime tick
// cannot create an unbounded series of sync runs. On the next eligible day a
// new key is opened and the rolling range still includes prior days; this is
// what re-observes a D transaction when it reaches a terminal status on D+2.
// Account-specific reconcilers append their configured-account identity before
// opening the durable run because multiple company accounts share a channel.
// Passing zero explicitly disables this repair pass.
func (schedule *ReconciliationDailySchedule) LateStatusWindows(now time.Time, overlapDays int) ([]CompanyFundReconciliationWindow, error) {
	if overlapDays == 0 {
		return nil, nil
	}
	if overlapDays < 0 || overlapDays > maxCompanyFundLateStatusOverlapDays {
		return nil, fmt.Errorf("company-fund late-status overlap days must be between 0 and %d", maxCompanyFundLateStatusOverlapDays)
	}
	latestDay, err := schedule.latestEligibleLocalDay(now)
	if err != nil {
		return nil, err
	}
	return []CompanyFundReconciliationWindow{{
		Key:   companyFundLateStatusWindowKeyPrefix + latestDay.Format("2006-01-02"),
		Start: latestDay.AddDate(0, 0, -(overlapDays - 1)).UTC(),
		End:   latestDay.AddDate(0, 0, 1).UTC(),
	}}, nil
}

func (schedule *ReconciliationDailySchedule) latestEligibleLocalDay(now time.Time) (time.Time, error) {
	if schedule == nil || schedule.location == nil {
		return time.Time{}, fmt.Errorf("company-fund reconciliation schedule is not configured")
	}
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("company-fund reconciliation schedule requires a non-zero current time")
	}
	localNow := now.In(schedule.location)
	todayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, schedule.location)
	todayRunAt := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), schedule.hour, schedule.minute, 0, 0, schedule.location)
	latestDay := todayStart.AddDate(0, 0, -1)
	if localNow.Before(todayRunAt) {
		latestDay = latestDay.AddDate(0, 0, -1)
	}
	return latestDay, nil
}

// SyncRunInputs turns due local calendar windows into the exact immutable
// input accepted by CompanyFundSyncRunStore. Safeheron and Airwallex callers
// use distinct SyncKind values, while each channel/day remains independently
// idempotent under the database unique key.
func (schedule *ReconciliationDailySchedule) SyncRunInputs(now time.Time, channel Channel, syncKind string) ([]CompanyFundSyncRunInput, error) {
	if !channel.Valid() {
		return nil, fmt.Errorf("unsupported reconciliation channel %q", channel)
	}
	if err := validateRequiredString("company-fund sync kind", syncKind, maxCompanyFundSyncKindBytes); err != nil {
		return nil, err
	}
	windows, err := schedule.DueWindows(now)
	if err != nil {
		return nil, err
	}
	inputs := make([]CompanyFundSyncRunInput, 0, len(windows))
	for _, window := range windows {
		inputs = append(inputs, CompanyFundSyncRunInput{
			Channel:     channel,
			SyncKind:    syncKind,
			WindowKey:   window.Key,
			WindowStart: window.Start,
			WindowEnd:   window.End,
		})
	}
	return inputs, nil
}
