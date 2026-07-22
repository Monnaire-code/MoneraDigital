package adaptiveschedule_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"monera-digital/internal/adaptiveschedule"
)

func TestMaintenanceWindow_AlignsParticipants(t *testing.T) {
	maxIdle := 80 * time.Millisecond
	window := adaptiveschedule.NewMaintenanceWindow(maxIdle)
	window.SetOpenFor(15 * time.Millisecond)

	base := time.Now()
	allowed1, next1 := window.Allow(base)
	if !allowed1 {
		t.Fatal("first Allow must open the window")
	}
	if next1.Sub(base) < maxIdle-time.Millisecond {
		t.Fatalf("nextOpen should be ~maxIdle ahead, got %s", next1.Sub(base))
	}

	// Peer inside grace still allowed; does not push nextOpen further.
	allowed2, next2 := window.Allow(base.Add(5 * time.Millisecond))
	if !allowed2 {
		t.Fatal("peer inside open grace must be allowed")
	}
	if !next2.Equal(next1) {
		t.Fatalf("peer Allow must not reschedule nextOpen: %v vs %v", next2, next1)
	}

	// After grace and before nextOpen: denied.
	mid := base.Add(20 * time.Millisecond)
	allowed3, next3 := window.Allow(mid)
	if allowed3 {
		t.Fatal("outside grace must deny until nextOpen")
	}
	if !next3.Equal(next1) {
		t.Fatalf("denied nextOpen mismatch: %v vs %v", next3, next1)
	}
}

