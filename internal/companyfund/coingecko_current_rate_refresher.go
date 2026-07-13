package companyfund

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const defaultCoinGeckoCurrentRateRefreshInterval = 5 * time.Minute

// CoinGeckoCurrentPriceClient is the narrow current-price surface used by the
// process-local refresher. It deliberately excludes history, quotas, and
// persistence: those have independent durable workflows.
type CoinGeckoCurrentPriceClient interface {
	FetchSimplePrices(context.Context, CoinGeckoSimplePriceRequest) (CoinGeckoPriceBatch, error)
	FetchTokenPrices(context.Context, CoinGeckoTokenPriceRequest) (CoinGeckoPriceBatch, error)
	FetchExchangeRates(context.Context) (CoinGeckoExchangeRatesBatch, error)
}

// CoinGeckoCurrentRateRefresherConfig controls only local refresh cadence.
// The cache owns freshness evaluation; the registry owns the account/policy
// snapshot. A zero interval uses the configured five-minute default.
type CoinGeckoCurrentRateRefresherConfig struct {
	RefreshInterval time.Duration
	Clock           func() time.Time
	SnapshotStore   CurrentRateSnapshotStore
	PolicyVersion   string
}

// CoinGeckoCurrentRateRefreshResult is intentionally metadata-only. Quote
// values remain in CurrentRateCache so a caller cannot accidentally reuse a
// partially assembled provider response.
type CoinGeckoCurrentRateRefreshResult struct {
	Refreshed     bool
	Skipped       bool
	Stale         bool
	RefreshFailed bool
	RestoreFailed bool
	FailureCount  uint64
	MappingCount  int
	QuoteCount    int
}

// CoinGeckoCurrentRateRefresherStatus exposes operational freshness without
// retaining provider response bytes or request URLs in memory.
type CoinGeckoCurrentRateRefresherStatus struct {
	LastSuccessfulRefreshAt time.Time
	LastRefreshError        error
	LastRefreshErrorAt      time.Time
	LastRestoreError        error
	LastRestoreErrorAt      time.Time
	Age                     time.Duration
}

// CoinGeckoCurrentRateRefresher obtains current USD quotes only for explicitly
// configured CoinGecko mappings, then atomically swaps the complete local
// cache through CurrentRateCache. It never infers an ID from a display ticker.
type CoinGeckoCurrentRateRefresher struct {
	client   CoinGeckoCurrentPriceClient
	registry *AccountRegistry
	cache    *CurrentRateCache
	store    CurrentRateSnapshotStore
	policy   string
	interval time.Duration
	now      func() time.Time

	mu                 sync.RWMutex
	lastSuccessAt      time.Time
	lastError          error
	lastErrorAt        time.Time
	lastRestoreError   error
	lastRestoreErrorAt time.Time
	running            bool
	runCancel          context.CancelFunc
	runDone            chan struct{}
}

func NewCoinGeckoCurrentRateRefresher(
	client CoinGeckoCurrentPriceClient,
	registry *AccountRegistry,
	cache *CurrentRateCache,
	config CoinGeckoCurrentRateRefresherConfig,
) (*CoinGeckoCurrentRateRefresher, error) {
	if client == nil {
		return nil, fmt.Errorf("CoinGecko current-rate client is required")
	}
	if registry == nil {
		return nil, fmt.Errorf("company-fund account registry is required for CoinGecko current-rate refresh")
	}
	if cache == nil {
		return nil, fmt.Errorf("current rate cache is required")
	}
	interval := config.RefreshInterval
	if interval == 0 {
		interval = defaultCoinGeckoCurrentRateRefreshInterval
	}
	if interval <= 0 {
		return nil, fmt.Errorf("CoinGecko current-rate refresh interval must be positive")
	}
	now := config.Clock
	if now == nil {
		now = time.Now
	}
	policyVersion := strings.TrimSpace(config.PolicyVersion)
	if policyVersion == "" {
		policyVersion = defaultCompanyFundCurrentValuationPolicyVersion
	}
	if err := validateRequiredString("CoinGecko current-rate policy version", policyVersion, maxRateSnapshotPolicyVersionBytes); err != nil {
		return nil, err
	}
	return &CoinGeckoCurrentRateRefresher{
		client: client, registry: registry, cache: cache, store: config.SnapshotStore,
		policy: policyVersion, interval: interval, now: now,
	}, nil
}

