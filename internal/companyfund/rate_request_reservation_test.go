package companyfund

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestReserveRateRequest_OnlyOneLogicalKeyGetsLastBudgetCall(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	first := newRateRequestReservationInput("coingecko:last-one")
	second := newRateRequestReservationInput("coingecko:other-key")
	frozenAt := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)

	expectRateReservationStart(mock, first, rateBudgetPeriodRows(41, first.Budget, 0, 0, nil))
	mock.ExpectQuery(regexp.QuoteMeta(selectActiveRateRequestForUpdateSQL)).
		WithArgs(first.Provider, first.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows(rateRequestAttemptColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestRateRequestForUpdateSQL)).
		WithArgs(first.Provider, first.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_no", "request_state"}))
	mock.ExpectQuery(regexp.QuoteMeta(reserveRateBudgetPeriodSQL)).
		WithArgs(int64(41), first.Provider).
		WillReturnRows(sqlmock.NewRows([]string{"reserved_calls"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(insertRateRequestAttemptSQL)).
		WithArgs(int64(41), first.Provider, first.LogicalRequestKey, first.RequestKind, nil, 1, RateRequestStatePending, nil).
		WillReturnRows(rateRequestAttemptRows(71, 41, first, 1, RateRequestStatePending, nil, nil))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).ReserveRateRequest(context.Background(), first)
	if err != nil {
		t.Fatalf("first ReserveRateRequest() error = %v", err)
	}
	if !result.Reserved || result.ReusedActive || result.Attempt.ID != 71 {
		t.Fatalf("first ReserveRateRequest() = %#v, want newly reserved attempt 71", result)
	}

	expectRateReservationStart(mock, second, rateBudgetPeriodRows(41, second.Budget, 1, 0, &frozenAt))
	mock.ExpectQuery(regexp.QuoteMeta(selectActiveRateRequestForUpdateSQL)).
		WithArgs(second.Provider, second.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows(rateRequestAttemptColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestRateRequestForUpdateSQL)).
		WithArgs(second.Provider, second.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_no", "request_state"}))
	mock.ExpectQuery(regexp.QuoteMeta(reserveRateBudgetPeriodSQL)).
		WithArgs(int64(41), second.Provider).
		WillReturnRows(sqlmock.NewRows([]string{"reserved_calls"}))
	mock.ExpectRollback()

	_, err = NewDBRepository(db).ReserveRateRequest(context.Background(), second)
	if !errors.Is(err, ErrRateBudgetExhausted) {
		t.Fatalf("second ReserveRateRequest() error = %v, want ErrRateBudgetExhausted", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestReserveRateRequest_ActiveLogicalKeyIsNotChargedTwice(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateRequestReservationInput("coingecko:active")
	frozenAt := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	expectRateReservationStart(mock, input, rateBudgetPeriodRows(41, input.Budget, 1, 0, &frozenAt))
	mock.ExpectQuery(regexp.QuoteMeta(selectActiveRateRequestForUpdateSQL)).
		WithArgs(input.Provider, input.LogicalRequestKey).
		WillReturnRows(rateRequestAttemptRows(72, 41, input, 1, RateRequestStateDispatched, nil, nil))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).ReserveRateRequest(context.Background(), input)
	if err != nil {
		t.Fatalf("ReserveRateRequest() error = %v", err)
	}
	if result.Reserved || !result.ReusedActive || result.Attempt.State != RateRequestStateDispatched {
		t.Fatalf("ReserveRateRequest() = %#v, want active dispatched attempt without a new charge", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestReserveRateRequest_ProviderBudgetMismatchIsRejectedBeforeSQL(t *testing.T) {
	input := newRateRequestReservationInput("coingecko:provider-mismatch")
	input.Budget.Provider = "cryptocompare"

	_, err := NewDBRepository(nil).ReserveRateRequest(context.Background(), input)
	if !errors.Is(err, ErrRateRequestProviderBudgetMismatch) {
		t.Fatalf("ReserveRateRequest() error = %v, want ErrRateRequestProviderBudgetMismatch", err)
	}
}

func TestReserveRateRequest_CrossProviderSameLogicalKeyReservesIndependently(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	first := newRateRequestReservationInput("same-logical-key")
	second := newRateRequestReservationInput("same-logical-key")
	second.Provider = "cryptocompare"
	second.Budget.Provider = second.Provider
	second.Budget.PeriodKey = "2026-07-cryptocompare"

	expectNewRateReservation(mock, 41, 71, first)
	expectNewRateReservation(mock, 42, 72, second)

	firstResult, err := NewDBRepository(db).ReserveRateRequest(context.Background(), first)
	if err != nil || !firstResult.Reserved {
		t.Fatalf("first cross-provider reserve = %#v, %v", firstResult, err)
	}
	secondResult, err := NewDBRepository(db).ReserveRateRequest(context.Background(), second)
	if err != nil || !secondResult.Reserved {
		t.Fatalf("second cross-provider reserve = %#v, %v", secondResult, err)
	}
	if firstResult.Attempt.Provider == secondResult.Attempt.Provider {
		t.Fatalf("cross-provider attempts unexpectedly share provider: %#v %#v", firstResult.Attempt, secondResult.Attempt)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestReserveRateRequest_RetryCreatesNextChargedAttempt(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateRequestReservationInput("coingecko:retry")
	notBefore := time.Date(2026, 7, 10, 4, 5, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	input.RequestKind = RateRequestKindRetry
	input.NotBefore = &notBefore
	frozenAt := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)

	expectRateReservationStart(mock, input, rateBudgetPeriodRows(41, input.Budget, 1, 1, &frozenAt))
	mock.ExpectQuery(regexp.QuoteMeta(selectActiveRateRequestForUpdateSQL)).
		WithArgs(input.Provider, input.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows(rateRequestAttemptColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestRateRequestForUpdateSQL)).
		WithArgs(input.Provider, input.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_no", "request_state"}).AddRow(1, RateRequestStateFailed))
	mock.ExpectQuery(regexp.QuoteMeta(reserveRateBudgetPeriodSQL)).
		WithArgs(int64(41), input.Provider).
		WillReturnRows(sqlmock.NewRows([]string{"reserved_calls"}).AddRow(2))
	mock.ExpectQuery(regexp.QuoteMeta(insertRateRequestAttemptSQL)).
		WithArgs(int64(41), input.Provider, input.LogicalRequestKey, RateRequestKindRetry, nil, 2, RateRequestStateRetryWait, notBefore.UTC()).
		WillReturnRows(rateRequestAttemptRows(73, 41, input, 2, RateRequestStateRetryWait, input.NotBefore, nil))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).ReserveRateRequest(context.Background(), input)
	if err != nil {
		t.Fatalf("retry ReserveRateRequest() error = %v", err)
	}
	if !result.Reserved || result.Attempt.AttemptNo != 2 || result.Attempt.State != RateRequestStateRetryWait {
		t.Fatalf("retry ReserveRateRequest() = %#v, want charged attempt 2 in RETRY_WAIT", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestReserveRateRequest_RetryRequiresFailedOrUnknownPreviousAttempt(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newRateRequestReservationInput("coingecko:successful-attempt")
	input.RequestKind = RateRequestKindRetry
	expectRateReservationStart(mock, input, rateBudgetPeriodRows(41, input.Budget, 1, 1, timePointer(time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC))))
	mock.ExpectQuery(regexp.QuoteMeta(selectActiveRateRequestForUpdateSQL)).
		WithArgs(input.Provider, input.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows(rateRequestAttemptColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestRateRequestForUpdateSQL)).
		WithArgs(input.Provider, input.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_no", "request_state"}).AddRow(1, RateRequestStateSucceeded))
	mock.ExpectRollback()

	_, err := NewDBRepository(db).ReserveRateRequest(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "FAILED or UNKNOWN") {
		t.Fatalf("successful attempt retry error = %v, want FAILED/UNKNOWN requirement", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestReserveRateRequest_ExistingFailedAttemptRequiresRetryKind(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		input RateRequestReservationInput
	}{
		{name: "current after failed", input: newRateRequestReservationInput("coingecko:failed-current")},
		{name: "historical after failed", input: historicalRateRequestInput("coingecko:failed-history")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			input := testCase.input
			expectRateReservationStart(mock, input, rateBudgetPeriodRows(41, input.Budget, 1, 1, timePointer(time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC))))
			mock.ExpectQuery(regexp.QuoteMeta(selectActiveRateRequestForUpdateSQL)).
				WithArgs(input.Provider, input.LogicalRequestKey).
				WillReturnRows(sqlmock.NewRows(rateRequestAttemptColumnNames()))
			mock.ExpectQuery(regexp.QuoteMeta(selectLatestRateRequestForUpdateSQL)).
				WithArgs(input.Provider, input.LogicalRequestKey).
				WillReturnRows(sqlmock.NewRows([]string{"attempt_no", "request_state"}).AddRow(1, RateRequestStateFailed))
			mock.ExpectRollback()

			_, err := NewDBRepository(db).ReserveRateRequest(context.Background(), input)
			if err == nil || !strings.Contains(err.Error(), "requires a RETRY") {
				t.Fatalf("existing failed %s error = %v, want RETRY requirement", input.RequestKind, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestReserveRateRequest_FrozenBudgetConfigIsNeverSilentlyOverwritten(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	input := newRateRequestReservationInput("coingecko:changed-limit")
	input.Budget.CallLimit = 500
	frozenAt := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	storedConfig := input.Budget
	storedConfig.CallLimit = 100
	expectRateReservationStart(mock, input, rateBudgetPeriodRows(41, storedConfig, 1, 1, &frozenAt))
	mock.ExpectRollback()

	_, err := NewDBRepository(db).ReserveRateRequest(context.Background(), input)
	if !errors.Is(err, ErrRateBudgetConfigurationMismatch) {
		t.Fatalf("ReserveRateRequest() error = %v, want ErrRateBudgetConfigurationMismatch", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}
