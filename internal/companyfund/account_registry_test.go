package companyfund

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

type accountRegistryLoaderFunc func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error)

func (fn accountRegistryLoaderFunc) LoadCompanyFundAccounts(ctx context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
	return fn(ctx)
}

func TestAccountRegistry_RefreshLoadsOnlyEnabledAccountsAndPolicies(t *testing.T) {
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return registryFixtureAccounts(), registryFixturePolicies(), nil
	}), 0)
	if got := registry.RefreshInterval(); got != time.Minute {
		t.Fatalf("default refresh interval = %s, want 1m", got)
	}
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	if account, ok := registry.LookupSafeheron("EVM", "0xABCD"); !ok || account.ID != 1 {
		t.Fatalf("LookupSafeheron EVM normalization = %#v, %v", account, ok)
	}
	if _, ok := registry.LookupSafeheron("EVM", "0xdead"); ok {
		t.Fatal("disabled Safeheron account must not be loaded")
	}
	if account, ok := registry.LookupAirwallex("awx-main"); !ok || account.ID != 2 {
		t.Fatalf("LookupAirwallex = %#v, %v", account, ok)
	}
	if _, ok := registry.LookupAirwallex("awx-disabled"); ok {
		t.Fatal("disabled Airwallex account must not be loaded")
	}

	policy, ok := registry.LookupAssetPolicy(1, AssetIdentity{
		Currency:         "usdt",
		ChainCode:        "ethereum",
		ProviderAssetKey: "USDT_ERC20",
		ContractAddress:  "0xDac17F958D2ee523a2206206994597C13D831ec7",
	})
	if !ok || policy.ID != 12 {
		t.Fatalf("specific enabled policy = %#v, %v; want ID 12", policy, ok)
	}
	if policy.Dust.Threshold == nil || !policy.Dust.Threshold.Equal(decimal.RequireFromString("0.000001")) {
		t.Fatalf("policy dust threshold must retain decimal precision: %#v", policy.Dust.Threshold)
	}
}

func TestAccountRegistry_SafeheronPreservesCaseSensitiveAddresses(t *testing.T) {
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return registryFixtureAccounts(), nil, nil
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if account, ok := registry.LookupSafeheron("TRON", "TAbC123"); !ok || account.ID != 3 {
		t.Fatalf("case-sensitive lookup = %#v, %v", account, ok)
	}
	if _, ok := registry.LookupSafeheron("TRON", "tabc123"); ok {
		t.Fatal("non-EVM case-sensitive address must not be lowercased")
	}
}

func TestAccountRegistry_SafeheronUsesWalletAddressWhenNormalizedAddressIsAbsent(t *testing.T) {
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return []CompanyFundAccount{{
			ID:            9,
			Channel:       ChannelSafeheron,
			WalletAddress: "0xAbCd",
			NetworkFamily: "EVM",
			AccountName:   "wallet-fallback",
			Enabled:       true,
		}}, nil, nil
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if account, ok := registry.LookupSafeheron("EVM", "0xABCD"); !ok || account.ID != 9 {
		t.Fatalf("wallet-address fallback lookup = %#v, %v", account, ok)
	}
}

func TestAccountRegistry_SafeheronProviderAccountKeyMembershipIsExactAndMayCoverMultipleWallets(t *testing.T) {
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return []CompanyFundAccount{
			{ID: 21, Channel: ChannelSafeheron, ProviderAccountKey: "safe-vault-main", NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true},
			{ID: 22, Channel: ChannelSafeheron, ProviderAccountKey: "safe-vault-main", NormalizedAddress: "0xdef", NetworkFamily: "EVM", Enabled: true},
		}, nil, nil
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !registry.HasSafeheronProviderAccountKey("safe-vault-main") {
		t.Fatal("configured Safeheron provider account key must be present")
	}
	if registry.HasSafeheronProviderAccountKey("SAFE-VAULT-MAIN") || registry.HasSafeheronProviderAccountKey(" safe-vault-main ") {
		t.Fatal("Safeheron provider account key lookup must be exact and must not trim caller input")
	}
}

func TestAccountRegistry_RefreshFailureRetainsLastGoodSnapshot(t *testing.T) {
	call := 0
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		call++
		if call == 1 {
			return registryFixtureAccounts(), registryFixturePolicies(), nil
		}
		return nil, nil, errors.New("database unavailable")
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := registry.Snapshot()
	if err := registry.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh() should expose loader failure")
	}
	if registry.Snapshot() != before {
		t.Fatal("failed refresh must retain the exact last-good snapshot pointer")
	}
	if _, ok := registry.LookupAirwallex("awx-main"); !ok {
		t.Fatal("last-good lookup disappeared after refresh failure")
	}
	status := registry.Status()
	if status.LastRefreshError == nil || status.Age < 0 || status.LastSuccessfulRefreshAt.IsZero() {
		t.Fatalf("failed refresh status = %#v", status)
	}
}

