package companyfund

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// CompanyFundCurrentValuator applies the lowest-risk immediately available USD
// result: provider transaction values and USD parity are final, while a fresh
// configured CoinGecko cache value is explicitly provisional at ingestion
// time. Historical finalization is intentionally separate from this local
// current-rate path.
type CompanyFundCurrentValuator struct {
	store         CompanyFundValuationCandidateStore
	registry      *AccountRegistry
	cache         *CurrentRateCache
	defaults      CoinGeckoDefaultRateMappings
	policyVersion string
	sweepCursor   companyFundValuationSweepCursor
}

func NewCompanyFundCurrentValuator(
	store CompanyFundValuationCandidateStore,
	registry *AccountRegistry,
	cache *CurrentRateCache,
	config CompanyFundCurrentValuatorConfig,
) (*CompanyFundCurrentValuator, error) {
	if store == nil {
		return nil, fmt.Errorf("company-fund valuation candidate store is required")
	}
	if registry == nil {
		return nil, fmt.Errorf("company-fund account registry is required for valuation")
	}
	if cache == nil {
		return nil, fmt.Errorf("company-fund current rate cache is required for valuation")
	}
	policyVersion := strings.TrimSpace(config.PolicyVersion)
	if policyVersion == "" {
		policyVersion = defaultCompanyFundCurrentValuationPolicyVersion
	}
	if err := validateRequiredString("company-fund current valuation policy version", policyVersion, maxValuationPolicyVersionBytes); err != nil {
		return nil, err
	}
	defaultMappings, err := normalizeCoinGeckoDefaultRateMappings(config.DefaultMappings)
	if err != nil {
		return nil, fmt.Errorf("validate company-fund default rate mappings: %w", err)
	}
	return &CompanyFundCurrentValuator{
		store: store, registry: registry, cache: cache, defaults: defaultMappings, policyVersion: policyVersion,
	}, nil
}

// ValueTransaction is safe to call after a successful ledger upsert. It never
// returns an error, which prevents an optional valuation failure from causing
// the durable provider event to be retried or duplicated. The caller may log
// result.Err and rely on Sweep for repair.
func (v *CompanyFundCurrentValuator) ValueTransaction(ctx context.Context, transactionID int64) CompanyFundValuationProcessResult {
	result := CompanyFundValuationProcessResult{TransactionID: transactionID}
	if v == nil || v.store == nil {
		result.Err = fmt.Errorf("company-fund current valuator is not configured")
		return result
	}
	if transactionID <= 0 {
		result.Err = fmt.Errorf("company-fund valuation transaction ID must be positive")
		return result
	}
	candidate, err := v.store.GetCompanyFundTransactionValuationCandidate(ctx, transactionID)
	if err != nil {
		result.Err = fmt.Errorf("read company-fund valuation candidate: %w", err)
		return result
	}
	if candidate == nil {
		result.Skipped = true
		return result
	}
	return v.valueCandidate(ctx, *candidate)
}

