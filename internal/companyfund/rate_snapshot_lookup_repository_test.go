package companyfund

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestFindLatestUsableRateSnapshot_UsesExactContractAndMissingStaysNil(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateSnapshotInput()
	lookup := RateSnapshotLookup{
		Provider:           input.Provider,
		AssetIdentityKey:   input.AssetIdentityKey,
		ProviderAssetID:    input.ProviderAssetID,
		ProviderPlatformID: input.ProviderPlatformID,
		AssetContract:      input.AssetContract,
		BaseCurrency:       input.BaseCurrency,
		QuoteCurrency:      input.QuoteCurrency,
		Method:             input.Method,
		Granularity:        input.Granularity,
		PolicyVersion:      input.PolicyVersion,
		AsOf:               input.AvailableAt.Add(time.Minute),
		MaxGap:             10 * time.Minute,
	}
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestUsableRateSnapshotSQL)).
		WithArgs(
			lookup.Provider,
			lookup.AssetIdentityKey,
			lookup.ProviderAssetID,
			nil,
			nil,
			lookup.BaseCurrency,
			lookup.QuoteCurrency,
			lookup.Method,
			lookup.Granularity,
			lookup.PolicyVersion,
			lookup.AsOf,
			lookup.AsOf,
			lookup.MaxGap.Microseconds(),
		).
		WillReturnRows(sqlmock.NewRows(rateSnapshotColumnNames(false)))

	snapshot, err := NewDBRepository(db).FindLatestUsableRateSnapshot(context.Background(), lookup)
	if err != nil {
		t.Fatalf("FindLatestUsableRateSnapshot() error = %v", err)
	}
	if snapshot != nil {
		t.Fatalf("missing rate must stay nil, got %#v", snapshot)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestFindLatestUsableRateSnapshot_AllowsHistoricalArrivalAfterTargetBeforeCutoff(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateSnapshotInput()
	target := input.BucketStart.Add(10 * time.Minute)
	priceAt := target.Add(-time.Minute)
	availableAt := target.Add(30 * time.Second)
	cutoff := target.Add(time.Minute)
	record := rateSnapshotRecordFromInput(input, 77, 1, nil)
	record.EffectiveAt = &priceAt
	record.AvailableAt = availableAt
	record.FetchedAt = availableAt
	lookup := RateSnapshotLookup{
		Provider:            input.Provider,
		AssetIdentityKey:    input.AssetIdentityKey,
		ProviderAssetID:     input.ProviderAssetID,
		BaseCurrency:        input.BaseCurrency,
		QuoteCurrency:       input.QuoteCurrency,
		Method:              input.Method,
		Granularity:         input.Granularity,
		PolicyVersion:       input.PolicyVersion,
		AsOf:                target,
		AvailableAtCutoffAt: &cutoff,
		MaxGap:              5 * time.Minute,
	}
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestUsableRateSnapshotSQL)).
		WithArgs(
			lookup.Provider,
			lookup.AssetIdentityKey,
			lookup.ProviderAssetID,
			nil,
			nil,
			lookup.BaseCurrency,
			lookup.QuoteCurrency,
			lookup.Method,
			lookup.Granularity,
			lookup.PolicyVersion,
			lookup.AsOf,
			cutoff,
			lookup.MaxGap.Microseconds(),
		).
		WillReturnRows(rateSnapshotRows(record, false))

	snapshot, err := NewDBRepository(db).FindLatestUsableRateSnapshot(context.Background(), lookup)
	if err != nil {
		t.Fatalf("FindLatestUsableRateSnapshot() error = %v", err)
	}
	if snapshot == nil || !snapshot.AvailableAt.After(target) || snapshot.AvailableAt.After(cutoff) {
		t.Fatalf("historical rate available after target but before cutoff must remain usable: %#v", snapshot)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestFindLatestUsableRateSnapshot_RejectsZeroAvailableAtCutoff(t *testing.T) {
	input := newRateSnapshotInput()
	zero := time.Time{}
	lookup := RateSnapshotLookup{
		Provider:            input.Provider,
		AssetIdentityKey:    input.AssetIdentityKey,
		BaseCurrency:        input.BaseCurrency,
		QuoteCurrency:       input.QuoteCurrency,
		Method:              input.Method,
		Granularity:         input.Granularity,
		PolicyVersion:       input.PolicyVersion,
		AsOf:                input.AvailableAt,
		AvailableAtCutoffAt: &zero,
		MaxGap:              time.Minute,
	}
	if _, err := NewDBRepository(nil).FindLatestUsableRateSnapshot(context.Background(), lookup); err == nil {
		t.Fatal("zero available-at cutoff must fail before database access")
	}
}

func TestRateSnapshotRepositorySQL_DoesNotTouchPayloadOrProcessStatus(t *testing.T) {
	allSQL := strings.ToLower(strings.Join([]string{
		rateSnapshotDependencyGraphAdvisoryLockSQL,
		rateSnapshotSeriesAdvisoryLockSQL,
		selectRateSnapshotRequestProvenanceSQL,
		selectRateSnapshotFactProvenanceSQL,
		selectRateSnapshotBySourceDigestSQL,
		selectEligibleRateSnapshotLeafSQL,
		selectRateSnapshotByIDForUpdateSQL,
		retireEligibleRateSnapshotLeafSQL,
		retireDependentRateSnapshotLeavesSQL,
		insertRateSnapshotSQL,
		selectLatestUsableRateSnapshotSQL,
	}, "\n"))
	for _, forbidden := range []string{"raw", "process_status", "owned_payload"} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("rate snapshot SQL must not access %q: %s", forbidden, allSQL)
		}
	}
	for _, required := range []string{
		"pg_advisory_xact_lock",
		"company-fund-rate-snapshot-dependency-graph-v1",
		"is_eligible_leaf = false",
		"with recursive dependent_snapshot_ids",
		"union\n\tselect child.id\n\tfrom dependent_snapshot_ids as dependency",
		"order by child.id\n\tfor update of child",
		"numerator.is_eligible_leaf = true",
		"denominator.is_eligible_leaf = true",
		"source_payload_digest",
		"normalized_bucket_start",
		"when request.request_kind = 'retry' and request.normalized_bucket_start is null then 'current'",
		"is not distinct from",
		"snapshot.available_at <= $12",
		"coalesce(snapshot.effective_at, snapshot.bucket_start) <= $11",
	} {
		if !strings.Contains(allSQL, required) {
			t.Fatalf("rate snapshot SQL is missing required contract %q", required)
		}
	}
	for _, forbidden := range []string{
		"snapshot.available_at <= $11",
		"coalesce(snapshot.effective_at, snapshot.bucket_start) <= $12",
		"union all\n\tselect child.id",
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("rate snapshot SQL must keep time/dependency invariant %q out", forbidden)
		}
	}
}
