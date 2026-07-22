package pool

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
	walletconfig "monera-digital/internal/wallet/config"
)

type mockRepo struct {
	getUserAddr   func(ctx context.Context, userID int, family string) (*Address, error)
	assignAvail   func(ctx context.Context, userID int, family string) (*Address, error)
	countByStatus func(ctx context.Context, family, status string) (int, error)
	bulkInsert    func(ctx context.Context, addrs []*Address) error
}

func (m *mockRepo) GetUserAddress(ctx context.Context, userID int, family string) (*Address, error) {
	return m.getUserAddr(ctx, userID, family)
}
func (m *mockRepo) AssignAvailable(ctx context.Context, userID int, family string) (*Address, error) {
	return m.assignAvail(ctx, userID, family)
}
func (m *mockRepo) CountByStatus(ctx context.Context, family, status string) (int, error) {
	return m.countByStatus(ctx, family, status)
}
func (m *mockRepo) BulkInsert(ctx context.Context, addrs []*Address) error {
	return m.bulkInsert(ctx, addrs)
}

type mockClient struct {
	createWallet func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error)
	addCoin      func(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error)
}

func (m *mockClient) CreateAssetWallet(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
	return m.createWallet(ctx, customerRefID, coinKeys)
}
func (m *mockClient) AddCoin(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error) {
	return m.addCoin(ctx, accountKey, coinKeys)
}
func (m *mockClient) ListAccountCoin(ctx context.Context, accountKey string) ([]safeheron.AccountCoin, error) {
	return nil, nil
}
func (m *mockClient) GetAccountByAddress(ctx context.Context, address string) (*safeheron.Account, error) {
	return nil, nil
}
func (m *mockClient) KytReport(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
	return nil, nil
}
func (m *mockClient) CreateTransaction(_ context.Context, _ safeheron.CreateTransactionRequest) (*safeheron.CreateTransactionResponse, error) {
	return nil, nil
}
func (m *mockClient) GetTransaction(_ context.Context, _ string) (*safeheron.TransactionDetail, error) {
	return nil, nil
}
func (m *mockClient) WebhookConvert(rawBody []byte) (*safeheron.WebhookEvent, error) {
	return nil, nil
}
func (m *mockClient) Close() error { return nil }

func testRegistry() *walletconfig.Registry {
	repo := &mockConfigRepo{}
	reg := walletconfig.NewRegistry(repo, 0)
	_ = reg.Load(context.Background())
	return reg
}

type mockConfigRepo struct{}

func (m *mockConfigRepo) LoadAll(ctx context.Context) ([]*walletconfig.Chain, []*walletconfig.Coin, []*walletconfig.CoinChain, error) {
	chains := []*walletconfig.Chain{
		{Code: "ETHEREUM", Name: "Ethereum", NetworkFamily: "EVM", Enabled: true},
		{Code: "TRON", Name: "Tron", NetworkFamily: "TRON", Enabled: true},
	}
	coins := []*walletconfig.Coin{
		{ID: 1, Symbol: "ETH", Enabled: true},
		{ID: 2, Symbol: "TRX", Enabled: true},
	}
	coinChains := []*walletconfig.CoinChain{
		{ID: 1, ChainCode: "ETHEREUM", CoinID: 1, SafeheronCoinKey: "ETH_SEPOLIA", DepositEnabled: true},
		{ID: 2, ChainCode: "TRON", CoinID: 2, SafeheronCoinKey: "TRX_SHASTA", DepositEnabled: true},
	}
	return chains, coins, coinChains, nil
}

func newTestManager(repo Repository, client safeheron.SafeheronClient, reg *walletconfig.Registry) *Manager {
	mgr := NewManager(repo, client, reg)
	mgr.retryDelays = []time.Duration{0, 0, 0}
	return mgr
}

