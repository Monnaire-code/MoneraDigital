package companyfund

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCompanyFundCurrentValuator_ValuesUSDCurrencyAtPar(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{
		10: newValuationRuntimeCandidate(10, "USD", decimal.RequireFromString("123.456789012345678")),
	}}
	valuator := newTestCompanyFundCurrentValuatorWithConfig(t, now, store, nil, nil, CompanyFundCurrentValuatorConfig{DefaultMappings: mappings})

	result := valuator.ValueTransaction(t.Context(), 10)
	if result.Err != nil || !result.Applied || result.Result.Status != USDValuationStatusFinal || result.Result.Source != USDValuationSourceUSDPar {
		t.Fatalf("ValueTransaction() = %#v; want final USD-par valuation", result)
	}
	if result.Result.Value == nil || !result.Result.Value.Equal(decimal.RequireFromString("123.456789012345678")) {
		t.Fatalf("USD-par value = %#v", result.Result.Value)
	}
	if len(store.applies) != 1 || store.applies[0].CalculatedUSDValue == nil || !store.applies[0].CalculatedUSDValue.Equal(*result.Result.Value) {
		t.Fatalf("USD-par apply input = %#v; want calculated exact value", store.applies)
	}
}

func TestCompanyFundCurrentValuator_UnrecognizedUSDWithoutProviderValueStaysMappingMissing(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	candidate := newValuationRuntimeCandidate(16, "USD", decimal.NewFromInt(2))
	candidate.IsUnrecognizedAsset = true
	store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{16: candidate}}
	valuator := newTestCompanyFundCurrentValuator(t, now, store, nil, nil)

	result := valuator.ValueTransaction(context.Background(), 16)
	if result.Err != nil || !result.Applied || result.Result.Value != nil || result.Result.Status != USDValuationStatusUnpriced || result.Result.Reason != USDValuationReasonMappingMissing {
		t.Fatalf("unrecognized policyless USD valuation = %#v", result)
	}
}

func TestCompanyFundCurrentValuator_PrefersEligibleProviderTransactionUSD(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	candidate := newValuationRuntimeCandidate(14, "ETH", decimal.NewFromInt(2))
	providerFactID := int64(44)
	providerUSD := decimal.NewFromInt(50)
	candidate.ProviderTransactionFactID = &providerFactID
	candidate.ProviderReportedUSD = &providerUSD
	candidate.ProviderValueScope = ProviderValueScopeDirectItem
	candidate.ProviderAllocationState = ProviderFactAllocationStateNotApplicable
	store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{14: candidate}}
	valuator := newTestCompanyFundCurrentValuator(t, now, store, nil, nil)

	result := valuator.ValueTransaction(context.Background(), 14)
	if result.Err != nil || !result.Applied || result.Result.Status != USDValuationStatusFinal || result.Result.Source != USDValuationSourceSafeheron || result.Result.Value == nil || !result.Result.Value.Equal(providerUSD) {
		t.Fatalf("provider transaction valuation = %#v", result)
	}
	if len(store.applies) != 1 || store.applies[0].CalculatedUSDValue != nil || store.applies[0].ProviderTransactionFactID == nil || *store.applies[0].ProviderTransactionFactID != providerFactID {
		t.Fatalf("provider transaction apply input = %#v", store.applies)
	}
}

