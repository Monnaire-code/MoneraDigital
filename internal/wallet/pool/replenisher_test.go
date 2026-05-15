package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

func TestReplenisher_TickTriggersReplenish(t *testing.T) {
	var replenished atomic.Int32

	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 10, nil
		},
		bulkInsert: func(ctx context.Context, addrs []*Address) error {
			replenished.Add(int32(len(addrs)))
			return nil
		},
	}

	client := &mockClient{
		createWallet: func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
			return &safeheron.Wallet{
				AccountKey:      "ak",
				CoinAddressList: []safeheron.CoinAddress{{Address: "0x1"}},
			}, nil
		},
		addCoin: func(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error) {
			return &safeheron.Wallet{}, nil
		},
	}

	reg := testRegistry()
	mgr := NewManager(repo, client, reg)

	cfg := ReplenisherConfig{
		Interval: 50 * time.Millisecond,
		Low:      map[string]int{"EVM": 20},
		Target:   map[string]int{"EVM": 30},
	}
	r := NewReplenisher(mgr, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r.Run(ctx)

	if replenished.Load() == 0 {
		t.Error("expected replenish to have created addresses")
	}
}

func TestReplenisher_AboveWatermark_NoReplenish(t *testing.T) {
	var replenished atomic.Int32

	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 100, nil
		},
	}

	mgr := NewManager(repo, nil, nil)
	cfg := ReplenisherConfig{
		Interval: 50 * time.Millisecond,
		Low:      map[string]int{"EVM": 50},
		Target:   map[string]int{"EVM": 100},
	}
	r := NewReplenisher(mgr, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	r.Run(ctx)

	if replenished.Load() != 0 {
		t.Error("should not replenish when above watermark")
	}
}

func TestReplenisher_PanicRecovery(t *testing.T) {
	callCount := atomic.Int32{}

	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			n := callCount.Add(1)
			if n == 1 {
				panic("simulated panic")
			}
			return 100, nil
		},
	}

	mgr := NewManager(repo, nil, nil)
	cfg := ReplenisherConfig{
		Interval: 50 * time.Millisecond,
		Low:      map[string]int{"EVM": 50},
		Target:   map[string]int{"EVM": 100},
	}
	r := NewReplenisher(mgr, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r.Run(ctx)

	if callCount.Load() < 2 {
		t.Error("replenisher should survive panic and continue ticking")
	}
}

func TestReplenisher_ContextCancel(t *testing.T) {
	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 100, nil
		},
	}

	mgr := NewManager(repo, nil, nil)
	cfg := ReplenisherConfig{
		Interval: time.Hour,
		Low:      map[string]int{"EVM": 50},
		Target:   map[string]int{"EVM": 100},
	}
	r := NewReplenisher(mgr, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("replenisher did not stop after context cancel")
	}
}

func TestReplenisher_DefaultInterval(t *testing.T) {
	r := NewReplenisher(nil, ReplenisherConfig{})
	if r.config.Interval != 10*time.Minute {
		t.Errorf("expected default 10m, got %s", r.config.Interval)
	}
}

func TestReplenisher_AlertOnReplenishFailure(t *testing.T) {
	var alertCalled atomic.Bool

	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 10, nil
		},
		bulkInsert: func(ctx context.Context, addrs []*Address) error {
			return nil
		},
	}

	client := &mockClient{
		createWallet: func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
			return nil, errors.New("sdk error")
		},
	}

	reg := testRegistry()
	mgr := newTestManager(repo, client, reg)
	mgr.SetAlertFunc(func(level, title, message string) {
		alertCalled.Store(true)
	})

	cfg := ReplenisherConfig{
		Interval: 50 * time.Millisecond,
		Low:      map[string]int{"EVM": 20},
		Target:   map[string]int{"EVM": 30},
	}
	r := NewReplenisher(mgr, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r.Run(ctx)

	if !alertCalled.Load() {
		t.Error("expected alert to be called on replenish failure")
	}
}

func TestReplenisher_MultipleFamilies(t *testing.T) {
	checkedFamilies := make(map[string]bool)
	var mu sync.Mutex

	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			mu.Lock()
			checkedFamilies[family] = true
			mu.Unlock()
			return 100, nil
		},
	}

	mgr := NewManager(repo, nil, nil)
	cfg := ReplenisherConfig{
		Interval: 50 * time.Millisecond,
		Low:      map[string]int{"EVM": 50, "TRON": 50},
		Target:   map[string]int{"EVM": 100, "TRON": 100},
	}
	r := NewReplenisher(mgr, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if !checkedFamilies["EVM"] || !checkedFamilies["TRON"] {
		t.Errorf("expected both families checked, got %v", checkedFamilies)
	}
}
