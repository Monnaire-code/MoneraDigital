package companyfund

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

type fakeSafeheronCoinLister struct {
	mu     sync.Mutex
	coins  []safeheron.Coin
	err    error
	listFn func(context.Context) ([]safeheron.Coin, error)
	calls  atomic.Int64
}

func (f *fakeSafeheronCoinLister) ListCoin(ctx context.Context) ([]safeheron.Coin, error) {
	f.calls.Add(1)
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]safeheron.Coin(nil), f.coins...), f.err
}

func (f *fakeSafeheronCoinLister) set(coins []safeheron.Coin, err error) {
	f.mu.Lock()
	f.coins = append([]safeheron.Coin(nil), coins...)
	f.err = err
	f.mu.Unlock()
}

func TestSafeheronCoinCatalogRefreshAndExactLookup(t *testing.T) {
	client := &fakeSafeheronCoinLister{coins: []safeheron.Coin{
		{CoinKey: "ETHEREUM_ETH", CoinName: "Ethereum", Symbol: "ETH", CoinDecimal: 18, FeeCoinKey: "ETHEREUM_ETH", BlockChain: "Ethereum", BlockchainType: "EVM", Network: "MAINNET"},
		{CoinKey: "ETHEREUM_USDT", CoinName: "Tether USD", Symbol: "USDT", CoinDecimal: 6, FeeCoinKey: "ETHEREUM_ETH", BlockChain: "Ethereum", BlockchainType: "EVM", Network: "MAINNET", TokenIdentifier: "0xdac17f"},
	}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.RefreshInterval() != 6*time.Hour {
		t.Fatalf("RefreshInterval() = %s, want 6h", catalog.RefreshInterval())
	}
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	coin, err := catalog.Lookup("ETHEREUM_USDT")
	if err != nil || coin.TokenIdentifier != "0xdac17f" || coin.CoinDecimal != 6 {
		t.Fatalf("Lookup() = %#v, %v", coin, err)
	}
	if _, err := catalog.Lookup("ethereum_usdt"); !errors.Is(err, ErrSafeheronCoinNotFound) {
		t.Fatalf("lower-case Lookup() error = %v, want exact case-sensitive miss", err)
	}
	if got := catalog.Snapshot(); len(got) != 2 {
		t.Fatalf("Snapshot() length = %d, want 2", len(got))
	}
}

func TestSafeheronCoinCatalogColdMissIsTyped(t *testing.T) {
	catalog, err := NewSafeheronCoinCatalog(&fakeSafeheronCoinLister{}, SafeheronCoinCatalogConfig{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = catalog.Lookup("ETHEREUM_ETH")
	var coldMiss *SafeheronCoinCatalogColdMissError
	if !errors.As(err, &coldMiss) || coldMiss.CoinKey != "ETHEREUM_ETH" || !errors.Is(err, ErrSafeheronCoinNotFound) {
		t.Fatalf("Lookup() error = %v, want typed cold miss", err)
	}
	if got := coldMiss.Error(); got == "" {
		t.Fatal("cold miss Error() is empty")
	}
}

func TestSafeheronCoinCatalogRefreshAtomicallyReplacesCompleteSnapshot(t *testing.T) {
	client := &fakeSafeheronCoinLister{coins: []safeheron.Coin{{CoinKey: "OLD"}}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	client.set([]safeheron.Coin{{CoinKey: "NEW_A"}, {CoinKey: "NEW_B"}}, nil)
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Lookup("OLD"); !errors.Is(err, ErrSafeheronCoinNotFound) {
		t.Fatalf("old entry survived replacement: %v", err)
	}
	if snapshot := catalog.Snapshot(); len(snapshot) != 2 || snapshot[0].CoinKey != "NEW_A" || snapshot[1].CoinKey != "NEW_B" {
		t.Fatalf("Snapshot() = %#v, want complete replacement", snapshot)
	}
}

func TestSafeheronCoinCatalogRejectsConflictingDuplicateWithoutLosingLastGood(t *testing.T) {
	client := &fakeSafeheronCoinLister{coins: []safeheron.Coin{{CoinKey: "ETH", Symbol: "ETH", CoinDecimal: 18}}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	client.set([]safeheron.Coin{
		{CoinKey: "USDT", Symbol: "USDT", CoinDecimal: 6, FeeCoinKey: "ETH"},
		{CoinKey: "USDT", Symbol: "USDT", CoinDecimal: 18, FeeCoinKey: "ETH"},
	}, nil)
	if err := catalog.Refresh(t.Context()); err == nil {
		t.Fatal("Refresh() error = nil, want duplicate metadata conflict")
	}
	coin, lookupErr := catalog.Lookup("ETH")
	if lookupErr != nil || coin.CoinDecimal != 18 || len(catalog.Snapshot()) != 1 {
		t.Fatalf("last-good snapshot lost: coin=%#v err=%v snapshot=%#v", coin, lookupErr, catalog.Snapshot())
	}
}

func TestSafeheronCoinCatalogRejectsInvalidExactCoinKey(t *testing.T) {
	for _, coinKey := range []string{"", " ETH "} {
		catalog, err := NewSafeheronCoinCatalog(&fakeSafeheronCoinLister{coins: []safeheron.Coin{{CoinKey: coinKey}}}, SafeheronCoinCatalogConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if err := catalog.Refresh(context.Background()); err == nil {
			t.Fatalf("CoinKey %q unexpectedly produced a valid catalog snapshot", coinKey)
		}
	}
}

func TestSafeheronCoinCatalogAllowsIdenticalDuplicate(t *testing.T) {
	coin := safeheron.Coin{CoinKey: "ETH", Symbol: "ETH", CoinDecimal: 18}
	catalog, err := NewSafeheronCoinCatalog(&fakeSafeheronCoinLister{coins: []safeheron.Coin{coin, coin}}, SafeheronCoinCatalogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if snapshot := catalog.Snapshot(); len(snapshot) != 1 || snapshot[0] != coin {
		t.Fatalf("Snapshot() = %#v, want one coalesced entry", snapshot)
	}
}

func TestSafeheronCoinCatalogRefreshFailureRetainsLastGood(t *testing.T) {
	client := &fakeSafeheronCoinLister{coins: []safeheron.Coin{{CoinKey: "BTC", Symbol: "BTC", CoinDecimal: 8}}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	client.set(nil, errors.New("provider unavailable"))
	if err := catalog.Refresh(t.Context()); err == nil {
		t.Fatal("Refresh() error = nil, want provider failure")
	}
	if coin, lookupErr := catalog.Lookup("BTC"); lookupErr != nil || coin.Symbol != "BTC" {
		t.Fatalf("Lookup() after refresh failure = %#v, %v", coin, lookupErr)
	}
}

func TestSafeheronCoinCatalogStartStopAreIdempotentAndCancelable(t *testing.T) {
	started := make(chan struct{}, 2)
	client := &fakeSafeheronCoinLister{listFn: func(ctx context.Context) ([]safeheron.Coin, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{RefreshInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	catalog.Start(t.Context())
	catalog.Start(t.Context())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("periodic refresh did not start")
	}
	catalog.Stop()
	catalog.Stop()
	if got := client.calls.Load(); got != 1 {
		t.Fatalf("ListCoin() calls = %d, want one loop call", got)
	}
}

func TestSafeheronCoinCatalogConcurrentRefreshAndLookup(t *testing.T) {
	client := &fakeSafeheronCoinLister{coins: []safeheron.Coin{{CoinKey: "ETH", Symbol: "ETH"}}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	for worker := range 8 {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for iteration := range 100 {
				if worker%2 == 0 {
					client.set([]safeheron.Coin{{CoinKey: "ETH", Symbol: fmt.Sprintf("ETH-%d", iteration)}}, nil)
					_ = catalog.Refresh(t.Context())
					continue
				}
				_, _ = catalog.Lookup("ETH")
				_ = catalog.Snapshot()
			}
		}(worker)
	}
	wait.Wait()
}

func TestNewSafeheronCoinCatalogValidatesConfiguration(t *testing.T) {
	if _, err := NewSafeheronCoinCatalog(nil, SafeheronCoinCatalogConfig{}); err == nil {
		t.Fatal("nil client error = nil")
	}
	if _, err := NewSafeheronCoinCatalog(&fakeSafeheronCoinLister{}, SafeheronCoinCatalogConfig{RefreshInterval: -time.Second}); err == nil {
		t.Fatal("negative interval error = nil")
	}
}

func TestSafeheronCoinCatalogDefensiveNilAndUnconfiguredBehavior(t *testing.T) {
	var nilCatalog *SafeheronCoinCatalog
	if nilCatalog.RefreshInterval() != 6*time.Hour {
		t.Fatal("nil catalog did not return default refresh interval")
	}
	if err := nilCatalog.Refresh(t.Context()); err == nil {
		t.Fatal("nil catalog Refresh() error = nil")
	}
	if _, err := nilCatalog.Lookup("ETH"); err == nil {
		t.Fatal("nil catalog Lookup() error = nil")
	}
	if snapshot := nilCatalog.Snapshot(); snapshot != nil {
		t.Fatalf("nil catalog Snapshot() = %#v", snapshot)
	}
	nilCatalog.Start(t.Context())
	nilCatalog.Stop()

	unconfigured := &SafeheronCoinCatalog{}
	if err := unconfigured.Refresh(t.Context()); err == nil {
		t.Fatal("unconfigured Refresh() error = nil")
	}
	if snapshot := unconfigured.Snapshot(); snapshot != nil {
		t.Fatalf("cold Snapshot() = %#v", snapshot)
	}
	unconfigured.Stop()
}

func TestSafeheronCoinCatalogAcceptsNilRefreshAndStartContexts(t *testing.T) {
	started := make(chan struct{}, 1)
	client := &fakeSafeheronCoinLister{listFn: func(ctx context.Context) ([]safeheron.Coin, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		return []safeheron.Coin{}, nil
	}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{RefreshInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Refresh(nil); err != nil {
		t.Fatalf("Refresh(nil) error = %v", err)
	}
	<-started
	if _, err := catalog.Lookup("ETH"); !errors.Is(err, ErrSafeheronCoinNotFound) {
		t.Fatalf("empty loaded catalog Lookup() error = %v", err)
	}
	catalog.Start(nil)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Start(nil) did not run refresh")
	}
	catalog.Stop()
}
