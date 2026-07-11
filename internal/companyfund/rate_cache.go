package companyfund

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const (
	defaultCurrentRateCacheTTL         = time.Minute
	defaultCurrentRateCacheMaxQuoteAge = 5 * time.Minute
)

// CoinGeckoQuoteCacheKey contains the full provider/asset mapping identity.
// Coin ID alone is not enough for contract assets: platform and contract stay
// in the key so same-symbol tokens cannot share an observed market price.
type CoinGeckoQuoteCacheKey struct {
	Provider         string
	AssetIdentityKey string
	CoinID           string
	PlatformID       string
	ContractAddress  string
	// FiatCode is an explicitly configured provider fiat code (for example
	// JPY). It is mutually exclusive with CoinID and contract mappings and
	// represents a USD-per-fiat quote derived from one /exchange_rates reply.
	FiatCode      string
	QuoteCurrency string
}

func (key CoinGeckoQuoteCacheKey) identity() string {
	return lengthPrefixedRateCacheIdentity(
		key.Provider,
		key.AssetIdentityKey,
		key.CoinID,
		key.PlatformID,
		key.ContractAddress,
		key.FiatCode,
		key.QuoteCurrency,
	)
}

func (key CoinGeckoQuoteCacheKey) validate() error {
	if err := validateRequiredString("rate cache provider", key.Provider, maxRateProviderBytes); err != nil {
		return err
	}
	if err := validateRequiredString("rate cache asset identity key", key.AssetIdentityKey, 512); err != nil {
		return err
	}
	if err := validateRequiredString("rate cache quote currency", key.QuoteCurrency, 64); err != nil {
		return err
	}
	hasCoinID := key.CoinID != ""
	hasPlatform := key.PlatformID != ""
	hasContract := key.ContractAddress != ""
	hasFiatCode := key.FiatCode != ""
	if hasFiatCode {
		if hasCoinID || hasPlatform || hasContract || !strings.EqualFold(key.QuoteCurrency, "USD") || !validCoinGeckoConfiguredFiatCode(key.FiatCode) {
			return fmt.Errorf("rate cache fiat key requires only a valid explicit fiat code")
		}
		return nil
	}
	if hasPlatform != hasContract || (!hasCoinID && !hasPlatform) {
		return fmt.Errorf("rate cache key requires a CoinGecko coin ID and/or a complete platform/contract mapping")
	}
	return nil
}

// CurrentRateCacheConfig controls a process-local cache. TTL controls refresh
// freshness, while MaxQuoteAge independently limits provider data freshness.
// Neither setting starts Redis, database, scheduler, or background HTTP work.
type CurrentRateCacheConfig struct {
	TTL         time.Duration
	MaxQuoteAge time.Duration
	Clock       func() time.Time
}

// CurrentRateCacheLoader lets a caller perform network work before giving the
// complete next snapshot to the cache. The cache itself never makes HTTP calls.
type CurrentRateCacheLoader func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error)

// CurrentRateCache is copy-on-write. Readers hold only a short RLock and never
// observe the map being assembled by a successful refresh.
type CurrentRateCache struct {
	mu          sync.RWMutex
	refreshMu   sync.Mutex
	ttl         time.Duration
	maxQuoteAge time.Duration
	now         func() time.Time
	snapshot    currentRateCacheSnapshot
}

type currentRateCacheSnapshot struct {
	quotes        map[CoinGeckoQuoteCacheKey]CoinGeckoQuote
	refreshedAt   time.Time
	refreshFailed bool
	failureCount  uint64
}

// CurrentRateCacheRead is a copied read result. Stale is explicit on cache
// expiry, provider quote-age expiry, and refresh failure, so callers cannot
// silently use an aged value as fresh valuation input.
type CurrentRateCacheRead struct {
	Quote         CoinGeckoQuote
	Stale         bool
	Age           time.Duration
	QuoteAge      time.Duration
	ProviderStale bool
	RefreshFailed bool
	FailureCount  uint64
}

// CurrentRateCacheRefreshResult describes a refresh outcome without carrying a
// quote. On failure callers must use Get with the complete mapping key, so one
// asset's retained value can never be mistaken for another asset's price.
type CurrentRateCacheRefreshResult struct {
	Refreshed     bool
	Stale         bool
	RefreshFailed bool
	FailureCount  uint64
}

// CurrentRateCacheSnapshot is an independently copied diagnostics view. Its
// Quotes map may be modified by the caller without affecting live cache state.
type CurrentRateCacheSnapshot struct {
	Quotes        map[CoinGeckoQuoteCacheKey]CoinGeckoQuote
	RefreshedAt   time.Time
	RefreshFailed bool
	FailureCount  uint64
}

func NewCurrentRateCache(config CurrentRateCacheConfig) (*CurrentRateCache, error) {
	ttl := config.TTL
	if ttl == 0 {
		ttl = defaultCurrentRateCacheTTL
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("current rate cache TTL must be positive")
	}
	maxQuoteAge := config.MaxQuoteAge
	if maxQuoteAge == 0 {
		maxQuoteAge = defaultCurrentRateCacheMaxQuoteAge
	}
	if maxQuoteAge <= 0 {
		return nil, fmt.Errorf("current rate cache max quote age must be positive")
	}
	now := config.Clock
	if now == nil {
		now = time.Now
	}
	return &CurrentRateCache{
		ttl:         ttl,
		maxQuoteAge: maxQuoteAge,
		now:         now,
		snapshot:    currentRateCacheSnapshot{quotes: make(map[CoinGeckoQuoteCacheKey]CoinGeckoQuote)},
	}, nil
}

