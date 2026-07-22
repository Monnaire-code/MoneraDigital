// Package adaptiveschedule provides a process-local background coordination
// seam: start-scan immediately, coalesce wake signals, drain while work
// exists, then progressively back off to a configurable idle ceiling.
//
// Correctness never depends on wake delivery. Callers must keep durable work
// state in PostgreSQL; startup scans and the max idle interval recover missed
// signals. Wakes only improve latency.
package adaptiveschedule

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	DefaultMinIdle = time.Second
	DefaultMaxIdle = 10 * time.Minute
)

// CycleOutcome describes one bounded work cycle for the coordinator.
//
// Worked means the cycle processed at least one unit of durable work.
// MoreWork means more claimable work may still exist (for example a drain
// limit was hit) and the loop should re-enter immediately without idle wait.
// NextDue, when non-zero, schedules the next scan no later than that instant
// (used for business retry/timeout deadlines that must beat progressive idle).
type CycleOutcome struct {
	Worked   bool
	MoreWork bool
	NextDue  time.Time
}

// CycleFunc is one bounded, cancelable work cycle. It must not panic; the
// coordinator still recovers panics as an isolated cycle failure.
type CycleFunc func(ctx context.Context) (CycleOutcome, error)

// Config owns process-local timing only. MinIdle is the first idle wait after
// an empty cycle; MaxIdle is the progressive ceiling (default 10 minutes).
type Config struct {
	// Name is a safe log label (task kind). Never put secrets or payloads here.
	Name    string
	MinIdle time.Duration
	MaxIdle time.Duration
	// Now injects a clock for tests. Defaults to time.Now.
	Now func() time.Time
	// OnCycle is optional metadata-only instrumentation after each cycle.
	// It must not log secrets, provider payloads, or database URLs.
	OnCycle func(CycleOutcome, error, time.Duration)
}