func TestAccountRegistry_SuccessfulEmptyRefreshSwapsToEmptySnapshot(t *testing.T) {
	accounts := registryFixtureAccounts()
	policies := registryFixturePolicies()
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return accounts, policies, nil
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := registry.Snapshot()
	accounts = nil
	policies = nil
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Snapshot() == before {
		t.Fatal("successful empty result must atomically replace the prior snapshot")
	}
	if _, ok := registry.LookupAirwallex("awx-main"); ok {
		t.Fatal("successful empty snapshot must not retain old accounts")
	}
}

func TestAccountRegistry_SnapshotIsStableAcrossRefreshAndConcurrentReaders(t *testing.T) {
	var stateMu sync.RWMutex
	currentName := "before"
	loader := accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		stateMu.RLock()
		name := currentName
		stateMu.RUnlock()
		return []CompanyFundAccount{{
			ID:                1,
			Channel:           ChannelSafeheron,
			NormalizedAddress: "0xabc",
			NetworkFamily:     "EVM",
			AccountName:       name,
			Enabled:           true,
		}}, nil, nil
	})
	registry := NewAccountRegistry(loader, time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := registry.Snapshot()

	stateMu.Lock()
	currentName = "after"
	stateMu.Unlock()
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if account, ok := before.LookupSafeheron("EVM", "0xABC"); !ok || account.AccountName != "before" {
		t.Fatalf("held snapshot changed after refresh: %#v, %v", account, ok)
	}

	var readers sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				account, ok := registry.Snapshot().LookupSafeheron("EVM", "0xABC")
				if !ok || (account.AccountName != "before" && account.AccountName != "after") {
					t.Errorf("reader observed partial snapshot: %#v, %v", account, ok)
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		stateMu.Lock()
		if i%2 == 0 {
			currentName = "before"
		} else {
			currentName = "after"
		}
		stateMu.Unlock()
		if err := registry.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
}

func TestAccountRegistrySnapshot_AssetPoliciesAreDetachedAndStable(t *testing.T) {
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return registryFixtureAccounts(), registryFixturePolicies(), nil
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	policies := snapshot.AssetPolicies()
	if len(policies) == 0 {
		t.Fatal("AssetPolicies() should expose enabled policy snapshots")
	}
	if policies[0].AccountID > policies[len(policies)-1].AccountID {
		t.Fatalf("AssetPolicies() must be stable by account/policy ID: %#v", policies)
	}
	if policies[0].Dust.Threshold != nil {
		*policies[0].Dust.Threshold = decimal.NewFromInt(999)
	}
	again := snapshot.AssetPolicies()
	if policies[0].Dust.Threshold != nil && again[0].Dust.Threshold != nil && again[0].Dust.Threshold.Equal(decimal.NewFromInt(999)) {
		t.Fatal("mutating returned AssetPolicies() must not change immutable registry snapshot")
	}
}

func TestAccountRegistry_StartHonorsCancellationAndDoesNotDuplicateLoop(t *testing.T) {
	entered := make(chan struct{}, 2)
	loader := accountRegistryLoaderFunc(func(ctx context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
	registry := NewAccountRegistry(loader, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	registry.Start(ctx)
	registry.Start(ctx)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("background registry did not invoke the loader")
	}
	select {
	case <-entered:
		t.Fatal("duplicate Start created a second refresh loop")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	registry.Stop()
}

func TestPostgresAccountRegistryLoader_LoadsEnabledRowsWithExactDecimalThreshold(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, channel, COALESCE\\(provider_account_key, ''\\)").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "channel", "provider_account_key", "wallet_address", "normalized_address", "network_family",
			"company_entity", "fund_account_name", "sub_account_name", "account_type", "account_name", "account_role", "is_enabled",
		}).
			AddRow(int64(1), "SAFEHERON", "", "0xAbC", "0xabc", "EVM", "Monera", "Treasury", "Main", "WALLET", "Safe EVM", "TREASURY", true))
	mock.ExpectQuery("SELECT id, company_fund_account_id, currency, COALESCE\\(chain_code, ''\\)").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "company_fund_account_id", "currency", "chain_code", "provider_asset_key", "asset_contract",
			"dust_detection_enabled", "dust_threshold", "auto_exclude_dust_from_summary", "valuation_provider_asset_id",
			"valuation_provider_platform_id", "valuation_contract_address", "is_enabled",
		}).
			AddRow(int64(12), int64(1), "USDT", "ETHEREUM", "USDT_ERC20", "0xdAC17F958D2ee523a2206206994597C13D831ec7", true, "0.000000000000000001", true, "tether", "ethereum", "0xdac17f958d2ee523a2206206994597c13d831ec7", true))
	mock.ExpectCommit()

	accounts, policies, err := NewPostgresAccountRegistryLoader(db).LoadCompanyFundAccounts(context.Background())
	if err != nil {
		t.Fatalf("LoadCompanyFundAccounts() error = %v", err)
	}
	if len(accounts) != 1 || accounts[0].Channel != ChannelSafeheron || !accounts[0].Enabled {
		t.Fatalf("accounts = %#v", accounts)
	}
	if len(policies) != 1 || policies[0].Dust.Threshold == nil || !policies[0].Dust.Threshold.Equal(decimal.RequireFromString("0.000000000000000001")) {
		t.Fatalf("policies lost exact decimal threshold: %#v", policies)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresAccountRegistryLoader_RollsBackConsistentReadOnPolicyQueryFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, channel, COALESCE\\(provider_account_key, ''\\)").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "channel", "provider_account_key", "wallet_address", "normalized_address", "network_family",
			"company_entity", "fund_account_name", "sub_account_name", "account_type", "account_name", "account_role", "is_enabled",
		}))
	mock.ExpectQuery("SELECT id, company_fund_account_id, currency, COALESCE\\(chain_code, ''\\)").
		WillReturnError(errors.New("policy query failed"))
	mock.ExpectRollback()

	if _, _, err := NewPostgresAccountRegistryLoader(db).LoadCompanyFundAccounts(context.Background()); err == nil {
		t.Fatal("LoadCompanyFundAccounts() should return policy query error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountRegistry_ConcurrentRefreshSerializesOldAndNewSnapshots(t *testing.T) {
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{}, 1)
	var loaderMu sync.Mutex
	call := 0
	loader := accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		loaderMu.Lock()
		call++
		currentCall := call
		loaderMu.Unlock()
		if currentCall == 1 {
			close(firstEntered)
			<-releaseFirst
			return registryAccountNamed("old"), nil, nil
		}
		secondEntered <- struct{}{}
		return registryAccountNamed("new"), nil, nil
	})
	registry := NewAccountRegistry(loader, time.Minute)

	firstDone := make(chan error, 1)
	go func() { firstDone <- registry.Refresh(context.Background()) }()
	<-firstEntered
	secondDone := make(chan error, 1)
	go func() { secondDone <- registry.Refresh(context.Background()) }()

	select {
	case <-secondEntered:
		t.Fatal("second loader call started before first refresh released; refreshes are not serialized")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second Refresh() error = %v", err)
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second refresh never reached loader")
	}
	if account, ok := registry.LookupSafeheron("EVM", "0xabc"); !ok || account.AccountName != "new" {
		t.Fatalf("later refresh must win after serialization: %#v, %v", account, ok)
	}
}

