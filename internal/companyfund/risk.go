package companyfund

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// PolicySubjectAccountID implements the finance-approved policy subject rule:
// inbound uses the configured destination, outbound uses the configured source,
// and internal transfers use the configured destination. The caller can retain
// a nil subject during early parsing, but a persisted movement must pass the
// structural relation validation separately.
func PolicySubjectAccountID(direction Direction, fromAccountID, toAccountID *int64) (int64, error) {
	switch direction {
	case DirectionInflow, DirectionInternalTransfer:
		if toAccountID == nil {
			return 0, fmt.Errorf("%s risk policy requires a configured destination account", direction)
		}
		return *toAccountID, nil
	case DirectionOutflow:
		if fromAccountID == nil {
			return 0, fmt.Errorf("outflow risk policy requires a configured source account")
		}
		return *fromAccountID, nil
	default:
		return 0, fmt.Errorf("unsupported risk policy direction %q", direction)
	}
}

// EvaluateRisk classifies risk without modifying manual finance fields. It
// intentionally does not turn absent provider bools into false; only an
// explicit true contributes a phishing or AML flag.
func EvaluateRisk(input RiskInput) (RiskAssessment, error) {
	if !input.Channel.Valid() {
		return RiskAssessment{}, fmt.Errorf("unsupported risk channel %q", input.Channel)
	}
	if !input.Direction.Valid() {
		return RiskAssessment{}, fmt.Errorf("unsupported risk direction %q", input.Direction)
	}
	if input.Amount.IsNegative() {
		return RiskAssessment{}, fmt.Errorf("risk amount must be non-negative")
	}

	result := RiskAssessment{}
	if subject, err := PolicySubjectAccountID(input.Direction, input.ConfiguredFromID, input.ConfiguredToID); err == nil {
		result.PolicySubjectAccountID = &subject
	}

	if input.Policy.Enabled && input.Policy.ID > 0 && input.Policy.Threshold != nil {
		if input.Policy.Threshold.IsNegative() {
			return RiskAssessment{}, fmt.Errorf("dust threshold must be non-negative")
		}
		if input.Amount.LessThan(*input.Policy.Threshold) {
			result.IsDust = true
			result.Flags = append(result.Flags, RiskFlagDust)
		}
	}

	sourcePhishing := boolTrue(input.SourcePhishing) && (input.Direction == DirectionInflow || input.Direction == DirectionInternalTransfer)
	destinationPhishing := boolTrue(input.DestinationPhishing) && (input.Direction == DirectionOutflow || input.Direction == DirectionInternalTransfer)
	if sourcePhishing {
		result.Flags = append(result.Flags, RiskFlagSourcePhishing)
	}
	if destinationPhishing {
		result.Flags = append(result.Flags, RiskFlagDestinationPhishing)
	}
	if input.UnrecognizedAsset {
		result.Flags = append(result.Flags, RiskFlagUnrecognizedAsset)
	}
	if input.Amount.IsZero() {
		result.Flags = append(result.Flags, RiskFlagZeroAmount)
	}
	if boolTrue(input.AMLLock) {
		result.Flags = append(result.Flags, RiskFlagAMLLock)
	}
	switch input.AMLRiskLevel {
	case AMLRiskLevelHigh:
		result.Flags = append(result.Flags, RiskFlagAMLHigh)
	case AMLRiskLevelCritical:
		result.Flags = append(result.Flags, RiskFlagAMLCritical)
	}

	result.ReviewRequired = sourcePhishing || destinationPhishing || boolTrue(input.AMLLock) ||
		input.AMLRiskLevel == AMLRiskLevelHigh || input.AMLRiskLevel == AMLRiskLevelCritical
	result.AutomaticExclusion = result.IsDust || sourcePhishing || destinationPhishing ||
		input.UnrecognizedAsset || input.Amount.IsZero() || boolTrue(input.AMLLock)
	result.ImmediateAlert = destinationPhishing && input.Direction == DirectionOutflow
	if input.SummaryOverride != nil {
		result.EffectiveSummaryIncluded = *input.SummaryOverride
	} else {
		result.EffectiveSummaryIncluded = !result.AutomaticExclusion
	}
	result.AlertAggregationKey = riskAlertAggregationKey(input, result)
	return result, nil
}