// Loop is the coordination seam for one background task kind.
type Loop struct {
	config Config
	cycle  CycleFunc
	wake   chan struct{}

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// NormalizeConfig applies defaults and rejects non-positive or inverted idle bounds.
func NormalizeConfig(config Config) (Config, error) {
	if config.MinIdle == 0 {
		config.MinIdle = DefaultMinIdle
	}
	if config.MaxIdle == 0 {
		config.MaxIdle = DefaultMaxIdle
	}
	if config.MinIdle <= 0 || config.MinIdle.Microseconds() <= 0 {
		return Config{}, fmt.Errorf("adaptive schedule min idle must be positive")
	}
	if config.MaxIdle <= 0 || config.MaxIdle.Microseconds() <= 0 {
		return Config{}, fmt.Errorf("adaptive schedule max idle must be positive")
	}
	if config.MaxIdle < config.MinIdle {
		return Config{}, fmt.Errorf("adaptive schedule max idle must be >= min idle")
	}
	if config.Name == "" {
		config.Name = "task"
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return config, nil
}

// New constructs a Loop. It performs no I/O and does not start background work.
func New(config Config, cycle CycleFunc) (*Loop, error) {
	if cycle == nil {
		return nil, fmt.Errorf("adaptive schedule cycle function is required")
	}
	normalized, err := NormalizeConfig(config)
	if err != nil {
		return nil, err
	}
	return &Loop{
		config: normalized,
		cycle:  cycle,
		// Capacity one coalesces bursty wakes without growing memory.
		wake: make(chan struct{}, 1),
	}, nil
}

// Notify coalesces a process-local wake. It is safe before Start. True means a
// new wake was queued; false means the wake was coalesced or the loop is nil.
func (loop *Loop) Notify() bool {
	if loop == nil || loop.wake == nil {
		return false
	}
	select {
	case loop.wake <- struct{}{}:
		return true
	default:
		return false
	}
}

// Start begins the context-cancellable background loop. Multiple calls are
// safe and never start a second runner.
func (loop *Loop) Start(parent context.Context) {
	if loop == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	loop.mu.Lock()
	if loop.running {
		loop.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	loop.running = true
	loop.cancel = cancel
	loop.done = done
	loop.mu.Unlock()

	go func() {
		defer func() {
			if rv := recover(); rv != nil {
				log.Printf("adaptive schedule panic recovered: kind=%s", loop.config.Name)
			}
			loop.mu.Lock()
			if loop.done == done {
				loop.running = false
				loop.cancel = nil
				loop.done = nil
			}
			loop.mu.Unlock()
			close(done)
		}()
		loop.Run(ctx)
	}()
}

// Stop cancels a Start-managed loop and waits for exit. Safe to call repeatedly.
func (loop *Loop) Stop() {
	if loop == nil {
		return
	}
	loop.mu.Lock()
	cancel := loop.cancel
	done := loop.done
	loop.mu.Unlock()
	if cancel == nil || done == nil {
		return
	}
	cancel()
	<-done
}

// Run executes until ctx is canceled. Prefer Start for lifecycle ownership.
//
// Scheduling policy:
//  1. First cycle runs immediately (startup recovery scan).
//  2. Worked or MoreWork resets idle delay to MinIdle (MoreWork re-enters now).
//  3. Empty cycles double the idle delay until MaxIdle.
//  4. Notify interrupts the idle wait and resets delay to MinIdle after the cycle.
//  5. Cycle errors and panics are isolated; the loop continues with progressive idle.
//  6. NextDue can pull the next wake earlier than progressive idle.
func (loop *Loop) Run(ctx context.Context) {
	if loop == nil || loop.cycle == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	idle := time.Duration(0) // first cycle immediately
	for {
		if err := ctx.Err(); err != nil {
			return
		}

		// Drain pending wake before the cycle so a burst of signals collapses
		// into one scan; further wakes during the cycle still re-arm after.
		loop.drainWakes()

		started := loop.config.Now()
		outcome, err := loop.runCycle(ctx)
		elapsed := loop.config.Now().Sub(started)
		if loop.config.OnCycle != nil {
			loop.config.OnCycle(outcome, err, elapsed)
		}

		if err := ctx.Err(); err != nil {
			return
		}

		// Decide next idle delay from cycle outcome. Wake during wait shortens it.
		switch {
		case outcome.MoreWork:
			idle = 0
		case outcome.Worked:
			idle = loop.config.MinIdle
		case idle == 0:
			// Empty or error after an immediate pass: begin progressive backoff.
			idle = loop.config.MinIdle
		default:
			idle = nextIdle(idle, loop.config.MinIdle, loop.config.MaxIdle)
		}

		wait := idle
		if dueWait, ok := waitUntil(loop.config.Now(), outcome.NextDue); ok && (wait == 0 || dueWait < wait) {
			wait = dueWait
		}

		if wait <= 0 {
			continue
		}
		if !loop.wait(ctx, wait) {
			return
		}
		// A wake during idle restores responsiveness: next empty cycle still
		// progresses from MinIdle only after another empty result.
		if loop.drainWakes() {
			idle = loop.config.MinIdle
		}
	}
}

func (loop *Loop) runCycle(ctx context.Context) (outcome CycleOutcome, err error) {
	defer func() {
		if rv := recover(); rv != nil {
			// Never format rv: it may carry provider or database values.
			log.Printf("adaptive schedule cycle panic recovered: kind=%s", loop.config.Name)
			outcome = CycleOutcome{}
			err = fmt.Errorf("adaptive schedule cycle panicked")
		}
	}()
	return loop.cycle(ctx)
}

func (loop *Loop) wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	case <-loop.wake:
		return true
	}
}

func (loop *Loop) drainWakes() bool {
	drained := false
	for {
		select {
		case <-loop.wake:
			drained = true
		default:
			return drained
		}
	}
}

func nextIdle(current, minIdle, maxIdle time.Duration) time.Duration {
	if current < minIdle {
		return minIdle
	}
	if current >= maxIdle {
		return maxIdle
	}
	// Double with overflow guard.
	if current > maxIdle/2 {
		return maxIdle
	}
	next := current * 2
	if next < minIdle {
		return minIdle
	}
	if next > maxIdle {
		return maxIdle
	}
	return next
}

func waitUntil(now, due time.Time) (time.Duration, bool) {
	if due.IsZero() || !due.After(now) {
		return 0, false
	}
	return due.Sub(now), true
}

// ProcessOneFunc claims and processes at most one unit of durable work.
// processed=false means the queue is empty for this cycle.
type ProcessOneFunc func(ctx context.Context) (processed bool, err error)

// DrainProcessOne repeatedly calls process until empty, error, or drainLimit.
// drainLimit <= 0 defaults to 100. MoreWork is set when the limit is hit with
// every attempt processed, so the adaptive loop re-enters without idle wait.
func DrainProcessOne(ctx context.Context, process ProcessOneFunc, drainLimit int) (CycleOutcome, error) {
	if process == nil {
		return CycleOutcome{}, fmt.Errorf("adaptive schedule process function is required")
	}
	if drainLimit <= 0 {
		drainLimit = 100
	}
	claimed := 0
	for claimed < drainLimit {
		if err := ctx.Err(); err != nil {
			return CycleOutcome{Worked: claimed > 0}, err
		}
		processed, err := process(ctx)
		if err != nil {
			return CycleOutcome{Worked: claimed > 0}, err
		}
		if !processed {
			return CycleOutcome{Worked: claimed > 0}, nil
		}
		claimed++
	}
	return CycleOutcome{Worked: true, MoreWork: true}, nil
}

// EarliestDue returns the soonest non-zero deadline.
func EarliestDue(dues ...time.Time) time.Time {
	var earliest time.Time
	for _, due := range dues {
		if due.IsZero() {
			continue
		}
		if earliest.IsZero() || due.Before(earliest) {
			earliest = due
		}
	}
	return earliest
}
