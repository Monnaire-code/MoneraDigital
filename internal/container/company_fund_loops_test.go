package container

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"monera-digital/internal/companyfund"
)

type companyFundLoopRateRefresherStub struct {
	calls  atomic.Int64
	notify chan struct{}
}

func (stub *companyFundLoopRateRefresherStub) Refresh(context.Context) (companyfund.CoinGeckoCurrentRateRefreshResult, error) {
	stub.calls.Add(1)
	select {
	case stub.notify <- struct{}{}:
	default:
	}
	return companyfund.CoinGeckoCurrentRateRefreshResult{}, nil
}

type companyFundLoopValuationSweeperStub struct {
	calls  atomic.Int64
	batch  atomic.Int64
	notify chan struct{}
}

func (stub *companyFundLoopValuationSweeperStub) Sweep(_ context.Context, batchSize int) companyfund.CompanyFundValuationSweepResult {
	stub.calls.Add(1)
	stub.batch.Store(int64(batchSize))
	select {
	case stub.notify <- struct{}{}:
	default:
	}
	return companyfund.CompanyFundValuationSweepResult{}
}

func TestCompanyFundCurrentValuationLoops_UseIndependentRefreshAndSweepIntervals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	refresher := &companyFundLoopRateRefresherStub{notify: make(chan struct{}, 4)}
	valuator := &companyFundLoopValuationSweeperStub{notify: make(chan struct{}, 8)}
	refreshDone := make(chan struct{})
	sweepDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		runCompanyFundCurrentRateRefreshLoop(ctx, refresher, time.Hour)
	}()
	go func() {
		defer close(sweepDone)
		runCompanyFundCurrentValuationSweepLoop(ctx, valuator, 10*time.Millisecond, 37)
	}()

	select {
	case <-refresher.notify:
	case <-time.After(time.Second):
		t.Fatal("rate refresh loop did not run immediately")
	}
	for count := 0; count < 3; count++ {
		select {
		case <-valuator.notify:
		case <-time.After(time.Second):
			t.Fatalf("valuation sweep %d did not run on its independent ticker", count+1)
		}
	}
	if got := refresher.calls.Load(); got != 1 {
		t.Fatalf("rate refresh calls = %d, want one; valuation sweeps must not induce provider refreshes", got)
	}
	if got := valuator.calls.Load(); got < 3 || valuator.batch.Load() != 37 {
		t.Fatalf("valuation sweeps=%d batch=%d, want at least 3 and 37", got, valuator.batch.Load())
	}

	cancel()
	select {
	case <-refreshDone:
	case <-time.After(time.Second):
		t.Fatal("rate refresh loop did not stop")
	}
	select {
	case <-sweepDone:
	case <-time.After(time.Second):
		t.Fatal("valuation sweep loop did not stop")
	}
}

func TestCompanyFundRateRefreshSuccessWakesValuationSweep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	refresher := &companyFundLoopRateRefresherStub{notify: make(chan struct{}, 2)}
	valuator := &companyFundLoopValuationSweeperStub{notify: make(chan struct{}, 3)}
	valuationLoop := newCompanyFundCurrentValuationSweepLoop(valuator, time.Hour, 10)
	rateLoop := newCompanyFundCurrentRateRefreshLoop(refresher, time.Hour, func() {
		_ = valuationLoop.Notify()
	})
	valuationDone := make(chan struct{})
	rateDone := make(chan struct{})
	go func() {
		defer close(valuationDone)
		valuationLoop.Run(ctx)
	}()
	select {
	case <-valuator.notify:
	case <-time.After(time.Second):
		t.Fatal("valuation startup sweep did not run")
	}
	go func() {
		defer close(rateDone)
		rateLoop.Run(ctx)
	}()

	select {
	case <-refresher.notify:
	case <-time.After(time.Second):
		t.Fatal("rate refresh did not run")
	}
	deadline := time.After(time.Second)
	for valuator.calls.Load() < 2 {
		select {
		case <-valuator.notify:
		case <-deadline:
			t.Fatalf("successful refresh did not wake valuation; calls=%d", valuator.calls.Load())
		}
	}
	cancel()
	<-rateDone
	<-valuationDone
}