func registryFixtureAccounts() []CompanyFundAccount {
	return []CompanyFundAccount{
		{ID: 1, Channel: ChannelSafeheron, ProviderAccountKey: "safe-vault-main", NormalizedAddress: "0xAbCd", NetworkFamily: "EVM", AccountName: "safe-evm", Enabled: true},
		{ID: 2, Channel: ChannelAirwallex, ProviderAccountKey: "awx-main", AccountName: "airwallex", Enabled: true},
		{ID: 3, Channel: ChannelSafeheron, NormalizedAddress: "TAbC123", NetworkFamily: "TRON", AccountName: "safe-tron", Enabled: true},
		{ID: 4, Channel: ChannelSafeheron, NormalizedAddress: "0xdead", NetworkFamily: "EVM", AccountName: "disabled-safe", Enabled: false},
		{ID: 5, Channel: ChannelAirwallex, ProviderAccountKey: "awx-disabled", AccountName: "disabled-airwallex", Enabled: false},
	}
}

func registryAccountNamed(name string) []CompanyFundAccount {
	return []CompanyFundAccount{{
		ID:                1,
		Channel:           ChannelSafeheron,
		NormalizedAddress: "0xabc",
		NetworkFamily:     "EVM",
		AccountName:       name,
		Enabled:           true,
	}}
}

func registryFixturePolicies() []AccountAssetPolicy {
	wildcardThreshold := decimal.RequireFromString("1")
	specificThreshold := decimal.RequireFromString("0.000001")
	return []AccountAssetPolicy{
		{ID: 11, AccountID: 1, Asset: AssetIdentity{Currency: "USDT"}, Dust: DustPolicy{Threshold: &wildcardThreshold}, Enabled: true},
		{ID: 12, AccountID: 1, Asset: AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20", ContractAddress: "0xdAC17F958D2ee523a2206206994597C13D831ec7"}, Dust: DustPolicy{Threshold: &specificThreshold}, Enabled: true},
		{ID: 13, AccountID: 1, Asset: AssetIdentity{Currency: "USDT"}, Enabled: false},
		{ID: 14, AccountID: 4, Asset: AssetIdentity{Currency: "USDT"}, Enabled: true},
	}
}
