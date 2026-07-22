package companyfund

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"monera-digital/internal/adaptiveschedule"
)

const defaultCoinGeckoCurrentRateRefreshInterval = 5 * time.Minute

// CoinGeckoCurrentPriceClient is the narrow current-price surface used by the
// process-local refresher. It deliberately excludes history, quotas, and
// persistence: those have independent durable workflows.
type CoinGeckoCurrentPriceClient interface {
	FetchSimplePrices(context.Context, CoinGeckoSimplePriceRequest) (CoinGeckoPriceBatch, error)
}

// CoinGeckoCurrentRateRefresherConfig controls only local refresh cadence.
// The cache owns freshness evaluation; the registry owns the account/policy
// snapshot. A zero interval uses the configured five-minute default.
type CoinGeckoCurrentRateRefresherConfig struct {
	RefreshInterval time.Duration
	Clock           func() time.Time
	SnapshotStore   CurrentRateSnapshotStore
	PolicyVersion   string
	DefaultMappings CoinGeckoDefaultRateMappings
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

// CoinGeckoCurrentRateRefresher obtains current USD quotes only for explicit
// system-default and account-policy CoinGecko mappings, then atomically swaps
// the complete local cache. It never infers an ID from a display ticker.
type CoinGeckoCurrentRateRefresher struct {
	client   CoinGeckoCurrentPriceClient
	registry *AccountRegistry
	cache    *CurrentRateCache
	store    CurrentRateSnapshotStore
	policy   string
	interval time.Duration
	now      func() time.Time
	defaults CoinGeckoDefaultRateMappings

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
	defaultMappings, err := normalizeCoinGeckoDefaultRateMappings(config.DefaultMappings)
	if err != nil {
		return nil, err
	}
	return &CoinGeckoCurrentRateRefresher{
		client: client, registry: registry, cache: cache, store: config.SnapshotStore,
		policy: policyVersion, interval: interval, now: now, defaults: defaultMappings,
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

// Refresh unions system defaults with one immutable registry snapshot. It does
// not touch the provider when neither source contains a valid CoinGecko ID.
func (r *CoinGeckoCurrentRateRefresher) Refresh(ctx context.Context) (CoinGeckoCurrentRateRefreshResult, error) {
	if r == nil || r.client == nil || r.registry == nil || r.cache == nil || r.now == nil {
		return CoinGeckoCurrentRateRefreshResult{}, fmt.Errorf("CoinGecko current-rate refresher is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	plan := buildCoinGeckoCurrentRatePlan(r.registry.Snapshot().AssetPolicies(), r.defaults)
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
	simpleIDs := sortedCoinGeckoPlanKeys(plan.simpleKeys)
	if len(plan.fiatKeys) > 0 && !containsCoinGeckoValue(simpleIDs, rateSnapshotBTCProviderAssetID) {
		simpleIDs = append(simpleIDs, rateSnapshotBTCProviderAssetID)
		sort.Strings(simpleIDs)
	}
	if len(simpleIDs) == 0 {
		return nil, fmt.Errorf("CoinGecko current rate plan has no explicit coin IDs")
	}
	if len(simpleIDs) > maxCoinGeckoBatchAssets {
		return nil, fmt.Errorf("CoinGecko current rate plan exceeds one simple-price request: got %d IDs, max %d", len(simpleIDs), maxCoinGeckoBatchAssets)
	}
	quoteCurrencies := []string{"usd"}
	for fiatCode := range plan.fiatKeys {
		quoteCurrencies = append(quoteCurrencies, strings.ToLower(fiatCode))
	}
	sort.Strings(quoteCurrencies)
	batch, err := r.client.FetchSimplePrices(ctx, CoinGeckoSimplePriceRequest{
		CoinIDs:         simpleIDs,
		QuoteCurrencies: quoteCurrencies,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch CoinGecko current price matrix: %w", err)
	}
	if batch.FetchedAt.IsZero() || !isLowerSHA256Hex(batch.ResponseDigest) {
		return nil, fmt.Errorf("CoinGecko current price matrix has incomplete audit metadata")
	}
	priceMatrix := coinGeckoPriceMatrix(batch)
	for coinID, keys := range plan.simpleKeys {
		quote, found := priceMatrix[coinID]["USD"]
		if !found {
			continue
		}
		for _, key := range keys {
			quotes[key] = quote
		}
	}
	if err := fillCurrentFiatUSDQuotes(plan.fiatKeys, priceMatrix, quotes); err != nil {
		return nil, err
	}
	if len(quotes) != plan.mappingCount {
		return nil, fmt.Errorf("CoinGecko current USD quote response is incomplete for configured mappings: got %d of %d", len(quotes), plan.mappingCount)
	}
	return quotes, nil
}

// fillCurrentFiatUSDQuotes derives USD-per-fiat from the BTC row in the same
// /simple/price response used for crypto/USD. BTC/USD divided by BTC/fiat
// yields USD per fiat unit while preserving one response digest for audit.
func fillCurrentFiatUSDQuotes(
	fiatKeys map[string][]CoinGeckoQuoteCacheKey,
	priceMatrix map[string]map[string]CoinGeckoQuote,
	quotes map[CoinGeckoQuoteCacheKey]CoinGeckoQuote,
) error {
	if len(fiatKeys) == 0 {
		return nil
	}
	bitcoinQuotes := priceMatrix[rateSnapshotBTCProviderAssetID]
	usdQuote, found := bitcoinQuotes["USD"]
	if !found || !usdQuote.Price.GreaterThan(decimal.Zero) {
		return fmt.Errorf("CoinGecko current price matrix is missing a positive BTC/USD quote")
	}
	for fiatCode, keys := range fiatKeys {
		fiatQuote, found := bitcoinQuotes[fiatCode]
		if !found || !fiatQuote.Price.GreaterThan(decimal.Zero) {
			continue
		}
		if fiatQuote.ResponseDigest != usdQuote.ResponseDigest || !fiatQuote.FetchedAt.Equal(usdQuote.FetchedAt) {
			return fmt.Errorf("CoinGecko BTC cross for %s does not share one response snapshot", fiatCode)
		}
		usdPerFiat, err := decimalDivideBank(usdQuote.Price, fiatQuote.Price)
		if err != nil {
			return fmt.Errorf("derive USD per %s from CoinGecko price matrix: %w", fiatCode, err)
		}
		if !usdPerFiat.GreaterThan(decimal.Zero) {
			return fmt.Errorf("derive USD per %s from CoinGecko price matrix: non-positive result", fiatCode)
		}
		quote := CoinGeckoQuote{
			Price:               usdPerFiat,
			ProviderUpdatedAt:   usdQuote.ProviderUpdatedAt.UTC(),
			FetchedAt:           usdQuote.FetchedAt.UTC(),
			ResponseDigest:      usdQuote.ResponseDigest,
			Method:              USDValuationMethodCoinGeckoBTCCross,
			BTCCrossNumerator:   usdQuote.Price,
			BTCCrossDenominator: fiatQuote.Price,
		}
		for _, key := range keys {
			quotes[key] = quote
		}
	}
	return nil
}

func coinGeckoPriceMatrix(batch CoinGeckoPriceBatch) map[string]map[string]CoinGeckoQuote {
	matrix := make(map[string]map[string]CoinGeckoQuote)
	for _, price := range batch.Prices {
		if price.Quote == nil || strings.TrimSpace(price.CoinID) == "" {
			continue
		}
		coinID := strings.ToLower(strings.TrimSpace(price.CoinID))
		quoteCurrency := strings.ToUpper(strings.TrimSpace(price.QuoteCurrency))
		if matrix[coinID] == nil {
			matrix[coinID] = make(map[string]CoinGeckoQuote)
		}
		matrix[coinID][quoteCurrency] = *price.Quote
	}
	return matrix
}

func containsCoinGeckoValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
		defer recoverCompanyFundTask("current_rate_refresh")
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
		loop, err := adaptiveschedule.New(adaptiveschedule.Config{
			Name:    "company-fund-coingecko-rate-refresher",
			MinIdle: interval,
			MaxIdle: adaptiveschedule.MaxIdleAtLeast(interval),
		}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
			_, err := r.Refresh(ctx)
			// Maintenance-only cadence under the shared idle budget.
			return adaptiveschedule.CycleOutcome{}, err
		})
		if err != nil {
			return
		}
		loop.Run(ctx)
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
	fiatKeys     map[string][]CoinGeckoQuoteCacheKey
	mappings     map[CoinGeckoQuoteCacheKey]currentRateSnapshotMapping
	mappingCount int
}

func buildCoinGeckoCurrentRatePlan(policies []AccountAssetPolicy, defaults CoinGeckoDefaultRateMappings) coinGeckoCurrentRatePlan {
	plan := coinGeckoCurrentRatePlan{
		simpleKeys: make(map[string][]CoinGeckoQuoteCacheKey),
		fiatKeys:   make(map[string][]CoinGeckoQuoteCacheKey),
		mappings:   make(map[CoinGeckoQuoteCacheKey]currentRateSnapshotMapping),
	}
	seen := make(map[CoinGeckoQuoteCacheKey]struct{})
	defaultCurrencies := make([]string, 0, len(defaults.Crypto)+len(defaults.Fiat))
	for currency := range defaults.Crypto {
		defaultCurrencies = append(defaultCurrencies, currency)
	}
	defaultCurrencies = append(defaultCurrencies, defaults.Fiat...)
	sort.Strings(defaultCurrencies)
	for _, currency := range defaultCurrencies {
		key, ok := CoinGeckoQuoteCacheKeyForDefault(AssetIdentity{Currency: currency}, defaults)
		if !ok {
			continue
		}
		addCoinGeckoCurrentRatePlanMapping(&plan, seen, key, currency)
	}
	for _, policy := range policies {
		key, ok := CoinGeckoQuoteCacheKeyForPolicy(policy)
		if !ok {
			continue
		}
		addCoinGeckoCurrentRatePlanMapping(&plan, seen, key, policy.Asset.Currency)
	}
	return plan
}

func addCoinGeckoCurrentRatePlanMapping(
	plan *coinGeckoCurrentRatePlan,
	seen map[CoinGeckoQuoteCacheKey]struct{},
	key CoinGeckoQuoteCacheKey,
	baseCurrency string,
) {
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	plan.mappingCount++
	plan.mappings[key] = currentRateSnapshotMapping{BaseCurrency: strings.ToUpper(strings.TrimSpace(baseCurrency))}
	if key.FiatCode != "" {
		plan.fiatKeys[key.FiatCode] = append(plan.fiatKeys[key.FiatCode], key)
		return
	}
	plan.simpleKeys[key.CoinID] = append(plan.simpleKeys[key.CoinID], key)
}

const coinGeckoFiatMappingPrefix = "fiat:"

// CoinGeckoQuoteCacheKeyForPolicy returns an exact, configured USD cache key.
// `valuation_provider_asset_id` (represented by CoinGeckoID here) reserves
// the explicit `fiat:<CODE>` form for BTC-cross mappings, for
// example `fiat:JPY`. The code must exactly equal the policy asset currency;
// no display-symbol or ordinary coin-ID fallback is attempted for this prefix.
// A policy with both a coin ID and contract mapping is intentionally skipped:
// choosing one silently would make configuration drift change asset pricing.
// A contract-only mapping is also skipped because scheduled refresh is pinned
// to one /simple/price call and therefore requires an explicit coin ID.
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
	platformID := strings.TrimSpace(policy.CoinGeckoPlatformID)
	contractAddress := strings.TrimSpace(policy.CoinGeckoContractAddress)
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
	return CoinGeckoQuoteCacheKey{}, false
}

// CoinGeckoFiatCodeForPolicy returns a configured provider fiat code only
// when it is deliberately marked with `fiat:` and matches the asset currency.
// It accepts arbitrary alphabetic provider codes because CoinGecko may add
// legitimate fiat currencies; the single simple-price request subsequently
// requires an explicit positive BTC quote in that currency before pricing it.
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