func riskAlertAggregationKey(input RiskInput, result RiskAssessment) string {
	if len(result.Flags) == 0 {
		return ""
	}
	subject := "unmatched"
	if result.PolicySubjectAccountID != nil {
		subject = fmt.Sprintf("%d", *result.PolicySubjectAccountID)
	}
	flags := make([]string, 0, len(result.Flags))
	for _, flag := range result.Flags {
		flags = append(flags, string(flag))
	}
	return strings.Join([]string{
		string(input.Channel),
		subject,
		normalizeAssetIdentity(input.Asset).canonicalKey(),
		strings.Join(flags, ","),
	}, "|")
}

func boolTrue(value *bool) bool {
	return value != nil && *value
}

// EvaluateUSDValue applies value-source eligibility separately from provider
// field merge precedence. Every returned decimal is explicitly banker-rounded
// to the storage scale; missing provider/market prices are nil, never a final
// numeric zero.
func EvaluateUSDValue(input USDValuationInput) (USDValuationResult, error) {
	if !input.Channel.Valid() {
		return USDValuationResult{}, fmt.Errorf("unsupported valuation channel %q", input.Channel)
	}
	if input.Amount.IsNegative() {
		return USDValuationResult{}, fmt.Errorf("valuation amount must be non-negative")
	}
	if input.ProviderReportedUSD != nil && input.ProviderReportedUSD.IsNegative() {
		return USDValuationResult{}, fmt.Errorf("provider-reported USD value must be non-negative")
	}
	if input.CoinGeckoUnitPrice != nil && input.CoinGeckoUnitPrice.IsNegative() {
		return USDValuationResult{}, fmt.Errorf("CoinGecko unit price must be non-negative")
	}

	providerReportedUSD := usableStorageDecimal(input.ProviderReportedUSD)
	result := USDValuationResult{ProviderReportedUSD: providerReportedUSD}
	if strings.EqualFold(strings.TrimSpace(input.Currency), "USD") {
		value := quantizeDecimalBank(input.Amount)
		timing, timed := providerValuationTiming(input)
		if !timed {
			return USDValuationResult{
				ProviderReportedUSD: providerReportedUSD,
				Source:              USDValuationSourceUSDPar,
				Method:              USDValuationMethodUSDPar,
				Status:              USDValuationStatusUnpriced,
				Reason:              USDValuationReasonMissingValuationTime,
			}, nil
		}
		return USDValuationResult{
			Value:               &value,
			UnitPrice:           decimal.NewFromInt(1),
			ProviderReportedUSD: providerReportedUSD,
			Source:              USDValuationSourceUSDPar,
			Method:              USDValuationMethodUSDPar,
			Status:              USDValuationStatusFinal,
			Basis:               timing.Basis,
			ValuationTargetAt:   timing.ValuationTargetAt,
			PriceAt:             timing.ValuationTargetAt,
			EffectiveAt:         timing.ValuationTargetAt,
			AvailableAt:         timing.AvailableAt,
		}, nil
	}
	if input.Amount.IsZero() {
		result.Status = USDValuationStatusUnpriced
		result.Reason = USDValuationReasonZeroAmount
		return result, nil
	}

	if eligibleSafeheronProviderUSD(input, providerReportedUSD) {
		return providerUSDResult(input, providerReportedUSD, USDValuationSourceSafeheron, USDValuationMethodProviderTransaction)
	}
	if eligibleAirwallexProviderUSD(input, providerReportedUSD) {
		return providerUSDResult(input, providerReportedUSD, USDValuationSourceAirwallex, USDValuationMethodProviderConversion)
	}
	if usableMarketPrice := usableStorageDecimal(input.CoinGeckoUnitPrice); usableMarketPrice != nil {
		return marketUSDResult(input, providerReportedUSD, usableMarketPrice)
	}

	result.Status = USDValuationStatusUnpriced
	switch {
	case providerReportedUSD != nil && input.Channel == ChannelAirwallex && isNonUSDAirwallexCross(input):
		result.Reason = USDValuationReasonNonUSDCross
	case input.CoinGeckoUnitPrice != nil && input.CoinGeckoUnitPrice.IsZero():
		result.Reason = USDValuationReasonRateMissing
	case providerReportedUSD != nil:
		result.Reason = USDValuationReasonUnprovenProviderScope
	default:
		result.Reason = USDValuationReasonRateMissing
	}
	return result, nil
}

