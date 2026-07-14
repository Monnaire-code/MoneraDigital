package companyfund

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCoinGeckoCurrentRateRefresher_RefreshesOnlyExplicitPolicyMappings(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	registry := newCurrentRateRefresherRegistry(t, []AccountAssetPolicy{
		{
			ID: 10, AccountID: 1,
			Asset:       AssetIdentity{Currency: "ETH", ChainCode: "ETHEREUM", ProviderAssetKey: "ETH"},
			CoinGeckoID: "ethereum", Enabled: true,
		},
		{
			ID: 11, AccountID: 1,
			Asset:                    AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20", ContractAddress: "0xAbC"},
			CoinGeckoPlatformID:      "ethereum",
			CoinGeckoContractAddress: "0xAbC",
			Enabled:                  true,
		},
		{
			ID: 12, AccountID: 1,
			Asset:   AssetIdentity{Currency: "BTC", ChainCode: "BITCOIN", ProviderAssetKey: "BTC"},
			Enabled: true,
		},
	})
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	client := &fakeCoinGeckoCurrentPriceClient{
		simple: map[string]CoinGeckoPriceBatch{
			"ethereum": fakeCoinGeckoPriceBatch(now, CoinGeckoPrice{
				CoinID: "ethereum", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("2500.123456789012345678", now),
			}),
		},
		tokens: map[string]CoinGeckoPriceBatch{
			"ethereum|0xabc": fakeCoinGeckoPriceBatch(now, CoinGeckoPrice{
				PlatformID: "ethereum", ContractAddress: "0xabc", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("0.999876543210987654", now),
			}),
		},
	}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, registry, cache, CoinGeckoCurrentRateRefresherConfig{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewCoinGeckoCurrentRateRefresher() error = %v", err)
	}

	result, err := refresher.Refresh(context.Background())
	if err != nil || result.Skipped || !result.Refreshed || result.MappingCount != 2 || result.QuoteCount != 2 {
		t.Fatalf("Refresh() = %#v, %v; want two refreshed quotes", result, err)
	}
	if got := client.simpleRequestKeys(); len(got) != 1 || got[0] != "ethereum" {
		t.Fatalf("simple requests = %#v, want only explicit ETH mapping", got)
	}
	if got := client.tokenRequestKeys(); len(got) != 1 || got[0] != "ethereum|0xabc" {
		t.Fatalf("token requests = %#v, want only explicit USDT contract mapping", got)
	}

	ethPolicy, _ := registry.Snapshot().LookupAssetPolicy(1, AssetIdentity{Currency: "ETH", ChainCode: "ETHEREUM", ProviderAssetKey: "ETH"})
	ethKey, ok := CoinGeckoQuoteCacheKeyForPolicy(ethPolicy)
	if !ok {
		t.Fatal("ETH policy should have an explicit cache key")
	}
	eth, found := cache.Get(ethKey)
	if !found || eth.Stale || !eth.Quote.Price.Equal(decimal.RequireFromString("2500.123456789012345678")) {
		t.Fatalf("ETH cache quote = %#v, found=%v", eth, found)
	}

	tokenPolicy, _ := registry.Snapshot().LookupAssetPolicy(1, AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20", ContractAddress: "0xAbC"})
	tokenKey, ok := CoinGeckoQuoteCacheKeyForPolicy(tokenPolicy)
	if !ok {
		t.Fatal("USDT policy should have an explicit contract cache key")
	}
	token, found := cache.Get(tokenKey)
	if !found || token.Stale || !token.Quote.Price.Equal(decimal.RequireFromString("0.999876543210987654")) {
		t.Fatalf("USDT cache quote = %#v, found=%v", token, found)
	}
}

func TestCoinGeckoCurrentRateRefresher_DefaultIntervalMatchesDemoBudget(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	refresher, err := NewCoinGeckoCurrentRateRefresher(
		&fakeCoinGeckoCurrentPriceClient{},
		newCurrentRateRefresherRegistry(t, nil),
		newTestCurrentRateCache(t, &now, 10*time.Minute),
		CoinGeckoCurrentRateRefresherConfig{Clock: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if refresher.RefreshInterval() != 5*time.Minute {
		t.Fatalf("default refresh interval = %s, want 5m", refresher.RefreshInterval())
	}
}

func TestCoinGeckoCurrentRateRefresher_PersistsAllQuotesBeforeCacheSwap(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policies := []AccountAssetPolicy{
		{ID: 10, AccountID: 1, Asset: AssetIdentity{Currency: "ETH", ChainCode: "ETHEREUM", ProviderAssetKey: "ETH"}, CoinGeckoID: "ethereum", Enabled: true},
		{ID: 11, AccountID: 1, Asset: AssetIdentity{Currency: "BTC", ChainCode: "BITCOIN", ProviderAssetKey: "BTC"}, CoinGeckoID: "bitcoin", Enabled: true},
	}
	registry := newCurrentRateRefresherRegistry(t, policies)
	cache := newTestCurrentRateCache(t, &now, 10*time.Minute)
	client := &fakeCoinGeckoCurrentPriceClient{simple: map[string]CoinGeckoPriceBatch{
		"bitcoin,ethereum": fakeCoinGeckoPriceBatch(now,
			CoinGeckoPrice{CoinID: "bitcoin", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("60000", now)},
			CoinGeckoPrice{CoinID: "ethereum", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("2500", now)},
		),
	}}
	store := &fakeCurrentRateSnapshotStore{nextID: 70}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, registry, cache, CoinGeckoCurrentRateRefresherConfig{
		Clock: func() time.Time { return now }, SnapshotStore: store, PolicyVersion: "current-usd-v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(t.Context())
	if err != nil || !result.Refreshed || len(store.appended) != 2 {
		t.Fatalf("Refresh() = %#v, %v; appended=%#v", result, err, store.appended)
	}
	for _, input := range store.appended {
		if input.Provider != "COINGECKO" || input.QuoteCurrency != "USD" ||
			input.Method != string(USDValuationMethodCoinGeckoDirect) ||
			input.Granularity != currentRateSnapshotGranularity ||
			input.PolicyVersion != "current-usd-v1" || input.PriceKind != MarketPriceKindCurrent ||
			input.BucketStart.IsZero() || input.EffectiveAt == nil || input.SourcePayloadDigest == "" {
			t.Fatalf("persisted current snapshot input = %#v", input)
		}
	}
	for _, policy := range policies {
		key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
		if !ok {
			t.Fatalf("policy %#v should create cache key", policy)
		}
		read, found := cache.Get(key)
		if !found || read.Quote.RateSnapshotID < 70 {
			t.Fatalf("persisted cache quote = %#v, found=%v", read, found)
		}
	}
}

func TestCoinGeckoCurrentRateRefresher_PersistenceFailureDoesNotSwapPartialCache(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policies := []AccountAssetPolicy{
		{ID: 10, AccountID: 1, Asset: AssetIdentity{Currency: "ETH"}, CoinGeckoID: "ethereum", Enabled: true},
		{ID: 11, AccountID: 1, Asset: AssetIdentity{Currency: "BTC"}, CoinGeckoID: "bitcoin", Enabled: true},
	}
	cache := newTestCurrentRateCache(t, &now, 10*time.Minute)
	client := &fakeCoinGeckoCurrentPriceClient{simple: map[string]CoinGeckoPriceBatch{
		"bitcoin,ethereum": fakeCoinGeckoPriceBatch(now,
			CoinGeckoPrice{CoinID: "bitcoin", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("60000", now)},
			CoinGeckoPrice{CoinID: "ethereum", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("2500", now)},
		),
	}}
	store := &fakeCurrentRateSnapshotStore{nextID: 70, appendErrAt: 2}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, newCurrentRateRefresherRegistry(t, policies), cache, CoinGeckoCurrentRateRefresherConfig{
		Clock: func() time.Time { return now }, SnapshotStore: store, PolicyVersion: "current-usd-v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(t.Context())
	if err == nil || !result.RefreshFailed || !result.Stale || len(cache.Snapshot().Quotes) != 0 {
		t.Fatalf("Refresh() = %#v, %v; cache=%#v", result, err, cache.Snapshot())
	}
}

func TestCoinGeckoCurrentRateRefresher_RestoresPersistedSnapshotBeforeProviderAttempt(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policy := AccountAssetPolicy{ID: 10, AccountID: 1, Asset: AssetIdentity{Currency: "ETH"}, CoinGeckoID: "ethereum", Enabled: true}
	key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
	if !ok {
		t.Fatal("policy should create cache key")
	}
	store := &fakeCurrentRateSnapshotStore{loaded: map[string]*RateSnapshotRecord{
		key.identity(): {
			ID: 91, Provider: "COINGECKO", AssetIdentityKey: key.AssetIdentityKey, ProviderAssetID: "ethereum",
			BaseCurrency: "ETH", QuoteCurrency: "USD", Rate: decimal.NewFromInt(2400), Method: string(USDValuationMethodCoinGeckoDirect),
			Granularity: "CURRENT", BucketStart: now.Add(-5 * time.Minute), EffectiveAt: timePointerUTC(now.Add(-time.Minute)),
			AvailableAt: now.Add(-time.Minute), FetchedAt: now.Add(-time.Minute), PolicyVersion: "current-usd-v1",
			SourcePayloadDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", IsEligibleLeaf: true,
		},
	}}
	cache := newTestCurrentRateCache(t, &now, 10*time.Minute)
	refresher, err := NewCoinGeckoCurrentRateRefresher(
		&fakeCoinGeckoCurrentPriceClient{simpleErr: errors.New("provider unavailable")},
		newCurrentRateRefresherRegistry(t, []AccountAssetPolicy{policy}),
		cache,
		CoinGeckoCurrentRateRefresherConfig{Clock: func() time.Time { return now }, SnapshotStore: store, PolicyVersion: "current-usd-v1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(t.Context())
	read, found := cache.Get(key)
	if err == nil || !result.RefreshFailed || !found || read.Quote.RateSnapshotID != 91 || !read.Quote.Price.Equal(decimal.NewFromInt(2400)) {
		t.Fatalf("Refresh() = %#v, %v; restored=%#v found=%v", result, err, read, found)
	}
}

func TestCoinGeckoCurrentRateRefresher_SkipsWhenNoExplicitMappingsExist(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	registry := newCurrentRateRefresherRegistry(t, []AccountAssetPolicy{
		{ID: 1, AccountID: 1, Asset: AssetIdentity{Currency: "BTC"}, Enabled: true},
		// USD is always ledger parity and must not cause an exchange-rate call.
		{ID: 2, AccountID: 1, Asset: AssetIdentity{Currency: "USD"}, CoinGeckoID: "fiat:USD", Enabled: true},
	})
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	client := &fakeCoinGeckoCurrentPriceClient{}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, registry, cache, CoinGeckoCurrentRateRefresherConfig{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(context.Background())
	if err != nil || !result.Skipped || result.MappingCount != 0 || result.QuoteCount != 0 {
		t.Fatalf("Refresh() = %#v, %v; want skipped empty mapping refresh", result, err)
	}
	if len(client.simpleRequests) != 0 || len(client.tokenRequests) != 0 || client.exchangeCalls != 0 {
		t.Fatalf("unmapped/USD-parity policy must not make CoinGecko calls: %#v %#v exchange=%d", client.simpleRequests, client.tokenRequests, client.exchangeCalls)
	}
}

func TestCoinGeckoCurrentRateRefresher_DerivesConfiguredFiatUSDFromOneExchangeRatesSnapshot(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policies := []AccountAssetPolicy{
		{ID: 21, AccountID: 1, Asset: AssetIdentity{Currency: "JPY"}, CoinGeckoID: "fiat:JPY", Enabled: true},
		{ID: 22, AccountID: 1, Asset: AssetIdentity{Currency: "SGD"}, CoinGeckoID: "fiat:SGD", Enabled: true},
		{ID: 23, AccountID: 1, Asset: AssetIdentity{Currency: "HKD"}, CoinGeckoID: "fiat:HKD", Enabled: true},
		{ID: 24, AccountID: 1, Asset: AssetIdentity{Currency: "CNY"}, CoinGeckoID: "fiat:CNY", Enabled: true},
	}
	registry := newCurrentRateRefresherRegistry(t, policies)
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	client := &fakeCoinGeckoCurrentPriceClient{exchange: CoinGeckoExchangeRatesBatch{
		FetchedAt:      now,
		ResponseDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Rates: map[string]CoinGeckoExchangeRate{
			"USD": {Code: "USD", Type: "fiat", Value: decimal.NewFromInt(60000)},
			"JPY": {Code: "JPY", Type: "fiat", Value: decimal.NewFromInt(9000000)},
			"SGD": {Code: "SGD", Type: "fiat", Value: decimal.NewFromInt(80000)},
			"HKD": {Code: "HKD", Type: "fiat", Value: decimal.NewFromInt(480000)},
			"CNY": {Code: "CNY", Type: "fiat", Value: decimal.NewFromInt(420000)},
		},
	}}
	store := &fakeCurrentRateSnapshotStore{nextID: 100}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, registry, cache, CoinGeckoCurrentRateRefresherConfig{
		Clock: func() time.Time { return now }, SnapshotStore: store, PolicyVersion: "current-usd-v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(context.Background())
	if err != nil || !result.Refreshed || result.MappingCount != 4 || result.QuoteCount != 4 || client.exchangeCalls != 1 || len(client.simpleRequests) != 0 || len(client.tokenRequests) != 0 {
		t.Fatalf("fiat Refresh() = %#v, %v; client=%#v", result, err, client)
	}
	for _, testCase := range []struct {
		currency string
		want     string
	}{
		{currency: "JPY", want: "0.006666666666666667"},
		{currency: "SGD", want: "0.750000000000000000"},
		{currency: "HKD", want: "0.125000000000000000"},
		{currency: "CNY", want: "0.142857142857142857"},
	} {
		policy := AccountAssetPolicy{Asset: AssetIdentity{Currency: testCase.currency}, CoinGeckoID: "fiat:" + testCase.currency}
		key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
		if !ok || key.FiatCode != testCase.currency {
			t.Fatalf("%s fiat policy cache key = %#v, %v", testCase.currency, key, ok)
		}
		quote, found := cache.Get(key)
		if !found || quote.Stale || !quote.Quote.Price.Equal(decimal.RequireFromString(testCase.want)) || !quote.Quote.ProviderUpdatedAt.Equal(now) || quote.Quote.ResponseDigest != client.exchange.ResponseDigest || quote.Quote.Method != USDValuationMethodCoinGeckoBTCCross || quote.Quote.RateSnapshotID <= 0 {
			t.Fatalf("%s derived fiat quote = %#v, found=%v", testCase.currency, quote, found)
		}
	}
	derivedCount := 0
	for _, input := range store.appended {
		if input.Method != string(USDValuationMethodCoinGeckoBTCCross) {
			continue
		}
		derivedCount++
		if input.NumeratorSnapshotID == nil || input.DenominatorSnapshotID == nil || input.ProviderAssetID == "" {
			t.Fatalf("fiat derived snapshot lacks auditable BTC legs: %#v", input)
		}
	}
	if derivedCount != len(policies) {
		t.Fatalf("derived fiat snapshots = %d, want %d; all=%#v", derivedCount, len(policies), store.appended)
	}
}

func TestCoinGeckoCurrentRateRefresher_ExposesSnapshotRestoreFailureWithoutBlockingProviderRepair(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policy := AccountAssetPolicy{ID: 10, AccountID: 1, Asset: AssetIdentity{Currency: "ETH"}, CoinGeckoID: "ethereum", Enabled: true}
	client := &fakeCoinGeckoCurrentPriceClient{simple: map[string]CoinGeckoPriceBatch{
		"ethereum": fakeCoinGeckoPriceBatch(now, CoinGeckoPrice{CoinID: "ethereum", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("2500", now)}),
	}}
	store := &fakeCurrentRateSnapshotStore{nextID: 70, findErr: errors.New("restore unavailable")}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, newCurrentRateRefresherRegistry(t, []AccountAssetPolicy{policy}), newTestCurrentRateCache(t, &now, 10*time.Minute), CoinGeckoCurrentRateRefresherConfig{
		Clock: func() time.Time { return now }, SnapshotStore: store, PolicyVersion: "current-usd-v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(t.Context())
	status := refresher.Status()
	if err != nil || !result.Refreshed || !result.RestoreFailed || status.LastRestoreError == nil || !status.LastRestoreErrorAt.Equal(now) {
		t.Fatalf("Refresh() = %#v, %v; status=%#v", result, err, status)
	}
}

func TestCoinGeckoFiatCodeForPolicy_RequiresExplicitMatchingNonUSDConfiguration(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		policy AccountAssetPolicy
		want   string
		ok     bool
	}{
		{name: "valid", policy: AccountAssetPolicy{Asset: AssetIdentity{Currency: "JPY"}, CoinGeckoID: "fiat:jpy"}, want: "JPY", ok: true},
		{name: "asset mismatch", policy: AccountAssetPolicy{Asset: AssetIdentity{Currency: "JPY"}, CoinGeckoID: "fiat:SGD"}},
		{name: "USD parity", policy: AccountAssetPolicy{Asset: AssetIdentity{Currency: "USD"}, CoinGeckoID: "fiat:USD"}},
		{name: "contract mixed", policy: AccountAssetPolicy{Asset: AssetIdentity{Currency: "JPY"}, CoinGeckoID: "fiat:JPY", CoinGeckoPlatformID: "ethereum"}},
		{name: "unknown syntax", policy: AccountAssetPolicy{Asset: AssetIdentity{Currency: "JPY"}, CoinGeckoID: "fiat:J-PY"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := CoinGeckoFiatCodeForPolicy(testCase.policy)
			if got != testCase.want || ok != testCase.ok {
				t.Fatalf("CoinGeckoFiatCodeForPolicy() = %q, %v; want %q, %v", got, ok, testCase.want, testCase.ok)
			}
			if !testCase.ok {
				if _, cacheKeyOK := CoinGeckoQuoteCacheKeyForPolicy(testCase.policy); cacheKeyOK {
					t.Fatalf("invalid explicit fiat policy must not fall through to ordinary coin mapping: %#v", testCase.policy)
				}
			}
		})
	}
}

func TestCoinGeckoCurrentRateRefresher_FailureRetainsStaleLastGoodSnapshot(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policy := AccountAssetPolicy{ID: 1, AccountID: 1, Asset: AssetIdentity{Currency: "ETH"}, CoinGeckoID: "ethereum", Enabled: true}
	registry := newCurrentRateRefresherRegistry(t, []AccountAssetPolicy{policy})
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
	if !ok {
		t.Fatal("test policy should create cache key")
	}
	if _, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("2000", now)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	client := &fakeCoinGeckoCurrentPriceClient{simpleErr: errors.New("temporarily unavailable")}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, registry, cache, CoinGeckoCurrentRateRefresherConfig{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(context.Background())
	if err == nil || !result.Stale || !result.RefreshFailed {
		t.Fatalf("Refresh() = %#v, %v; want retained stale result", result, err)
	}
	read, found := cache.Get(key)
	if !found || !read.Stale || !read.RefreshFailed || !read.Quote.Price.Equal(decimal.NewFromInt(2000)) {
		t.Fatalf("cache after failed provider refresh = %#v, found=%v", read, found)
	}
}

func TestCoinGeckoCurrentRateRefresher_IncompleteExplicitFiatResponseRetainsStaleSnapshot(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policy := AccountAssetPolicy{ID: 1, AccountID: 1, Asset: AssetIdentity{Currency: "JPY"}, CoinGeckoID: "fiat:JPY", Enabled: true}
	registry := newCurrentRateRefresherRegistry(t, []AccountAssetPolicy{policy})
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
	if !ok {
		t.Fatal("test fiat policy should create cache key")
	}
	if _, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("0.006", now)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	client := &fakeCoinGeckoCurrentPriceClient{exchange: CoinGeckoExchangeRatesBatch{
		FetchedAt:      now,
		ResponseDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Rates: map[string]CoinGeckoExchangeRate{
			"USD": {Code: "USD", Type: "fiat", Value: decimal.NewFromInt(60000)},
		},
	}}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, registry, cache, CoinGeckoCurrentRateRefresherConfig{Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(context.Background())
	if err == nil || !result.Stale || !result.RefreshFailed || client.exchangeCalls != 1 {
		t.Fatalf("incomplete fiat refresh = %#v, %v", result, err)
	}
	read, found := cache.Get(key)
	if !found || !read.Stale || !read.RefreshFailed || !read.Quote.Price.Equal(decimal.RequireFromString("0.006")) {
		t.Fatalf("incomplete fiat response must retain stale cache quote: %#v found=%v", read, found)
	}
}

func newCurrentRateRefresherRegistry(t *testing.T, policies []AccountAssetPolicy) *AccountRegistry {
	t.Helper()
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return []CompanyFundAccount{{
			ID: 1, Channel: ChannelSafeheron, NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true,
		}}, policies, nil
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatalf("registry.Refresh() error = %v", err)
	}
	return registry
}

type fakeCoinGeckoCurrentPriceClient struct {
	simple         map[string]CoinGeckoPriceBatch
	tokens         map[string]CoinGeckoPriceBatch
	exchange       CoinGeckoExchangeRatesBatch
	simpleErr      error
	tokenErr       error
	exchangeErr    error
	simpleRequests []CoinGeckoSimplePriceRequest
	tokenRequests  []CoinGeckoTokenPriceRequest
	exchangeCalls  int
}

type fakeCurrentRateSnapshotStore struct {
	nextID      int64
	appendErrAt int
	appended    []RateSnapshotInput
	loaded      map[string]*RateSnapshotRecord
	findErr     error
}

func (store *fakeCurrentRateSnapshotStore) AppendRateSnapshot(_ context.Context, input RateSnapshotInput) (RateSnapshotAppendResult, error) {
	store.appended = append(store.appended, input)
	if store.appendErrAt > 0 && len(store.appended) == store.appendErrAt {
		return RateSnapshotAppendResult{}, errors.New("persist current rate snapshot")
	}
	store.nextID++
	record := RateSnapshotRecord{
		ID: store.nextID, Provider: input.Provider, AssetIdentityKey: input.AssetIdentityKey,
		ProviderAssetID: input.ProviderAssetID, ProviderPlatformID: input.ProviderPlatformID, AssetContract: input.AssetContract,
		BaseCurrency: input.BaseCurrency, QuoteCurrency: input.QuoteCurrency, Rate: input.Rate, Method: input.Method,
		Granularity: input.Granularity, BucketStart: input.BucketStart, EffectiveAt: input.EffectiveAt,
		AvailableAt: input.AvailableAt, FetchedAt: input.FetchedAt, SnapshotGroupID: input.SnapshotGroupID,
		PolicyVersion: input.PolicyVersion, SourcePayloadDigest: input.SourcePayloadDigest, IsEligibleLeaf: true,
		NumeratorSnapshotID: input.NumeratorSnapshotID, DenominatorSnapshotID: input.DenominatorSnapshotID,
	}
	return RateSnapshotAppendResult{Snapshot: record, Inserted: true}, nil
}

func (store *fakeCurrentRateSnapshotStore) FindLatestUsableRateSnapshot(_ context.Context, lookup RateSnapshotLookup) (*RateSnapshotRecord, error) {
	if store.findErr != nil {
		return nil, store.findErr
	}
	key := CoinGeckoQuoteCacheKey{
		Provider: lookup.Provider, AssetIdentityKey: lookup.AssetIdentityKey, CoinID: lookup.ProviderAssetID,
		PlatformID: lookup.ProviderPlatformID, ContractAddress: lookup.AssetContract, QuoteCurrency: lookup.QuoteCurrency,
	}
	if len(lookup.ProviderAssetID) > len(coinGeckoFiatMappingPrefix) && lookup.ProviderAssetID[:len(coinGeckoFiatMappingPrefix)] == coinGeckoFiatMappingPrefix {
		key.CoinID = ""
		key.FiatCode = lookup.ProviderAssetID[len(coinGeckoFiatMappingPrefix):]
	}
	return store.loaded[key.identity()], nil
}

func (client *fakeCoinGeckoCurrentPriceClient) FetchSimplePrices(_ context.Context, request CoinGeckoSimplePriceRequest) (CoinGeckoPriceBatch, error) {
	client.simpleRequests = append(client.simpleRequests, request)
	if client.simpleErr != nil {
		return CoinGeckoPriceBatch{}, client.simpleErr
	}
	if batch, found := client.simple[strings.Join(request.CoinIDs, ",")]; found {
		return batch, nil
	}
	return client.simple[request.CoinIDs[0]], nil
}

func (client *fakeCoinGeckoCurrentPriceClient) FetchTokenPrices(_ context.Context, request CoinGeckoTokenPriceRequest) (CoinGeckoPriceBatch, error) {
	client.tokenRequests = append(client.tokenRequests, request)
	if client.tokenErr != nil {
		return CoinGeckoPriceBatch{}, client.tokenErr
	}
	return client.tokens[request.PlatformID+"|"+request.ContractAddresses[0]], nil
}

func (client *fakeCoinGeckoCurrentPriceClient) FetchExchangeRates(context.Context) (CoinGeckoExchangeRatesBatch, error) {
	client.exchangeCalls++
	if client.exchangeErr != nil {
		return CoinGeckoExchangeRatesBatch{}, client.exchangeErr
	}
	return client.exchange, nil
}

func (client *fakeCoinGeckoCurrentPriceClient) simpleRequestKeys() []string {
	keys := make([]string, 0, len(client.simpleRequests))
	for _, request := range client.simpleRequests {
		keys = append(keys, request.CoinIDs...)
	}
	sort.Strings(keys)
	return keys
}

func (client *fakeCoinGeckoCurrentPriceClient) tokenRequestKeys() []string {
	keys := make([]string, 0, len(client.tokenRequests))
	for _, request := range client.tokenRequests {
		for _, contract := range request.ContractAddresses {
			keys = append(keys, request.PlatformID+"|"+contract)
		}
	}
	sort.Strings(keys)
	return keys
}

func fakeCoinGeckoPriceBatch(now time.Time, prices ...CoinGeckoPrice) CoinGeckoPriceBatch {
	return CoinGeckoPriceBatch{Prices: prices, FetchedAt: now, ResponseDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}

func fakeCoinGeckoQuote(value string, now time.Time) *CoinGeckoQuote {
	return &CoinGeckoQuote{
		Price:             decimal.RequireFromString(value),
		ProviderUpdatedAt: now.Add(-time.Second),
		FetchedAt:         now,
		ResponseDigest:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}