// Sweep is the crash-repair path. It visits only missing/UNPRICED/STALE
// current records selected by the database. One candidate failure is retained
// in the result and does not prevent the rest of the bounded batch from
// converging; a later sweep retries it through the same idempotent fingerprint.
func (v *CompanyFundCurrentValuator) Sweep(ctx context.Context, limit int) CompanyFundValuationSweepResult {
	result := CompanyFundValuationSweepResult{}
	if v == nil || v.store == nil {
		result.Err = fmt.Errorf("company-fund current valuator is not configured")
		return result
	}
	normalizedLimit, err := normalizeCompanyFundValuationRepairLimit(limit)
	if err != nil {
		result.Err = err
		return result
	}
	v.sweepCursor.mu.Lock()
	defer v.sweepCursor.mu.Unlock()
	candidates, err := v.store.ListCompanyFundValuationRepairCandidatesAfter(ctx, v.sweepCursor.afterID, normalizedLimit)
	if err != nil {
		result.Err = fmt.Errorf("list company-fund valuation repair candidates: %w", err)
		return result
	}
	if len(candidates) == 0 && v.sweepCursor.afterID > 0 {
		v.sweepCursor.afterID = 0
		candidates, err = v.store.ListCompanyFundValuationRepairCandidatesAfter(ctx, 0, normalizedLimit)
		if err != nil {
			result.Err = fmt.Errorf("restart company-fund valuation repair scan: %w", err)
			return result
		}
	}
	result.CandidateCount = len(candidates)
	for _, candidate := range candidates {
		result.Attempted++
		processed := v.valueCandidate(ctx, candidate)
		if processed.Err != nil {
			result.Failed++
			if result.Err == nil {
				result.Err = processed.Err
			}
			continue
		}
		if processed.Applied {
			result.Applied++
		}
		if processed.Converged {
			result.Converged++
		}
		if processed.Superseded {
			result.Superseded++
		}
		if processed.SkippedManual {
			result.SkippedManual++
		}
	}
	if len(candidates) > 0 {
		v.sweepCursor.afterID = candidates[len(candidates)-1].ID
	}
	return result
}

func (v *CompanyFundCurrentValuator) valueCandidate(ctx context.Context, candidate CompanyFundTransactionValuationCandidate) CompanyFundValuationProcessResult {
	result := CompanyFundValuationProcessResult{TransactionID: candidate.ID}
	if candidate.CurrentValuationSource == USDValuationSourceManual {
		result.Skipped = true
		result.SkippedManual = true
		return result
	}
	if v == nil || v.store == nil || v.registry == nil || v.cache == nil {
		result.Err = fmt.Errorf("company-fund current valuator is not configured")
		return result
	}
	if err := candidate.validate(); err != nil {
		result.Err = err
		return result
	}

	valuation, policy, rateKey, quoteRead, err := v.evaluateCandidate(candidate)
	if err != nil {
		result.Err = err
		return result
	}
	result.Result = valuation
	apply, err := v.applyInputForCandidate(candidate, valuation, policy, rateKey, quoteRead)
	if err != nil {
		result.Err = err
		return result
	}
	applied, err := v.store.ApplyCompanyFundValuation(ctx, apply)
	if err != nil {
		result.Err = fmt.Errorf("apply company-fund USD valuation: %w", err)
		return result
	}
	result.Superseded = applied.Superseded
	result.Applied = applied.Inserted
	result.Converged = !applied.Superseded
	return result
}

func (v *CompanyFundCurrentValuator) evaluateCandidate(candidate CompanyFundTransactionValuationCandidate) (USDValuationResult, *AccountAssetPolicy, *CoinGeckoQuoteCacheKey, *CurrentRateCacheRead, error) {
	input := candidate.usdValuationInput()
	direct, err := EvaluateUSDValue(input)
	if err != nil {
		return USDValuationResult{}, nil, nil, nil, err
	}
	if direct.Status == USDValuationStatusFinal {
		return direct, nil, nil, nil, nil
	}

	subjectAccountID, err := PolicySubjectAccountID(candidate.Direction, candidate.FromCompanyFundAccountID, candidate.ToCompanyFundAccountID)
	if err != nil {
		return companyFundUnpricedMappingResult(direct), nil, nil, nil, nil
	}
	policy, rateKey, configured := v.resolveCurrentRateMapping(candidate, subjectAccountID)
	if !configured || rateKey == nil {
		return companyFundUnpricedMappingResult(direct), policy, nil, nil, nil
	}
	quoteRead, found := v.cache.Get(*rateKey)
	if !found {
		return direct, policy, rateKey, nil, nil
	}
	if quoteRead.Stale {
		return companyFundStaleCurrentRateResult(input, direct.ProviderReportedUSD, quoteRead), policy, rateKey, &quoteRead, nil
	}

	price := quoteRead.Quote.Price
	input.CoinGeckoUnitPrice = &price
	input.CoinGeckoPriceKind = MarketPriceKindCurrent
	input.CoinGeckoPriceAt = timePointerUTC(quoteRead.Quote.ProviderUpdatedAt)
	input.CoinGeckoEffectiveAt = timePointerUTC(quoteRead.Quote.ProviderUpdatedAt)
	input.CoinGeckoAvailableAt = timePointerUTC(quoteRead.Quote.FetchedAt)
	input.CoinGeckoGranularity = "CURRENT"
	market, err := EvaluateUSDValue(input)
	if err != nil {
		return USDValuationResult{}, nil, nil, nil, err
	}
	market.Method = quoteRead.Quote.valuationMethod()
	return market, policy, rateKey, &quoteRead, nil
}

