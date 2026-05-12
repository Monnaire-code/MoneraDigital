package deposit

import (
	"context"
	"errors"
	"log"
	"runtime/debug"
	"time"
)

// WorkerConfig controls the polling cadence + back-off.
type WorkerConfig struct {
	// Interval between drain cycles when the queue is empty. Default 1s.
	Interval time.Duration
	// PanicBackoff how long the worker pauses after a panic before resuming.
	// Default 5s.
	PanicBackoff time.Duration
}

// Worker drains PENDING webhook events through Service.ProcessOne.
type Worker struct {
	svc    *Service
	config WorkerConfig
}

func NewWorker(svc *Service, cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.PanicBackoff <= 0 {
		cfg.PanicBackoff = 5 * time.Second
	}
	return &Worker{svc: svc, config: cfg}
}

// Run pumps the worker until ctx is cancelled. Panics inside the drain are
// recovered + logged so a single bad event doesn't tear the goroutine down.
func (w *Worker) Run(ctx context.Context) {
	log.Printf("deposit worker started: interval=%s", w.config.Interval)
	defer log.Println("deposit worker stopped")

	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()

	// Drain immediately on start so backlog from a crash recovers fast.
	w.drainSafely(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.drainSafely(ctx)
		}
	}
}

func (w *Worker) drainSafely(ctx context.Context) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("deposit worker panic recovered: %v\n%s", rv, debug.Stack())
			// Back off so a deterministic panic doesn't hot-loop.
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
			// T7-I-5: if MarkEventError itself failed the row stays PENDING.
			// Yield to the ticker interval so we don't hot-loop relocking it.
			if errors.Is(err, ErrMarkErrorFailed) {
				return
			}
			// Other errors mean the event was committed in ERROR state — safe
			// to continue draining the next PENDING row.
			continue
		}
		if !processed {
			return
		}
	}
}
