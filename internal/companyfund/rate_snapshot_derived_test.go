package companyfund

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAppendRateSnapshot_DerivedHistoricalValidatesInputsAndAppends(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input, numerator, denominator := newHistoricalBTCCrossRateSnapshot()
	inserted := rateSnapshotRecordFromInput(input, 303, 1, nil)
	mock.ExpectBegin()
	expectRateSnapshotAppendLocks(mock, input)
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotRequestProvenanceSQL)).
		WithArgs(*input.OriginatingRateRequestID).
		WillReturnRows(rateSnapshotRequestProvenanceRows(input.Provider, "HISTORICAL", &input.BucketStart))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotBySourceDigestSQL)).
		WithArgs(rateSnapshotSeriesArgs(input, input.SourcePayloadDigest)...).
		WillReturnRows(sqlmock.NewRows(rateSnapshotColumnNames(true)))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotByIDForUpdateSQL)).
		WithArgs(numerator.ID).WillReturnRows(rateSnapshotRows(numerator, true))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotByIDForUpdateSQL)).
		WithArgs(denominator.ID).WillReturnRows(rateSnapshotRows(denominator, true))
	mock.ExpectQuery(regexp.QuoteMeta(selectEligibleRateSnapshotLeafSQL)).
		WithArgs(rateSnapshotSeriesArgs(input)...).WillReturnRows(sqlmock.NewRows(rateSnapshotColumnNames(true)))
	mock.ExpectQuery(regexp.QuoteMeta(insertRateSnapshotSQL)).
		WithArgs(rateSnapshotInsertArgs(input, 1, nil)...).WillReturnRows(rateSnapshotRows(inserted, false))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input)
	if err != nil {
		t.Fatalf("AppendRateSnapshot() error = %v", err)
	}
	if !result.Inserted || result.Snapshot.ID != inserted.ID {
		t.Fatalf("derived rate snapshot was not appended: %#v", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestAppendRateSnapshot_RejectsMismatchedSupersededOrFutureDerivedLeg(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*RateSnapshotRecord, *RateSnapshotRecord)
	}{
		{
			name: "mismatched bucket is a different series",
			mutate: func(_, denominator *RateSnapshotRecord) {
				denominator.BucketStart = denominator.BucketStart.Add(time.Hour)
			},
		},
		{
			name: "superseded denominator",
			mutate: func(_, denominator *RateSnapshotRecord) {
				denominator.IsEligibleLeaf = false
			},
		},
		{
			name: "future historical denominator",
			mutate: func(_, denominator *RateSnapshotRecord) {
				future := denominator.EffectiveAt.Add(4 * time.Minute)
				denominator.EffectiveAt = &future
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()

			input, numerator, denominator := newHistoricalBTCCrossRateSnapshot()
			testCase.mutate(&numerator, &denominator)
			mock.ExpectBegin()
			expectRateSnapshotAppendLocks(mock, input)
			mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotRequestProvenanceSQL)).
				WithArgs(*input.OriginatingRateRequestID).
				WillReturnRows(rateSnapshotRequestProvenanceRows(input.Provider, "HISTORICAL", &input.BucketStart))
			mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotBySourceDigestSQL)).
				WithArgs(rateSnapshotSeriesArgs(input, input.SourcePayloadDigest)...).
				WillReturnRows(sqlmock.NewRows(rateSnapshotColumnNames(true)))
			mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotByIDForUpdateSQL)).
				WithArgs(numerator.ID).WillReturnRows(rateSnapshotRows(numerator, true))
			mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotByIDForUpdateSQL)).
				WithArgs(denominator.ID).WillReturnRows(rateSnapshotRows(denominator, true))
			mock.ExpectRollback()

			if _, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input); err == nil {
				t.Fatal("invalid derived legs must be rejected")
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestAppendRateSnapshot_RejectsCurrentDerivedResponseGroupMismatch(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input, numerator, denominator := newHistoricalBTCCrossRateSnapshot()
	input.PriceKind = MarketPriceKindCurrent
	input.SnapshotGroupID = "response-a"
	input.DerivedTargetAt = nil
	numerator.OriginatingRequestKind = "CURRENT"
	denominator.OriginatingRequestKind = "CURRENT"
	numerator.SnapshotGroupID = "response-a"
	denominator.SnapshotGroupID = "response-b"

	mock.ExpectBegin()
	expectRateSnapshotAppendLocks(mock, input)
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotRequestProvenanceSQL)).
		WithArgs(*input.OriginatingRateRequestID).
		WillReturnRows(rateSnapshotRequestProvenanceRows(input.Provider, "CURRENT", nil))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotBySourceDigestSQL)).
		WithArgs(rateSnapshotSeriesArgs(input, input.SourcePayloadDigest)...).
		WillReturnRows(sqlmock.NewRows(rateSnapshotColumnNames(true)))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotByIDForUpdateSQL)).
		WithArgs(numerator.ID).WillReturnRows(rateSnapshotRows(numerator, true))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotByIDForUpdateSQL)).
		WithArgs(denominator.ID).WillReturnRows(rateSnapshotRows(denominator, true))
	mock.ExpectRollback()

	if _, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), input); err == nil {
		t.Fatal("current cross inputs from different response groups must be rejected")
	}
	assertCompanyFundMockExpectations(t, mock)
}
