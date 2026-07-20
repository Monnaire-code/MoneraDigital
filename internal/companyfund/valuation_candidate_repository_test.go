package companyfund

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestListCompanyFundValuationRepairCandidates_SelectsOnlyRepairableCurrentStates(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	candidate := newValuationRuntimeCandidate(81, "ETH", decimal.RequireFromString("1.234567890123456789"))
	candidate.Asset = AssetIdentity{Currency: "ETH", ChainCode: "ETHEREUM", ProviderAssetKey: "ETH"}
	providerFactID := int64(71)
	candidate.ProviderTransactionFactID = &providerFactID
	reportedUSD := decimal.RequireFromString("3000.123456789012345678")
	candidate.ProviderReportedUSD = &reportedUSD
	candidate.ProviderValueScope = ProviderValueScopeDirectItem
	candidate.ProviderAllocationState = ProviderFactAllocationStateNotApplicable

	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundValuationRepairCandidatesSQL)).
		WithArgs(25).
		WillReturnRows(companyFundValuationCandidateRows(candidate))

	result, err := NewDBRepository(db).ListCompanyFundValuationRepairCandidates(context.Background(), 25)
	if err != nil || len(result) != 1 {
		t.Fatalf("ListCompanyFundValuationRepairCandidates() = %#v, %v", result, err)
	}
	got := result[0]
	if got.ID != candidate.ID || !got.Amount.Equal(candidate.Amount) || got.ProviderReportedUSD == nil || !got.ProviderReportedUSD.Equal(reportedUSD) || got.ProviderTransactionFactID == nil || *got.ProviderTransactionFactID != providerFactID {
		t.Fatalf("candidate lost exact provider facts: %#v", got)
	}
	assertCompanyFundMockExpectations(t, mock)

	lower := strings.ToLower(selectCompanyFundValuationRepairCandidatesSQL)
	for _, required := range []string{
		"movement.current_valuation_history_id is null",
		"current_history.usd_valuation_status in ('unpriced', 'stale')",
		"order by movement.first_seen_at, movement.id",
	} {
		if !strings.Contains(lower, required) {
			t.Fatalf("repair candidate SQL missing %q: %s", required, selectCompanyFundValuationRepairCandidatesSQL)
		}
	}
	if strings.Contains(lower, "'provisional'") || strings.Contains(lower, "'final'") {
		t.Fatalf("repair sweep must not continuously revalue priced current rows: %s", selectCompanyFundValuationRepairCandidatesSQL)
	}
}

