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
	log.Printf("deposit worker started: minIdle=%s maxIdle=%s kytScan=%s amlPoll=%s",
		w.config.Interval, w.config.MaxIdle, w.config.KYTScanInterval, w.config.AMLPollInterval)
	defer log.Println("deposit worker stopped")

	// Startup recovery: risk scans are eligible immediately once.
	now := time.Now()
	w.nextKYT = now
	w.nextAML = now

	w.mu.Lock()
	loop := w.loop
	w.mu.Unlock()
	if loop == nil {
		log.Printf("deposit worker adaptive schedule disabled")
		return
	}
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
		// New deposits may enter KYT_PENDING; schedule risk follow-up by business cadence.
		w.nextKYT = now.Add(w.config.KYTScanInterval)
		w.nextAML = now.Add(w.config.AMLPollInterval)
	}

	if !w.nextKYT.IsZero() && !w.nextKYT.After(now) {
		w.runWithRecover(ctx, "KYT scan", w.svc.ScanKYTTimeouts)
		w.nextKYT = time.Time{}
	}
	if !w.nextAML.IsZero() && !w.nextAML.After(now) {
		w.runWithRecover(ctx, "AML poll", w.svc.ScanAmlPending)
		w.nextAML = time.Time{}
	}

	// Fully idle maintenance: re-arm risk checks at MaxIdle so stranded KYT/AML
	// rows recover without 1s/1m empty loops.
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
			log.Printf("deposit worker process error: %v", err)
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

func (w *Worker) runWithRecover(ctx context.Context, label string, fn func(context.Context)) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("deposit worker %s panic recovered: %v\n%s", label, rv, debug.Stack())
		}
	}()
	fn(ctx)
}
