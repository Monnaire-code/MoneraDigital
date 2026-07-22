package pool

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"monera-digital/internal/adaptiveschedule"
)

type ReplenisherConfig struct {
	// Interval is the minimum idle wait after a healthy check (default 10m).
	Interval time.Duration
	// MaxIdle is the progressive ceiling (default 10m).
	MaxIdle time.Duration
	Low     map[string]int
	Target  map[string]int
}

type Replenisher struct {
	mgr    *Manager
	config ReplenisherConfig

	mu   sync.Mutex
	loop *adaptiveschedule.Loop
}

func NewReplenisher(mgr *Manager, cfg ReplenisherConfig) *Replenisher {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Minute
	}
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = adaptiveschedule.DefaultMaxIdle
	}
	if cfg.MaxIdle < cfg.Interval {
		cfg.MaxIdle = cfg.Interval
	}
	r := &Replenisher{mgr: mgr, config: cfg}
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    "wallet-pool-replenisher",
		MinIdle: cfg.Interval,
		MaxIdle: cfg.MaxIdle,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		worked := r.tick(ctx)
		return adaptiveschedule.CycleOutcome{Worked: worked}, nil
	})
	if err == nil {
		r.loop = loop
	}
	return r
}

// Notify wakes pool maintenance after a real allocation or known low watermark.
// Safe before Run.
func (r *Replenisher) Notify() bool {
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

func (r *Replenisher) Run(ctx context.Context) {
	log.Printf("pool replenisher started: minIdle=%s maxIdle=%s low=%v target=%v",
		r.config.Interval, r.config.MaxIdle, r.config.Low, r.config.Target)
	defer log.Println("pool replenisher stopped")

	r.mu.Lock()
	loop := r.loop
	r.mu.Unlock()
	if loop == nil {
		log.Printf("pool replenisher adaptive schedule disabled")
		return
	}
	loop.Run(ctx)
}

func (r *Replenisher) tick(ctx context.Context) bool {
	defer func() {
		if recover() != nil {
			log.Printf("pool replenisher panic recovered: kind=maintenance_cycle")
		}
	}()

	worked := false
	for family, low := range r.config.Low {
		target, ok := r.config.Target[family]
		if !ok {
			continue
		}

		count, err := r.mgr.repo.CountByStatus(ctx, family, StatusAvailable)
		if err != nil {
			log.Printf("pool replenish check deferred: family=%s kind=repository_error", family)
			continue
		}

		if count >= low {
			continue
		}

		log.Printf("pool replenish: %s %d→%d", family, count, target)
		worked = true
		if err := r.mgr.Replenish(ctx, family, target); err != nil {
			log.Printf("pool replenish failed: family=%s kind=replenish_error", family)
			r.alert(family, count, target)
		}
	}
	return worked
}

func (r *Replenisher) alert(family string, current, target int) {
	log.Printf("ALERT: pool replenish failed family=%s current=%d target=%d",
		family, current, target)

	if fn := r.mgr.getAlertFn(); fn != nil {
		fn("ERROR", "Pool Replenish Failed",
			fmt.Sprintf("family=%s current=%d target=%d", family, current, target))
	}
}
