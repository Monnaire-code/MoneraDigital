package companyfund

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestApplyCompanyFundValuation_AppendsHistoryAndUpdatesOnlyProjection(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationApplyInput()
	history := valuationHistoryFromInput(input, 301, 1, nil)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, nil, ""))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryByApplyIdentitySQL)).
		WithArgs(input.TransactionID, input.DependencyFingerprint, input.PolicyVersion, input.TransitionTrigger).
		WillReturnRows(sqlmock.NewRows(companyFundValuationHistoryColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestValuationHistoryForUpdateSQL)).
		WithArgs(input.TransactionID).WillReturnRows(sqlmock.NewRows(companyFundValuationHistoryColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationHistorySQL)).
		WithArgs(valuationHistoryInsertArgs(input, 1, nil)...).WillReturnRows(companyFundValuationHistoryRows(history))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionValuationProjectionSQL)).
		WithArgs(valuationProjectionArgs(input, history)...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(input.TransactionID))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).ApplyCompanyFundValuation(context.Background(), input)
	if err != nil {
		t.Fatalf("ApplyCompanyFundValuation() error = %v", err)
	}
	if !result.Inserted || result.History.ID != history.ID || result.History.ValuationVersion != 1 {
		t.Fatalf("ApplyCompanyFundValuation() = %#v, want appended history", result)
	}
	assertCompanyFundMockExpectations(t, mock)

	for _, forbidden := range []string{
		"finance_category", "is_operating_income_expense", "business_description", "classification_",
		"risk_", "is_dust", "aml_", "raw_snapshot_digest", "provider_status", "provider_extras",
	} {
		if strings.Contains(strings.ToLower(updateCompanyFundTransactionValuationProjectionSQL), forbidden) {
			t.Fatalf("valuation projection must not update manual/risk/provider field %q", forbidden)
		}
	}
}

