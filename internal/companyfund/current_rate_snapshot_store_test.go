package companyfund

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestAppendRateSnapshot_CurrentBTCCrossUsesProviderTimeForRawLegs(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	providerUpdatedAt := time.Date(2026, time.July, 16, 3, 54, 45, 0, time.UTC)
	fetchedAt := providerUpdatedAt.Add(2 * time.Second)
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	quote := CoinGeckoQuote{
		Price:               decimal.RequireFromString("0.147727272727272727"),
		ProviderUpdatedAt:   providerUpdatedAt,
		FetchedAt:           fetchedAt,
		ResponseDigest:      digest,
		Method:              USDValuationMethodCoinGeckoBTCCross,
		BTCCrossNumerator:   decimal.NewFromInt(65000),
		BTCCrossDenominator: decimal.NewFromInt(440000),
	}
	numeratorInput := currentRateBTCLegSnapshotInput("USD", quote.BTCCrossNumerator, quote, "current-usd-v1")
	denominatorInput := currentRateBTCLegSnapshotInput("CNY", quote.BTCCrossDenominator, quote, "current-usd-v1")
	numerator := rateSnapshotRecordFromInput(numeratorInput, 101, 1, nil)
	denominator := rateSnapshotRecordFromInput(denominatorInput, 202, 1, nil)
	key := CoinGeckoQuoteCacheKey{
		Provider: rateSnapshotCoinGeckoProvider, AssetIdentityKey: "3:CNY0:0:0:", FiatCode: "CNY", QuoteCurrency: "USD",
	}
	derivedInput := currentRateSnapshotInput(key, currentRateSnapshotMapping{BaseCurrency: "CNY"}, quote, "current-usd-v1")
	derivedInput.NumeratorSnapshotID = &numerator.ID
	derivedInput.DenominatorSnapshotID = &denominator.ID
	inserted := rateSnapshotRecordFromInput(derivedInput, 303, 1, nil)

	mock.ExpectBegin()
	expectRateSnapshotAppendLocks(mock, derivedInput)
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotBySourceDigestSQL)).
		WithArgs(rateSnapshotSeriesArgs(derivedInput, derivedInput.SourcePayloadDigest)...).
		WillReturnRows(sqlmock.NewRows(rateSnapshotColumnNames(true)))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotByIDForUpdateSQL)).
		WithArgs(numerator.ID).WillReturnRows(rateSnapshotRows(numerator, true))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateSnapshotByIDForUpdateSQL)).
		WithArgs(denominator.ID).WillReturnRows(rateSnapshotRows(denominator, true))
	mock.ExpectQuery(regexp.QuoteMeta(selectEligibleRateSnapshotLeafSQL)).
		WithArgs(rateSnapshotSeriesArgs(derivedInput)...).WillReturnRows(sqlmock.NewRows(rateSnapshotColumnNames(true)))
	mock.ExpectQuery(regexp.QuoteMeta(insertRateSnapshotSQL)).
		WithArgs(rateSnapshotInsertArgs(derivedInput, 1, nil)...).WillReturnRows(rateSnapshotRows(inserted, false))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).AppendRateSnapshot(context.Background(), derivedInput)
	if err != nil {
		t.Fatalf("AppendRateSnapshot() error = %v", err)
	}
	if !result.Inserted || result.Snapshot.ID != inserted.ID {
		t.Fatalf("derived current BTC-cross snapshot = %#v", result)
	}
	for _, leg := range []RateSnapshotInput{numeratorInput, denominatorInput} {
		if leg.EffectiveAt == nil || !leg.EffectiveAt.Equal(providerUpdatedAt) ||
			!leg.AvailableAt.Equal(fetchedAt) || !leg.FetchedAt.Equal(fetchedAt) ||
			leg.SnapshotGroupID != digest || leg.SourcePayloadDigest != digest {
			t.Fatalf("raw BTC leg lost provider/fetch audit boundaries: %#v", leg)
		}
	}
	if derivedInput.NumeratorSnapshotID == nil || *derivedInput.NumeratorSnapshotID != numerator.ID ||
		derivedInput.DenominatorSnapshotID == nil || *derivedInput.DenominatorSnapshotID != denominator.ID ||
		derivedInput.SnapshotGroupID != digest || derivedInput.SourcePayloadDigest != digest {
		t.Fatalf("derived BTC-cross lost response group or dependency FKs: %#v", derivedInput)
	}
	assertCompanyFundMockExpectations(t, mock)
}
