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
	KYTScanInterval time.Duration // KYT timeout scan interval. Default 1m.
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
	if cfg.PanicBackoff <= 0 {
		cfg.PanicBackoff = 5 * time.Second
	}
	return &Worker{svc: svc, config: cfg}
}

// Run pumps the worker until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	log.Printf("deposit worker started: interval=%s kytScan=%s", w.config.Interval, w.config.KYTScanInterval)
	defer log.Println("deposit worker stopped")

	mainTicker := time.NewTicker(w.config.Interval)
	kytScanTicker := time.NewTicker(w.config.KYTScanInterval)
	defer mainTicker.Stop()
	defer kytScanTicker.Stop()

	w.drainSafely(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-mainTicker.C:
			w.drainSafely(ctx)
		case <-kytScanTicker.C:
			w.scanKYTSafely(ctx)
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

func (w *Worker) scanKYTSafely(ctx context.Context) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("deposit worker KYT scan panic recovered: %v\n%s", rv, debug.Stack())
		}
	}()

	w.svc.ScanKYTTimeouts(ctx)
}
