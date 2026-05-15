package config

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type mockRepo struct {
	loadAllFn func(ctx context.Context) ([]*Chain, []*Coin, []*CoinChain, error)
	callCount int
	mu        sync.Mutex
}

func (m *mockRepo) LoadAll(ctx context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	return m.loadAllFn(ctx)
}

func (m *mockRepo) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func testData() ([]*Chain, []*Coin, []*CoinChain) {
	ethChain := &Chain{Code: "ETHEREUM", Name: "Ethereum", NetworkFamily: "EVM", ChainID: "1", NativeSymbol: "ETH", Enabled: true, DisplayOrder: 10}
	tronChain := &Chain{Code: "TRON", Name: "TRON", NetworkFamily: "TRON", NativeSymbol: "TRX", Enabled: true, DisplayOrder: 30}

	ethCoin := &Coin{ID: 1, Symbol: "ETH", Name: "Ether", Enabled: true, DisplayOrder: 10}
	usdtCoin := &Coin{ID: 4, Symbol: "USDT", Name: "Tether USD", IsStable: true, Enabled: true, DisplayOrder: 40}
	trxCoin := &Coin{ID: 3, Symbol: "TRX", Name: "TRON", Enabled: true, DisplayOrder: 30}

	chains := []*Chain{ethChain, tronChain}
	coins := []*Coin{ethCoin, usdtCoin, trxCoin}
	coinChains := []*CoinChain{
		{ID: 1, ChainCode: "ETHEREUM", CoinID: 1, IsNative: true, Decimals: 18, SafeheronCoinKey: "ETH", MinDepositAmount: "0.001", DepositEnabled: true, DisplayOrder: 10},
		{ID: 2, ChainCode: "ETHEREUM", CoinID: 4, IsNative: false, TokenContract: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6, SafeheronCoinKey: "USDT_ERC20", MinDepositAmount: "1", DepositEnabled: true, DisplayOrder: 20},
		{ID: 7, ChainCode: "TRON", CoinID: 3, IsNative: true, Decimals: 6, SafeheronCoinKey: "TRX", MinDepositAmount: "1", DepositEnabled: true, DisplayOrder: 70},
	}
	return chains, coins, coinChains
}

func TestRegistry_Load_Success(t *testing.T) {
	chains, coins, ccs := testData()
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return chains, coins, ccs, nil
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	if err := reg.Load(context.Background()); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if c, ok := reg.GetChain("ETHEREUM"); !ok || c.Name != "Ethereum" {
		t.Fatal("GetChain(ETHEREUM) failed")
	}
	if _, ok := reg.GetChain("BSC"); ok {
		t.Fatal("BSC should not exist")
	}

	if c, ok := reg.GetCoin("ETH"); !ok || c.Name != "Ether" {
		t.Fatal("GetCoin(ETH) failed")
	}
	if c, ok := reg.GetCoinByID(4); !ok || c.Symbol != "USDT" {
		t.Fatal("GetCoinByID(4) failed")
	}

	if cc, ok := reg.GetCoinChain("ETHEREUM", "ETH"); !ok || cc.SafeheronCoinKey != "ETH" {
		t.Fatal("GetCoinChain(ETHEREUM, ETH) failed")
	}
	if cc, ok := reg.GetCoinChainBySafeheronKey("USDT_ERC20"); !ok || cc.Decimals != 6 {
		t.Fatal("GetCoinChainBySafeheronKey(USDT_ERC20) failed")
	}

	evmList := reg.ListEnabledCoinChainsByChain("ETHEREUM")
	if len(evmList) != 2 {
		t.Fatalf("expected 2 EVM coin_chains, got %d", len(evmList))
	}

	tronList := reg.ListEnabledCoinChainsByFamily("TRON")
	if len(tronList) != 1 || tronList[0].SafeheronCoinKey != "TRX" {
		t.Fatalf("TRON family unexpected: %+v", tronList)
	}

	evmKeys := reg.SafeheronCoinKeysByFamily("EVM")
	if len(evmKeys) != 2 {
		t.Fatalf("expected 2 EVM coinKeys, got %d", len(evmKeys))
	}

	allChains := reg.AllChains()
	if len(allChains) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(allChains))
	}
}

