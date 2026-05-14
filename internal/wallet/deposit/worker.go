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
	Interval        time.Duration // Main drain cycle interval. Default 1s.
	KYTScanInterval time.Duration // KYT 20-min timeout scan interval. Default 1m.
	AMLPollInterval time.Duration // Active KYT result poll (aml_risk_level='PENDING'). Default 100s.
	PanicBackoff    time.Duration // Pause after panic. Default 5s.
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
	if cfg.KYTScanInterval <= 0 {
		cfg.KYTScanInterval = time.Minute
	}
	if cfg.AMLPollInterval <= 0 {
		cfg.AMLPollInterval = 100 * time.Second
	}
	if cfg.PanicBackoff <= 0 {
		cfg.PanicBackoff = 5 * time.Second
	}
	return &Worker{svc: svc, config: cfg}
}

// Run pumps the worker until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	log.Printf("deposit worker started: interval=%s kytScan=%s amlPoll=%s",
		w.config.Interval, w.config.KYTScanInterval, w.config.AMLPollInterval)
	defer log.Println("deposit worker stopped")

	mainTicker := time.NewTicker(w.config.Interval)
	kytScanTicker := time.NewTicker(w.config.KYTScanInterval)
	amlPollTicker := time.NewTicker(w.config.AMLPollInterval)
	defer mainTicker.Stop()
	defer kytScanTicker.Stop()
	defer amlPollTicker.Stop()

	w.drainSafely(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-mainTicker.C:
			w.drainSafely(ctx)
		case <-kytScanTicker.C:
			w.runWithRecover(ctx, "KYT scan", w.svc.ScanKYTTimeouts)
		case <-amlPollTicker.C:
			w.runWithRecover(ctx, "AML poll", w.svc.ScanAmlPending)
		}
	}
}

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

// runWithRecover invokes fn under a panic recover. Used for the periodic
// scan/poll tickers where a panic must not kill the worker loop.
func (w *Worker) runWithRecover(ctx context.Context, label string, fn func(context.Context)) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("deposit worker %s panic recovered: %v\n%s", label, rv, debug.Stack())
		}
	}()
	fn(ctx)
}
