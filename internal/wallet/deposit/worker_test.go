package deposit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorker_DrainsUntilEmpty(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := NewService(repo, reg, nil)

	// 3 events queued at start.
	for i := 0; i < 3; i++ {
		enqueueRaw(t, repo, PayloadEnvelope{
			EventType: "TRANSACTION_STATUS_CHANGED",
			EventDetail: PayloadEventDetail{
				TxKey:                "tx-" + intToStr(i),
				CoinKey:              "K",
				TxAmount:             "0.5",
				TransactionStatus:    "COMPLETED",
				TransactionSubStatus: "CONFIRMED",
				TransactionDirection: "INFLOW",
				DestinationAddress:   "0xdest",
			},
		})
	}

	w := NewWorker(svc, WorkerConfig{Interval: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()

	// Wait until queue drained (all 3 marked DONE).
	deadline := time.After(500 * time.Millisecond)
	for {
		repo.mu.Lock()
		n := len(repo.doneIDs)
		repo.mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("worker did not drain in time, doneIDs=%d", n)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

// TestWorker_BacksOffWhenMarkErrorFails simulates the pathological branch:
// processEvent fails AND MarkEventError fails. The event stays PENDING.
// Without back-off the worker would relock the same row in a tight loop,
// burning CPU + DB connections. drainSafely must exit on ErrMarkErrorFailed
// and yield to the ticker interval — observable as exactly one BeginTx call
// instead of an unbounded number.
// Regression: T7-I-5.
func TestWorker_BacksOffWhenMarkErrorFails(t *testing.T) {
	repo := newMockRepo()
	repo.markErrorErr = errors.New("db down")
	// One un-processable event (invalid JSON forces processEvent to fail).
	// LockNextPendingEvent pops from repo.pending, but with markErrorErr the
	// tx rolls back so subsequent LockNextPendingEvent in the same drainSafely
	// would relock the same row in a real DB. Our mock pops, so we re-seed
	// every Begin: simplest is to fail the BeginTx after the first call.
	repo.pending = []*Event{{
		ID:         1,
		EventType:  "TRANSACTION_STATUS_CHANGED",
		RawPayload: []byte(`{not-json`),
	}}

	w := NewWorker(NewService(repo, nil, nil), WorkerConfig{Interval: time.Second})
	done := make(chan struct{})
	go func() {
		w.drainSafely(context.Background())
		close(done)
	}()

	select {
	case <-done:
		// pass
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("drainSafely did not return after MarkErrorFailed — hot-loop bug")
	}

	repo.mu.Lock()
	beginCalls := repo.beginTxCalls
	errored := len(repo.errorIDs)
	repo.mu.Unlock()

	if beginCalls != 1 {
		t.Errorf("drainSafely must exit after one BeginTx, got %d (hot-loop)", beginCalls)
	}
	if errored != 0 {
		t.Errorf("MarkEventError must not have recorded the ID (it failed), got %d errored IDs", errored)
	}
}

func TestWorker_PanicRecovered(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := NewService(repo, reg, nil)

	// Panic on every serial-fn call so we can observe recovery without
	// fighting the mockRepo's locked-row simulation.
	var panicCount atomic.Int32
	svc.SetSerialFunc(func() string {
		panicCount.Add(1)
		panic("synthetic panic")
	})

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-panic",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})

	w := NewWorker(svc, WorkerConfig{Interval: 5 * time.Millisecond, PanicBackoff: 5 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	<-ctx.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker stuck after panic")
	}

	if panicCount.Load() < 1 {
		t.Errorf("expected at least one serial-fn invocation, got %d", panicCount.Load())
	}
}

func TestWorker_StopsOnContextCancel(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, &stubRegistry{}, nil)
	w := NewWorker(svc, WorkerConfig{Interval: time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop on cancel")
	}
}

func TestWorker_DefaultsIntervalAndBackoff(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, &stubRegistry{}, nil)
	w := NewWorker(svc, WorkerConfig{}) // zero config triggers defaults
	if w.config.Interval != time.Second {
		t.Errorf("expected default 1s interval, got %s", w.config.Interval)
	}
	if w.config.PanicBackoff != 5*time.Second {
		t.Errorf("expected default 5s panic-backoff, got %s", w.config.PanicBackoff)
	}
}

func TestWorker_ScanKYTSafelyRecoversPanic(t *testing.T) {
	base := newMockRepo()
	// Overdue KYT anchor so the worker schedules an immediate risk scan.
	base.kytPendingAnchor = time.Now().Add(-time.Hour)
	panickingRepo := &panickingScanRepo{mockRepo: base}
	svc := NewService(panickingRepo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, time.Minute)

	w := NewWorker(svc, WorkerConfig{
		Interval:     20 * time.Millisecond,
		MaxIdle:      50 * time.Millisecond,
		PanicBackoff: 5 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()

	<-ctx.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker stuck after KYT scan panic")
	}

	if panickingRepo.panicCount.Load() < 1 {
		t.Error("expected at least one panic from scanKYTSafely")
	}
}

type panickingScanRepo struct {
	*mockRepo
	panicCount atomic.Int32
}

func (r *panickingScanRepo) LockOneKYTPendingTimeout(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	r.panicCount.Add(1)
	panic("synthetic KYT scan panic")
}

func (r *panickingScanRepo) LockOneAmlPending(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	return nil, ErrNoPending
}

func TestWorker_ProcessErrorYieldsCycleInsteadOfHotLoop(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	repo.depositErr = errors.New("synthetic db error")
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := NewService(repo, reg, nil)

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-err",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})

	w := NewWorker(svc, WorkerConfig{Interval: 10 * time.Millisecond, MaxIdle: 50 * time.Millisecond})
	outcome, err := w.drainOnce(context.Background())
	if err == nil {
		t.Fatal("expected process error to surface from drainOnce")
	}
	if outcome.MoreWork {
		t.Fatal("process errors must not set MoreWork (avoids zero-wait hot loop)")
	}
	if outcome.Worked {
		t.Fatal("failed process without successful claim must not report Worked")
	}

	// Adaptive Run must keep going (and record the error path) without spinning.
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	if len(repo.noTxErrorIDs) == 0 {
		t.Error("expected at least one error event recorded across yielded cycles")
	}
}

func TestWorker_NotifyIsAvailableBeforeRun(t *testing.T) {
	w := NewWorker(NewService(newMockRepo(), nil, nil), WorkerConfig{Interval: time.Hour, MaxIdle: time.Hour})
	if !w.Notify() {
		t.Fatal("Notify must queue before Run starts")
	}
	if w.Notify() {
		t.Fatal("second Notify before Run must coalesce")
	}
}

func TestWorker_UnsupportedEventDoesNotStarveLaterValidEvent(t *testing.T) {
	if got := len(testUnknownCoinKey64); got != 64 {
		t.Fatalf("test fixture CoinKey length = %d, want 64", got)
	}

	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	reg := newTestRegistry("ETH", "ETHEREUM", "ETH", "0.0001", 11)
	svc := NewService(repo, reg, nil)

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_CREATED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-poison",
			CoinKey:              testUnknownCoinKey64,
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_CREATED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-valid",
			CoinKey:              "ETH",
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})

	NewWorker(svc, WorkerConfig{}).drainSafely(context.Background())

	if got := repo.deposits["tx-poison"]; got == nil || got.Status != DepositStatusManualReview {
		t.Fatalf("poison event must terminate in MANUAL_REVIEW, got %+v", got)
	} else if got.SafeheronCoinKey != testUnknownCoinKey64 || got.ChainCode != "" || got.CoinChainID != 0 {
		t.Fatalf("poison evidence/mapping = %q/%q/%d", got.SafeheronCoinKey, got.ChainCode, got.CoinChainID)
	}
	if got := repo.deposits["tx-valid"]; got == nil || got.Status != DepositStatusCredited {
		t.Fatalf("later valid event must be credited, got %+v", got)
	}
	if len(repo.doneIDs) != 2 {
		t.Fatalf("both events must be finalized, got DONE ids %v", repo.doneIDs)
	}
}