func (v *CompanyFundCurrentValuator) resolveCurrentRateMapping(candidate CompanyFundTransactionValuationCandidate, accountID int64) (*AccountAssetPolicy, *CoinGeckoQuoteCacheKey, bool) {
	policy, hasPolicy := v.registry.Snapshot().LookupAssetPolicy(accountID, candidate.Asset)
	var selectedPolicy *AccountAssetPolicy
	if hasPolicy {
		selectedPolicy = &policy
		if key, configured := CoinGeckoQuoteCacheKeyForPolicy(policy); configured {
			return selectedPolicy, &key, true
		}
		if !companyFundPolicyValuationMappingBlank(policy) {
			return selectedPolicy, nil, false
		}
	}
	if candidate.IsUnrecognizedAsset {
		return selectedPolicy, nil, false
	}
	key, configured := CoinGeckoQuoteCacheKeyForDefault(candidate.Asset, v.defaults)
	if !configured {
		return selectedPolicy, nil, false
	}
	return selectedPolicy, &key, true
}

func companyFundPolicyValuationMappingBlank(policy AccountAssetPolicy) bool {
	return strings.TrimSpace(policy.CoinGeckoID) == "" &&
		strings.TrimSpace(policy.CoinGeckoPlatformID) == "" &&
		strings.TrimSpace(policy.CoinGeckoContractAddress) == ""
}

func companyFundUnpricedMappingResult(previous USDValuationResult) USDValuationResult {
	if previous.Reason != USDValuationReasonRateMissing {
		return previous
	}
	previous.Reason = USDValuationReasonMappingMissing
	return previous
}

func companyFundStaleCurrentRateResult(input USDValuationInput, providerReportedUSD *decimal.Decimal, quote CurrentRateCacheRead) USDValuationResult {
	ingestionAt := copyTime(input.IngestionAt)
	priceAt := timePointerUTC(quote.Quote.ProviderUpdatedAt)
	availableAt := timePointerUTC(quote.Quote.FetchedAt)
	return USDValuationResult{
		ProviderReportedUSD: providerReportedUSD,
		Source:              USDValuationSourceCoinGecko,
		Method:              quote.Quote.valuationMethod(),
		Status:              USDValuationStatusStale,
		Reason:              USDValuationReasonCacheStale,
		Basis:               USDValuationBasisIngestionTime,
		ValuationTargetAt:   ingestionAt,
		PriceAt:             priceAt,
		EffectiveAt:         copyTime(priceAt),
		AvailableAt:         availableAt,
		Granularity:         "CURRENT",
	}
}

func (candidate CompanyFundTransactionValuationCandidate) usdValuationInput() USDValuationInput {
	ingestionAt := candidate.FirstSeenAt.UTC()
	return USDValuationInput{
		Channel:                 candidate.Channel,
		MovementKind:            candidate.MovementKind,
		Currency:                candidate.Currency,
		UnrecognizedAsset:       candidate.IsUnrecognizedAsset,
		Amount:                  candidate.Amount,
		ProviderReportedUSD:     cloneValuationDecimal(candidate.ProviderReportedUSD),
		ProviderValueScope:      candidate.ProviderValueScope,
		ProviderScopeProven:     candidate.ProviderAllocationState == ProviderFactAllocationStateProvenDerivable,
		AirwallexConversionFrom: candidate.AirwallexConversionFrom,
		AirwallexConversionTo:   candidate.AirwallexConversionTo,
		ValuationTargetAt:       candidate.transactionValuationTime(),
		IngestionAt:             &ingestionAt,
	}
}