func (r *CoinGeckoCurrentRateRefresher) RefreshInterval() time.Duration {
	if r == nil || r.interval <= 0 {
		return defaultCoinGeckoCurrentRateRefreshInterval
	}
	return r.interval
}

func (r *CoinGeckoCurrentRateRefresher) Status() CoinGeckoCurrentRateRefresherStatus {
	if r == nil {
		return CoinGeckoCurrentRateRefresherStatus{}
	}
	r.mu.RLock()
	status := CoinGeckoCurrentRateRefresherStatus{
		LastSuccessfulRefreshAt: r.lastSuccessAt,
		LastRefreshError:        r.lastError,
		LastRefreshErrorAt:      r.lastErrorAt,
		LastRestoreError:        r.lastRestoreError,
		LastRestoreErrorAt:      r.lastRestoreErrorAt,
	}
	r.mu.RUnlock()
	if !status.LastSuccessfulRefreshAt.IsZero() {
		status.Age = r.now().UTC().Sub(status.LastSuccessfulRefreshAt)
		if status.Age < 0 {
			status.Age = 0
		}
	}
	return status
}

// Refresh builds its mapping plan from a single immutable registry snapshot.
// It does not touch the provider when no enabled asset policy has an explicit
// valid CoinGecko ID or full platform/contract mapping.
func (r *CoinGeckoCurrentRateRefresher) Refresh(ctx context.Context) (CoinGeckoCurrentRateRefreshResult, error) {
	if r == nil || r.client == nil || r.registry == nil || r.cache == nil || r.now == nil {
		return CoinGeckoCurrentRateRefreshResult{}, fmt.Errorf("CoinGecko current-rate refresher is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	plan := buildCoinGeckoCurrentRatePlan(r.registry.Snapshot().AssetPolicies())
	result := CoinGeckoCurrentRateRefreshResult{MappingCount: plan.mappingCount}
	if plan.mappingCount == 0 {
		result.Skipped = true
		return result, nil
	}
	// Recovery is best-effort: a provider refresh may repair a missing or stale
	// durable cache, while a later persistence failure still prevents an
	// untracked quote from being published to readers.
	if err := r.restoreCurrentRateQuotes(ctx, plan); err != nil {
		result.RestoreFailed = true
		r.recordRestoreFailure(err)
	} else {
		r.recordRestoreSuccess()
	}

	cacheResult, err := r.cache.Refresh(ctx, func(refreshContext context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		quotes, fetchErr := r.fetchCurrentUSDQuotes(refreshContext, plan)
		if fetchErr != nil {
			return nil, fetchErr
		}
		return r.persistCurrentRateQuotes(refreshContext, plan, quotes)
	})
	result.Refreshed = cacheResult.Refreshed
	result.Stale = cacheResult.Stale
	result.RefreshFailed = cacheResult.RefreshFailed
	result.FailureCount = cacheResult.FailureCount
	if err != nil {
		r.recordRefreshFailure(err)
		return result, err
	}
	result.QuoteCount = len(r.cache.Snapshot().Quotes)
	r.recordRefreshSuccess()
	return result, nil
}

func (r *CoinGeckoCurrentRateRefresher) fetchCurrentUSDQuotes(ctx context.Context, plan coinGeckoCurrentRatePlan) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
	quotes := make(map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, plan.mappingCount)
	if len(plan.fiatKeys) > 0 {
		if err := r.fetchCurrentFiatUSDQuotes(ctx, plan.fiatKeys, quotes); err != nil {
			return nil, err
		}
	}

	simpleIDs := sortedCoinGeckoPlanKeys(plan.simpleKeys)
	for _, batchIDs := range chunkCoinGeckoValues(simpleIDs, maxCoinGeckoBatchAssets) {
		batch, err := r.client.FetchSimplePrices(ctx, CoinGeckoSimplePriceRequest{
			CoinIDs:         batchIDs,
			QuoteCurrencies: []string{"usd"},
		})
		if err != nil {
			return nil, fmt.Errorf("fetch CoinGecko current simple USD prices: %w", err)
		}
		for _, price := range batch.Prices {
			if price.Quote == nil || !strings.EqualFold(price.QuoteCurrency, "USD") {
				continue
			}
			for _, key := range plan.simpleKeys[strings.ToLower(strings.TrimSpace(price.CoinID))] {
				quotes[key] = *price.Quote
			}
		}
	}

	platforms := make([]string, 0, len(plan.tokenKeys))
	for platform := range plan.tokenKeys {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		contracts := sortedCoinGeckoPlanKeys(plan.tokenKeys[platform])
		for _, batchContracts := range chunkCoinGeckoValues(contracts, maxCoinGeckoBatchAssets) {
			batch, err := r.client.FetchTokenPrices(ctx, CoinGeckoTokenPriceRequest{
				PlatformID:        platform,
				ContractAddresses: batchContracts,
				QuoteCurrencies:   []string{"usd"},
			})
			if err != nil {
				return nil, fmt.Errorf("fetch CoinGecko current token USD prices: %w", err)
			}
			for _, price := range batch.Prices {
				if price.Quote == nil || !strings.EqualFold(price.QuoteCurrency, "USD") || !strings.EqualFold(price.PlatformID, platform) {
					continue
				}
				for _, key := range plan.tokenKeys[platform][strings.ToLower(strings.TrimSpace(price.ContractAddress))] {
					quotes[key] = *price.Quote
				}
			}
		}
	}
	if len(quotes) != plan.mappingCount {
		return nil, fmt.Errorf("CoinGecko current USD quote response is incomplete for configured mappings: got %d of %d", len(quotes), plan.mappingCount)
	}
	return quotes, nil
}

// fetchCurrentFiatUSDQuotes derives USD-per-fiat from one exchange-rate
// response. CoinGecko expresses both rates as units per BTC, so USD/BTC divided
// by fiat/BTC yields USD per fiat unit. The endpoint has no usable provider
// timestamp; FetchedAt is therefore retained as the observation time and the
// downstream valuation remains CURRENT/PROVISIONAL, never transaction-final.
func (r *CoinGeckoCurrentRateRefresher) fetchCurrentFiatUSDQuotes(
	ctx context.Context,
	fiatKeys map[string][]CoinGeckoQuoteCacheKey,
	quotes map[CoinGeckoQuoteCacheKey]CoinGeckoQuote,
) error {
	batch, err := r.client.FetchExchangeRates(ctx)
	if err != nil {
		return fmt.Errorf("fetch CoinGecko current exchange rates: %w", err)
	}
	if batch.FetchedAt.IsZero() || !isLowerSHA256Hex(batch.ResponseDigest) {
		return fmt.Errorf("CoinGecko current exchange rates response has incomplete audit metadata")
	}
	usdRate, found := batch.Rates["USD"]
	if !found || !strings.EqualFold(usdRate.Type, "fiat") || !usdRate.Value.GreaterThan(decimal.Zero) {
		return fmt.Errorf("CoinGecko current exchange rates response is missing a positive fiat USD rate")
	}
	for fiatCode, keys := range fiatKeys {
		fiatRate, found := batch.Rates[fiatCode]
		if !found || !strings.EqualFold(fiatRate.Type, "fiat") || !fiatRate.Value.GreaterThan(decimal.Zero) {
			// An unknown/non-fiat provider code is a missing explicit quote, never
			// a reason to infer an alternate ticker or hard-code a peg.
			continue
		}
		usdPerFiat, err := decimalDivideBank(usdRate.Value, fiatRate.Value)
		if err != nil {
			return fmt.Errorf("derive USD per %s from CoinGecko exchange rates: %w", fiatCode, err)
		}
		if !usdPerFiat.GreaterThan(decimal.Zero) {
			return fmt.Errorf("derive USD per %s from CoinGecko exchange rates: non-positive result", fiatCode)
		}
		quote := CoinGeckoQuote{
			Price:               usdPerFiat,
			ProviderUpdatedAt:   batch.FetchedAt.UTC(),
			FetchedAt:           batch.FetchedAt.UTC(),
			ResponseDigest:      batch.ResponseDigest,
			Method:              USDValuationMethodCoinGeckoBTCCross,
			BTCCrossNumerator:   usdRate.Value,
			BTCCrossDenominator: fiatRate.Value,
		}
		for _, key := range keys {
			quotes[key] = quote
		}
	}
	return nil
}

func (r *CoinGeckoCurrentRateRefresher) recordRefreshSuccess() {
	r.mu.Lock()
	r.lastSuccessAt = r.now().UTC()
	r.lastError = nil
	r.lastErrorAt = time.Time{}
	r.mu.Unlock()
}

func (r *CoinGeckoCurrentRateRefresher) recordRefreshFailure(err error) {
	r.mu.Lock()
	r.lastError = err
	r.lastErrorAt = r.now().UTC()
	r.mu.Unlock()
}

func (r *CoinGeckoCurrentRateRefresher) recordRestoreSuccess() {
	r.mu.Lock()
	r.lastRestoreError = nil
	r.lastRestoreErrorAt = time.Time{}
	r.mu.Unlock()
}

func (r *CoinGeckoCurrentRateRefresher) recordRestoreFailure(err error) {
	r.mu.Lock()
	r.lastRestoreError = err
	r.lastRestoreErrorAt = r.now().UTC()
	r.mu.Unlock()
}

// Start owns at most one background loop. It makes one immediate warm-up
// attempt, then refreshes at the configured interval. Refresh errors are kept
// in Status and leave the previous cache snapshot available but stale.
func (r *CoinGeckoCurrentRateRefresher) Start(parent context.Context) {
	if r == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	r.running = true
	r.runCancel = cancel
	r.runDone = done
	interval := r.interval
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			if r.runDone == done {
				r.running = false
				r.runCancel = nil
				r.runDone = nil
			}
			r.mu.Unlock()
			close(done)
		}()
		_, _ = r.Refresh(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = r.Refresh(ctx)
			}
		}
	}()
}