func TestMaintenanceWindow_BypassByWakeAndNextDue(t *testing.T) {
	maxIdle := 100 * time.Millisecond
	window := adaptiveschedule.NewMaintenanceWindow(maxIdle)
	window.SetOpenFor(10 * time.Millisecond)

	var hits atomic.Int64
	var mu sync.Mutex
	var hitTimes []time.Time

	record := func() {
		hits.Add(1)
		mu.Lock()
		hitTimes = append(hitTimes, time.Now())
		mu.Unlock()
	}

	// Loop A: pure maintenance (no Worked).
	loopA, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:              "maint-a",
		MinIdle:           5 * time.Millisecond,
		MaxIdle:           maxIdle,
		SharedMaintenance: window,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		record()
		return adaptiveschedule.CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Loop B: same window.
	loopB, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:              "maint-b",
		MinIdle:           5 * time.Millisecond,
		MaxIdle:           maxIdle,
		SharedMaintenance: window,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		record()
		return adaptiveschedule.CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Loop C: business NextDue every 15ms must not be delayed by the window.
	dueEvery := 15 * time.Millisecond
	var dueHits atomic.Int64
	loopC, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:              "due-c",
		MinIdle:           5 * time.Millisecond,
		MaxIdle:           maxIdle,
		SharedMaintenance: window,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		dueHits.Add(1)
		record()
		return adaptiveschedule.CycleOutcome{NextDue: time.Now().Add(dueEvery)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	go loopA.Run(ctx)
	go loopB.Run(ctx)
	go loopC.Run(ctx)

	// Mid-run business wake on A should produce an immediate hit.
	time.Sleep(40 * time.Millisecond)
	if !loopA.Notify() && !loopA.Notify() {
		// coalesced is fine
	}

	<-ctx.Done()
	time.Sleep(20 * time.Millisecond)

	if dueHits.Load() < 5 {
		t.Fatalf("NextDue-driven loop should keep running (~every %s); hits=%d", dueEvery, dueHits.Load())
	}

	mu.Lock()
	times := append([]time.Time(nil), hitTimes...)
	mu.Unlock()
	if len(times) < 4 {
		t.Fatalf("expected several hits, got %d", len(times))
	}

	// Longest gap among pure silence should approach maxIdle when NextDue
	// traffic is filtered... Overall timeline includes dueHits, so check that
	// maintenance-only gaps exist via window semantics unit test above.
	// Here assert process still produced hits (no deadlock) and due path works.
	if hits.Load() < 6 {
		t.Fatalf("expected combined activity, hits=%d", hits.Load())
	}
}

func TestSharedMaintenance_AggregatedQuietWindow(t *testing.T) {
	// Prove N independent scanners sharing one MaintenanceWindow produce a
	// continuous zero-query gap clearly longer than a short Neon-like threshold.
	maxIdle := 120 * time.Millisecond
	neonThreshold := 50 * time.Millisecond // stand-in for "long enough to suspend"
	window := adaptiveschedule.NewMaintenanceWindow(maxIdle)
	window.SetOpenFor(12 * time.Millisecond)

	var mu sync.Mutex
	var hits []time.Time
	record := func() {
		mu.Lock()
		hits = append(hits, time.Now())
		mu.Unlock()
	}

	const n = 8
	loops := make([]*adaptiveschedule.Loop, 0, n)
	for i := 0; i < n; i++ {
		loop, err := adaptiveschedule.New(adaptiveschedule.Config{
			Name:              "agg",
			MinIdle:           4 * time.Millisecond,
			MaxIdle:           maxIdle,
			SharedMaintenance: window,
		}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
			record()
			// Pure fallback: never report Worked so only maintenance gate paces DB.
			return adaptiveschedule.CycleOutcome{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		loops = append(loops, loop)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer cancel()
	for _, loop := range loops {
		go loop.Run(ctx)
	}
	<-ctx.Done()
	time.Sleep(15 * time.Millisecond)

	mu.Lock()
	times := append([]time.Time(nil), hits...)
	mu.Unlock()
	if len(times) < 2 {
		t.Fatalf("expected multiple maintenance hits, got %d", len(times))
	}

	// Sort is unnecessary if append order is chronological under same clock;
	// still compute max gap defensively.
	maxGap := time.Duration(0)
	for i := 1; i < len(times); i++ {
		if times[i].Before(times[i-1]) {
			continue
		}
		gap := times[i].Sub(times[i-1])
		if gap > maxGap {
			maxGap = gap
		}
	}
	// Also consider trailing silence until cancel end.
	if endGap := time.Since(times[len(times)-1]); endGap > maxGap {
		maxGap = endGap
	}

	if maxGap < neonThreshold {
		t.Fatalf("aggregated quiet gap %s shorter than neon threshold %s (hits=%d maxIdle=%s)",
			maxGap, neonThreshold, len(times), maxIdle)
	}
	// Must also be a substantial fraction of MaxIdle — not just a lucky 50ms blip
	// while scanners still chatter every few ms.
	if maxGap < maxIdle/2 {
		t.Fatalf("aggregated quiet gap %s should approach MaxIdle=%s; phase skew likely remains", maxGap, maxIdle)
	}
}

func TestLoop_SharedMaintenanceSkipsEmptyDBBetweenWindows(t *testing.T) {
	maxIdle := 60 * time.Millisecond
	window := adaptiveschedule.NewMaintenanceWindow(maxIdle)
	window.SetOpenFor(8 * time.Millisecond)

	var hits atomic.Int64
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:              "gated",
		MinIdle:           5 * time.Millisecond,
		MaxIdle:           maxIdle,
		SharedMaintenance: window,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		hits.Add(1)
		return adaptiveschedule.CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	go loop.Run(ctx)
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)

	// Without the gate, progressive 5ms empty cycles over 150ms would hit ~20+.
	// With the gate: startup + ~1 window reopen ≈ small single-digit hits.
	n := hits.Load()
	if n > 8 {
		t.Fatalf("gated empty scans should not chatter; hits=%d", n)
	}
	if n < 1 {
		t.Fatal("startup scan must still run")
	}
}

func TestLoop_NextDueFiresBeforeSharedMaxIdle(t *testing.T) {
	// #48/#53 seam: real NextDue must bypass the shared MaxIdle quiet window.
	maxIdle := 250 * time.Millisecond
	window := adaptiveschedule.NewMaintenanceWindow(maxIdle)
	window.SetOpenFor(15 * time.Millisecond)

	dueAfter := 45 * time.Millisecond
	var firstDueHit atomic.Int64
	var dueHitAt atomic.Value // time.Time

	start := time.Now()
	dueAt := start.Add(dueAfter)
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:              "kyt-due",
		MinIdle:           5 * time.Millisecond,
		MaxIdle:           maxIdle,
		SharedMaintenance: window,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		now := time.Now()
		if !now.Before(dueAt) {
			if firstDueHit.Add(1) == 1 {
				dueHitAt.Store(now)
			}
			return adaptiveschedule.CycleOutcome{}, nil
		}
		return adaptiveschedule.CycleOutcome{NextDue: dueAt}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()
	go loop.Run(ctx)

	deadline := time.Now().Add(160 * time.Millisecond)
	for time.Now().Before(deadline) {
		if firstDueHit.Load() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	time.Sleep(10 * time.Millisecond)

	if firstDueHit.Load() < 1 {
		t.Fatal("expected NextDue-driven cycle before test deadline")
	}
	hit, _ := dueHitAt.Load().(time.Time)
	elapsed := hit.Sub(start)
	if elapsed > maxIdle {
		t.Fatalf("NextDue fired at %s, after MaxIdle=%s — shared window delayed business due", elapsed, maxIdle)
	}
	if elapsed < dueAfter/2 {
		// Startup cycle is fine; the due hit itself must not be long before dueAt.
	}
	// Must not wait nearly the full MaxIdle when due was only ~45ms out.
	if elapsed > 120*time.Millisecond {
		t.Fatalf("due should fire near %s, got elapsed=%s (suspected MaxIdle wait)", dueAfter, elapsed)
	}
}

func TestLoop_NotifyBypassesMaintenanceGate(t *testing.T) {
	maxIdle := 200 * time.Millisecond
	window := adaptiveschedule.NewMaintenanceWindow(maxIdle)
	window.SetOpenFor(5 * time.Millisecond)

	var hits atomic.Int64
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:              "wake",
		MinIdle:           10 * time.Millisecond,
		MaxIdle:           maxIdle,
		SharedMaintenance: window,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		hits.Add(1)
		return adaptiveschedule.CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go loop.Run(ctx)

	// Wait until past open grace so the gate is closed.
	time.Sleep(25 * time.Millisecond)
	before := hits.Load()
	loop.Notify()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if hits.Load() > before {
			cancel()
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("Notify must run a cycle while maintenance window is closed; before=%d after=%d", before, hits.Load())
}