func TestGetOrAssign_ExistingAddress(t *testing.T) {
	existing := &Address{ID: 1, Address: "0xabc", Status: StatusAssigned}
	repo := &mockRepo{
		getUserAddr: func(ctx context.Context, userID int, family string) (*Address, error) {
			return existing, nil
		},
	}
	mgr := NewManager(repo, nil, nil)
	wakeCount := 0
	mgr.SetOnAllocated(func() { wakeCount++ })

	addr, err := mgr.GetOrAssign(context.Background(), 1, "EVM")
	if err != nil {
		t.Fatal(err)
	}
	if addr.Address != "0xabc" {
		t.Errorf("expected existing address")
	}
	if wakeCount != 0 {
		t.Fatalf("existing assignment must not wake replenisher: %d", wakeCount)
	}
}

func TestGetOrAssign_AssignNew(t *testing.T) {
	assigned := &Address{ID: 5, Address: "0xnew", Status: StatusAssigned}
	repo := &mockRepo{
		getUserAddr: func(ctx context.Context, userID int, family string) (*Address, error) {
			return nil, sql.ErrNoRows
		},
		assignAvail: func(ctx context.Context, userID int, family string) (*Address, error) {
			return assigned, nil
		},
	}
	mgr := NewManager(repo, nil, nil)
	wakeCount := 0
	mgr.SetOnAllocated(func() { wakeCount++ })

	addr, err := mgr.GetOrAssign(context.Background(), 2, "EVM")
	if err != nil {
		t.Fatal(err)
	}
	if addr.ID != 5 {
		t.Errorf("expected newly assigned address")
	}
	if wakeCount != 1 {
		t.Fatalf("replenisher wakes=%d, want 1 after allocation", wakeCount)
	}
}

func TestGetOrAssign_PoolEmptyTriggersReplenish(t *testing.T) {
	callCount := 0
	repo := &mockRepo{
		getUserAddr: func(ctx context.Context, userID int, family string) (*Address, error) {
			return nil, sql.ErrNoRows
		},
		assignAvail: func(ctx context.Context, userID int, family string) (*Address, error) {
			callCount++
			if callCount == 1 {
				return nil, ErrPoolEmpty
			}
			return &Address{ID: 10, Address: "0xreplenished", Status: StatusAssigned}, nil
		},
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 0, nil
		},
		bulkInsert: func(ctx context.Context, addrs []*Address) error {
			return nil
		},
	}

	client := &mockClient{
		createWallet: func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
			return &safeheron.Wallet{
				AccountKey: "ak-new",
				CoinAddressList: []safeheron.CoinAddress{
					{Address: "0xfresh", CoinKey: "ETH"},
				},
			}, nil
		},
		addCoin: func(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error) {
			return &safeheron.Wallet{}, nil
		},
	}

	reg := testRegistry()
	mgr := newTestManager(repo, client, reg)

	addr, err := mgr.GetOrAssign(context.Background(), 3, "EVM")
	if err != nil {
		t.Fatal(err)
	}
	if addr.Address != "0xreplenished" {
		t.Errorf("expected replenished address, got %s", addr.Address)
	}
}

func TestGetOrAssign_ReplenishFails(t *testing.T) {
	repo := &mockRepo{
		getUserAddr: func(ctx context.Context, userID int, family string) (*Address, error) {
			return nil, sql.ErrNoRows
		},
		assignAvail: func(ctx context.Context, userID int, family string) (*Address, error) {
			return nil, ErrPoolEmpty
		},
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 0, nil
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

	_, err := mgr.GetOrAssign(context.Background(), 4, "EVM")
	if err == nil {
		t.Fatal("expected error when replenish fails")
	}
}

func TestReplenish_AlreadyFull(t *testing.T) {
	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 100, nil
		},
	}
	mgr := NewManager(repo, nil, nil)

	err := mgr.Replenish(context.Background(), "EVM", 100)
	if err != nil {
		t.Fatal(err)
	}
}