func (candidate CompanyFundTransactionValuationCandidate) transactionValuationTime() *time.Time {
	if candidate.OccurredAt != nil && !candidate.OccurredAt.IsZero() {
		return timePointerUTC(*candidate.OccurredAt)
	}
	if candidate.CompletedAt != nil && !candidate.CompletedAt.IsZero() {
		return timePointerUTC(*candidate.CompletedAt)
	}
	return nil
}

func (v *CompanyFundCurrentValuator) applyInputForCandidate(
	candidate CompanyFundTransactionValuationCandidate,
	valuation USDValuationResult,
	policy *AccountAssetPolicy,
	rateKey *CoinGeckoQuoteCacheKey,
	quoteRead *CurrentRateCacheRead,
) (CompanyFundValuationApplyInput, error) {
	dependencyFingerprint := companyFundCurrentValuationFingerprint(candidate, valuation, policy, rateKey, quoteRead, v.policyVersion)
	input := CompanyFundValuationApplyInput{
		TransactionID:             candidate.ID,
		Result:                    valuation,
		CalculatedUSDValue:        companyFundCalculatedUSDValue(valuation),
		ProviderTransactionFactID: cloneCompanyFundValuationID(candidate.ProviderTransactionFactID),
		ProviderValueScope:        persistedCandidateProviderValueScope(candidate.ProviderValueScope),
		DerivationMethod:          companyFundValuationDerivationMethod(valuation, candidate.ProviderValueScope),
		DependencyFingerprint:     dependencyFingerprint,
		PolicyVersion:             v.policyVersion,
		TransitionTrigger:         companyFundCurrentValuationTrigger,
	}
	if quoteRead != nil && quoteRead.Quote.RateSnapshotID > 0 {
		rateSnapshotID := quoteRead.Quote.RateSnapshotID
		input.RateSnapshotID = &rateSnapshotID
	}
	if candidate.CurrentValuationHistoryID == nil {
		expectation := ValuationCurrentStateExpectationNone
		input.ExpectedCurrentState = &expectation
	} else {
		expectation := ValuationCurrentStateExpectationHistory
		input.ExpectedCurrentState = &expectation
		input.ExpectedCurrentHistoryID = cloneCompanyFundValuationID(candidate.CurrentValuationHistoryID)
		input.ExpectedCurrentDependencyFingerprint = candidate.CurrentValuationDependencyFingerprint
	}
	if err := input.validate(); err != nil {
		return CompanyFundValuationApplyInput{}, err
	}
	return input, nil
}

func companyFundCalculatedUSDValue(result USDValuationResult) *decimal.Decimal {
	if (result.Status != USDValuationStatusFinal && result.Status != USDValuationStatusProvisional) || result.Value == nil {
		return nil
	}
	if result.Source != USDValuationSourceCoinGecko && result.Source != USDValuationSourceUSDPar {
		return nil
	}
	return cloneValuationDecimal(result.Value)
}

func persistedCandidateProviderValueScope(scope ProviderValueScope) ProviderValueScope {
	if validPersistedProviderValueScope(scope) {
		return scope
	}
	return ""
}

func companyFundValuationDerivationMethod(result USDValuationResult, scope ProviderValueScope) ValuationDerivationMethod {
	switch result.Source {
	case USDValuationSourceCoinGecko:
		return ValuationDerivationMethodMarketPrice
	case USDValuationSourceUSDPar:
		return ValuationDerivationMethodDirectItem
	case USDValuationSourceSafeheron, USDValuationSourceAirwallex:
		if scope == ProviderValueScopeDerivedFromParent {
			return ValuationDerivationMethodDerivedFromParent
		}
		return ValuationDerivationMethodDirectItem
	default:
		return ""
	}
}

