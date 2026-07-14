package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppendRateSnapshot appends an immutable observation under a fixed dependency
// graph lock and then its exact revision-series lock. The graph lock keeps a
// correction's descendant retirement from racing a newly appended derived leaf.
// It deliberately performs no network work and never updates a business
// observation.
func (r *DBRepository) AppendRateSnapshot(ctx context.Context, input RateSnapshotInput) (RateSnapshotAppendResult, error) {
	if err := input.validate(); err != nil {
		return RateSnapshotAppendResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return RateSnapshotAppendResult{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RateSnapshotAppendResult{}, fmt.Errorf("begin rate snapshot append: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, rateSnapshotDependencyGraphAdvisoryLockSQL); err != nil {
		return RateSnapshotAppendResult{}, fmt.Errorf("lock rate snapshot dependency graph: %w", err)
	}
	if _, err := tx.ExecContext(ctx, rateSnapshotSeriesAdvisoryLockSQL, input.seriesKey()); err != nil {
		return RateSnapshotAppendResult{}, fmt.Errorf("lock rate snapshot series: %w", err)
	}
	if err := verifyRateSnapshotProvenance(ctx, tx, input); err != nil {
		return RateSnapshotAppendResult{}, err
	}

	existing, err := scanRateSnapshot(tx.QueryRowContext(ctx, selectRateSnapshotBySourceDigestSQL, input.seriesArgs(input.SourcePayloadDigest)...), true)
	if err == nil {
		if conflictField := immutableRateSnapshotConflict(existing, input); conflictField != "" {
			return RateSnapshotAppendResult{}, fmt.Errorf("rate snapshot source digest conflicts on immutable field %s", conflictField)
		}
		if err := tx.Commit(); err != nil {
			return RateSnapshotAppendResult{}, fmt.Errorf("commit rate snapshot digest readback: %w", err)
		}
		committed = true
		return RateSnapshotAppendResult{Snapshot: existing}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RateSnapshotAppendResult{}, fmt.Errorf("read rate snapshot by source digest: %w", err)
	}

	if input.Method == string(USDValuationMethodCoinGeckoBTCCross) {
		if err := validateDerivedRateSnapshot(ctx, tx, input); err != nil {
			return RateSnapshotAppendResult{}, err
		}
	}

	leaf, err := scanRateSnapshot(tx.QueryRowContext(ctx, selectEligibleRateSnapshotLeafSQL, input.seriesArgs()...), true)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RateSnapshotAppendResult{}, fmt.Errorf("read eligible rate snapshot leaf: %w", err)
	}

	internalRevision := 1
	var supersedesSnapshotID *int64
	if err == nil {
		internalRevision = leaf.InternalRevision + 1
		supersedesSnapshotID = &leaf.ID
		var retiredID int64
		if err := tx.QueryRowContext(ctx, retireEligibleRateSnapshotLeafSQL, leaf.ID).Scan(&retiredID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return RateSnapshotAppendResult{}, fmt.Errorf("eligible rate snapshot leaf %d changed while series was locked", leaf.ID)
			}
			return RateSnapshotAppendResult{}, fmt.Errorf("retire eligible rate snapshot leaf: %w", err)
		}
		if _, err := tx.ExecContext(ctx, retireDependentRateSnapshotLeavesSQL, leaf.ID); err != nil {
			return RateSnapshotAppendResult{}, fmt.Errorf("retire dependent derived rate snapshot leaves: %w", err)
		}
	}

	snapshot, err := scanRateSnapshot(tx.QueryRowContext(ctx, insertRateSnapshotSQL,
		input.Provider,
		input.AssetIdentityKey,
		nullableString(input.ProviderAssetID),
		nullableString(input.ProviderPlatformID),
		nullableString(input.AssetContract),
		input.BaseCurrency,
		input.QuoteCurrency,
		input.Rate.String(),
		input.Method,
		input.Granularity,
		input.BucketStart,
		nullableTime(input.EffectiveAt),
		input.AvailableAt,
		input.FetchedAt,
		nullableTime(input.CutoffAt),
		nullableString(input.SnapshotGroupID),
		input.PolicyVersion,
		nullableString(input.ProviderRevision),
		internalRevision,
		nullableInt64(supersedesSnapshotID),
		nullableInt64(input.NumeratorSnapshotID),
		nullableInt64(input.DenominatorSnapshotID),
		nullableInt64(input.SourceProviderFactID),
		input.SourcePayloadDigest,
		input.IsFinal,
		nullableInt64(input.OriginatingRateRequestID),
	), false)
	if err != nil {
		return RateSnapshotAppendResult{}, fmt.Errorf("append immutable rate snapshot: %w", err)
	}
	snapshot.OriginatingRequestKind = string(input.PriceKind)

	if err := tx.Commit(); err != nil {
		return RateSnapshotAppendResult{}, fmt.Errorf("commit rate snapshot append: %w", err)
	}
	committed = true
	return RateSnapshotAppendResult{Snapshot: snapshot, Inserted: true}, nil
}

