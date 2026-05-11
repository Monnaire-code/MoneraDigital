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

func TestWorker_ProcessErrorContinues(t *testing.T) {
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

	w := NewWorker(svc, WorkerConfig{Interval: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(repo.errorIDs) == 0 {
		t.Error("expected at least one error event recorded")
	}
}