func eligibleSafeheronProviderUSD(input USDValuationInput, providerReportedUSD *decimal.Decimal) bool {
	if input.Channel != ChannelSafeheron || input.MovementKind != MovementKindPrincipal || providerReportedUSD == nil {
		return false
	}
	return input.ProviderValueScope == ProviderValueScopeDirectItem ||
		(input.ProviderValueScope == ProviderValueScopeDerivedFromParent && input.ProviderScopeProven)
}

func eligibleAirwallexProviderUSD(input USDValuationInput, providerReportedUSD *decimal.Decimal) bool {
	if input.Channel != ChannelAirwallex || input.MovementKind != MovementKindConversion || providerReportedUSD == nil {
		return false
	}
	if input.ProviderValueScope != ProviderValueScopeDirectItem {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(input.AirwallexConversionFrom), "USD") ||
		strings.EqualFold(strings.TrimSpace(input.AirwallexConversionTo), "USD")
}

func isNonUSDAirwallexCross(input USDValuationInput) bool {
	return strings.TrimSpace(input.AirwallexConversionFrom) != "" &&
		strings.TrimSpace(input.AirwallexConversionTo) != "" &&
		!strings.EqualFold(strings.TrimSpace(input.AirwallexConversionFrom), "USD") &&
		!strings.EqualFold(strings.TrimSpace(input.AirwallexConversionTo), "USD")
}

func providerUSDResult(input USDValuationInput, providerReportedUSD *decimal.Decimal, source USDValuationSource, method USDValuationMethod) (USDValuationResult, error) {
	unitPrice, err := decimalDivideBank(*providerReportedUSD, input.Amount)
	if err != nil {
		return USDValuationResult{}, err
	}
	value := *providerReportedUSD
	timing, timed := providerValuationTiming(input)
	if !timed {
		return USDValuationResult{
			ProviderReportedUSD: providerReportedUSD,
			Source:              source,
			Method:              method,
			Status:              USDValuationStatusUnpriced,
			Reason:              USDValuationReasonMissingValuationTime,
		}, nil
	}
	return USDValuationResult{
		Value:               &value,
		UnitPrice:           unitPrice,
		ProviderReportedUSD: providerReportedUSD,
		Source:              source,
		Method:              method,
		Status:              USDValuationStatusFinal,
		Basis:               timing.Basis,
		ValuationTargetAt:   timing.ValuationTargetAt,
		AvailableAt:         timing.AvailableAt,
	}, nil
}

func marketUSDResult(input USDValuationInput, providerReportedUSD, marketUnitPrice *decimal.Decimal) (USDValuationResult, error) {
	timing, reason, eligible := marketValuationTiming(input)
	if !eligible {
		status := USDValuationStatusStale
		if reason == USDValuationReasonMissingValuationTime {
			status = USDValuationStatusUnpriced
		}
		return USDValuationResult{
			ProviderReportedUSD: providerReportedUSD,
			Source:              USDValuationSourceCoinGecko,
			Method:              USDValuationMethodCoinGeckoDirect,
			Status:              status,
			Reason:              reason,
			Basis:               timing.Basis,
			ValuationTargetAt:   timing.ValuationTargetAt,
			PriceAt:             timing.PriceAt,
			EffectiveAt:         timing.EffectiveAt,
			AvailableAt:         timing.AvailableAt,
			Granularity:         input.CoinGeckoGranularity,
		}, nil
	}

	value := quantizeDecimalBank(input.Amount.Mul(*marketUnitPrice))
	return USDValuationResult{
		Value:               &value,
		UnitPrice:           *marketUnitPrice,
		ProviderReportedUSD: providerReportedUSD,
		Source:              USDValuationSourceCoinGecko,
		Method:              USDValuationMethodCoinGeckoDirect,
		Status:              timing.Status,
		Basis:               timing.Basis,
		ValuationTargetAt:   timing.ValuationTargetAt,
		PriceAt:             timing.PriceAt,
		EffectiveAt:         timing.EffectiveAt,
		AvailableAt:         timing.AvailableAt,
		Granularity:         input.CoinGeckoGranularity,
	}, nil
}

