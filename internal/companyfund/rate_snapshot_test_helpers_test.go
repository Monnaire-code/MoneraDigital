package companyfund

import (
	"database/sql/driver"
	"regexp"
	"strings"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func expectRateSnapshotAppendLocks(mock sqlmock.Sqlmock, input RateSnapshotInput) {
	mock.ExpectExec(regexp.QuoteMeta(rateSnapshotDependencyGraphAdvisoryLockSQL)).
		WithoutArgs().WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(rateSnapshotSeriesAdvisoryLockSQL)).
		WithArgs(input.seriesKey()).WillReturnResult(sqlmock.NewResult(0, 1))
}

func newRateSnapshotInput() RateSnapshotInput {
	bucket := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	effectiveAt := bucket.Add(20 * time.Second)
	availableAt := bucket.Add(30 * time.Second)
	requestID := int64(91)
	return RateSnapshotInput{
		Provider:                 "COINGECKO",
		AssetIdentityKey:         "crypto:bitcoin",
		ProviderAssetID:          "bitcoin",
		BaseCurrency:             "BTC",
		QuoteCurrency:            "USD",
		Rate:                     decimal.RequireFromString("60000.123456789012345678"),
		Method:                   string(USDValuationMethodCoinGeckoDirect),
		Granularity:              "HOUR",
		BucketStart:              bucket,
		EffectiveAt:              &effectiveAt,
		AvailableAt:              availableAt,
		FetchedAt:                availableAt.Add(time.Second),
		SnapshotGroupID:          "current-group-1",
		PolicyVersion:            "valuation-v1",
		ProviderRevision:         "source-v5",
		SourcePayloadDigest:      strings.Repeat("a", 64),
		OriginatingRateRequestID: &requestID,
		IsFinal:                  true,
		PriceKind:                MarketPriceKindCurrent,
	}
}

func newHistoricalBTCCrossRateSnapshot() (RateSnapshotInput, RateSnapshotRecord, RateSnapshotRecord) {
	target := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	bucket := target.Truncate(time.Hour)
	numeratorEffective := target.Add(-time.Minute)
	denominatorEffective := target.Add(-2 * time.Minute)
	numeratorAvailable := target.Add(-50 * time.Second)
	denominatorAvailable := target.Add(-90 * time.Second)
	numeratorID := int64(101)
	denominatorID := int64(202)
	requestID := int64(303)

	numerator := RateSnapshotRecord{
		ID:                     numeratorID,
		Provider:               "COINGECKO",
		AssetIdentityKey:       "crypto:bitcoin",
		ProviderAssetID:        "bitcoin",
		BaseCurrency:           "BTC",
		QuoteCurrency:          "USD",
		Rate:                   decimal.RequireFromString("60000"),
		Method:                 string(USDValuationMethodCoinGeckoDirect),
		Granularity:            "HOUR",
		BucketStart:            bucket,
		EffectiveAt:            &numeratorEffective,
		AvailableAt:            numeratorAvailable,
		FetchedAt:              numeratorAvailable,
		PolicyVersion:          "valuation-v1",
		InternalRevision:       1,
		SourcePayloadDigest:    strings.Repeat("b", 64),
		IsEligibleLeaf:         true,
		IsFinal:                true,
		OriginatingRequestKind: "HISTORICAL",
	}
	denominator := numerator
	denominator.ID = denominatorID
	denominator.QuoteCurrency = "JPY"
	denominator.Rate = decimal.RequireFromString("9000000")
	denominator.EffectiveAt = &denominatorEffective
	denominator.AvailableAt = denominatorAvailable
	denominator.FetchedAt = denominatorAvailable
	denominator.SourcePayloadDigest = strings.Repeat("c", 64)

	input := RateSnapshotInput{
		Provider:                 "COINGECKO",
		AssetIdentityKey:         "fiat:JPY",
		ProviderAssetID:          "japanese-yen",
		BaseCurrency:             "JPY",
		QuoteCurrency:            "USD",
		Rate:                     decimal.RequireFromString("0.006666666666666667"),
		Method:                   string(USDValuationMethodCoinGeckoBTCCross),
		Granularity:              "HOUR",
		BucketStart:              bucket,
		EffectiveAt:              &numeratorEffective,
		AvailableAt:              numeratorAvailable,
		FetchedAt:                target,
		PolicyVersion:            "valuation-v1",
		SourcePayloadDigest:      strings.Repeat("d", 64),
		OriginatingRateRequestID: &requestID,
		IsFinal:                  true,
		NumeratorSnapshotID:      &numeratorID,
		DenominatorSnapshotID:    &denominatorID,
		PriceKind:                MarketPriceKindHistorical,
		DerivedTargetAt:          &target,
		HistoricalMaxGap:         5 * time.Minute,
	}
	return input, numerator, denominator
}