func TestListCompanyFundValuationRepairCandidates_AcceptsUnpricedHistoryWithoutSource(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	candidate := newValuationRuntimeCandidate(82, "USDT", decimal.RequireFromString("0.01"))
	historyID := int64(72)
	candidate.CurrentValuationHistoryID = &historyID
	candidate.CurrentValuationDependencyFingerprint = strings.Repeat("a", 64)
	candidate.CurrentValuationStatus = USDValuationStatusUnpriced
	candidate.CurrentValuationSource = ""

	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundValuationRepairCandidatesSQL)).
		WithArgs(25).
		WillReturnRows(companyFundValuationCandidateRows(candidate))

	result, err := NewDBRepository(db).ListCompanyFundValuationRepairCandidates(context.Background(), 25)
	if err != nil || len(result) != 1 {
		t.Fatalf("ListCompanyFundValuationRepairCandidates() = %#v, %v; want legal unpriced candidate", result, err)
	}
	if result[0].CurrentValuationSource != "" || result[0].CurrentValuationStatus != USDValuationStatusUnpriced {
		t.Fatalf("unpriced candidate state = %#v", result[0])
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestGetCompanyFundTransactionValuationCandidate_ReturnsNilForMissingRow(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionValuationCandidateSQL)).
		WithArgs(int64(99)).
		WillReturnRows(sqlmock.NewRows(companyFundValuationCandidateColumnNames()))

	candidate, err := NewDBRepository(db).GetCompanyFundTransactionValuationCandidate(context.Background(), 99)
	if err != nil || candidate != nil {
		t.Fatalf("GetCompanyFundTransactionValuationCandidate() = %#v, %v; want no candidate", candidate, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestCompanyFundValuationCandidateQueriesExcludeOtherAccountMovements(t *testing.T) {
	for name, query := range map[string]string{
		"single": selectCompanyFundTransactionValuationCandidateSQL,
		"repair": selectCompanyFundValuationRepairCandidatesSQL,
		"cursor": selectCompanyFundValuationRepairCandidatesAfterSQL,
	} {
		lower := strings.ToLower(query)
		for _, required := range []string{
			"not exists",
			"from company_fund_accounts as account",
			"account.channel = 'other'",
			"account.id = movement.from_company_fund_account_id",
			"account.id = movement.to_company_fund_account_id",
		} {
			if !strings.Contains(lower, required) {
				t.Errorf("%s valuation candidate query missing OTHER exclusion %q", name, required)
			}
		}
	}
}

func companyFundValuationCandidateColumnNames() []string {
	return []string{
		"id", "channel", "movement_kind", "transaction_direction", "currency", "amount", "chain_code", "provider_asset_key", "asset_contract", "is_unrecognized_asset",
		"from_company_fund_account_id", "to_company_fund_account_id", "occurred_at", "completed_at", "first_seen_at", "provider_transaction_fact_id",
		"provider_reported_usd_value", "value_scope", "allocation_state", "conversion_from_currency", "conversion_to_currency",
		"current_valuation_history_id", "dependency_fingerprint", "usd_valuation_status", "usd_valuation_source",
	}
}

func companyFundValuationCandidateRows(candidate CompanyFundTransactionValuationCandidate) *sqlmock.Rows {
	return sqlmock.NewRows(companyFundValuationCandidateColumnNames()).AddRow(
		candidate.ID,
		candidate.Channel,
		candidate.MovementKind,
		candidate.Direction,
		candidate.Currency,
		candidate.Amount.String(),
		candidate.Asset.ChainCode,
		candidate.Asset.ProviderAssetKey,
		candidate.Asset.ContractAddress,
		candidate.IsUnrecognizedAsset,
		valuationTestID(candidate.FromCompanyFundAccountID),
		valuationTestID(candidate.ToCompanyFundAccountID),
		valuationTestTime(candidate.OccurredAt),
		valuationTestTime(candidate.CompletedAt),
		candidate.FirstSeenAt,
		valuationTestID(candidate.ProviderTransactionFactID),
		valuationTestDecimal(candidate.ProviderReportedUSD),
		candidate.ProviderValueScope,
		candidate.ProviderAllocationState,
		candidate.AirwallexConversionFrom,
		candidate.AirwallexConversionTo,
		valuationTestID(candidate.CurrentValuationHistoryID),
		candidate.CurrentValuationDependencyFingerprint,
		candidate.CurrentValuationStatus,
		candidate.CurrentValuationSource,
	)
}

func TestCompanyFundValuationCandidate_UsesFirstSeenFallbackOnlyWhenProviderTimesMissing(t *testing.T) {
	firstSeen := time.Date(2026, time.July, 11, 3, 0, 0, 0, time.UTC)
	candidate := newValuationRuntimeCandidate(90, "USD", decimal.NewFromInt(1))
	candidate.FirstSeenAt = firstSeen
	if target := candidate.transactionValuationTime(); target != nil {
		t.Fatalf("transaction target without provider times = %v, want nil so ingestion fallback is explicit", target)
	}
	completed := firstSeen.Add(-time.Minute)
	candidate.CompletedAt = &completed
	if target := candidate.transactionValuationTime(); target == nil || !target.Equal(completed) {
		t.Fatalf("completed target = %v, want %v", target, completed)
	}
	occurred := completed.Add(-time.Minute)
	candidate.OccurredAt = &occurred
	if target := candidate.transactionValuationTime(); target == nil || !target.Equal(occurred) {
		t.Fatalf("occurred target = %v, want %v", target, occurred)
	}
}

func TestCompanyFundValuationCandidate_ValidatesCurrentSourceByStatus(t *testing.T) {
	historyID := int64(74)
	for _, testCase := range []struct {
		name    string
		status  USDValuationStatus
		source  USDValuationSource
		wantErr bool
	}{
		{name: "unpriced without source", status: USDValuationStatusUnpriced},
		{name: "priced without source", status: USDValuationStatusFinal, wantErr: true},
		{name: "unpriced with unsupported source", status: USDValuationStatusUnpriced, source: "UNKNOWN", wantErr: true},
		{name: "priced with supported source", status: USDValuationStatusFinal, source: USDValuationSourceCoinGecko},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := newValuationRuntimeCandidate(83, "USDT", decimal.RequireFromString("0.01"))
			candidate.CurrentValuationHistoryID = &historyID
			candidate.CurrentValuationDependencyFingerprint = strings.Repeat("c", 64)
			candidate.CurrentValuationStatus = testCase.status
			candidate.CurrentValuationSource = testCase.source

			err := candidate.validate()
			if (err != nil) != testCase.wantErr {
				t.Fatalf("validate() error = %v, wantErr %t", err, testCase.wantErr)
			}
		})
	}
}