func TestCompanyFundCurrentValuator_UsesFreshCurrentQuoteAsProvisional(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policy := AccountAssetPolicy{
		ID: 7, AccountID: 9,
		Asset:       AssetIdentity{Currency: "ETH", ChainCode: "ETHEREUM", ProviderAssetKey: "ETH"},
		CoinGeckoID: "ethereum", Enabled: true,
	}
	registry := newCurrentRateRefresherRegistryWithAccount(t, 9, []AccountAssetPolicy{policy})
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
	if !ok {
		t.Fatal("policy should have cache key")
	}
	if _, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		quote := newCoinGeckoCacheQuote("2500.123456789012345678", now)
		quote.RateSnapshotID = 91
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: quote}, nil
	}); err != nil {
		t.Fatal(err)
	}
	candidate := newValuationRuntimeCandidate(11, "ETH", decimal.RequireFromString("1.5"))
	candidate.ToCompanyFundAccountID = int64Pointer(9)
	candidate.Asset = policy.Asset
	store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{11: candidate}}
	valuator := newTestCompanyFundCurrentValuator(t, now, store, registry, cache)

	result := valuator.ValueTransaction(context.Background(), 11)
	if result.Err != nil || result.Result.Status != USDValuationStatusProvisional || result.Result.Source != USDValuationSourceCoinGecko || result.Result.Basis != USDValuationBasisIngestionTime {
		t.Fatalf("ValueTransaction() = %#v; want provisional fresh CoinGecko valuation", result)
	}
	if result.Result.Value == nil || !result.Result.Value.Equal(decimal.RequireFromString("3750.185185183518518517")) {
		t.Fatalf("current-market USD value = %#v", result.Result.Value)
	}
	if len(store.applies) != 1 || store.applies[0].CalculatedUSDValue == nil || !store.applies[0].CalculatedUSDValue.Equal(*result.Result.Value) || store.applies[0].DerivationMethod != ValuationDerivationMethodMarketPrice || store.applies[0].RateSnapshotID == nil || *store.applies[0].RateSnapshotID != 91 {
		t.Fatalf("market apply input = %#v", store.applies)
	}
}

func TestCompanyFundCurrentValuator_UsesConfiguredSystemDefaultForRecognizedPolicylessAsset(t *testing.T) {
	now := time.Date(2026, time.July, 16, 5, 0, 0, 0, time.UTC)
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	key, ok := CoinGeckoQuoteCacheKeyForDefault(AssetIdentity{Currency: "USDT"}, mappings)
	if !ok {
		t.Fatal("USDT system default should have a cache key")
	}
	if _, err := cache.Refresh(t.Context(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("0.999", now)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	candidate := newValuationRuntimeCandidate(17, "USDT", decimal.RequireFromString("0.01"))
	candidate.ToCompanyFundAccountID = int64Pointer(9)
	candidate.Asset = AssetIdentity{Currency: "USDT", ChainCode: "BINANCE_SMART_CHAIN", ProviderAssetKey: "USDT_BEP20"}
	store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{candidate.ID: candidate}}
	valuator := newTestCompanyFundCurrentValuatorWithConfig(t, now, store, newCurrentRateRefresherRegistryWithAccount(t, 9, nil), cache, CompanyFundCurrentValuatorConfig{
		DefaultMappings: mappings,
	})

	result := valuator.ValueTransaction(t.Context(), candidate.ID)
	if result.Err != nil || !result.Applied || result.Result.Status != USDValuationStatusProvisional || result.Result.Source != USDValuationSourceCoinGecko || result.Result.Value == nil || !result.Result.Value.Equal(decimal.RequireFromString("0.00999")) {
		t.Fatalf("policyless default valuation = %#v", result)
	}
}

func TestCompanyFundCurrentValuator_AccountPolicyPrecedenceOverSystemDefault(t *testing.T) {
	now := time.Date(2026, time.July, 16, 5, 0, 0, 0, time.UTC)
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	asset := AssetIdentity{Currency: "USDT", ChainCode: "BINANCE_SMART_CHAIN", ProviderAssetKey: "USDT_BEP20"}
	defaultKey, ok := CoinGeckoQuoteCacheKeyForDefault(asset, mappings)
	if !ok {
		t.Fatal("USDT system default should have a cache key")
	}
	policy := AccountAssetPolicy{ID: 18, AccountID: 9, Asset: asset, CoinGeckoID: "bridged-tether", Enabled: true}
	policyKey, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
	if !ok {
		t.Fatal("explicit account policy should have a cache key")
	}
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	if _, err := cache.Refresh(t.Context(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{
			defaultKey: newCoinGeckoCacheQuote("1", now),
			policyKey:  newCoinGeckoCacheQuote("2", now),
		}, nil
	}); err != nil {
		t.Fatal(err)
	}
	candidate := newValuationRuntimeCandidate(18, "USDT", decimal.NewFromInt(3))
	candidate.ToCompanyFundAccountID = int64Pointer(policy.AccountID)
	candidate.Asset = asset
	store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{candidate.ID: candidate}}
	valuator := newTestCompanyFundCurrentValuatorWithConfig(t, now, store, newCurrentRateRefresherRegistryWithAccount(t, policy.AccountID, []AccountAssetPolicy{policy}), cache, CompanyFundCurrentValuatorConfig{
		DefaultMappings: mappings,
	})

	result := valuator.ValueTransaction(t.Context(), candidate.ID)
	if result.Err != nil || result.Result.Value == nil || !result.Result.Value.Equal(decimal.NewFromInt(6)) {
		t.Fatalf("explicit-policy valuation = %#v; want account policy price", result)
	}
}

