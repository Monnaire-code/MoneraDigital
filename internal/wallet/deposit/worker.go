package deposit

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"monera-digital/internal/adaptiveschedule"
)

// WorkerConfig controls adaptive drain + risk follow-up scheduling.
type WorkerConfig struct {
	// Interval is the minimum idle wait after an empty drain (default 1s).
	Interval time.Duration
	// MaxIdle is the progressive idle ceiling (default 10m).
	MaxIdle time.Duration
	// KYTScanInterval is retained for config compatibility; risk scans are
	// scheduled from durable updated_at + KYT_TIMEOUT, not this cadence.
	KYTScanInterval time.Duration
	// AMLPollInterval re-arms AML after a still-pending or retryable scan
	// (default 60s). First poll uses AML_FIRST_POLL_DELAY from DB anchors.
	AMLPollInterval time.Duration
	// PanicBackoff pauses after a panic before the next cycle (default 5s).
	PanicBackoff time.Duration
}

// Worker drains PENDING webhook events through Service.ProcessOne and runs
// KYT/AML follow-up from durable DB due times with adaptive idle backoff.
type Worker struct {
	svc    *Service
	config WorkerConfig

	mu   sync.Mutex
	loop *adaptiveschedule.Loop

	// amlRearm is a process-local retry schedule after AML still-pending or
	// retryable errors. Lost on restart; recovered via DB first-poll due.
	amlRearm time.Time

	// lastKYTDue / lastAMLDue retain the most recent successful durable due so a
	// transient earliest-due query failure does not silently drop NextDue and
	// fall through to MaxIdle-only discovery.
	lastKYTDue time.Time
	lastAMLDue time.Time
}

func NewWorker(svc *Service, cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = adaptiveschedule.DefaultMaxIdle
	}
	if cfg.MaxIdle < cfg.Interval {
		cfg.MaxIdle = cfg.Interval
	}
	if cfg.KYTScanInterval <= 0 {
		cfg.KYTScanInterval = time.Minute
	}
	if cfg.AMLPollInterval <= 0 {
		cfg.AMLPollInterval = 60 * time.Second
	}
	if cfg.PanicBackoff <= 0 {
		cfg.PanicBackoff = 5 * time.Second
	}
	w := &Worker{svc: svc, config: cfg}
	// Create the loop at construction so Notify works before Run (webhook race).
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    "deposit",
		MinIdle: cfg.Interval,
		MaxIdle: cfg.MaxIdle,
	}, w.cycle)
	if err != nil {
		// Invalid config should be rare after normalization; keep worker usable
		// for drainSafely tests even if adaptive loop cannot start.
		return w
	}
	w.loop = loop
	return w
}

// Notify wakes the deposit worker after a durable webhook insert. Advisory only.
// Safe before Run.
func (w *Worker) Notify() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	loop := w.loop
	w.mu.Unlock()
	if loop == nil {
		return false
	}
	return loop.Notify()
}

// Run pumps the worker until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.svc == nil {
		return
	}
	log.Printf("deposit worker started: minIdle=%s maxIdle=%s amlPoll=%s kytTimeout=%s amlFirst=%s",
		w.config.Interval, w.config.MaxIdle, w.config.AMLPollInterval,
		w.svc.KYTTimeout(), w.svc.AMLFirstPollDelay())
	defer log.Println("deposit worker stopped")

	w.mu.Lock()
	loop := w.loop
	w.mu.Unlock()
	if loop == nil {
		log.Printf("deposit worker adaptive schedule disabled")
		return
	}
	// Startup recovery: first cycle queries DB due times and processes any
	// already-overdue KYT/AML work (no process-local timer to restore).
	loop.Run(ctx)
}

func (w *Worker) cycle(ctx context.Context) (outcome adaptiveschedule.CycleOutcome, err error) {
	defer func() {
		if recover() != nil {
			log.Printf("deposit worker panic recovered: kind=cycle")
			outcome = adaptiveschedule.CycleOutcome{}
			err = errors.New("deposit worker cycle panicked")
			select {
			case <-ctx.Done():
			case <-time.After(w.config.PanicBackoff):
			}
		}
	}()

	drain, drainErr := w.drainOnce(ctx)
	outcome.Worked = drain.Worked
	outcome.MoreWork = drain.MoreWork

	now := time.Now()
	kytDue, amlDue := w.loadRiskDues(ctx)

	// Run overdue risk work this cycle (immediate for already-due rows).
	if riskDueNow(kytDue, now) {
		w.runWithRecover(ctx, "KYT scan", w.svc.ScanKYTTimeouts)
	}
	amlShouldRun := riskDueNow(amlDue, now) || riskDueNow(w.amlRearm, now)
	if amlShouldRun {
		w.runWithRecover(ctx, "AML poll", w.svc.ScanAmlPending)
		// After a poll attempt, still-pending / retryable errors keep the same
		// updated_at anchor (must not rewrite it). Re-arm with AML_POLL_INTERVAL
		// so we do not form a zero-delay hot loop on the still-overdue DB due.
		w.amlRearm = now.Add(w.config.AMLPollInterval)
	}

	// Recompute durable dues after scans (rows may have left KYT_PENDING).
	kytDue, amlDue = w.loadRiskDues(ctx)
	if !amlDue.IsZero() && !riskDueNow(amlDue, now) {
		// First poll still in the future: drop process-local rearm so DB due wins.
		if riskDueNow(w.amlRearm, now) || w.amlRearm.IsZero() {
			w.amlRearm = time.Time{}
		}
	}
	if amlDue.IsZero() {
		w.amlRearm = time.Time{}
	}

	// Floor overdue dues so adaptive wait is never zero solely due to past NextDue.
	// KYT uses MinIdle floor; AML prefers AMLPollInterval rearm / floor.
	nextKYT := FloorOverdueDue(kytDue, now, w.config.Interval)
	nextAML := amlDue
	if !w.amlRearm.IsZero() {
		nextAML = adaptiveschedule.EarliestDue(FloorOverdueDue(amlDue, now, w.config.AMLPollInterval), w.amlRearm)
	} else {
		nextAML = FloorOverdueDue(amlDue, now, w.config.AMLPollInterval)
	}

	outcome.NextDue = adaptiveschedule.EarliestDue(nextKYT, nextAML)
	return outcome, drainErr
}