func verifyRateSnapshotProvenance(ctx context.Context, tx *sql.Tx, input RateSnapshotInput) error {
	if input.OriginatingRateRequestID != nil {
		var requestProvider string
		var requestKind string
		var normalizedBucketStart sql.NullTime
		if err := tx.QueryRowContext(ctx, selectRateSnapshotRequestProvenanceSQL, *input.OriginatingRateRequestID).Scan(&requestProvider, &requestKind, &normalizedBucketStart); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("originating rate request %d does not exist", *input.OriginatingRateRequestID)
			}
			return fmt.Errorf("read originating rate request provenance: %w", err)
		}
		if requestProvider != input.Provider {
			return fmt.Errorf("originating rate request provider %q does not match snapshot provider %q", requestProvider, input.Provider)
		}
		priceKind, err := rateSnapshotPriceKindForRequest(requestKind, normalizedBucketStart)
		if err != nil {
			return err
		}
		if priceKind != input.PriceKind {
			return fmt.Errorf("originating rate request kind %q does not match snapshot price kind %q", requestKind, input.PriceKind)
		}
	}
	if input.SourceProviderFactID != nil {
		var factChannel string
		var factDigest string
		if err := tx.QueryRowContext(ctx, selectRateSnapshotFactProvenanceSQL, *input.SourceProviderFactID).Scan(&factChannel, &factDigest); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("source provider fact %d does not exist", *input.SourceProviderFactID)
			}
			return fmt.Errorf("read source provider fact provenance: %w", err)
		}
		if !strings.EqualFold(factChannel, input.Provider) {
			return fmt.Errorf("source provider fact channel %q does not match snapshot provider %q", factChannel, input.Provider)
		}
		if factDigest != input.SourcePayloadDigest {
			return fmt.Errorf("source provider fact payload digest does not match rate snapshot")
		}
	}
	return nil
}

func rateSnapshotPriceKindForRequest(requestKind string, normalizedBucketStart sql.NullTime) (MarketPriceKind, error) {
	switch requestKind {
	case string(MarketPriceKindCurrent), "CONTRACT_CHECK":
		return MarketPriceKindCurrent, nil
	case string(MarketPriceKindHistorical):
		return MarketPriceKindHistorical, nil
	case "RETRY":
		if normalizedBucketStart.Valid {
			return MarketPriceKindHistorical, nil
		}
		return MarketPriceKindCurrent, nil
	default:
		return "", fmt.Errorf("unsupported originating rate request kind %q", requestKind)
	}
}

func immutableRateSnapshotConflict(existing RateSnapshotRecord, input RateSnapshotInput) string {
	checks := []struct {
		field string
		equal bool
	}{
		{"provider", existing.Provider == input.Provider},
		{"asset_identity_key", existing.AssetIdentityKey == input.AssetIdentityKey},
		{"provider_asset_id", existing.ProviderAssetID == input.ProviderAssetID},
		{"provider_platform_id", existing.ProviderPlatformID == input.ProviderPlatformID},
		{"asset_contract", existing.AssetContract == input.AssetContract},
		{"base_currency", existing.BaseCurrency == input.BaseCurrency},
		{"quote_currency", existing.QuoteCurrency == input.QuoteCurrency},
		{"rate", existing.Rate.Equal(input.Rate)},
		{"method", existing.Method == input.Method},
		{"granularity", existing.Granularity == input.Granularity},
		{"bucket_start", existing.BucketStart.Equal(input.BucketStart)},
		{"effective_at", equalRateSnapshotTime(existing.EffectiveAt, input.EffectiveAt)},
		{"available_at", existing.AvailableAt.Equal(input.AvailableAt)},
		{"fetched_at", existing.FetchedAt.Equal(input.FetchedAt)},
		{"cutoff_at", equalRateSnapshotTime(existing.CutoffAt, input.CutoffAt)},
		{"snapshot_group_id", existing.SnapshotGroupID == input.SnapshotGroupID},
		{"policy_version", existing.PolicyVersion == input.PolicyVersion},
		{"provider_revision", existing.ProviderRevision == input.ProviderRevision},
		{"source_provider_fact_id", equalRateSnapshotID(existing.SourceProviderFactID, input.SourceProviderFactID)},
		{"source_payload_digest", existing.SourcePayloadDigest == input.SourcePayloadDigest},
		{"is_final", existing.IsFinal == input.IsFinal},
		{"numerator_snapshot_id", equalRateSnapshotID(existing.NumeratorSnapshotID, input.NumeratorSnapshotID)},
		{"denominator_snapshot_id", equalRateSnapshotID(existing.DenominatorSnapshotID, input.DenominatorSnapshotID)},
		{"originating_rate_request_id", equalRateSnapshotID(existing.OriginatingRateRequestID, input.OriginatingRateRequestID)},
		{"price_kind", storedRateSnapshotPriceKind(existing) == input.PriceKind},
	}
	for _, check := range checks {
		if !check.equal {
			return check.field
		}
	}
	return ""
}

func equalRateSnapshotTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func equalRateSnapshotID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