func TestCompanyFundCurrentValuator_BlankPolicyFallsBackButMalformedMappingFailsClosed(t *testing.T) {
	now := time.Date(2026, time.July, 16, 5, 0, 0, 0, time.UTC)
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	asset := AssetIdentity{Currency: "USDT", ChainCode: "BINANCE_SMART_CHAIN", ProviderAssetKey: "USDT_BEP20"}
	defaultKey, ok := CoinGeckoQuoteCacheKeyForDefault(asset, mappings)
	if !ok {
		t.Fatal("USDT system default should have a cache key")
	}
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	if _, err := cache.Refresh(t.Context(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{defaultKey: newCoinGeckoCacheQuote("1", now)}, nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		name       string
		policy     AccountAssetPolicy
		wantStatus USDValuationStatus
		wantReason USDValuationReason
	}{
		{
			name:       "blank valuation mapping uses default",
			policy:     AccountAssetPolicy{ID: 19, AccountID: 9, Asset: asset, Enabled: true},
			wantStatus: USDValuationStatusProvisional,
		},
		{
			name: "malformed explicit mapping does not use default",
			policy: AccountAssetPolicy{
				ID: 20, AccountID: 9, Asset: asset, CoinGeckoID: "tether", CoinGeckoPlatformID: "ethereum", Enabled: true,
			},
			wantStatus: USDValuationStatusUnpriced,
			wantReason: USDValuationReasonMappingMissing,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := newValuationRuntimeCandidate(19, "USDT", decimal.NewFromInt(1))
			candidate.ToCompanyFundAccountID = int64Pointer(testCase.policy.AccountID)
			candidate.Asset = asset
			store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{candidate.ID: candidate}}
			valuator := newTestCompanyFundCurrentValuatorWithConfig(t, now, store, newCurrentRateRefresherRegistryWithAccount(t, testCase.policy.AccountID, []AccountAssetPolicy{testCase.policy}), cache, CompanyFundCurrentValuatorConfig{
				DefaultMappings: mappings,
			})

			result := valuator.ValueTransaction(t.Context(), candidate.ID)
			if result.Err != nil || result.Result.Status != testCase.wantStatus || result.Result.Reason != testCase.wantReason {
				t.Fatalf("ValueTransaction() = %#v", result)
			}
		})
	}
}

