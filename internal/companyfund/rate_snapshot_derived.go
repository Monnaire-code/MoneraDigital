package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func validateDerivedRateSnapshot(ctx context.Context, tx *sql.Tx, input RateSnapshotInput) error {
	if !strings.EqualFold(input.Provider, rateSnapshotCoinGeckoProvider) {
		return fmt.Errorf("%s rate snapshots require provider %q", USDValuationMethodCoinGeckoBTCCross, rateSnapshotCoinGeckoProvider)
	}
	if input.NumeratorSnapshotID == nil || input.DenominatorSnapshotID == nil {
		return fmt.Errorf("%s rate snapshot requires numerator and denominator snapshots", USDValuationMethodCoinGeckoBTCCross)
	}
	if *input.NumeratorSnapshotID == *input.DenominatorSnapshotID {
		return fmt.Errorf("%s rate snapshot numerator and denominator must differ", USDValuationMethodCoinGeckoBTCCross)
	}
	if input.QuoteCurrency != "USD" {
		return fmt.Errorf("%s rate snapshot quote currency must be USD", USDValuationMethodCoinGeckoBTCCross)
	}

	inputs, err := loadDerivedRateSnapshotInputs(ctx, tx, *input.NumeratorSnapshotID, *input.DenominatorSnapshotID)
	if err != nil {
		return err
	}
	numerator := inputs[*input.NumeratorSnapshotID]
	denominator := inputs[*input.DenominatorSnapshotID]
	return validateDerivedRateSnapshotLegs(input, numerator, denominator)
}

func loadDerivedRateSnapshotInputs(ctx context.Context, tx *sql.Tx, numeratorID, denominatorID int64) (map[int64]RateSnapshotRecord, error) {
	ids := []int64{numeratorID, denominatorID}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	inputs := make(map[int64]RateSnapshotRecord, len(ids))
	for _, id := range ids {
		snapshot, err := scanRateSnapshot(tx.QueryRowContext(ctx, selectRateSnapshotByIDForUpdateSQL, id), true)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("derived rate snapshot input %d does not exist", id)
			}
			return nil, fmt.Errorf("lock derived rate snapshot input %d: %w", id, err)
		}
		inputs[id] = snapshot
	}
	return inputs, nil
}

