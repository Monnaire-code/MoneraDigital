package fundrouting

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"monera-digital/internal/adaptiveschedule"
)

// adaptiveRunner is shared by fund-routing background workers so each keeps a
// coalescible wake channel and the same idle budget. The loop is created at
// construction so Notify is safe before Run.
type adaptiveRunner struct {
	name    string
	minIdle time.Duration
	maxIdle time.Duration
	process adaptiveschedule.ProcessOneFunc
	mu      sync.Mutex
	loop    *adaptiveschedule.Loop
	// onWorked is optional; invoked after a cycle that processed work (for
	// downstream wakes such as routing → projection).
	onWorked func()
	nextDue  func(context.Context) (time.Time, error)
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
	r := &adaptiveRunner{name: name, minIdle: minIdle, maxIdle: maxIdle, process: process}
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    name,
		MinIdle: minIdle,
		MaxIdle: maxIdle,
	}, r.cycle)
	if err != nil {
		return r
	}
	r.loop = loop
	return r
}

func (r *adaptiveRunner) cycle(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
	outcome, err := adaptiveschedule.DrainProcessOne(ctx, r.process, 100)
	if !outcome.MoreWork && r.nextDue != nil {
		due, dueErr := r.nextDue(ctx)
		outcome.NextDue = adaptiveschedule.EarliestDue(outcome.NextDue, due)
		err = errors.Join(err, dueErr)
	}
	if err != nil && ctx.Err() == nil {
		log.Printf("%s cycle error", r.name)
	}
	if outcome.Worked && r.onWorked != nil {
		r.onWorked()
	}
	return outcome, err
}

func (r *adaptiveRunner) setNextDue(fn func(context.Context) (time.Time, error)) {
	if r == nil {
		return
	}
	r.nextDue = fn
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

	r.mu.Lock()
	loop := r.loop
	r.mu.Unlock()
	if loop == nil {
		log.Printf("%s adaptive schedule disabled: configuration invalid", r.name)
		return
	}
	loop.Run(ctx)
}