func TestCompanyFundCurrentValuator_UnrecognizedAssetNeverUsesSystemDefault(t *testing.T) {
	now := time.Date(2026, time.July, 16, 5, 0, 0, 0, time.UTC)
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := CoinGeckoQuoteCacheKeyForDefault(AssetIdentity{Currency: "USDT"}, mappings)
	if !ok {
		t.Fatal("USDT system default should have a cache key")
	}
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	if _, err := cache.Refresh(t.Context(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("1", now)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	candidate := newValuationRuntimeCandidate(20, "USDT", decimal.NewFromInt(1))
	candidate.ToCompanyFundAccountID = int64Pointer(9)
	candidate.IsUnrecognizedAsset = true
	store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{candidate.ID: candidate}}
	valuator := newTestCompanyFundCurrentValuatorWithConfig(t, now, store, newCurrentRateRefresherRegistryWithAccount(t, 9, nil), cache, CompanyFundCurrentValuatorConfig{
		DefaultMappings: mappings,
	})

	result := valuator.ValueTransaction(t.Context(), candidate.ID)
	if result.Err != nil || result.Result.Status != USDValuationStatusUnpriced || result.Result.Reason != USDValuationReasonMappingMissing || result.Result.Source != "" {
		t.Fatalf("unrecognized default valuation = %#v", result)
	}
}

func TestCompanyFundCurrentValuator_UsesExplicitFiatMatrixQuoteAsProvisional(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policy := AccountAssetPolicy{
		ID: 8, AccountID: 9,
		Asset:       AssetIdentity{Currency: "JPY"},
		CoinGeckoID: "fiat:JPY", Enabled: true,
	}
	registry := newCurrentRateRefresherRegistryWithAccount(t, 9, []AccountAssetPolicy{policy})
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	client := &fakeCoinGeckoCurrentPriceClient{simple: map[string]CoinGeckoPriceBatch{
		"bitcoin": fakeCoinGeckoPriceBatch(now,
			CoinGeckoPrice{CoinID: "bitcoin", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("60000", now)},
			CoinGeckoPrice{CoinID: "bitcoin", QuoteCurrency: "jpy", Quote: fakeCoinGeckoQuote("6000000", now)},
		),
	}}
	storeSnapshots := &fakeCurrentRateSnapshotStore{nextID: 200}
	refresher, err := NewCoinGeckoCurrentRateRefresher(client, registry, cache, CoinGeckoCurrentRateRefresherConfig{
		Clock: func() time.Time { return now }, SnapshotStore: storeSnapshots, PolicyVersion: "current-usd-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("fiat rate refresh error = %v", err)
	}
	if len(client.simpleRequests) != 1 {
		t.Fatalf("fiat refresh requests = %#v, want one matrix request", client.simpleRequests)
	}

	candidate := newValuationRuntimeCandidate(15, "JPY", decimal.NewFromInt(6000000))
	candidate.ToCompanyFundAccountID = int64Pointer(9)
	candidate.Asset = policy.Asset
	store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{15: candidate}}
	valuator := newTestCompanyFundCurrentValuator(t, now, store, registry, cache)

	result := valuator.ValueTransaction(context.Background(), 15)
	if result.Err != nil || !result.Applied || result.Result.Status != USDValuationStatusProvisional || result.Result.Source != USDValuationSourceCoinGecko || result.Result.Method != USDValuationMethodCoinGeckoBTCCross || result.Result.Basis != USDValuationBasisIngestionTime {
		t.Fatalf("fiat ValueTransaction() = %#v", result)
	}
	if result.Result.Value == nil || !result.Result.Value.Equal(decimal.NewFromInt(60000)) || result.Result.PriceAt == nil || !result.Result.PriceAt.Equal(now.Add(-time.Second)) {
		t.Fatalf("fiat provisional valuation must preserve derived current quote and fetched timestamp: %#v", result.Result)
	}
	if len(store.applies) != 1 || store.applies[0].RateSnapshotID == nil || *store.applies[0].RateSnapshotID <= 0 {
		t.Fatalf("fiat valuation must retain derived snapshot ID: %#v", store.applies)
	}
}

func TestCompanyFundCurrentValuator_LeavesMissingAndStaleQuotesExplicitlyUnpriced(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	policy := AccountAssetPolicy{
		ID: 7, AccountID: 9,
		Asset:       AssetIdentity{Currency: "ETH", ChainCode: "ETHEREUM", ProviderAssetKey: "ETH"},
		CoinGeckoID: "ethereum", Enabled: true,
	}
	registry := newCurrentRateRefresherRegistryWithAccount(t, 9, []AccountAssetPolicy{policy})
	newCandidate := func(id int64) CompanyFundTransactionValuationCandidate {
		candidate := newValuationRuntimeCandidate(id, "ETH", decimal.NewFromInt(1))
		candidate.ToCompanyFundAccountID = int64Pointer(9)
		candidate.Asset = policy.Asset
		return candidate
	}

	t.Run("missing", func(t *testing.T) {
		cache := newTestCurrentRateCache(t, &now, time.Minute)
		store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{12: newCandidate(12)}}
		valuator := newTestCompanyFundCurrentValuator(t, now, store, registry, cache)
		result := valuator.ValueTransaction(context.Background(), 12)
		if result.Err != nil || result.Result.Status != USDValuationStatusUnpriced || result.Result.Reason != USDValuationReasonRateMissing || result.Result.Value != nil {
			t.Fatalf("missing quote result = %#v", result)
		}
	})

	t.Run("stale", func(t *testing.T) {
		cache := newTestCurrentRateCache(t, &now, time.Minute)
		key, _ := CoinGeckoQuoteCacheKeyForPolicy(policy)
		if _, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
			return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("2500", now)}, nil
		}); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
		store := &fakeCompanyFundValuationCandidateStore{candidates: map[int64]CompanyFundTransactionValuationCandidate{13: newCandidate(13)}}
		valuator := newTestCompanyFundCurrentValuator(t, now, store, registry, cache)
		result := valuator.ValueTransaction(context.Background(), 13)
		if result.Err != nil || result.Result.Status != USDValuationStatusStale || result.Result.Reason != USDValuationReasonCacheStale || result.Result.Value != nil {
			t.Fatalf("stale quote result = %#v", result)
		}
	})
}

