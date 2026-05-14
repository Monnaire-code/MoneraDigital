package pool

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"time"
)

type ReplenisherConfig struct {
	Interval time.Duration
	Low      map[string]int
	Target   map[string]int
}

type Replenisher struct {
	mgr    *Manager
	config ReplenisherConfig
}

func NewReplenisher(mgr *Manager, cfg ReplenisherConfig) *Replenisher {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Minute
	}
	return &Replenisher{mgr: mgr, config: cfg}
}

func (r *Replenisher) Run(ctx context.Context) {
	log.Printf("pool replenisher started: interval=%s low=%v target=%v",
		r.config.Interval, r.config.Low, r.config.Target)

	r.tick(ctx)

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("pool replenisher stopped")
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Replenisher) tick(ctx context.Context) {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("pool replenisher panic recovered: %v\n%s", rv, debug.Stack())
		}
	}()

	for family, low := range r.config.Low {
		target, ok := r.config.Target[family]
		if !ok {
			continue
		}

		count, err := r.mgr.repo.CountByStatus(ctx, family, StatusAvailable)
		if err != nil {
			log.Printf("pool replenish check error (%s): %v", family, err)
			continue
		}

		log.Printf("pool replenish check: %s=%d", family, count)

		if count >= low {
			continue
		}

		log.Printf("pool replenish: %s %d→%d", family, count, target)
		if err := r.mgr.Replenish(ctx, family, target); err != nil {
			log.Printf("pool replenish failed (%s): %v", family, err)
			r.alert(family, count, target, err)
		}
	}
}

func (r *Replenisher) alert(family string, current, target int, err error) {
	log.Printf("ALERT: pool replenish failed family=%s current=%d target=%d err=%v",
		family, current, target, err)

	if fn := r.mgr.getAlertFn(); fn != nil {
		fn("ERROR", "Pool Replenish Failed",
			fmt.Sprintf("family=%s current=%d target=%d", family, current, target))
	}
}
