package adaptiveschedule

import (
	"sync"
	"time"
)

// DefaultMaintenanceOpenFor is the grace period after a shared window opens so
// all pure-fallback scanners started in the same process can run once without
// phase-skewing into separate MaxIdle clocks.
const DefaultMaintenanceOpenFor = 5 * time.Second

// MaintenanceWindow is the process-level gate for pure fallback database scans.
//
// MaxIdle is an aggregate quiet budget for the whole MD process: when no
// business wake, backlog drain, or real NextDue forces work, scanners must
// share one open window instead of each running on an independent 10-minute
// phase. Business signals and real NextDue still bypass the gate.
type MaintenanceWindow struct {
	maxIdle time.Duration
	openFor time.Duration
	now     func() time.Time

	mu        sync.Mutex
	openUntil time.Time
	nextOpen  time.Time
}

// NewMaintenanceWindow builds a shared gate. maxIdle <= 0 defaults to DefaultMaxIdle.
func NewMaintenanceWindow(maxIdle time.Duration) *MaintenanceWindow {
	if maxIdle <= 0 {
		maxIdle = DefaultMaxIdle
	}
	return &MaintenanceWindow{
		maxIdle: maxIdle,
		openFor: defaultMaintenanceOpenFor(maxIdle),
		now:     time.Now,
	}
}

// SetOpenFor overrides the in-window grace period (tests).
func (m *MaintenanceWindow) SetOpenFor(d time.Duration) {
	if m == nil || d <= 0 {
		return
	}
	m.mu.Lock()
	m.openFor = d
	m.mu.Unlock()
}

// SetNow injects a clock (tests).
func (m *MaintenanceWindow) SetNow(now func() time.Time) {
	if m == nil || now == nil {
		return
	}
	m.mu.Lock()
	m.now = now
	m.mu.Unlock()
}

// MaxIdle returns the configured aggregate quiet budget.
func (m *MaintenanceWindow) MaxIdle() time.Duration {
	if m == nil {
		return DefaultMaxIdle
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxIdle <= 0 {
		return DefaultMaxIdle
	}
	return m.maxIdle
}

// Allow reports whether a pure-fallback scan may touch the database now.
// When the window opens, nextOpen advances by MaxIdle so subsequent empty
// scans align on the same quiet interval.
//
// The second return is the next time the window will be open if currently
// closed (or the end of the current quiet budget when open).
func (m *MaintenanceWindow) Allow(now time.Time) (allowed bool, nextOpen time.Time) {
	if m == nil {
		return true, time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxIdle <= 0 {
		m.maxIdle = DefaultMaxIdle
	}
	if m.openFor <= 0 {
		m.openFor = defaultMaintenanceOpenFor(m.maxIdle)
	}
	if now.IsZero() {
		if m.now != nil {
			now = m.now()
		} else {
			now = time.Now()
		}
	}

	// Still inside the open grace period: all participants may run.
	if !m.openUntil.IsZero() && now.Before(m.openUntil) {
		return true, m.nextOpen
	}
	// Open a new window when due (including first call: nextOpen zero).
	if m.nextOpen.IsZero() || !now.Before(m.nextOpen) {
		m.openUntil = now.Add(m.openFor)
		m.nextOpen = now.Add(m.maxIdle)
		return true, m.nextOpen
	}
	return false, m.nextOpen
}

// Next returns when the next pure-fallback window opens (zero if unrestricted).
func (m *MaintenanceWindow) Next(now time.Time) time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now.IsZero() {
		if m.now != nil {
			now = m.now()
		} else {
			now = time.Now()
		}
	}
	if !m.openUntil.IsZero() && now.Before(m.openUntil) {
		return time.Time{} // currently open: no future gate
	}
	if m.nextOpen.IsZero() || !now.Before(m.nextOpen) {
		return time.Time{}
	}
	return m.nextOpen
}

func defaultMaintenanceOpenFor(maxIdle time.Duration) time.Duration {
	if maxIdle <= 0 {
		return DefaultMaintenanceOpenFor
	}
	// Keep grace short relative to the quiet budget so tests with small MaxIdle
	// still observe a long zero-query gap, while production (10m) gets ~5s.
	if maxIdle < 5*DefaultMaintenanceOpenFor {
		open := maxIdle / 5
		if open < time.Millisecond {
			return time.Millisecond
		}
		return open
	}
	return DefaultMaintenanceOpenFor
}

// Process-wide maintenance window (optional). Loops without an explicit
// SharedMaintenance config attach to this gate so production can align all
// pure-fallback scanners with one SetProcessMaintenance call.
var (
	processMaintenanceMu sync.RWMutex
	processMaintenance   *MaintenanceWindow
)

// SetProcessMaintenance installs the process-wide shared maintenance gate.
// Pass nil to clear (tests).
func SetProcessMaintenance(window *MaintenanceWindow) {
	processMaintenanceMu.Lock()
	processMaintenance = window
	processMaintenanceMu.Unlock()
}

// ProcessMaintenance returns the process-wide gate, or nil if unset.
func ProcessMaintenance() *MaintenanceWindow {
	processMaintenanceMu.RLock()
	defer processMaintenanceMu.RUnlock()
	return processMaintenance
}
