package companyfund

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestAppendRateSnapshot_CorrectedObservationRetiresLeafAndAppendsRevision(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateSnapshotInput()
	previous := rateSnapshotRecordFromInput(input, 31, 4, nil)
	previous.SourcePayloadDigest = strings.Repeat("b", 64)
	previous.ProviderRevision = "source-v4"
	previousID := previous.ID
	inserted := rateSnapshotRecordFromInput(input, 32, 5, &previousID)

	mock.ExpectBegin()
	expectRateSnapshotAppendLocks(mock, input)
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotRequestProvenanceSQL)).
		WithArgs(*input.OriginatingRateRequestID).
		WillReturnRows(rateSnapshotRequestProvenanceRows(input.Provider, "CURRENT", nil))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotBySourceDigestSQL)).
		WithArgs(rateSnapshotSeriesArgs(input, input.SourcePayloadDigest)...).
		WillReturnRows(sqlmock.NewRows(rateSnapshotColumnNames(true)))
	mock.ExpectQuery(regexp.QuoteMeta(selectEligibleRateSnapshotLeafSQL)).
		WithArgs(rateSnapshotSeriesArgs(input)...).WillReturnRows(rateSnapshotRows(previous, true))
	mock.ExpectQuery(regexp.QuoteMeta(retireEligibleRateSnapshotLeafSQL)).
		WithArgs(previous.ID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(previous.ID))
	mock.ExpectExec(regexp.QuoteMeta(retireDependentRateSnapshotLeavesSQL)).
		WithArgs(previous.ID).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery(regexp.QuoteMeta(insertRateSnapshotSQL)).
		WithArgs(rateSnapshotInsertArgs(input, 5, &previousID)...).WillReturnRows(rateSnapshotRows(inserted, false))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input)
	if err != nil {
		t.Fatalf("AppendRateSnapshot() error = %v", err)
	}
	if !result.Inserted || result.Snapshot.ID != inserted.ID || result.Snapshot.InternalRevision != 5 {
		t.Fatalf("AppendRateSnapshot() = %#v, want appended revision 5", result)
	}
	if result.Snapshot.SupersedesSnapshotID == nil || *result.Snapshot.SupersedesSnapshotID != previous.ID {
		t.Fatalf("new revision must supersede the old exact-series leaf: %#v", result.Snapshot)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestAppendRateSnapshot_ExactDigestRetryReadsExistingImmutableRow(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateSnapshotInput()
	existing := rateSnapshotRecordFromInput(input, 41, 3, nil)
	existing.IsEligibleLeaf = false
	expectExactRateSnapshotDigestReadback(mock, input, existing, "CURRENT", nil)

	result, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input)
	if err != nil {
		t.Fatalf("AppendRateSnapshot() error = %v", err)
	}
	if result.Inserted || result.Snapshot.ID != existing.ID || result.Snapshot.InternalRevision != existing.InternalRevision {
		t.Fatalf("exact digest retry must read back, got %#v", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestAppendRateSnapshot_SameDigestRejectsDifferentImmutableObservation(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateSnapshotInput()
	existing := rateSnapshotRecordFromInput(input, 42, 1, nil)
	input.Rate = decimal.RequireFromString("60001.123456789012345678")

	mock.ExpectBegin()
	expectRateSnapshotAppendLocks(mock, input)
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotRequestProvenanceSQL)).
		WithArgs(*input.OriginatingRateRequestID).
		WillReturnRows(rateSnapshotRequestProvenanceRows(input.Provider, "CURRENT", nil))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotBySourceDigestSQL)).
		WithArgs(rateSnapshotSeriesArgs(input, input.SourcePayloadDigest)...).
		WillReturnRows(rateSnapshotRows(existing, true))
	mock.ExpectRollback()

	if _, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input); err == nil || !strings.Contains(err.Error(), "immutable field rate") {
		t.Fatalf("same digest with changed business fact must be rejected, got %v", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestAppendRateSnapshot_TakesDependencyGraphLockBeforeAnySeriesWork(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateSnapshotInput()
	graphLockFailure := errors.New("dependency graph lock unavailable")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(rateSnapshotDependencyGraphAdvisoryLockSQL)).
		WithoutArgs().WillReturnError(graphLockFailure)
	mock.ExpectRollback()

	if _, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input); !errors.Is(err, graphLockFailure) {
		t.Fatalf("AppendRateSnapshot() error = %v, want dependency graph lock failure", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestImmutableRateSnapshotConflict_CoversProvenanceFinalityAndDerivedLegs(t *testing.T) {
	input := newRateSnapshotInput()
	existing := rateSnapshotRecordFromInput(input, 43, 1, nil)
	for _, testCase := range []struct {
		name   string
		mutate func(*RateSnapshotInput)
		want   string
	}{
		{
			name:   "provider asset mapping",
			mutate: func(value *RateSnapshotInput) { value.ProviderAssetID = "wrapped-bitcoin" },
			want:   "provider_asset_id",
		},
		{
			name:   "originating request",
			mutate: func(value *RateSnapshotInput) { value.OriginatingRateRequestID = rateSnapshotIDPointer(92) },
			want:   "originating_rate_request_id",
		},
		{
			name:   "finality",
			mutate: func(value *RateSnapshotInput) { value.IsFinal = false },
			want:   "is_final",
		},
		{
			name:   "derived numerator",
			mutate: func(value *RateSnapshotInput) { value.NumeratorSnapshotID = rateSnapshotIDPointer(44) },
			want:   "numerator_snapshot_id",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			incoming := input
			testCase.mutate(&incoming)
			if actual := immutableRateSnapshotConflict(existing, incoming); actual != testCase.want {
				t.Fatalf("immutableRateSnapshotConflict() = %q, want %q", actual, testCase.want)
			}
		})
	}
}

func TestAppendRateSnapshot_RejectsSourceProviderMismatch(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateSnapshotInput()
	input.OriginatingRateRequestID = nil
	factID := int64(77)
	input.SourceProviderFactID = &factID
	mock.ExpectBegin()
	expectRateSnapshotAppendLocks(mock, input)
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotFactProvenanceSQL)).
		WithArgs(factID).
		WillReturnRows(sqlmock.NewRows([]string{"channel", "source_payload_digest"}).AddRow("SAFEHERON", input.SourcePayloadDigest))
	mock.ExpectRollback()

	if _, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input); err == nil || !strings.Contains(err.Error(), "does not match snapshot provider") {
		t.Fatalf("cross-provider source fact must be rejected, got %v", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestAppendRateSnapshot_RejectsOriginatingRequestProviderMismatch(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateSnapshotInput()
	mock.ExpectBegin()
	expectRateSnapshotAppendLocks(mock, input)
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotRequestProvenanceSQL)).
		WithArgs(*input.OriginatingRateRequestID).
		WillReturnRows(rateSnapshotRequestProvenanceRows("OTHER_PROVIDER", "CURRENT", nil))
	mock.ExpectRollback()

	if _, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input); err == nil || !strings.Contains(err.Error(), "originating rate request provider") {
		t.Fatalf("cross-provider originating request must be rejected, got %v", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestAppendRateSnapshot_RetryRequestKindUsesNormalizedBucket(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		priceKind    MarketPriceKind
		bucket       bool
		wantErr      bool
		existingKind string
	}{
		{name: "current retry has no bucket", priceKind: MarketPriceKindCurrent, existingKind: "CURRENT"},
		{name: "historical retry has bucket", priceKind: MarketPriceKindHistorical, bucket: true, existingKind: "HISTORICAL"},
		{name: "historical retry rejects caller current label", priceKind: MarketPriceKindCurrent, bucket: true, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()

			input := newRateSnapshotInput()
			input.PriceKind = testCase.priceKind
			var normalizedBucket *time.Time
			if testCase.bucket {
				normalizedBucket = &input.BucketStart
			}
			mock.ExpectBegin()
			expectRateSnapshotAppendLocks(mock, input)
			mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotRequestProvenanceSQL)).
				WithArgs(*input.OriginatingRateRequestID).
				WillReturnRows(rateSnapshotRequestProvenanceRows(input.Provider, "RETRY", normalizedBucket))
			if testCase.wantErr {
				mock.ExpectRollback()
			} else {
				existing := rateSnapshotRecordFromInput(input, 51, 1, nil)
				existing.OriginatingRequestKind = testCase.existingKind
				mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotBySourceDigestSQL)).
					WithArgs(rateSnapshotSeriesArgs(input, input.SourcePayloadDigest)...).
					WillReturnRows(rateSnapshotRows(existing, true))
				mock.ExpectCommit()
			}

			_, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input)
			if testCase.wantErr && err == nil {
				t.Fatal("bucketed retry must reject a caller-supplied CURRENT label")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("retry classification should read back existing snapshot: %v", err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestAppendRateSnapshot_RejectsNonPositiveOrInexactDecimal(t *testing.T) {
	for _, testCase := range []struct {
		name string
		rate decimal.Decimal
	}{
		{name: "zero", rate: decimal.Zero},
		{name: "negative", rate: decimal.NewFromInt(-1)},
		{name: "fractional precision", rate: decimal.RequireFromString("1.0000000000000000001")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := newRateSnapshotInput()
			input.Rate = testCase.rate
			if _, err := NewDBRepository(nil).AppendRateSnapshot(context.Background(), input); err == nil {
				t.Fatal("invalid rate must fail before database access")
			}
		})
	}
}

func expectExactRateSnapshotDigestReadback(mock sqlmock.Sqlmock, input RateSnapshotInput, existing RateSnapshotRecord, requestKind string, bucket *time.Time) {
	mock.ExpectBegin()
	expectRateSnapshotAppendLocks(mock, input)
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotRequestProvenanceSQL)).
		WithArgs(*input.OriginatingRateRequestID).
		WillReturnRows(rateSnapshotRequestProvenanceRows(input.Provider, requestKind, bucket))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotBySourceDigestSQL)).
		WithArgs(rateSnapshotSeriesArgs(input, input.SourcePayloadDigest)...).
		WillReturnRows(rateSnapshotRows(existing, true))
	mock.ExpectCommit()
}