func rateSnapshotRecordFromInput(input RateSnapshotInput, id int64, revision int, supersedes *int64) RateSnapshotRecord {
	return RateSnapshotRecord{
		ID:                       id,
		Provider:                 input.Provider,
		AssetIdentityKey:         input.AssetIdentityKey,
		ProviderAssetID:          input.ProviderAssetID,
		ProviderPlatformID:       input.ProviderPlatformID,
		AssetContract:            input.AssetContract,
		BaseCurrency:             input.BaseCurrency,
		QuoteCurrency:            input.QuoteCurrency,
		Rate:                     input.Rate,
		Method:                   input.Method,
		Granularity:              input.Granularity,
		BucketStart:              input.BucketStart,
		EffectiveAt:              input.EffectiveAt,
		AvailableAt:              input.AvailableAt,
		FetchedAt:                input.FetchedAt,
		CutoffAt:                 input.CutoffAt,
		SnapshotGroupID:          input.SnapshotGroupID,
		PolicyVersion:            input.PolicyVersion,
		ProviderRevision:         input.ProviderRevision,
		InternalRevision:         revision,
		SupersedesSnapshotID:     supersedes,
		NumeratorSnapshotID:      input.NumeratorSnapshotID,
		DenominatorSnapshotID:    input.DenominatorSnapshotID,
		SourceProviderFactID:     input.SourceProviderFactID,
		SourcePayloadDigest:      input.SourcePayloadDigest,
		IsEligibleLeaf:           true,
		IsFinal:                  input.IsFinal,
		OriginatingRateRequestID: input.OriginatingRateRequestID,
		OriginatingRequestKind:   string(input.PriceKind),
	}
}

func rateSnapshotColumnNames(includeRequestKind bool) []string {
	columns := []string{
		"id", "provider", "asset_identity_key", "provider_asset_id", "provider_platform_id", "asset_contract",
		"base_currency", "quote_currency", "rate", "method", "granularity", "bucket_start", "effective_at",
		"available_at", "fetched_at", "cutoff_at", "snapshot_group_id", "policy_version", "provider_revision",
		"internal_revision", "supersedes_snapshot_id", "numerator_snapshot_id", "denominator_snapshot_id",
		"source_provider_fact_id", "source_payload_digest", "is_eligible_leaf", "is_final", "originating_rate_request_id",
	}
	if includeRequestKind {
		columns = append(columns, "price_kind")
	}
	return columns
}

func rateSnapshotRows(snapshot RateSnapshotRecord, includeRequestKind bool) *sqlmock.Rows {
	values := []driver.Value{
		snapshot.ID,
		snapshot.Provider,
		snapshot.AssetIdentityKey,
		snapshot.ProviderAssetID,
		snapshot.ProviderPlatformID,
		snapshot.AssetContract,
		snapshot.BaseCurrency,
		snapshot.QuoteCurrency,
		snapshot.Rate.String(),
		snapshot.Method,
		snapshot.Granularity,
		snapshot.BucketStart,
		rateSnapshotNullableTime(snapshot.EffectiveAt),
		snapshot.AvailableAt,
		snapshot.FetchedAt,
		rateSnapshotNullableTime(snapshot.CutoffAt),
		snapshot.SnapshotGroupID,
		snapshot.PolicyVersion,
		snapshot.ProviderRevision,
		snapshot.InternalRevision,
		rateSnapshotNullableID(snapshot.SupersedesSnapshotID),
		rateSnapshotNullableID(snapshot.NumeratorSnapshotID),
		rateSnapshotNullableID(snapshot.DenominatorSnapshotID),
		rateSnapshotNullableID(snapshot.SourceProviderFactID),
		snapshot.SourcePayloadDigest,
		snapshot.IsEligibleLeaf,
		snapshot.IsFinal,
		rateSnapshotNullableID(snapshot.OriginatingRateRequestID),
	}
	if includeRequestKind {
		values = append(values, snapshot.OriginatingRequestKind)
	}
	return sqlmock.NewRows(rateSnapshotColumnNames(includeRequestKind)).AddRow(values...)
}

func rateSnapshotRequestProvenanceRows(provider, kind string, normalizedBucketStart *time.Time) *sqlmock.Rows {
	var bucket driver.Value
	if normalizedBucketStart != nil {
		bucket = *normalizedBucketStart
	}
	return sqlmock.NewRows([]string{"provider", "request_kind", "normalized_bucket_start"}).
		AddRow(provider, kind, bucket)
}

func rateSnapshotSeriesArgs(input RateSnapshotInput, extra ...driver.Value) []driver.Value {
	args := []driver.Value{
		input.Provider,
		input.AssetIdentityKey,
		input.QuoteCurrency,
		input.Method,
		input.Granularity,
		input.BucketStart,
		input.PolicyVersion,
	}
	return append(args, extra...)
}

func rateSnapshotInsertArgs(input RateSnapshotInput, revision int, supersedes *int64) []driver.Value {
	return []driver.Value{
		input.Provider,
		input.AssetIdentityKey,
		rateSnapshotNullableString(input.ProviderAssetID),
		rateSnapshotNullableString(input.ProviderPlatformID),
		rateSnapshotNullableString(input.AssetContract),
		input.BaseCurrency,
		input.QuoteCurrency,
		input.Rate.String(),
		input.Method,
		input.Granularity,
		input.BucketStart,
		rateSnapshotNullableTime(input.EffectiveAt),
		input.AvailableAt,
		input.FetchedAt,
		rateSnapshotNullableTime(input.CutoffAt),
		rateSnapshotNullableString(input.SnapshotGroupID),
		input.PolicyVersion,
		rateSnapshotNullableString(input.ProviderRevision),
		revision,
		rateSnapshotNullableID(supersedes),
		rateSnapshotNullableID(input.NumeratorSnapshotID),
		rateSnapshotNullableID(input.DenominatorSnapshotID),
		rateSnapshotNullableID(input.SourceProviderFactID),
		input.SourcePayloadDigest,
		input.IsFinal,
		rateSnapshotNullableID(input.OriginatingRateRequestID),
	}
}

func rateSnapshotNullableString(value string) driver.Value {
	if value == "" {
		return nil
	}
	return value
}

func rateSnapshotNullableTime(value *time.Time) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func rateSnapshotNullableID(value *int64) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func rateSnapshotIDPointer(value int64) *int64 {
	return &value
}
