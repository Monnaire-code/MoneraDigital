package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// FindLatestUsableRateSnapshot returns nil, nil when no exact eligible leaf is
// usable at the caller's valuation time. It never manufactures a zero rate for
// a missing, future or stale observation.
func (r *DBRepository) FindLatestUsableRateSnapshot(ctx context.Context, lookup RateSnapshotLookup) (*RateSnapshotRecord, error) {
	if err := lookup.validate(); err != nil {
		return nil, err
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}

	snapshot, err := scanRateSnapshot(r.db.QueryRowContext(ctx, selectLatestUsableRateSnapshotSQL,
		lookup.Provider,
		lookup.AssetIdentityKey,
		nullableString(lookup.ProviderAssetID),
		nullableString(lookup.ProviderPlatformID),
		nullableString(lookup.AssetContract),
		lookup.BaseCurrency,
		lookup.QuoteCurrency,
		lookup.Method,
		lookup.Granularity,
		lookup.PolicyVersion,
		lookup.AsOf,
		lookup.availableAtCutoff(),
		lookup.MaxGap.Microseconds(),
	), false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find latest usable rate snapshot: %w", err)
	}
	return &snapshot, nil
}

func (lookup RateSnapshotLookup) validate() error {
	for _, field := range []struct {
		label string
		value string
		max   int
	}{
		{"rate lookup provider", lookup.Provider, maxRateSnapshotProviderBytes},
		{"rate lookup asset identity key", lookup.AssetIdentityKey, maxRateSnapshotAssetIdentityKeyBytes},
		{"rate lookup base currency", lookup.BaseCurrency, maxRateSnapshotCurrencyBytes},
		{"rate lookup quote currency", lookup.QuoteCurrency, maxRateSnapshotCurrencyBytes},
		{"rate lookup method", lookup.Method, maxRateSnapshotMethodBytes},
		{"rate lookup granularity", lookup.Granularity, maxRateSnapshotGranularityBytes},
		{"rate lookup policy version", lookup.PolicyVersion, maxRateSnapshotPolicyVersionBytes},
	} {
		if err := validateRateSnapshotString(field.label, field.value, field.max, true); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		label string
		value string
		max   int
	}{
		{"rate lookup provider asset ID", lookup.ProviderAssetID, maxRateSnapshotProviderAssetIDBytes},
		{"rate lookup provider platform ID", lookup.ProviderPlatformID, maxRateSnapshotProviderAssetIDBytes},
		{"rate lookup asset contract", lookup.AssetContract, maxRateSnapshotProviderAssetIDBytes},
	} {
		if err := validateRateSnapshotString(field.label, field.value, field.max, false); err != nil {
			return err
		}
	}
	if lookup.AsOf.IsZero() || lookup.MaxGap < 0 {
		return fmt.Errorf("rate lookup requires an as-of time and non-negative maximum gap")
	}
	if lookup.AvailableAtCutoffAt != nil && lookup.AvailableAtCutoffAt.IsZero() {
		return fmt.Errorf("rate lookup available-at cutoff cannot be zero")
	}
	return nil
}

func (lookup RateSnapshotLookup) availableAtCutoff() time.Time {
	if lookup.AvailableAtCutoffAt != nil {
		return *lookup.AvailableAtCutoffAt
	}
	return lookup.AsOf
}

type rateSnapshotScanner interface {
	Scan(dest ...any) error
}

func scanRateSnapshot(row rateSnapshotScanner, includeRequestKind bool) (RateSnapshotRecord, error) {
	var snapshot RateSnapshotRecord
	var rateText string
	var effectiveAt sql.NullTime
	var cutoffAt sql.NullTime
	var supersedesSnapshotID sql.NullInt64
	var numeratorSnapshotID sql.NullInt64
	var denominatorSnapshotID sql.NullInt64
	var sourceProviderFactID sql.NullInt64
	var originatingRateRequestID sql.NullInt64
	dest := []any{
		&snapshot.ID,
		&snapshot.Provider,
		&snapshot.AssetIdentityKey,
		&snapshot.ProviderAssetID,
		&snapshot.ProviderPlatformID,
		&snapshot.AssetContract,
		&snapshot.BaseCurrency,
		&snapshot.QuoteCurrency,
		&rateText,
		&snapshot.Method,
		&snapshot.Granularity,
		&snapshot.BucketStart,
		&effectiveAt,
		&snapshot.AvailableAt,
		&snapshot.FetchedAt,
		&cutoffAt,
		&snapshot.SnapshotGroupID,
		&snapshot.PolicyVersion,
		&snapshot.ProviderRevision,
		&snapshot.InternalRevision,
		&supersedesSnapshotID,
		&numeratorSnapshotID,
		&denominatorSnapshotID,
		&sourceProviderFactID,
		&snapshot.SourcePayloadDigest,
		&snapshot.IsEligibleLeaf,
		&snapshot.IsFinal,
		&originatingRateRequestID,
	}
	if includeRequestKind {
		dest = append(dest, &snapshot.OriginatingRequestKind)
	}
	if err := row.Scan(dest...); err != nil {
		return RateSnapshotRecord{}, err
	}
	rate, err := decimal.NewFromString(rateText)
	if err != nil {
		return RateSnapshotRecord{}, fmt.Errorf("parse persisted rate snapshot decimal: %w", err)
	}
	if err := validateRateSnapshotDecimal(rate); err != nil {
		return RateSnapshotRecord{}, fmt.Errorf("invalid persisted rate snapshot: %w", err)
	}
	snapshot.Rate = rate
	snapshot.EffectiveAt = nullableRateSnapshotTime(effectiveAt)
	snapshot.CutoffAt = nullableRateSnapshotTime(cutoffAt)
	snapshot.SupersedesSnapshotID = nullableRateSnapshotID(supersedesSnapshotID)
	snapshot.NumeratorSnapshotID = nullableRateSnapshotID(numeratorSnapshotID)
	snapshot.DenominatorSnapshotID = nullableRateSnapshotID(denominatorSnapshotID)
	snapshot.SourceProviderFactID = nullableRateSnapshotID(sourceProviderFactID)
	snapshot.OriginatingRateRequestID = nullableRateSnapshotID(originatingRateRequestID)
	return snapshot, nil
}

func nullableRateSnapshotTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time
	return &copy
}

func nullableRateSnapshotID(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}