func companyFundCurrentValuationFingerprint(
	candidate CompanyFundTransactionValuationCandidate,
	result USDValuationResult,
	policy *AccountAssetPolicy,
	rateKey *CoinGeckoQuoteCacheKey,
	quoteRead *CurrentRateCacheRead,
	policyVersion string,
) string {
	values := []string{
		"company-fund-current-valuation-v1",
		fmt.Sprintf("%d", candidate.ID),
		string(candidate.Channel),
		string(candidate.MovementKind),
		string(candidate.Direction),
		strings.ToUpper(strings.TrimSpace(candidate.Currency)),
		candidate.Amount.String(),
		normalizeAssetIdentity(candidate.Asset).canonicalKey(),
		fmt.Sprintf("unrecognized=%t", candidate.IsUnrecognizedAsset),
		companyFundValuationFingerprintTime(candidate.OccurredAt),
		companyFundValuationFingerprintTime(candidate.CompletedAt),
		candidate.FirstSeenAt.UTC().Format(time.RFC3339Nano),
		companyFundValuationFingerprintID(candidate.ProviderTransactionFactID),
		companyFundValuationFingerprintDecimal(candidate.ProviderReportedUSD),
		string(candidate.ProviderValueScope),
		string(candidate.ProviderAllocationState),
		candidate.AirwallexConversionFrom,
		candidate.AirwallexConversionTo,
		policyVersion,
		string(result.Status),
		string(result.Reason),
		string(result.Source),
		string(result.Method),
		string(result.Basis),
		companyFundValuationFingerprintDecimal(result.Value),
		result.UnitPrice.String(),
		companyFundValuationFingerprintTime(result.ValuationTargetAt),
		companyFundValuationFingerprintTime(result.PriceAt),
		companyFundValuationFingerprintTime(result.AvailableAt),
		result.Granularity,
	}
	if policy != nil {
		values = append(values,
			fmt.Sprintf("%d", policy.ID),
			normalizeAssetIdentity(policy.Asset).canonicalKey(),
			strings.TrimSpace(policy.CoinGeckoID),
			strings.TrimSpace(policy.CoinGeckoPlatformID),
			strings.TrimSpace(policy.CoinGeckoContractAddress),
		)
	} else {
		values = append(values, "no-policy")
	}
	if rateKey != nil && (policy == nil || companyFundPolicyValuationMappingBlank(*policy)) {
		values = append(values, "rate-mapping", rateKey.identity())
	}
	if quoteRead != nil {
		values = append(values,
			quoteRead.Quote.Price.String(),
			quoteRead.Quote.ProviderUpdatedAt.UTC().Format(time.RFC3339Nano),
			quoteRead.Quote.FetchedAt.UTC().Format(time.RFC3339Nano),
			quoteRead.Quote.ResponseDigest,
			fmt.Sprintf("rate-snapshot-id=%d", quoteRead.Quote.RateSnapshotID),
			fmt.Sprintf("stale=%t", quoteRead.Stale),
			fmt.Sprintf("provider-stale=%t", quoteRead.ProviderStale),
			fmt.Sprintf("refresh-failed=%t", quoteRead.RefreshFailed),
		)
	} else {
		values = append(values, "no-quote")
	}
	digest := sha256.Sum256([]byte(lengthDelimitedTuple(values)))
	return hex.EncodeToString(digest[:])
}

func companyFundValuationFingerprintTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func companyFundValuationFingerprintID(value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%d", *value)
}

func companyFundValuationFingerprintDecimal(value *decimal.Decimal) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func cloneValuationDecimal(value *decimal.Decimal) *decimal.Decimal {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneCompanyFundValuationID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func timePointerUTC(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value.UTC()
	return &copy
}