func validateDerivedRateSnapshotLegs(input RateSnapshotInput, numerator, denominator RateSnapshotRecord) error {
	if numerator.Provider != input.Provider || denominator.Provider != input.Provider {
		return fmt.Errorf("%s inputs must use snapshot provider %q", USDValuationMethodCoinGeckoBTCCross, input.Provider)
	}
	if !strings.EqualFold(numerator.ProviderAssetID, rateSnapshotBTCProviderAssetID) ||
		!strings.EqualFold(denominator.ProviderAssetID, rateSnapshotBTCProviderAssetID) {
		return fmt.Errorf("%s inputs must use Bitcoin provider asset ID", USDValuationMethodCoinGeckoBTCCross)
	}
	if numerator.QuoteCurrency != "USD" || denominator.QuoteCurrency == "USD" || denominator.QuoteCurrency != input.BaseCurrency {
		return fmt.Errorf("%s inputs must be bitcoin/USD and bitcoin/%s", USDValuationMethodCoinGeckoBTCCross, input.BaseCurrency)
	}
	if numerator.PolicyVersion != input.PolicyVersion || denominator.PolicyVersion != input.PolicyVersion ||
		numerator.Granularity != input.Granularity || denominator.Granularity != input.Granularity ||
		!numerator.BucketStart.Equal(input.BucketStart) || !denominator.BucketStart.Equal(input.BucketStart) {
		return fmt.Errorf("%s inputs do not share provider policy, granularity and normalized bucket", USDValuationMethodCoinGeckoBTCCross)
	}
	if !numerator.IsEligibleLeaf || !denominator.IsEligibleLeaf {
		return fmt.Errorf("%s inputs must be current eligible leaves", USDValuationMethodCoinGeckoBTCCross)
	}
	if storedRateSnapshotPriceKind(numerator) != input.PriceKind || storedRateSnapshotPriceKind(denominator) != input.PriceKind {
		return fmt.Errorf("%s inputs must both be %s observations", USDValuationMethodCoinGeckoBTCCross, input.PriceKind)
	}
	if input.PriceKind == MarketPriceKindCurrent {
		if numerator.SnapshotGroupID == "" || denominator.SnapshotGroupID == "" || numerator.SnapshotGroupID != denominator.SnapshotGroupID || input.SnapshotGroupID != numerator.SnapshotGroupID {
			return fmt.Errorf("current %s inputs and result must share one non-empty response group", USDValuationMethodCoinGeckoBTCCross)
		}
	} else {
		if err := validateHistoricalDerivedRateSnapshotLeg(input, numerator); err != nil {
			return fmt.Errorf("numerator: %w", err)
		}
		if err := validateHistoricalDerivedRateSnapshotLeg(input, denominator); err != nil {
			return fmt.Errorf("denominator: %w", err)
		}
	}
	if input.IsFinal && (!numerator.IsFinal || !denominator.IsFinal) {
		return fmt.Errorf("final %s rate snapshot requires final numerator and denominator", USDValuationMethodCoinGeckoBTCCross)
	}

	expectedRate, err := decimalDivideBank(numerator.Rate, denominator.Rate)
	if err != nil {
		return fmt.Errorf("derive %s rate: %w", USDValuationMethodCoinGeckoBTCCross, err)
	}
	if !input.Rate.Equal(expectedRate) {
		return fmt.Errorf("%s rate %s does not equal exact numerator/denominator result %s", USDValuationMethodCoinGeckoBTCCross, input.Rate, expectedRate)
	}
	if numerator.EffectiveAt == nil || denominator.EffectiveAt == nil || input.EffectiveAt == nil {
		return fmt.Errorf("%s requires effective times for both inputs and result", USDValuationMethodCoinGeckoBTCCross)
	}
	expectedEffectiveAt := laterTime(*numerator.EffectiveAt, *denominator.EffectiveAt)
	if !input.EffectiveAt.Equal(expectedEffectiveAt) {
		return fmt.Errorf("%s effective time must be the later input effective time", USDValuationMethodCoinGeckoBTCCross)
	}
	expectedAvailableAt := laterTime(numerator.AvailableAt, denominator.AvailableAt)
	if !input.AvailableAt.Equal(expectedAvailableAt) {
		return fmt.Errorf("%s available time must be the later input available time", USDValuationMethodCoinGeckoBTCCross)
	}
	return nil
}

func validateHistoricalDerivedRateSnapshotLeg(input RateSnapshotInput, leg RateSnapshotRecord) error {
	if input.DerivedTargetAt == nil || input.DerivedTargetAt.IsZero() || input.HistoricalMaxGap < 0 {
		return fmt.Errorf("historical %s requires valuation target and non-negative maximum gap", USDValuationMethodCoinGeckoBTCCross)
	}
	if leg.EffectiveAt == nil || leg.EffectiveAt.IsZero() || leg.EffectiveAt.After(*input.DerivedTargetAt) {
		return fmt.Errorf("input effective time is missing or after valuation target")
	}
	if input.DerivedTargetAt.Sub(*leg.EffectiveAt) > input.HistoricalMaxGap {
		return fmt.Errorf("input effective time exceeds historical maximum gap")
	}
	cutoff := *input.DerivedTargetAt
	if input.AvailableAtCutoffAt != nil {
		cutoff = *input.AvailableAtCutoffAt
	}
	if leg.AvailableAt.After(cutoff) {
		return fmt.Errorf("input available time is after cutoff")
	}
	return nil
}

func storedRateSnapshotPriceKind(snapshot RateSnapshotRecord) MarketPriceKind {
	if kind, ok := marketPriceKindFromRequestKind(snapshot.OriginatingRequestKind); ok {
		return kind
	}
	if snapshot.Granularity == string(MarketPriceKindCurrent) {
		return MarketPriceKindCurrent
	}
	if snapshot.EffectiveAt == nil {
		return MarketPriceKindCurrent
	}
	return MarketPriceKindHistorical
}

func marketPriceKindFromRequestKind(requestKind string) (MarketPriceKind, bool) {
	switch requestKind {
	case string(MarketPriceKindCurrent):
		return MarketPriceKindCurrent, true
	case string(MarketPriceKindHistorical):
		return MarketPriceKindHistorical, true
	default:
		return "", false
	}
}
