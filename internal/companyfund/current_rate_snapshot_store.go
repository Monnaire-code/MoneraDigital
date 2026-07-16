package companyfund

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const currentRateSnapshotGranularity = "CURRENT"

// CurrentRateSnapshotStore is the narrow durable boundary required by the
// process-local cache. DBRepository already implements it; tests can use a
// small fake without coupling provider orchestration to SQL details.
type CurrentRateSnapshotStore interface {
	AppendRateSnapshot(context.Context, RateSnapshotInput) (RateSnapshotAppendResult, error)
	FindLatestUsableRateSnapshot(context.Context, RateSnapshotLookup) (*RateSnapshotRecord, error)
}

type currentRateSnapshotMapping struct {
	BaseCurrency string
}

func (r *CoinGeckoCurrentRateRefresher) persistCurrentRateQuotes(
	ctx context.Context,
	plan coinGeckoCurrentRatePlan,
	quotes map[CoinGeckoQuoteCacheKey]CoinGeckoQuote,
) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
	if r.store == nil {
		return quotes, nil
	}
	persisted := make(map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, len(quotes))
	for _, key := range sortedCurrentRateSnapshotKeys(plan.mappings) {
		quote, found := quotes[key]
		if !found {
			return nil, fmt.Errorf("persist CoinGecko current rates: quote is missing for configured mapping")
		}
		// Each append is an independently valid provider fact. A partial durable
		// set is safe to retry and repair; the process-local cache is published
		// only after every configured mapping has a durable final snapshot.
		appendResult, err := r.persistCurrentRateQuote(ctx, key, plan.mappings[key], quote)
		if err != nil {
			return nil, fmt.Errorf("persist CoinGecko current rate snapshot: %w", err)
		}
		if appendResult.Snapshot.ID <= 0 {
			return nil, fmt.Errorf("persist CoinGecko current rate snapshot: repository returned an invalid ID")
		}
		quote.RateSnapshotID = appendResult.Snapshot.ID
		persisted[key] = quote
	}
	return persisted, nil
}

func (r *CoinGeckoCurrentRateRefresher) persistCurrentRateQuote(
	ctx context.Context,
	key CoinGeckoQuoteCacheKey,
	mapping currentRateSnapshotMapping,
	quote CoinGeckoQuote,
) (RateSnapshotAppendResult, error) {
	if quote.valuationMethod() != USDValuationMethodCoinGeckoBTCCross {
		return r.store.AppendRateSnapshot(ctx, currentRateSnapshotInput(key, mapping, quote, r.policy))
	}

	numerator, err := r.store.AppendRateSnapshot(ctx, currentRateBTCLegSnapshotInput("USD", quote.BTCCrossNumerator, quote, r.policy))
	if err != nil {
		return RateSnapshotAppendResult{}, err
	}
	denominator, err := r.store.AppendRateSnapshot(ctx, currentRateBTCLegSnapshotInput(key.FiatCode, quote.BTCCrossDenominator, quote, r.policy))
	if err != nil {
		return RateSnapshotAppendResult{}, err
	}
	input := currentRateSnapshotInput(key, mapping, quote, r.policy)
	input.NumeratorSnapshotID = &numerator.Snapshot.ID
	input.DenominatorSnapshotID = &denominator.Snapshot.ID
	return r.store.AppendRateSnapshot(ctx, input)
}

