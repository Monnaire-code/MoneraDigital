package deposit

import (
	"context"
	"errors"
	"log"
	"runtime/debug"
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
	// KYTScanInterval is the follow-up cadence while deposit activity has armed
	// risk recovery (default 1m). Fully idle systems fall back to MaxIdle.
	KYTScanInterval time.Duration
	// AMLPollInterval is the follow-up cadence while deposit activity has armed
	// risk recovery (default 60s). SQL minAge still gates actual AML polls.
	AMLPollInterval time.Duration
	// PanicBackoff pauses after a panic before the next cycle (default 5s).
	PanicBackoff time.Duration
}

// Worker drains PENDING webhook events through Service.ProcessOne and runs
// KYT/AML follow-up on demand with adaptive idle backoff.
type Worker struct {
	svc    *Service
	config WorkerConfig

	mu   sync.Mutex
	loop *adaptiveschedule.Loop

	nextKYT time.Time
	nextAML time.Time
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
	return &Worker{svc: svc, config: cfg}
}

// Notify wakes the deposit worker after a durable webhook insert. Advisory only.
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
	log.Printf("deposit worker started: minIdle=%s maxIdle=%s kytScan=%s amlPoll=%s",
		w.config.Interval, w.config.MaxIdle, w.config.KYTScanInterval, w.config.AMLPollInterval)
	defer log.Println("deposit worker stopped")

	// Startup recovery: risk scans are eligible immediately once.
	now := time.Now()
	w.nextKYT = now
	w.nextAML = now

	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    "deposit",
		MinIdle: w.config.Interval,
		MaxIdle: w.config.MaxIdle,
	}, w.cycle)
	if err != nil {
		log.Printf("deposit worker adaptive schedule disabled: %v", err)
		return
	}
	w.mu.Lock()
	w.loop = loop
	w.mu.Unlock()
	loop.Run(ctx)
}

func (w *Worker) cycle(ctx context.Context) (outcome adaptiveschedule.CycleOutcome, err error) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("deposit worker panic recovered: %v\n%s", rv, debug.Stack())
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
	if drain.Worked {
		// New deposits may enter KYT_PENDING; keep risk follow-up on business cadence.
		w.nextKYT = now.Add(w.config.KYTScanInterval)
		w.nextAML = now.Add(w.config.AMLPollInterval)
	}

	if !w.nextKYT.IsZero() && !w.nextKYT.After(now) {
		w.runWithRecover(ctx, "KYT scan", w.svc.ScanKYTTimeouts)
		// Clear explicit arm; re-arm below either from deposit activity or MaxIdle maintenance.
		w.nextKYT = time.Time{}
	}
	if !w.nextAML.IsZero() && !w.nextAML.After(now) {
		w.runWithRecover(ctx, "AML poll", w.svc.ScanAmlPending)
		w.nextAML = time.Time{}
	}

	// Fully idle maintenance: re-arm risk checks at MaxIdle so stranded KYT/AML
	// rows recover without 1s/1m empty loops while progressive backoff still applies.
	if !outcome.Worked {
		if w.nextKYT.IsZero() {
			w.nextKYT = now.Add(w.config.MaxIdle)
		}
		if w.nextAML.IsZero() {
			w.nextAML = now.Add(w.config.MaxIdle)
		}
	}

	outcome.NextDue = adaptiveschedule.EarliestDue(w.nextKYT, w.nextAML)
	return outcome, drainErr
}

func (w *Worker) drainOnce(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
	return adaptiveschedule.DrainProcessOne(ctx, func(ctx context.Context) (bool, error) {
		processed, err := w.svc.ProcessOne(ctx)
		if err != nil {
			log.Printf("deposit worker process error: %v", err)
			if errors.Is(err, ErrMarkErrorFailed) || errors.Is(err, ErrKYTAPIBackoff) {
				// Yield the cycle so adaptive backoff can slow repeated failures.
				return false, err
			}
			// Transient process errors: continue draining other events.
			return true, nil
		}
		return processed, nil
	}, 100)
}

// drainSafely is retained for unit tests that exercise the drain path without
// the adaptive loop. Production uses cycle() via Run.
func (w *Worker) drainSafely(ctx context.Context) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("deposit worker panic recovered: %v\n%s", rv, debug.Stack())
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
			log.Printf("deposit worker process error: %v", err)
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

// runWithRecover invokes fn under a panic recover. Used for risk scans where a
// panic must not kill the worker loop.
func (w *Worker) runWithRecover(ctx context.Context, label string, fn func(context.Context)) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("deposit worker %s panic recovered: %v\n%s", label, rv, debug.Stack())
		}
	}()
	fn(ctx)
}
