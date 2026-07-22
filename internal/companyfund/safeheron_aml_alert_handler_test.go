package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSafeheronAMLAlertHandler_UpdatesLinkedCompanyTransaction(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForAMLAlertSQL)).
		WithArgs("safeheron-tx").
		WillReturnRows(sqlmock.NewRows([]string{"company_fund_transaction_id"}).AddRow(int64(71)))
	mock.ExpectExec(regexp.QuoteMeta(updateSafeheronCompanyFundTransactionAMLAlertSQL)).
		WithArgs(int64(71), "safeheron-tx", string(AMLScreeningStateScreened), string(AMLRiskLevelLow)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := NewSafeheronAMLAlertHandler(db).HandleCompanyFundAMLAlert(context.Background(), SafeheronAMLAlertInput{
		TransactionKey: "safeheron-tx",
		ScreeningState: "TRIGGERED",
		RiskLevel:      "LOW",
	})
	if err != nil || result != SafeheronAMLAlertApplied {
		t.Fatalf("HandleCompanyFundAMLAlert() = %q, %v", result, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeheronAMLAlertHandler_DefersUntilCompanyProjectionExists(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForAMLAlertSQL)).
		WithArgs("safeheron-tx").
		WillReturnRows(sqlmock.NewRows([]string{"company_fund_transaction_id"}).AddRow(nil))

	result, err := NewSafeheronAMLAlertHandler(db).HandleCompanyFundAMLAlert(context.Background(), SafeheronAMLAlertInput{
		TransactionKey: "safeheron-tx",
		ScreeningState: "TRIGGERED",
		RiskLevel:      "LOW",
	})
	if err != nil || result != SafeheronAMLAlertDeferred {
		t.Fatalf("HandleCompanyFundAMLAlert() = %q, %v", result, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeheronAMLAlertHandler_LeavesCustomerAlertToDepositPipeline(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForAMLAlertSQL)).
		WithArgs("customer-tx").
		WillReturnError(sql.ErrNoRows)

	result, err := NewSafeheronAMLAlertHandler(db).HandleCompanyFundAMLAlert(context.Background(), SafeheronAMLAlertInput{
		TransactionKey: "customer-tx",
		ScreeningState: "TRIGGERED",
		RiskLevel:      "LOW",
	})
	if err != nil || result != SafeheronAMLAlertNotCompany {
		t.Fatalf("HandleCompanyFundAMLAlert() = %q, %v", result, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeSafeheronAMLAlertState(t *testing.T) {
	testCases := []struct {
		name      string
		risk      string
		wantState AMLScreeningState
		wantRisk  AMLRiskLevel
	}{
		{name: "low is screened", risk: "LOW", wantState: AMLScreeningStateScreened, wantRisk: AMLRiskLevelLow},
		{name: "severe is critical", risk: "SEVERE", wantState: AMLScreeningStateScreened, wantRisk: AMLRiskLevelCritical},
		{name: "pending stays pending", risk: "PENDING", wantState: AMLScreeningStatePending, wantRisk: AMLRiskLevelUnknown},
		{name: "provider failure requires review", risk: "FAILED", wantState: AMLScreeningStateReviewRequired, wantRisk: AMLRiskLevelUnknown},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state, risk, err := normalizeSafeheronAMLAlertState(testCase.risk)
			if err != nil || state != testCase.wantState || risk != testCase.wantRisk {
				t.Fatalf("normalizeSafeheronAMLAlertState(%q) = %q, %q, %v", testCase.risk, state, risk, err)
			}
		})
	}
}

func TestNormalizeSafeheronAMLAlertState_RejectsUnknownRisk(t *testing.T) {
	_, _, err := normalizeSafeheronAMLAlertState("unexpected")
	if !errors.Is(err, ErrInvalidSafeheronAMLAlertRisk) {
		t.Fatalf("normalizeSafeheronAMLAlertState() error = %v", err)
	}
}