// Refresh invokes loader without holding the cache lock. A valid complete map
// is copied then swapped atomically; a failure retains the previous snapshot
// and marks it stale without storing an arbitrary error string.
func (c *CurrentRateCache) Refresh(ctx context.Context, loader CurrentRateCacheLoader) (CurrentRateCacheRefreshResult, error) {
	if c == nil || c.now == nil {
		return CurrentRateCacheRefreshResult{}, fmt.Errorf("current rate cache is not configured")
	}
	if loader == nil {
		return CurrentRateCacheRefreshResult{}, fmt.Errorf("current rate cache loader is required")
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	quotes, err := loader(ctx)
	if err != nil {
		return c.recordRefreshFailure(), err
	}
	copy, err := copyCurrentRateQuotes(quotes)
	if err != nil {
		return c.recordRefreshFailure(), err
	}

	c.mu.Lock()
	c.snapshot = currentRateCacheSnapshot{
		quotes:       copy,
		refreshedAt:  c.now().UTC(),
		failureCount: c.snapshot.failureCount,
	}
	result := CurrentRateCacheRefreshResult{Refreshed: true, FailureCount: c.snapshot.failureCount}
	c.mu.Unlock()
	return result, nil
}

func (c *CurrentRateCache) recordRefreshFailure() CurrentRateCacheRefreshResult {
	c.mu.Lock()
	c.snapshot.refreshFailed = true
	c.snapshot.failureCount++
	result := CurrentRateCacheRefreshResult{Stale: true, RefreshFailed: true, FailureCount: c.snapshot.failureCount}
	c.mu.Unlock()
	return result
}

func (c *CurrentRateCache) Get(key CoinGeckoQuoteCacheKey) (CurrentRateCacheRead, bool) {
	if c == nil || c.now == nil {
		return CurrentRateCacheRead{}, false
	}
	c.mu.RLock()
	quote, found := c.snapshot.quotes[key]
	refreshedAt := c.snapshot.refreshedAt
	refreshFailed := c.snapshot.refreshFailed
	failureCount := c.snapshot.failureCount
	ttl := c.ttl
	maxQuoteAge := c.maxQuoteAge
	now := c.now().UTC()
	c.mu.RUnlock()
	if !found {
		return CurrentRateCacheRead{}, false
	}
	age := now.Sub(refreshedAt)
	if age < 0 {
		age = 0
	}
	quoteAge := now.Sub(quote.ProviderUpdatedAt)
	providerStale := quoteAge < 0 || quoteAge >= maxQuoteAge
	if quoteAge < 0 {
		quoteAge = 0
	}
	return CurrentRateCacheRead{
		Quote:         quote,
		Stale:         refreshFailed || age >= ttl || providerStale,
		Age:           age,
		QuoteAge:      quoteAge,
		ProviderStale: providerStale,
		RefreshFailed: refreshFailed,
		FailureCount:  failureCount,
	}, true
}

func (c *CurrentRateCache) Snapshot() CurrentRateCacheSnapshot {
	if c == nil {
		return CurrentRateCacheSnapshot{Quotes: make(map[CoinGeckoQuoteCacheKey]CoinGeckoQuote)}
	}
	c.mu.RLock()
	result := CurrentRateCacheSnapshot{
		Quotes:        copyCurrentRateQuoteMap(c.snapshot.quotes),
		RefreshedAt:   c.snapshot.refreshedAt,
		RefreshFailed: c.snapshot.refreshFailed,
		FailureCount:  c.snapshot.failureCount,
	}
	c.mu.RUnlock()
	return result
}

func copyCurrentRateQuotes(quotes map[CoinGeckoQuoteCacheKey]CoinGeckoQuote) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
	if len(quotes) == 0 {
		return nil, fmt.Errorf("current rate cache refresh cannot replace a snapshot with no quotes")
	}
	copy := make(map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, len(quotes))
	for key, quote := range quotes {
		if err := key.validate(); err != nil {
			return nil, err
		}
		if !quote.Price.GreaterThan(decimal.Zero) || quote.ProviderUpdatedAt.IsZero() || quote.FetchedAt.IsZero() || quote.ProviderUpdatedAt.After(quote.FetchedAt) || !isLowerSHA256Hex(quote.ResponseDigest) {
			return nil, fmt.Errorf("current rate cache quote for %q must have positive price and complete audit metadata", key.AssetIdentityKey)
		}
		copy[key] = quote
	}
	return copy, nil
}

func copyCurrentRateQuoteMap(source map[CoinGeckoQuoteCacheKey]CoinGeckoQuote) map[CoinGeckoQuoteCacheKey]CoinGeckoQuote {
	copy := make(map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, len(source))
	for key, quote := range source {
		copy[key] = quote
	}
	return copy
}

func lengthPrefixedRateCacheIdentity(values ...string) string {
	var builder strings.Builder
	for _, value := range values {
		builder.WriteString(fmt.Sprintf("%d:", len(value)))
		builder.WriteString(value)
	}
	return builder.String()
}
