package companyfund

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCurrentRateCache_RefreshAtomicallySwapsCopiedSnapshotAndHonorsTTL(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	key := newCoinGeckoQuoteCacheKey("ethereum", "", "")
	quotes := map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("2500.123456789012345678", now)}

	result, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return quotes, nil
	})
	if err != nil || !result.Refreshed || result.Stale {
		t.Fatalf("Refresh() = %#v, %v; want successful fresh snapshot", result, err)
	}
	quotes[key] = newCoinGeckoCacheQuote("1", now)
	read, found := cache.Get(key)
	if !found || read.Stale || !read.Quote.Price.Equal(decimal.RequireFromString("2500.123456789012345678")) {
		t.Fatalf("Get() after caller mutation = %#v, found=%v; want immutable stored quote", read, found)
	}

	snapshot := cache.Snapshot()
	snapshot.Quotes[key] = newCoinGeckoCacheQuote("2", now)
	read, _ = cache.Get(key)
	if !read.Quote.Price.Equal(decimal.RequireFromString("2500.123456789012345678")) {
		t.Fatalf("mutating returned snapshot changed cache: %#v", read)
	}

	now = now.Add(time.Minute)
	read, found = cache.Get(key)
	if !found || !read.Stale || read.Age != time.Minute {
		t.Fatalf("Get() at TTL = %#v, found=%v; want explicit stale quote", read, found)
	}
}

func TestCurrentRateCache_FailedRefreshRetainsLastGoodQuoteAndMarksStale(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	key := newCoinGeckoQuoteCacheKey("usd-coin", "", "")
	if _, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("0.999999999999999999", now)}, nil
	}); err != nil {
		t.Fatalf("initial Refresh() error = %v", err)
	}

	providerFailure := errors.New("provider unavailable")
	result, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return nil, providerFailure
	})
	if !errors.Is(err, providerFailure) || !result.Stale || !result.RefreshFailed || result.FailureCount != 1 {
		t.Fatalf("failed Refresh() = %#v, %v; want stale retained result", result, err)
	}
	read, found := cache.Get(key)
	if !found || !read.Stale || !read.RefreshFailed || read.FailureCount != 1 || !read.Quote.Price.Equal(decimal.RequireFromString("0.999999999999999999")) {
		t.Fatalf("Get() after failed refresh = %#v, found=%v; want explicitly stale retained quote", read, found)
	}
}

func TestCurrentRateCache_ProviderQuoteAgeExpiresFreshSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	cache := newTestCurrentRateCacheWithMaxQuoteAge(t, &now, time.Minute, time.Hour)
	key := newCoinGeckoQuoteCacheKey("bitcoin", "", "")
	oldProviderQuote := newCoinGeckoCacheQuote("65000", now)
	oldProviderQuote.ProviderUpdatedAt = now.Add(-2 * time.Hour)

	if _, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: oldProviderQuote}, nil
	}); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	read, found := cache.Get(key)
	if !found || !read.Stale || !read.ProviderStale || read.Age != 0 || read.QuoteAge != 2*time.Hour {
		t.Fatalf("fresh snapshot with old provider timestamp = %#v, found=%v; want unusable provider-stale quote", read, found)
	}
}

func TestCurrentRateCache_DefaultsProviderQuoteAgeLimit(t *testing.T) {
	cache, err := NewCurrentRateCache(CurrentRateCacheConfig{})
	if err != nil {
		t.Fatalf("NewCurrentRateCache() error = %v", err)
	}
	if cache.maxQuoteAge != defaultCurrentRateCacheMaxQuoteAge {
		t.Fatalf("default max quote age = %s, want %s", cache.maxQuoteAge, defaultCurrentRateCacheMaxQuoteAge)
	}
}

func TestCurrentRateCache_UsesCompleteProviderAssetMappingKey(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	first := newCoinGeckoQuoteCacheKey("", "ethereum", "0xaaa")
	second := newCoinGeckoQuoteCacheKey("", "ethereum", "0xbbb")
	if first == second || first.identity() == second.identity() {
		t.Fatal("different contract mappings must never share a cache key")
	}
	_, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{
			first:  newCoinGeckoCacheQuote("1.01", now),
			second: newCoinGeckoCacheQuote("0.99", now),
		}, nil
	})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	firstRead, _ := cache.Get(first)
	secondRead, _ := cache.Get(second)
	if !firstRead.Quote.Price.Equal(decimal.RequireFromString("1.01")) || !secondRead.Quote.Price.Equal(decimal.RequireFromString("0.99")) {
		t.Fatalf("mapping-isolated reads = %#v %#v", firstRead, secondRead)
	}
}

func TestCurrentRateCache_SerializesRefreshesWithoutBlockingReaders(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	cache := newTestCurrentRateCache(t, &now, time.Minute)
	key := newCoinGeckoQuoteCacheKey("bitcoin", "", "")
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
			close(started)
			<-release
			return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("10", now)}, nil
		})
		firstDone <- err
	}()
	<-started
	go func() {
		_, err := cache.Refresh(context.Background(), func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
			return map[CoinGeckoQuoteCacheKey]CoinGeckoQuote{key: newCoinGeckoCacheQuote("20", now)}, nil
		})
		secondDone <- err
	}()
	if _, found := cache.Get(key); found {
		t.Fatal("reader must not observe a partially assembled slow refresh")
	}
	select {
	case err := <-secondDone:
		t.Fatalf("second refresh completed before the first released: %v", err)
	default:
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first refresh error = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second refresh error = %v", err)
	}
	read, found := cache.Get(key)
	if !found || !read.Quote.Price.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("serialized refresh result = %#v, found=%v; want later full snapshot", read, found)
	}
}

func newTestCurrentRateCache(t *testing.T, now *time.Time, ttl time.Duration) *CurrentRateCache {
	return newTestCurrentRateCacheWithMaxQuoteAge(t, now, ttl, 0)
}

func newTestCurrentRateCacheWithMaxQuoteAge(t *testing.T, now *time.Time, ttl, maxQuoteAge time.Duration) *CurrentRateCache {
	t.Helper()
	cache, err := NewCurrentRateCache(CurrentRateCacheConfig{TTL: ttl, MaxQuoteAge: maxQuoteAge, Clock: func() time.Time { return *now }})
	if err != nil {
		t.Fatalf("NewCurrentRateCache() error = %v", err)
	}
	return cache
}

func newCoinGeckoQuoteCacheKey(coinID, platformID, contractAddress string) CoinGeckoQuoteCacheKey {
	return CoinGeckoQuoteCacheKey{
		Provider: "COINGECKO", AssetIdentityKey: "asset:" + coinID + ":" + contractAddress,
		CoinID: coinID, PlatformID: platformID, ContractAddress: contractAddress, QuoteCurrency: "USD",
	}
}

func newCoinGeckoCacheQuote(price string, now time.Time) CoinGeckoQuote {
	return CoinGeckoQuote{Price: decimal.RequireFromString(price), ProviderUpdatedAt: now.Add(-time.Second), FetchedAt: now, ResponseDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}