// TestRegistry_AllChains_FiltersDisabled verifies disabled chains are not
// exposed by AllChains so supported-chains never advertises a chain ops has
// turned off. Regression: T6-S-1.
func TestRegistry_AllChains_FiltersDisabled(t *testing.T) {
	chains, coins, ccs := testData()
	// Disable TRON
	for _, c := range chains {
		if c.Code == "TRON" {
			c.Enabled = false
		}
	}
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return chains, coins, ccs, nil
		},
	}
	reg := NewRegistry(repo, 60*time.Second)
	if err := reg.Load(context.Background()); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got := reg.AllChains()
	if len(got) != 1 {
		t.Fatalf("expected 1 enabled chain, got %d", len(got))
	}
	if got[0].Code != "ETHEREUM" {
		t.Errorf("expected ETHEREUM remaining, got %s", got[0].Code)
	}
}

func TestRegistry_Load_ChainCoinReferences(t *testing.T) {
	chains, coins, ccs := testData()
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return chains, coins, ccs, nil
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	reg.Load(context.Background())

	cc, _ := reg.GetCoinChainBySafeheronKey("ETH")
	if cc.Chain == nil || cc.Chain.Code != "ETHEREUM" {
		t.Fatal("CoinChain.Chain should reference ETHEREUM")
	}
	if cc.Coin == nil || cc.Coin.Symbol != "ETH" {
		t.Fatal("CoinChain.Coin should reference ETH")
	}
}

func TestRegistry_Load_Failure(t *testing.T) {
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return nil, nil, nil, errors.New("db connection failed")
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	err := reg.Load(context.Background())
	if err == nil || err.Error() != "registry load: db connection failed" {
		t.Fatalf("expected db error, got: %v", err)
	}
}

func TestRegistry_RefreshKeepsOldDataOnFailure(t *testing.T) {
	chains, coins, ccs := testData()
	callNum := 0
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			callNum++
			if callNum == 1 {
				return chains, coins, ccs, nil
			}
			return nil, nil, nil, errors.New("refresh failed")
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	if err := reg.Load(context.Background()); err != nil {
		t.Fatalf("initial Load failed: %v", err)
	}

	err := reg.Load(context.Background())
	if err == nil {
		t.Fatal("expected refresh failure")
	}

	if c, ok := reg.GetChain("ETHEREUM"); !ok || c.Name != "Ethereum" {
		t.Fatal("old data should be preserved after refresh failure")
	}
}

func TestRegistry_BackgroundRefresh(t *testing.T) {
	chains, coins, ccs := testData()
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return chains, coins, ccs, nil
		},
	}

	reg := NewRegistry(repo, 50*time.Millisecond)
	reg.Load(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	reg.StartBackgroundRefresh(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	if repo.getCallCount() < 2 {
		t.Fatalf("expected at least 2 loads (initial + refresh), got %d", repo.getCallCount())
	}
}

func TestRegistry_BackgroundRefreshAlertOnFailure(t *testing.T) {
	callNum := 0
	chains, coins, ccs := testData()
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			callNum++
			if callNum == 1 {
				return chains, coins, ccs, nil
			}
			return nil, nil, nil, errors.New("db down")
		},
	}

	var alertCalled bool
	var alertMu sync.Mutex

	reg := NewRegistry(repo, 50*time.Millisecond)
	reg.SetAlertFunc(func(level, title, message string) {
		alertMu.Lock()
		alertCalled = true
		alertMu.Unlock()
	})

	reg.Load(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	reg.StartBackgroundRefresh(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	alertMu.Lock()
	if !alertCalled {
		t.Fatal("alert function should have been called on refresh failure")
	}
	alertMu.Unlock()
}

func TestRegistry_ConcurrentReadWrite(t *testing.T) {
	chains, coins, ccs := testData()
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return chains, coins, ccs, nil
		},
	}

	reg := NewRegistry(repo, 10*time.Millisecond)
	reg.Load(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	reg.StartBackgroundRefresh(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				reg.GetChain("ETHEREUM")
				reg.GetCoin("ETH")
				reg.GetCoinChainBySafeheronKey("ETH")
				reg.ListEnabledCoinChainsByFamily("EVM")
				reg.SafeheronCoinKeysByFamily("TRON")
			}
		}()
	}

	wg.Wait()
	cancel()
}