func TestReplenish_CreatesWallets(t *testing.T) {
	var inserted []*Address
	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 0, nil
		},
		bulkInsert: func(ctx context.Context, addrs []*Address) error {
			inserted = addrs
			return nil
		},
	}

	createCount := 0
	client := &mockClient{
		createWallet: func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
			createCount++
			return &safeheron.Wallet{
				AccountKey: "ak-" + customerRefID[:8],
				CoinAddressList: []safeheron.CoinAddress{
					{Address: "0x" + customerRefID[:8], CoinKey: "ETH"},
				},
			}, nil
		},
		addCoin: func(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error) {
			return &safeheron.Wallet{}, nil
		},
	}

	reg := testRegistry()
	mgr := newTestManager(repo, client, reg)

	err := mgr.Replenish(context.Background(), "EVM", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(inserted) != 3 {
		t.Errorf("expected 3 addresses, got %d", len(inserted))
	}
	if createCount != 3 {
		t.Errorf("expected 3 wallet creations, got %d", createCount)
	}
}

func TestReplenish_PartialFailure(t *testing.T) {
	var inserted []*Address
	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 0, nil
		},
		bulkInsert: func(ctx context.Context, addrs []*Address) error {
			inserted = append(inserted, addrs...)
			return nil
		},
	}

	callCount := 0
	client := &mockClient{
		createWallet: func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
			callCount++
			if callCount == 2 {
				return nil, errors.New("permanent sdk error")
			}
			return &safeheron.Wallet{
				AccountKey:      "ak-" + customerRefID[:8],
				CoinAddressList: []safeheron.CoinAddress{{Address: "0x" + customerRefID[:8]}},
			}, nil
		},
	}

	reg := testRegistry()
	mgr := NewManager(repo, client, reg)
	mgr.retryDelays = nil

	err := mgr.Replenish(context.Background(), "EVM", 3)
	if err == nil {
		t.Fatal("expected error for partial failure")
	}
	if len(inserted) != 2 {
		t.Errorf("expected 2 successful addresses, got %d", len(inserted))
	}
}

func TestReplenish_NoCoinKeys(t *testing.T) {
	repo := &mockRepo{
		countByStatus: func(ctx context.Context, family, status string) (int, error) {
			return 0, nil
		},
	}

	emptyRepo := &mockConfigRepo{}
	reg := walletconfig.NewRegistry(emptyRepo, 0)
	_ = reg.Load(context.Background())

	mgr := NewManager(repo, nil, reg)

	err := mgr.Replenish(context.Background(), "UNKNOWN", 10)
	if err == nil {
		t.Fatal("expected error for unknown family")
	}
}

func TestCreateWithRetry_SuccessOnSecondAttempt(t *testing.T) {
	callCount := 0
	client := &mockClient{
		createWallet: func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
			callCount++
			if callCount == 1 {
				return nil, errors.New("temp failure")
			}
			return &safeheron.Wallet{AccountKey: "ak"}, nil
		},
	}

	reg := testRegistry()
	mgr := newTestManager(nil, client, reg)

	wallet, err := mgr.createWithRetry(context.Background(), "cref", []string{"ETH"})
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AccountKey != "ak" {
		t.Errorf("unexpected wallet")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestCreateWithRetry_AllFail(t *testing.T) {
	client := &mockClient{
		createWallet: func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
			return nil, errors.New("permanent failure")
		},
	}

	reg := testRegistry()
	mgr := newTestManager(nil, client, reg)

	_, err := mgr.createWithRetry(context.Background(), "cref", []string{"ETH"})
	if err == nil {
		t.Fatal("expected error after all retries")
	}
}

func TestCreateWithRetry_ContextCancelled(t *testing.T) {
	client := &mockClient{
		createWallet: func(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
			return nil, errors.New("fail")
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := NewManager(nil, client, nil)
	mgr.retryDelays = []time.Duration{time.Hour}

	_, err := mgr.createWithRetry(ctx, "cref", []string{"ETH"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSetAlertFunc(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	called := false
	mgr.SetAlertFunc(func(level, title, message string) {
		called = true
	})
	if mgr.alertFn == nil {
		t.Fatal("alert func not set")
	}
	mgr.alertFn("INFO", "test", "msg")
	if !called {
		t.Error("alert func not called")
	}
}