func TestApplyCompanyFundValuation_ExactDependencyReadsExistingHistory(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationApplyInput()
	existing := valuationHistoryFromInput(input, 302, 4, nil)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, nil, ""))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryByApplyIdentitySQL)).
		WithArgs(input.TransactionID, input.DependencyFingerprint, input.PolicyVersion, input.TransitionTrigger).
		WillReturnRows(companyFundValuationHistoryRows(existing))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).ApplyCompanyFundValuation(context.Background(), input)
	if err != nil {
		t.Fatalf("ApplyCompanyFundValuation() error = %v", err)
	}
	if result.Inserted || result.History.ID != existing.ID {
		t.Fatalf("same dependency/policy must read back immutable history, got %#v", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestApplyCompanyFundValuation_StaleExpectedCurrentHistoryIsSupersededNoOp(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationApplyInput()
	expectedHistoryID := int64(303)
	input.ExpectedCurrentHistoryID = &expectedHistoryID
	input.ExpectedCurrentDependencyFingerprint = input.DependencyFingerprint
	currentFingerprint := strings.Repeat("b", 64)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, &expectedHistoryID, currentFingerprint))
	mock.ExpectRollback()

	result, err := NewDBRepository(db).ApplyCompanyFundValuation(context.Background(), input)
	if err != nil || !result.Superseded || result.Inserted || result.History.ID != 0 {
		t.Fatalf("ApplyCompanyFundValuation() = %#v, %v; want superseded no-op", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestApplyCompanyFundValuation_ChangedDependencyAppendsSupersedingVersion(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationApplyInput()
	input.DependencyFingerprint = strings.Repeat("b", 64)
	previousInput := newCompanyFundValuationApplyInput()
	previous := valuationHistoryFromInput(previousInput, 303, 7, nil)
	previousID := previous.ID
	history := valuationHistoryFromInput(input, 304, 8, &previousID)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, nil, ""))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryByApplyIdentitySQL)).
		WithArgs(input.TransactionID, input.DependencyFingerprint, input.PolicyVersion, input.TransitionTrigger).
		WillReturnRows(sqlmock.NewRows(companyFundValuationHistoryColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestValuationHistoryForUpdateSQL)).
		WithArgs(input.TransactionID).WillReturnRows(companyFundValuationHistoryRows(previous))
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationHistorySQL)).
		WithArgs(valuationHistoryInsertArgs(input, 8, &previousID)...).WillReturnRows(companyFundValuationHistoryRows(history))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionValuationProjectionSQL)).
		WithArgs(valuationProjectionArgs(input, history)...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(input.TransactionID))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).ApplyCompanyFundValuation(context.Background(), input)
	if err != nil {
		t.Fatalf("ApplyCompanyFundValuation() error = %v", err)
	}
	if !result.Inserted || result.History.ValuationVersion != 8 || result.History.SupersedesHistoryID == nil || *result.History.SupersedesHistoryID != previous.ID {
		t.Fatalf("changed dependency must append a superseding history: %#v", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestApplyCompanyFundValuation_SameDependencyCanAdvancePendingToFinal(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationApplyInput()
	input.TransitionTrigger = "REVALUATION_COMPLETED"
	pendingInput := input
	pendingInput.TransitionTrigger = "DEPENDENCY_INVALIDATED"
	pendingInput.Result.Status = USDValuationStatusStale
	pendingInput.Result.Reason = USDValuationReasonRevaluationPending
	pendingInput.Result.Value = nil
	pendingInput.Result.UnitPrice = decimal.Zero
	pendingInput.CalculatedUSDValue = nil
	pending := valuationHistoryFromInput(pendingInput, 305, 5, nil)
	input.ExpectedCurrentHistoryID = &pending.ID
	input.ExpectedCurrentDependencyFingerprint = input.DependencyFingerprint
	pendingID := pending.ID
	final := valuationHistoryFromInput(input, 306, 6, &pendingID)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, &pendingID, input.DependencyFingerprint))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryByApplyIdentitySQL)).
		WithArgs(input.TransactionID, input.DependencyFingerprint, input.PolicyVersion, input.TransitionTrigger).
		WillReturnRows(sqlmock.NewRows(companyFundValuationHistoryColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestValuationHistoryForUpdateSQL)).
		WithArgs(input.TransactionID).WillReturnRows(companyFundValuationHistoryRows(pending))
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationHistorySQL)).
		WithArgs(valuationHistoryInsertArgs(input, 6, &pendingID)...).WillReturnRows(companyFundValuationHistoryRows(final))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionValuationProjectionSQL)).
		WithArgs(valuationProjectionArgs(input, final)...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(input.TransactionID))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).ApplyCompanyFundValuation(context.Background(), input)
	if err != nil || !result.Inserted || result.History.ID != final.ID || result.History.SupersedesHistoryID == nil || *result.History.SupersedesHistoryID != pending.ID {
		t.Fatalf("ApplyCompanyFundValuation() = %#v, %v; want pending-to-final append", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestApplyCompanyFundValuation_RejectsImmutableConflictAndUnpricedZero(t *testing.T) {
	t.Run("same dependency with changed result", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()

		input := newCompanyFundValuationApplyInput()
		existing := valuationHistoryFromInput(input, 305, 1, nil)
		changed := decimal.RequireFromString("2469.135780246913578025")
		input.Result.Value = &changed
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
			WithArgs(input.TransactionID).WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, nil, ""))
		mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryByApplyIdentitySQL)).
			WithArgs(input.TransactionID, input.DependencyFingerprint, input.PolicyVersion, input.TransitionTrigger).
			WillReturnRows(companyFundValuationHistoryRows(existing))
		mock.ExpectRollback()

		if _, err := NewDBRepository(db).ApplyCompanyFundValuation(context.Background(), input); err == nil || !strings.Contains(err.Error(), "immutable field usd_value") {
			t.Fatalf("immutable history conflict must be rejected, got %v", err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("unpriced must not persist zero", func(t *testing.T) {
		input := newCompanyFundValuationApplyInput()
		zero := decimal.Zero
		input.Result.Status = USDValuationStatusUnpriced
		input.Result.Value = &zero
		input.Result.UnitPrice = decimal.Zero
		input.CalculatedUSDValue = nil
		if _, err := NewDBRepository(nil).ApplyCompanyFundValuation(context.Background(), input); err == nil {
			t.Fatal("unpriced synthetic zero must fail before database access")
		}
	})

	t.Run("expected current guard requires ID and fingerprint together", func(t *testing.T) {
		input := newCompanyFundValuationApplyInput()
		expectedHistoryID := int64(501)
		input.ExpectedCurrentHistoryID = &expectedHistoryID
		if _, err := NewDBRepository(nil).ApplyCompanyFundValuation(context.Background(), input); err == nil {
			t.Fatal("partial expected-current guard must fail before database access")
		}
	})
}

func TestCompanyFundValuationHistorySQL_IsAppendOnlyAndLocksCurrentState(t *testing.T) {
	allSQL := strings.Join([]string{
		selectCompanyFundTransactionForValuationSQL,
		selectValuationHistoryByApplyIdentitySQL,
		selectLatestValuationHistoryForUpdateSQL,
		insertCompanyFundValuationHistorySQL,
		updateCompanyFundTransactionValuationProjectionSQL,
	}, "\n")
	for _, contract := range []string{
		"FOR UPDATE OF transaction",
		"dependency_fingerprint = $2",
		"valuation_policy_version = $3",
		"transition_trigger = $4",
		"current_valuation_history_id = $17",
	} {
		if !strings.Contains(allSQL, contract) {
			t.Fatalf("valuation history SQL is missing %q", contract)
		}
	}
	for _, forbidden := range []string{
		"UPDATE company_fund_transaction_valuation_history",
		"DELETE FROM company_fund_transaction_valuation_history",
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("valuation history must remain append-only; found %q", forbidden)
		}
	}
}