func TestRegistry_AllEnabledCoinChains(t *testing.T) {
	chains, coins, ccs := testData()
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return chains, coins, ccs, nil
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	reg.Load(context.Background())

	all := reg.AllEnabledCoinChains()
	if len(all) != 3 {
		t.Fatalf("expected 3 coin_chains, got %d", len(all))
	}

	keys := make(map[string]bool)
	for _, cc := range all {
		keys[cc.SafeheronCoinKey] = true
	}
	for _, expect := range []string{"ETH", "USDT_ERC20", "TRX"} {
		if !keys[expect] {
			t.Errorf("missing coinKey %s", expect)
		}
	}

	for i := 1; i < len(all); i++ {
		if all[i-1].DisplayOrder > all[i].DisplayOrder {
			t.Errorf("AllEnabledCoinChains not sorted: index %d (order %d) > index %d (order %d)",
				i-1, all[i-1].DisplayOrder, i, all[i].DisplayOrder)
		}
	}
}

func TestRegistry_AllEnabledCoinChains_Empty(t *testing.T) {
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return []*Chain{}, []*Coin{}, []*CoinChain{}, nil
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	reg.Load(context.Background())

	all := reg.AllEnabledCoinChains()
	if len(all) != 0 {
		t.Fatalf("expected 0 coin_chains, got %d", len(all))
	}
}

func TestRegistry_AllCoins(t *testing.T) {
	chains, coins, ccs := testData()
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return chains, coins, ccs, nil
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	reg.Load(context.Background())

	all := reg.AllCoins()
	if len(all) != 3 {
		t.Fatalf("expected 3 enabled coins, got %d", len(all))
	}

	symbols := make(map[string]bool)
	for _, c := range all {
		symbols[c.Symbol] = true
	}
	for _, expect := range []string{"ETH", "USDT", "TRX"} {
		if !symbols[expect] {
			t.Errorf("missing coin %s", expect)
		}
	}
}

func TestRegistry_AllCoins_FiltersDisabled(t *testing.T) {
	chains, coins, ccs := testData()
	for _, c := range coins {
		if c.Symbol == "USDT" {
			c.Enabled = false
		}
	}
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return chains, coins, ccs, nil
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	reg.Load(context.Background())

	all := reg.AllCoins()
	if len(all) != 2 {
		t.Fatalf("expected 2 enabled coins (USDT disabled), got %d", len(all))
	}
	for _, c := range all {
		if c.Symbol == "USDT" {
			t.Error("disabled USDT should not appear in AllCoins")
		}
	}
}

func TestRegistry_DefaultRefreshInterval(t *testing.T) {
	reg := NewRegistry(&mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return nil, nil, nil, nil
		},
	}, 0)
	if reg.refreshInterval != 60*time.Second {
		t.Fatalf("expected 60s default, got %v", reg.refreshInterval)
	}
}

func TestRegistry_EmptyData(t *testing.T) {
	repo := &mockRepo{
		loadAllFn: func(_ context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
			return []*Chain{}, []*Coin{}, []*CoinChain{}, nil
		},
	}

	reg := NewRegistry(repo, 60*time.Second)
	reg.Load(context.Background())

	if _, ok := reg.GetChain("ETHEREUM"); ok {
		t.Fatal("should not find any chains")
	}
	if len(reg.AllChains()) != 0 {
		t.Fatal("AllChains should be empty")
	}
}