func (w *Worker) loadRiskDues(ctx context.Context) (kytDue, amlDue time.Time) {
	if w == nil || w.svc == nil {
		return time.Time{}, time.Time{}
	}
	now := time.Now()

	kytDue, kytErr := w.svc.EarliestKYTDue(ctx)
	if kytErr != nil {
		log.Printf("deposit worker earliest KYT due deferred: kind=database_query")
		kytDue = retainDueOnQueryError(w.lastKYTDue, now, w.config.Interval)
	} else {
		w.lastKYTDue = kytDue
	}

	amlDue, amlErr := w.svc.EarliestAMLFirstPollDue(ctx)
	if amlErr != nil {
		log.Printf("deposit worker earliest AML due deferred: kind=database_query")
		amlDue = retainDueOnQueryError(w.lastAMLDue, now, w.config.AMLPollInterval)
	} else {
		w.lastAMLDue = amlDue
	}
	return kytDue, amlDue
}

// retainDueOnQueryError keeps the last successful due when a read fails. With no
// history, schedule a short floor rearm so risk work is retried well before MaxIdle.
func retainDueOnQueryError(last, now time.Time, floor time.Duration) time.Time {
	if !last.IsZero() {
		return last
	}
	if floor <= 0 {
		floor = time.Second
	}
	return now.Add(floor)
}

func riskDueNow(due, now time.Time) bool {
	return !due.IsZero() && !due.After(now)
}

func (w *Worker) drainOnce(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
	// Custom drain (not DrainProcessOne with true-on-error): process failures
	// must yield the cycle so adaptive backoff applies. Returning processed=true
	// on error would fill the drain limit and set MoreWork → zero-wait hot loop.
	const limit = 100
	claimed := 0
	for claimed < limit {
		if err := ctx.Err(); err != nil {
			return adaptiveschedule.CycleOutcome{Worked: claimed > 0}, err
		}
		processed, err := w.svc.ProcessOne(ctx)
		if err != nil {
			log.Printf("deposit worker process deferred: kind=%s", depositWorkerErrorKind(err))
			// Surface error and stop this drain; adaptive loop will back off.
			return adaptiveschedule.CycleOutcome{Worked: claimed > 0}, err
		}
		if !processed {
			return adaptiveschedule.CycleOutcome{Worked: claimed > 0}, nil
		}
		claimed++
	}
	return adaptiveschedule.CycleOutcome{Worked: true, MoreWork: true}, nil
}

// drainSafely is retained for unit tests that exercise continuous drain without
// the adaptive loop (poison-event ordering). Production uses cycle() via Run.
func (w *Worker) drainSafely(ctx context.Context) {
	defer func() {
		if recover() != nil {
			log.Printf("deposit worker panic recovered: kind=drain")
			select {
			case <-ctx.Done():
			case <-time.After(w.config.PanicBackoff):
			}
		}
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := w.svc.ProcessOne(ctx)
		if err != nil {
			log.Printf("deposit worker process deferred: kind=%s", depositWorkerErrorKind(err))
			if errors.Is(err, ErrMarkErrorFailed) || errors.Is(err, ErrKYTAPIBackoff) {
				return
			}
			continue
		}
		if !processed {
			return
		}
	}
}

func (w *Worker) runWithRecover(ctx context.Context, label string, fn func(context.Context)) {
	defer func() {
		if recover() != nil {
			log.Printf("deposit worker risk task panic recovered: kind=%s", label)
		}
	}()
	fn(ctx)
}

func depositWorkerErrorKind(err error) string {
	switch {
	case errors.Is(err, ErrMarkErrorFailed):
		return "mark_error_failed"
	case errors.Is(err, ErrKYTAPIBackoff):
		return "kyt_api_backoff"
	default:
		return "processing_error"
	}
}