type valuationTiming struct {
	Status            USDValuationStatus
	Basis             USDValuationBasis
	ValuationTargetAt *time.Time
	PriceAt           *time.Time
	EffectiveAt       *time.Time
	AvailableAt       *time.Time
}

func providerValuationTiming(input USDValuationInput) (valuationTiming, bool) {
	if input.ValuationTargetAt != nil && !input.ValuationTargetAt.IsZero() {
		return valuationTiming{
			Status:            USDValuationStatusFinal,
			Basis:             USDValuationBasisTransactionTime,
			ValuationTargetAt: copyTime(input.ValuationTargetAt),
			AvailableAt:       copyTime(input.IngestionAt),
		}, true
	}
	if input.IngestionAt != nil && !input.IngestionAt.IsZero() {
		return valuationTiming{
			Status:            USDValuationStatusFinal,
			Basis:             USDValuationBasisIngestionTime,
			ValuationTargetAt: copyTime(input.IngestionAt),
			AvailableAt:       copyTime(input.IngestionAt),
		}, true
	}
	return valuationTiming{}, false
}

func marketValuationTiming(input USDValuationInput) (valuationTiming, USDValuationReason, bool) {
	timing := valuationTiming{
		PriceAt:     copyTime(input.CoinGeckoPriceAt),
		EffectiveAt: copyTime(input.CoinGeckoEffectiveAt),
		AvailableAt: copyTime(input.CoinGeckoAvailableAt),
	}
	if timing.EffectiveAt == nil {
		timing.EffectiveAt = copyTime(input.CoinGeckoPriceAt)
	}
	if input.CoinGeckoPriceKind == "" || input.CoinGeckoPriceKind == MarketPriceKindCurrent {
		if input.IngestionAt == nil || input.IngestionAt.IsZero() {
			return timing, USDValuationReasonMissingValuationTime, false
		}
		timing.Status = USDValuationStatusProvisional
		timing.Basis = USDValuationBasisIngestionTime
		timing.ValuationTargetAt = copyTime(input.IngestionAt)
		return timing, USDValuationReasonNone, true
	}
	if input.CoinGeckoPriceKind != MarketPriceKindHistorical || input.ValuationTargetAt == nil || input.ValuationTargetAt.IsZero() || input.CoinGeckoPriceAt == nil || input.CoinGeckoAvailableAt == nil || input.CoinGeckoAvailableAt.IsZero() || input.HistoricalMaxGap < 0 {
		return timing, USDValuationReasonHistoricalGap, false
	}
	target := *input.ValuationTargetAt
	if input.CoinGeckoPriceAt.After(target) || target.Sub(*input.CoinGeckoPriceAt) > input.HistoricalMaxGap {
		return timing, USDValuationReasonHistoricalGap, false
	}
	if input.AvailableAtCutoffAt != nil && input.CoinGeckoAvailableAt.After(*input.AvailableAtCutoffAt) {
		return timing, USDValuationReasonHistoricalGap, false
	}
	timing.Status = USDValuationStatusFinal
	timing.Basis = USDValuationBasisTransactionTime
	timing.ValuationTargetAt = copyTime(input.ValuationTargetAt)
	return timing, USDValuationReasonNone, true
}

func usableStorageDecimal(value *decimal.Decimal) *decimal.Decimal {
	if value == nil || value.IsZero() {
		return nil
	}
	quantized := quantizeDecimalBank(*value)
	if quantized.IsZero() {
		return nil
	}
	return &quantized
}