func (r *CoinGeckoCurrentRateRefresher) restoreCurrentRateQuotes(ctx context.Context, plan coinGeckoCurrentRatePlan) error {
	if r.store == nil || len(plan.mappings) == 0 || len(r.cache.Snapshot().Quotes) != 0 {
		return nil
	}
	now := r.now().UTC()
	restored := make(map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, len(plan.mappings))
	for _, key := range sortedCurrentRateSnapshotKeys(plan.mappings) {
		mapping := plan.mappings[key]
		method := USDValuationMethodCoinGeckoDirect
		if key.FiatCode != "" {
			method = USDValuationMethodCoinGeckoBTCCross
		}
		snapshot, err := r.store.FindLatestUsableRateSnapshot(ctx, RateSnapshotLookup{
			Provider: key.Provider, AssetIdentityKey: key.AssetIdentityKey,
			ProviderAssetID: currentRateProviderAssetID(key), ProviderPlatformID: key.PlatformID,
			AssetContract: key.ContractAddress, BaseCurrency: mapping.BaseCurrency,
			QuoteCurrency: key.QuoteCurrency, Method: string(method),
			Granularity: currentRateSnapshotGranularity, PolicyVersion: r.policy,
			AsOf: now, AvailableAtCutoffAt: &now, MaxGap: r.cache.maxQuoteAge,
		})
		if err != nil {
			return fmt.Errorf("restore CoinGecko current rate snapshot: %w", err)
		}
		if snapshot == nil {
			return nil
		}
		providerUpdatedAt := snapshot.BucketStart
		if snapshot.EffectiveAt != nil {
			providerUpdatedAt = *snapshot.EffectiveAt
		}
		restored[key] = CoinGeckoQuote{
			Price: snapshot.Rate, ProviderUpdatedAt: providerUpdatedAt.UTC(),
			FetchedAt: snapshot.FetchedAt.UTC(), ResponseDigest: snapshot.SourcePayloadDigest,
			Method: method, RateSnapshotID: snapshot.ID,
		}
	}
	_, err := r.cache.Refresh(ctx, func(context.Context) (map[CoinGeckoQuoteCacheKey]CoinGeckoQuote, error) {
		return restored, nil
	})
	if err != nil {
		return fmt.Errorf("restore CoinGecko current rate cache: %w", err)
	}
	return nil
}

func currentRateSnapshotInput(
	key CoinGeckoQuoteCacheKey,
	mapping currentRateSnapshotMapping,
	quote CoinGeckoQuote,
	policyVersion string,
) RateSnapshotInput {
	effectiveAt := quote.ProviderUpdatedAt.UTC()
	fetchedAt := quote.FetchedAt.UTC()
	return RateSnapshotInput{
		Provider: key.Provider, AssetIdentityKey: key.AssetIdentityKey,
		ProviderAssetID: currentRateProviderAssetID(key), ProviderPlatformID: key.PlatformID,
		AssetContract: key.ContractAddress, BaseCurrency: mapping.BaseCurrency,
		QuoteCurrency: strings.ToUpper(key.QuoteCurrency), Rate: quote.Price,
		Method: string(quote.valuationMethod()), Granularity: currentRateSnapshotGranularity,
		BucketStart: fetchedAt, EffectiveAt: &effectiveAt, AvailableAt: fetchedAt, FetchedAt: fetchedAt,
		SnapshotGroupID: quote.ResponseDigest, PolicyVersion: policyVersion,
		ProviderRevision: effectiveAt.Format(time.RFC3339Nano), SourcePayloadDigest: quote.ResponseDigest,
		IsFinal: false, PriceKind: MarketPriceKindCurrent,
	}
}

func currentRateBTCLegSnapshotInput(
	quoteCurrency string,
	rate decimal.Decimal,
	quote CoinGeckoQuote,
	policyVersion string,
) RateSnapshotInput {
	effectiveAt := quote.ProviderUpdatedAt.UTC()
	fetchedAt := quote.FetchedAt.UTC()
	bitcoinIdentity := normalizeAssetIdentity(AssetIdentity{Currency: "BTC", ProviderAssetKey: rateSnapshotBTCProviderAssetID}).canonicalKey()
	return RateSnapshotInput{
		Provider: rateSnapshotCoinGeckoProvider, AssetIdentityKey: bitcoinIdentity,
		ProviderAssetID: rateSnapshotBTCProviderAssetID, BaseCurrency: "BTC",
		QuoteCurrency: strings.ToUpper(quoteCurrency), Rate: rate,
		Method: string(USDValuationMethodCoinGeckoDirect), Granularity: currentRateSnapshotGranularity,
		BucketStart: fetchedAt, EffectiveAt: &effectiveAt, AvailableAt: fetchedAt, FetchedAt: fetchedAt,
		SnapshotGroupID: quote.ResponseDigest, PolicyVersion: policyVersion,
		ProviderRevision: effectiveAt.Format(time.RFC3339Nano), SourcePayloadDigest: quote.ResponseDigest,
		IsFinal: false, PriceKind: MarketPriceKindCurrent,
	}
}

func currentRateProviderAssetID(key CoinGeckoQuoteCacheKey) string {
	if key.FiatCode != "" {
		return coinGeckoFiatMappingPrefix + key.FiatCode
	}
	return key.CoinID
}

func sortedCurrentRateSnapshotKeys(mappings map[CoinGeckoQuoteCacheKey]currentRateSnapshotMapping) []CoinGeckoQuoteCacheKey {
	keys := make([]CoinGeckoQuoteCacheKey, 0, len(mappings))
	for key := range mappings {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].identity() < keys[j].identity() })
	return keys
}