func TestCompanyFundCurrentValuator_SweepRepairsEligibleCandidatesWithoutFailingLedgerCaller(t *testing.T) {
	now := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	store := &fakeCompanyFundValuationCandidateStore{
		candidates: map[int64]CompanyFundTransactionValuationCandidate{
			21: newValuationRuntimeCandidate(21, "USD", decimal.NewFromInt(1)),
			22: newValuationRuntimeCandidate(22, "USD", decimal.NewFromInt(2)),
		},
		sweep: []CompanyFundTransactionValuationCandidate{
			newValuationRuntimeCandidate(21, "USD", decimal.NewFromInt(1)),
			newValuationRuntimeCandidate(22, "USD", decimal.NewFromInt(2)),
		},
	}
	valuator := newTestCompanyFundCurrentValuator(t, now, store, nil, nil)

	result := valuator.Sweep(context.Background(), 10)
	if result.Err != nil || result.CandidateCount != 2 || result.Attempted != 2 || result.Applied != 2 || len(store.applies) != 2 || store.lastSweepLimit != 10 || store.lastSweepAfter != 0 {
		t.Fatalf("Sweep() = %#v, applies=%#v, limit=%d after=%d", result, store.applies, store.lastSweepLimit, store.lastSweepAfter)
	}

	store.getErr = errors.New("transient valuation read failure")
	bestEffort := valuator.ValueTransaction(context.Background(), 21)
	if bestEffort.Err == nil || bestEffort.Applied {
		t.Fatalf("best-effort ValueTransaction must report, not return, post-ledger failure: %#v", bestEffort)
	}
}

func TestCompanyFundCurrentValuator_SweepRevaluesExistingUnpricedHistoryAfterRateBecomesAvailable(t *testing.T) {
	now := time.Date(2026, time.July, 16, 4, 28, 45, 0, time.UTC)
	policy := AccountAssetPolicy{
		ID: 17, AccountID: 19,
		Asset:       AssetIdentity{Currency: "USDT", ChainCode: "BINANCE_SMART_CHAIN", ProviderAssetKey: "USDT_BEP20"},
		CoinGeckoID: "tether", Enabled: true,
	}
	registry := newCurrentRateRefresherRegistryWithAccount(t, policy.AccountID, []AccountAssetPolicy{policy})
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
	if !ok {
		t.Fatal("policy should have cache key")
	}
	if _, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		quote := newCoinGeckoCacheQuote("0.9991545673733531", now)
		quote.RateSnapshotID = 191
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: quote}, nil
	}); err != nil {
		t.Fatal(err)
	}

	candidate := newValuationRuntimeCandidate(23, "USDT", decimal.RequireFromString("0.01"))
	candidate.ToCompanyFundAccountID = int64Pointer(policy.AccountID)
	candidate.Asset = policy.Asset
	historyID := int64(73)
	candidate.CurrentValuationHistoryID = &historyID
	candidate.CurrentValuationDependencyFingerprint = strings.Repeat("b", 64)
	candidate.CurrentValuationStatus = USDValuationStatusUnpriced
	candidate.CurrentValuationSource = ""
	store := &fakeCompanyFundValuationCandidateStore{sweep: []CompanyFundTransactionValuationCandidate{candidate}}
	valuator := newTestCompanyFundCurrentValuator(t, now, store, registry, cache)

	result := valuator.Sweep(context.Background(), 10)
	if result.Err != nil || result.Failed != 0 || result.Applied != 1 || len(store.applies) != 1 {
		t.Fatalf("Sweep() = %#v, applies=%#v; want repaired valuation", result, store.applies)
	}
	apply := store.applies[0]
	if apply.Result.Status != USDValuationStatusProvisional || apply.Result.Source != USDValuationSourceCoinGecko || apply.Result.Value == nil || !apply.Result.Value.Equal(decimal.RequireFromString("0.009991545673733531")) {
		t.Fatalf("repaired valuation = %#v", apply.Result)
	}
	if apply.ExpectedCurrentHistoryID == nil || *apply.ExpectedCurrentHistoryID != historyID || apply.ExpectedCurrentDependencyFingerprint != candidate.CurrentValuationDependencyFingerprint {
		t.Fatalf("repair lost current-history compare-and-set guard: %#v", apply)
	}
}

