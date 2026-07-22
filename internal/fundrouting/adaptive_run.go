package fundrouting

import (
	"context"
	"log"
	"sync"
	"time"

	"monera-digital/internal/adaptiveschedule"
)

// adaptiveRunner is shared by fund-routing background workers so each keeps a
// coalescible wake channel and the same idle budget.
type adaptiveRunner struct {
	name     string
	minIdle  time.Duration
	maxIdle  time.Duration
	process  adaptiveschedule.ProcessOneFunc
	mu       sync.Mutex
	loop     *adaptiveschedule.Loop
}

func newAdaptiveRunner(name string, minIdle, maxIdle time.Duration, process adaptiveschedule.ProcessOneFunc) *adaptiveRunner {
	if minIdle <= 0 {
		minIdle = time.Second
	}
	if maxIdle <= 0 {
		maxIdle = adaptiveschedule.DefaultMaxIdle
	}
	if maxIdle < minIdle {
		maxIdle = minIdle
	}
	return &adaptiveRunner{name: name, minIdle: minIdle, maxIdle: maxIdle, process: process}
}

func (r *adaptiveRunner) Notify() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	loop := r.loop
	r.mu.Unlock()
	if loop == nil {
		return false
	}
	return loop.Notify()
}

func (r *adaptiveRunner) Run(ctx context.Context) {
	if r == nil || r.process == nil {
		return
	}
	log.Printf("%s started: minIdle=%s maxIdle=%s", r.name, r.minIdle, r.maxIdle)
	defer log.Printf("%s stopped", r.name)

	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    r.name,
		MinIdle: r.minIdle,
		MaxIdle: r.maxIdle,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		outcome, err := adaptiveschedule.DrainProcessOne(ctx, r.process, 100)
		if err != nil && ctx.Err() == nil {
			log.Printf("%s cycle error", r.name)
		}
		return outcome, err
	})
	if err != nil {
		log.Printf("%s adaptive schedule disabled: configuration invalid", r.name)
		return
	}
	r.mu.Lock()
	r.loop = loop
	r.mu.Unlock()
	loop.Run(ctx)
}