func (r *CoinGeckoCurrentRateRefresher) Stop() {
	if r == nil {
		return
	}
	r.mu.RLock()
	cancel := r.runCancel
	done := r.runDone
	r.mu.RUnlock()
	if cancel == nil || done == nil {
		return
	}
	cancel()
	<-done
}

type coinGeckoCurrentRatePlan struct {
	simpleKeys   map[string][]CoinGeckoQuoteCacheKey
	tokenKeys    map[string]map[string][]CoinGeckoQuoteCacheKey
	fiatKeys     map[string][]CoinGeckoQuoteCacheKey
	mappings     map[CoinGeckoQuoteCacheKey]currentRateSnapshotMapping
	mappingCount int
}

func buildCoinGeckoCurrentRatePlan(policies []AccountAssetPolicy) coinGeckoCurrentRatePlan {
	plan := coinGeckoCurrentRatePlan{
		simpleKeys: make(map[string][]CoinGeckoQuoteCacheKey),
		tokenKeys:  make(map[string]map[string][]CoinGeckoQuoteCacheKey),
		fiatKeys:   make(map[string][]CoinGeckoQuoteCacheKey),
		mappings:   make(map[CoinGeckoQuoteCacheKey]currentRateSnapshotMapping),
	}
	seen := make(map[CoinGeckoQuoteCacheKey]struct{})
	for _, policy := range policies {
		key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
		if !ok {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		plan.mappingCount++
		plan.mappings[key] = currentRateSnapshotMapping{BaseCurrency: strings.ToUpper(strings.TrimSpace(policy.Asset.Currency))}
		if key.FiatCode != "" {
			plan.fiatKeys[key.FiatCode] = append(plan.fiatKeys[key.FiatCode], key)
			continue
		}
		if key.CoinID != "" {
			plan.simpleKeys[key.CoinID] = append(plan.simpleKeys[key.CoinID], key)
			continue
		}
		if plan.tokenKeys[key.PlatformID] == nil {
			plan.tokenKeys[key.PlatformID] = make(map[string][]CoinGeckoQuoteCacheKey)
		}
		plan.tokenKeys[key.PlatformID][key.ContractAddress] = append(plan.tokenKeys[key.PlatformID][key.ContractAddress], key)
	}
	return plan
}

const coinGeckoFiatMappingPrefix = "fiat:"

// CoinGeckoQuoteCacheKeyForPolicy returns an exact, configured USD cache key.
// `valuation_provider_asset_id` (represented by CoinGeckoID here) reserves
// the explicit `fiat:<CODE>` form for CoinGecko /exchange_rates mappings, for
// example `fiat:JPY`. The code must exactly equal the policy asset currency;
// no display-symbol or ordinary coin-ID fallback is attempted for this prefix.
// A policy with both a coin ID and contract mapping is intentionally skipped:
// choosing one silently would make configuration drift change asset pricing.
func CoinGeckoQuoteCacheKeyForPolicy(policy AccountAssetPolicy) (CoinGeckoQuoteCacheKey, bool) {
	asset := normalizeAssetIdentity(policy.Asset)
	if asset.empty() {
		return CoinGeckoQuoteCacheKey{}, false
	}
	rawProviderAssetID := strings.TrimSpace(policy.CoinGeckoID)
	if strings.HasPrefix(strings.ToLower(rawProviderAssetID), coinGeckoFiatMappingPrefix) {
		fiatCode, ok := CoinGeckoFiatCodeForPolicy(policy)
		if !ok {
			return CoinGeckoQuoteCacheKey{}, false
		}
		key := CoinGeckoQuoteCacheKey{
			Provider:         rateSnapshotCoinGeckoProvider,
			AssetIdentityKey: asset.canonicalKey(),
			FiatCode:         fiatCode,
			QuoteCurrency:    "USD",
		}
		return key, key.validate() == nil
	}
	coinID := strings.ToLower(rawProviderAssetID)
	platformID := strings.ToLower(strings.TrimSpace(policy.CoinGeckoPlatformID))
	contractAddress := strings.ToLower(strings.TrimSpace(policy.CoinGeckoContractAddress))
	if coinID != "" && (platformID != "" || contractAddress != "") {
		return CoinGeckoQuoteCacheKey{}, false
	}
	if coinID != "" {
		if _, err := normalizeCoinGeckoValue("coin ID", coinID, false); err != nil {
			return CoinGeckoQuoteCacheKey{}, false
		}
		key := CoinGeckoQuoteCacheKey{
			Provider:         rateSnapshotCoinGeckoProvider,
			AssetIdentityKey: asset.canonicalKey(),
			CoinID:           coinID,
			QuoteCurrency:    "USD",
		}
		return key, key.validate() == nil
	}
	if platformID == "" || contractAddress == "" {
		return CoinGeckoQuoteCacheKey{}, false
	}
	if _, err := normalizeCoinGeckoValue("platform ID", platformID, true); err != nil {
		return CoinGeckoQuoteCacheKey{}, false
	}
	if _, err := normalizeCoinGeckoValue("contract address", contractAddress, false); err != nil {
		return CoinGeckoQuoteCacheKey{}, false
	}
	key := CoinGeckoQuoteCacheKey{
		Provider:         rateSnapshotCoinGeckoProvider,
		AssetIdentityKey: asset.canonicalKey(),
		PlatformID:       platformID,
		ContractAddress:  contractAddress,
		QuoteCurrency:    "USD",
	}
	return key, key.validate() == nil
}

// CoinGeckoFiatCodeForPolicy returns a configured provider fiat code only
// when it is deliberately marked with `fiat:` and matches the asset currency.
// It accepts arbitrary alphabetic provider codes because CoinGecko may add
// legitimate fiat currencies; fetchCurrentFiatUSDQuotes subsequently requires
// the returned rate's provider type to be exactly `fiat` before pricing it.
func CoinGeckoFiatCodeForPolicy(policy AccountAssetPolicy) (string, bool) {
	asset := normalizeAssetIdentity(policy.Asset)
	rawProviderAssetID := strings.TrimSpace(policy.CoinGeckoID)
	if !strings.HasPrefix(strings.ToLower(rawProviderAssetID), coinGeckoFiatMappingPrefix) {
		return "", false
	}
	fiatCode := strings.ToUpper(strings.TrimSpace(rawProviderAssetID[len(coinGeckoFiatMappingPrefix):]))
	if asset.Currency == "USD" || !validCoinGeckoConfiguredFiatCode(fiatCode) || asset.Currency != fiatCode {
		return "", false
	}
	if strings.TrimSpace(policy.CoinGeckoPlatformID) != "" || strings.TrimSpace(policy.CoinGeckoContractAddress) != "" {
		return "", false
	}
	return fiatCode, true
}

func validCoinGeckoConfiguredFiatCode(value string) bool {
	if len(value) < 2 || len(value) > 16 || value != strings.ToUpper(value) {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func sortedCoinGeckoPlanKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func chunkCoinGeckoValues(values []string, size int) [][]string {
	if len(values) == 0 || size <= 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, values[start:end])
	}
	return chunks
}