func newTestCompanyFundCurrentValuator(
	t *testing.T,
	now time.Time,
	store CompanyFundValuationCandidateStore,
	registry *AccountRegistry,
	cache *CurrentRateCache,
) *CompanyFundCurrentValuator {
	t.Helper()
	return newTestCompanyFundCurrentValuatorWithConfig(t, now, store, registry, cache, CompanyFundCurrentValuatorConfig{})
}

func newTestCompanyFundCurrentValuatorWithConfig(
	t *testing.T,
	now time.Time,
	store CompanyFundValuationCandidateStore,
	registry *AccountRegistry,
	cache *CurrentRateCache,
	config CompanyFundCurrentValuatorConfig,
) *CompanyFundCurrentValuator {
	t.Helper()
	if registry == nil {
		registry = newCurrentRateRefresherRegistryWithAccount(t, 1, nil)
	}
	if cache == nil {
		cache = newTestCurrentRateCache(t, &now, time.Minute)
	}
	if config.PolicyVersion == "" {
		config.PolicyVersion = "current-usd-v1"
	}
	valuator, err := NewCompanyFundCurrentValuator(store, registry, cache, config)
	if err != nil {
		t.Fatalf("NewCompanyFundCurrentValuator() error = %v", err)
	}
	return valuator
}

func newCurrentRateRefresherRegistryWithAccount(t *testing.T, accountID int64, policies []AccountAssetPolicy) *AccountRegistry {
	t.Helper()
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return []CompanyFundAccount{{
			ID: accountID, Channel: AccountChannelSafeheron, NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true,
		}}, policies, nil
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatalf("registry.Refresh() error = %v", err)
	}
	return registry
}

func newValuationRuntimeCandidate(id int64, currency string, amount decimal.Decimal) CompanyFundTransactionValuationCandidate {
	firstSeen := time.Date(2026, time.July, 11, 2, 59, 0, 0, time.UTC)
	return CompanyFundTransactionValuationCandidate{
		ID: id, Channel: ChannelSafeheron, MovementKind: MovementKindPrincipal, Direction: DirectionInflow,
		Currency: currency, Amount: amount, Asset: AssetIdentity{Currency: currency}, FirstSeenAt: firstSeen,
	}
}

type fakeCompanyFundValuationCandidateStore struct {
	candidates     map[int64]CompanyFundTransactionValuationCandidate
	sweep          []CompanyFundTransactionValuationCandidate
	applies        []CompanyFundValuationApplyInput
	getErr         error
	sweepErr       error
	applyErr       error
	lastSweepLimit int
	lastSweepAfter int64
}

func (store *fakeCompanyFundValuationCandidateStore) GetCompanyFundTransactionValuationCandidate(_ context.Context, transactionID int64) (*CompanyFundTransactionValuationCandidate, error) {
	if store.getErr != nil {
		return nil, store.getErr
	}
	candidate, ok := store.candidates[transactionID]
	if !ok {
		return nil, nil
	}
	return &candidate, nil
}

func (store *fakeCompanyFundValuationCandidateStore) ListCompanyFundValuationRepairCandidates(_ context.Context, limit int) ([]CompanyFundTransactionValuationCandidate, error) {
	store.lastSweepLimit = limit
	if store.sweepErr != nil {
		return nil, store.sweepErr
	}
	return append([]CompanyFundTransactionValuationCandidate(nil), store.sweep...), nil
}

func (store *fakeCompanyFundValuationCandidateStore) ListCompanyFundValuationRepairCandidatesAfter(_ context.Context, afterID int64, limit int) ([]CompanyFundTransactionValuationCandidate, error) {
	store.lastSweepAfter = afterID
	return store.ListCompanyFundValuationRepairCandidates(context.Background(), limit)
}

func (store *fakeCompanyFundValuationCandidateStore) ApplyCompanyFundValuation(_ context.Context, input CompanyFundValuationApplyInput) (CompanyFundValuationApplyResult, error) {
	if store.applyErr != nil {
		return CompanyFundValuationApplyResult{}, store.applyErr
	}
	store.applies = append(store.applies, input)
	return CompanyFundValuationApplyResult{Inserted: true}, nil
}
