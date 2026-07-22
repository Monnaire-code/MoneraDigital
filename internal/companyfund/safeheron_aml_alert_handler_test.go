package companyfund

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSafeheronAMLAlertHandler_UpdatesAllProjectedCompanyMovements(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForAMLAlertSQL)).
		WithArgs("safeheron-tx").
		WillReturnRows(sqlmock.NewRows([]string{"company_case_count", "pending_company_projection_count", "pending_customer_projection_count"}).AddRow(2, 0, 0))
	mock.ExpectExec(regexp.QuoteMeta(updateSafeheronCompanyFundTransactionAMLAlertSQL)).
		WithArgs("safeheron-tx", string(AMLScreeningStateScreened), string(AMLRiskLevelLow)).
		WillReturnResult(sqlmock.NewResult(0, 2))

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

func TestSafeheronAMLAlertHandler_DefersUntilEveryRequiredProjectionExists(t *testing.T) {
	testCases := []struct {
		name                      string
		pendingCompanyProjection  int
		pendingCustomerProjection int
	}{
		{name: "company projection pending", pendingCompanyProjection: 1},
		{name: "customer projection pending", pendingCustomerProjection: 1},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()

			mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForAMLAlertSQL)).
				WithArgs("safeheron-tx").
				WillReturnRows(sqlmock.NewRows([]string{"company_case_count", "pending_company_projection_count", "pending_customer_projection_count"}).AddRow(1, testCase.pendingCompanyProjection, testCase.pendingCustomerProjection))

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
		})
	}
}

func TestSafeheronAMLAlertHandler_LeavesCustomerAlertToDepositPipeline(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForAMLAlertSQL)).
		WithArgs("customer-tx").
		WillReturnRows(sqlmock.NewRows([]string{"company_case_count", "pending_company_projection_count", "pending_customer_projection_count"}).AddRow(0, 0, 0))

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