func quantizeDecimalBank(value decimal.Decimal) decimal.Decimal {
	return value.RoundBank(DecimalCalculationScale)
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// SelectHistoricalPrice returns the latest sample at or before target and only
// when it is within the policy max gap. It never selects a nearer future price.
func SelectHistoricalPrice(points []HistoricalPricePoint, target time.Time, maxGap time.Duration) (HistoricalPricePoint, bool) {
	if target.IsZero() || maxGap < 0 {
		return HistoricalPricePoint{}, false
	}
	var selected HistoricalPricePoint
	found := false
	for _, point := range points {
		if point.Price.IsNegative() || point.Price.IsZero() || point.PriceAt.IsZero() || point.PriceAt.After(target) {
			continue
		}
		if target.Sub(point.PriceAt) > maxGap {
			continue
		}
		if !found || point.PriceAt.After(selected.PriceAt) {
			selected = point
			found = true
		}
	}
	return selected, found
}

// DeriveBTCCross returns USD-per-fiat from BTC/USD divided by BTC/fiat. Both
// legs must be eligible final leaves in the same normalized series. Current
// quotes additionally require the same non-empty response group; historical
// requests may be independent as long as their series/bucket rules match.
func DeriveBTCCross(numerator, denominator BTCCrossLeg, target time.Time, maxGap time.Duration) (BTCCrossResult, bool) {
	if !validBTCCrossLeg(numerator, target, maxGap) || !validBTCCrossLeg(denominator, target, maxGap) {
		return BTCCrossResult{}, false
	}
	if !strings.EqualFold(numerator.Provider, denominator.Provider) ||
		!strings.EqualFold(numerator.AssetID, "bitcoin") || !strings.EqualFold(denominator.AssetID, "bitcoin") ||
		!strings.EqualFold(numerator.Quote, "USD") || strings.EqualFold(denominator.Quote, "USD") ||
		numerator.PolicyVersion != denominator.PolicyVersion || numerator.Granularity != denominator.Granularity ||
		!numerator.BucketStart.Equal(denominator.BucketStart) || numerator.PriceKind != denominator.PriceKind || denominator.Price.IsZero() {
		return BTCCrossResult{}, false
	}
	if numerator.PriceKind == MarketPriceKindCurrent && (numerator.SnapshotGroupID == "" || denominator.SnapshotGroupID == "" || numerator.SnapshotGroupID != denominator.SnapshotGroupID) {
		return BTCCrossResult{}, false
	}
	unitPrice, err := decimalDivideBank(numerator.Price, denominator.Price)
	if err != nil {
		return BTCCrossResult{}, false
	}
	snapshotGroupID := ""
	if numerator.PriceKind == MarketPriceKindCurrent {
		snapshotGroupID = numerator.SnapshotGroupID
	}
	return BTCCrossResult{
		UnitPrice:       unitPrice,
		PriceAt:         laterTime(numerator.PriceAt, denominator.PriceAt),
		AvailableAt:     laterTime(numerator.AvailableAt, denominator.AvailableAt),
		IsFinal:         numerator.IsFinal && denominator.IsFinal,
		PriceKind:       numerator.PriceKind,
		SnapshotGroupID: snapshotGroupID,
	}, true
}

func validBTCCrossLeg(leg BTCCrossLeg, target time.Time, maxGap time.Duration) bool {
	if !leg.PriceKind.Valid() || !leg.IsEligibleLeaf || !leg.IsFinal || leg.Price.IsNegative() || leg.Price.IsZero() || leg.PriceAt.IsZero() || leg.AvailableAt.IsZero() || leg.PriceAt.After(target) {
		return false
	}
	return target.Sub(leg.PriceAt) <= maxGap
}

func laterTime(first, second time.Time) time.Time {
	if second.After(first) {
		return second
	}
	return first
}

// decimalDivideBank divides exact non-negative decimal values at the fixed
// calculation scale and applies banker rounding only at the storage boundary.
func decimalDivideBank(numerator, denominator decimal.Decimal) (decimal.Decimal, error) {
	if denominator.IsZero() {
		return decimal.Zero, fmt.Errorf("cannot divide by zero")
	}
	if numerator.IsNegative() || denominator.IsNegative() {
		return decimal.Zero, fmt.Errorf("decimal division expects non-negative values")
	}
	quotient, remainder := numerator.QuoRem(denominator, DecimalCalculationScale)
	comparison := remainder.Abs().Mul(decimal.NewFromInt(2)).Shift(DecimalCalculationScale).Cmp(denominator.Abs())
	step := decimal.New(1, -DecimalCalculationScale)
	switch {
	case comparison > 0:
		return quotient.Add(step), nil
	case comparison < 0:
		return quotient, nil
	case quotient.Coefficient().Bit(0) == 0:
		return quotient, nil
	default:
		return quotient.Add(step), nil
	}
}
