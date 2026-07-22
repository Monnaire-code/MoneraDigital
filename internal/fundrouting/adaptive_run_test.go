package fundrouting

import (
	"context"
	"testing"
	"time"

	"monera-digital/internal/adaptiveschedule"
)

func TestAdaptiveRunner_NotifyAvailableBeforeRun(t *testing.T) {
	t.Parallel()
	var calls int
	runner := newAdaptiveRunner("test-runner", time.Hour, time.Hour, func(context.Context) (bool, error) {
		calls++
		return false, nil
	})
	if !runner.Notify() {
		t.Fatal("Notify must queue before Run")
	}
	if runner.Notify() {
		t.Fatal("second Notify must coalesce")
	}
	if calls != 0 {
		t.Fatalf("Notify must not invoke process before Run; calls=%d", calls)
	}
}

func TestAdaptiveRunner_OnWorkedFiresAfterSuccessfulDrain(t *testing.T) {
	t.Parallel()
	var processCalls, workedCalls int
	runner := newAdaptiveRunner("test-on-worked", 5*time.Millisecond, 20*time.Millisecond, func(context.Context) (bool, error) {
		processCalls++
		// First cycle: one unit of work then empty on subsequent ProcessOne in Drain.
		if processCalls == 1 {
			return true, nil
		}
		return false, nil
	})
	runner.onWorked = func() { workedCalls++ }

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runner.Run(ctx)

	if workedCalls < 1 {
		t.Fatalf("onWorked calls=%d, want at least 1 after Worked cycle", workedCalls)
	}
}

func TestAdaptiveRunner_DrainUsesSharedSeam(t *testing.T) {
	t.Parallel()
	// Ensure DrainProcessOne still reports MoreWork at limit for runners.
	n := 0
	outcome, err := adaptiveschedule.DrainProcessOne(context.Background(), func(context.Context) (bool, error) {
		n++
		return true, nil
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Worked || !outcome.MoreWork || n != 3 {
		t.Fatalf("outcome=%#v n=%d", outcome, n)
	}
}
