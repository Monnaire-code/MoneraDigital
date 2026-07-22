package adaptiveschedule

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeConfigDefaultsAndRejectsInvalid(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeConfig(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinIdle != DefaultMinIdle || cfg.MaxIdle != DefaultMaxIdle || cfg.Name != "task" || cfg.Now == nil {
		t.Fatalf("defaults = %#v", cfg)
	}

	for _, tc := range []Config{
		{MinIdle: -time.Second, MaxIdle: time.Minute},
		{MinIdle: time.Second, MaxIdle: -time.Minute},
		{MinIdle: time.Minute, MaxIdle: time.Second},
	} {
		if _, err := NormalizeConfig(tc); err == nil {
			t.Fatalf("expected error for %#v", tc)
		}
	}
}

func TestLoop_StartupScanRunsImmediately(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	started := make(chan struct{}, 1)
	loop, err := New(Config{
		Name:    "startup",
		MinIdle: time.Hour,
		MaxIdle: time.Hour,
	}, func(context.Context) (CycleOutcome, error) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
		}
		return CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)
	defer loop.Stop()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup scan did not run immediately")
	}
}

func TestLoop_NotifyCoalescesBeforeStart(t *testing.T) {
	t.Parallel()

	loop, err := New(Config{
		Name:    "coalesce",
		MinIdle: time.Hour,
		MaxIdle: time.Hour,
	}, func(context.Context) (CycleOutcome, error) {
		return CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !loop.Notify() {
		t.Fatal("first Notify should queue a wake")
	}
	if loop.Notify() {
		t.Fatal("second Notify should coalesce while one wake is pending")
	}
}

func TestLoop_NotifyWakesIdleWait(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	enteredSecond := make(chan struct{}, 1)
	loop, err := New(Config{
		Name:    "wake",
		MinIdle: 50 * time.Millisecond,
		MaxIdle: time.Second,
	}, func(context.Context) (CycleOutcome, error) {
		n := calls.Add(1)
		if n == 2 {
			enteredSecond <- struct{}{}
		}
		// First cycle empty → enter idle; later cycles also empty.
		return CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)
	defer loop.Stop()

	// Wait until first empty cycle completed and loop is likely sleeping.
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() < 1 {
		t.Fatal("first cycle never ran")
	}

	_ = loop.Notify()

	select {
	case <-enteredSecond:
	case <-time.After(time.Second):
		t.Fatalf("wake did not interrupt idle wait; calls=%d", calls.Load())
	}
}

func TestLoop_DrainsWhileMoreWorkWithoutIdleGap(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var mu sync.Mutex
	var timestamps []time.Time
	done := make(chan struct{})

	loop, err := New(Config{
		Name:    "drain",
		MinIdle: 200 * time.Millisecond,
		MaxIdle: time.Second,
		Now:     time.Now,
	}, func(context.Context) (CycleOutcome, error) {
		n := calls.Add(1)
		mu.Lock()
		timestamps = append(timestamps, time.Now())
		mu.Unlock()
		if n < 5 {
			return CycleOutcome{Worked: true, MoreWork: true}, nil
		}
		if n == 5 {
			close(done)
			return CycleOutcome{Worked: true}, nil
		}
		return CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)
	defer loop.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not drain MoreWork cycles quickly")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(timestamps) < 5 {
		t.Fatalf("timestamps=%d", len(timestamps))
	}
	// Five continuous drain cycles should finish well under one MinIdle gap.
	if timestamps[4].Sub(timestamps[0]) >= 150*time.Millisecond {
		t.Fatalf("MoreWork path waited too long: %s", timestamps[4].Sub(timestamps[0]))
	}
}

func TestLoop_ProgressiveBackoffReachesMaxIdle(t *testing.T) {
	t.Parallel()

	minIdle := 20 * time.Millisecond
	maxIdle := 80 * time.Millisecond
	var gaps []time.Duration
	var last time.Time
	var mu sync.Mutex
	reached := make(chan struct{}, 1)

	loop, err := New(Config{
		Name:    "backoff",
		MinIdle: minIdle,
		MaxIdle: maxIdle,
	}, func(context.Context) (CycleOutcome, error) {
		now := time.Now()
		mu.Lock()
		if !last.IsZero() {
			gaps = append(gaps, now.Sub(last))
			if len(gaps) >= 4 {
				select {
				case reached <- struct{}{}:
				default:
				}
			}
		}
		last = now
		mu.Unlock()
		return CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)
	defer loop.Stop()

	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("did not observe enough empty cycles")
	}

	mu.Lock()
	defer mu.Unlock()
	// Expected progressive: ~20ms, ~40ms, ~80ms, ~80ms (ceiling).
	if len(gaps) < 3 {
		t.Fatalf("gaps=%v", gaps)
	}
	// First gap after immediate startup should be near minIdle.
	if gaps[0] < minIdle/2 || gaps[0] > minIdle+25*time.Millisecond {
		t.Fatalf("first idle gap = %s, want ~%s", gaps[0], minIdle)
	}
	// Later gap should approach maxIdle (allow timer slack).
	lastGap := gaps[len(gaps)-1]
	if lastGap < maxIdle/2 {
		t.Fatalf("last idle gap = %s, want approaching max %s (gaps=%v)", lastGap, maxIdle, gaps)
	}
}

func TestLoop_WorkResetsBackoffToMinIdle(t *testing.T) {
	t.Parallel()

	minIdle := 30 * time.Millisecond
	maxIdle := 200 * time.Millisecond
	var calls atomic.Int32
	var afterWorkGap time.Duration
	var last time.Time
	var mu sync.Mutex
	done := make(chan struct{})

	loop, err := New(Config{
		Name:    "reset",
		MinIdle: minIdle,
		MaxIdle: maxIdle,
	}, func(context.Context) (CycleOutcome, error) {
		n := calls.Add(1)
		now := time.Now()
		mu.Lock()
		prev := last
		last = now
		mu.Unlock()

		// Empty cycles 1-3 build backoff; cycle 4 works; cycle 5 empty measures gap.
		switch {
		case n <= 3:
			return CycleOutcome{}, nil
		case n == 4:
			return CycleOutcome{Worked: true}, nil
		case n == 5:
			mu.Lock()
			if !prev.IsZero() {
				afterWorkGap = now.Sub(prev)
			}
			mu.Unlock()
			close(done)
			return CycleOutcome{}, nil
		default:
			return CycleOutcome{}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)
	defer loop.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reset observation timed out")
	}

	// After work, next empty wait should be minIdle, not the previous doubled value.
	if afterWorkGap < minIdle/2 || afterWorkGap > minIdle+40*time.Millisecond {
		t.Fatalf("after-work idle gap = %s, want ~%s", afterWorkGap, minIdle)
	}
}

func TestLoop_NextDueBeatsProgressiveIdle(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	second := make(chan struct{}, 1)
	loop, err := New(Config{
		Name:    "due",
		MinIdle: 200 * time.Millisecond,
		MaxIdle: time.Second,
	}, func(context.Context) (CycleOutcome, error) {
		n := calls.Add(1)
		if n == 1 {
			return CycleOutcome{NextDue: time.Now().Add(25 * time.Millisecond)}, nil
		}
		second <- struct{}{}
		return CycleOutcome{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)
	defer loop.Stop()

	select {
	case <-second:
	case <-time.After(150 * time.Millisecond):
		t.Fatalf("NextDue did not advance schedule; calls=%d", calls.Load())
	}
}

func TestLoop_CancelStopsLoop(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	loop, err := New(Config{
		Name:    "cancel",
		MinIdle: time.Hour,
		MaxIdle: time.Hour,
	}, func(ctx context.Context) (CycleOutcome, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return CycleOutcome{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	loop.Start(ctx)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("loop did not start")
	}
	cancel()
	stopped := make(chan struct{})
	go func() {
		loop.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not complete after cancel")
	}
}

func TestLoop_ErrorAndPanicAreIsolated(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	recovered := make(chan struct{}, 1)
	loop, err := New(Config{
		Name:    "isolate",
		MinIdle: 10 * time.Millisecond,
		MaxIdle: 40 * time.Millisecond,
	}, func(context.Context) (CycleOutcome, error) {
		n := calls.Add(1)
		switch n {
		case 1:
			return CycleOutcome{}, errors.New("provider payload must not escape")
		case 2:
			panic("secret-or-payload")
		default:
			select {
			case recovered <- struct{}{}:
			default:
			}
			return CycleOutcome{}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)
	defer loop.Stop()

	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatalf("loop did not continue after error/panic; calls=%d", calls.Load())
	}
	if calls.Load() < 3 {
		t.Fatalf("calls=%d, want continued cycles", calls.Load())
	}
}

func TestNextIdleDoublesUntilCeiling(t *testing.T) {
	t.Parallel()
	minIdle := time.Second
	maxIdle := 10 * time.Minute
	got := nextIdle(0, minIdle, maxIdle)
	if got != minIdle {
		t.Fatalf("from zero = %s", got)
	}
	got = nextIdle(minIdle, minIdle, maxIdle)
	if got != 2*time.Second {
		t.Fatalf("from min = %s", got)
	}
	got = nextIdle(8*time.Minute, minIdle, maxIdle)
	if got != maxIdle {
		t.Fatalf("near max = %s", got)
	}
	got = nextIdle(maxIdle, minIdle, maxIdle)
	if got != maxIdle {
		t.Fatalf("at max = %s", got)
	}
}
