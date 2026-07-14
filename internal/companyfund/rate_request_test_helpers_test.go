package companyfund

import (
	"regexp"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func expectNewRateReservation(mock sqlmock.Sqlmock, periodID, requestID int64, input RateRequestReservationInput) {
	expectRateReservationStart(mock, input, rateBudgetPeriodRows(periodID, input.Budget, 0, 0, nil))
	mock.ExpectQuery(regexp.QuoteMeta(selectActiveRateRequestForUpdateSQL)).
		WithArgs(input.Provider, input.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows(rateRequestAttemptColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestRateRequestForUpdateSQL)).
		WithArgs(input.Provider, input.LogicalRequestKey).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_no", "request_state"}))
	mock.ExpectQuery(regexp.QuoteMeta(reserveRateBudgetPeriodSQL)).
		WithArgs(periodID, input.Provider).
		WillReturnRows(sqlmock.NewRows([]string{"reserved_calls"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(insertRateRequestAttemptSQL)).
		WithArgs(periodID, input.Provider, input.LogicalRequestKey, input.RequestKind, nil, 1, RateRequestStatePending, nil).
		WillReturnRows(rateRequestAttemptRows(requestID, periodID, input, 1, RateRequestStatePending, nil, nil))
	mock.ExpectCommit()
}

func expectRateReservationStart(mock sqlmock.Sqlmock, input RateRequestReservationInput, periodRows *sqlmock.Rows) {
	budget := input.Budget.canonical()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(lockRateRequestLogicalKeySQL)).
		WithArgs(RateRequestAdvisoryLockKey(input.Provider, input.LogicalRequestKey)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(lockRateBudgetPeriodSQL)).
		WithArgs(RateBudgetAdvisoryLockKey(budget.Provider, budget.BillingAnchor, budget.PeriodKey)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(insertRateBudgetPeriodSQL)).
		WithArgs(budget.Provider, budget.BillingAnchor, budget.PeriodKey, budget.PeriodStart, budget.PeriodEnd, budget.CallLimit, nullableString(budget.PlanName), nullableString(budget.LicenseReference), budget.ConfigVersion).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(selectRateBudgetPeriodForUpdateSQL)).
		WithArgs(budget.Provider, budget.BillingAnchor, budget.PeriodKey).
		WillReturnRows(periodRows)
}

func newRateRequestReservationInput(logicalRequestKey string) RateRequestReservationInput {
	periodStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	return RateRequestReservationInput{
		Provider:          "coingecko",
		LogicalRequestKey: logicalRequestKey,
		RequestKind:       RateRequestKindCurrent,
		Budget: RateBudgetConfig{
			Provider: "coingecko", BillingAnchor: periodStart, PeriodKey: "2026-07", PeriodStart: periodStart,
			PeriodEnd: periodStart.AddDate(0, 1, 0), CallLimit: 1, PlanName: "free", LicenseReference: "public-api", ConfigVersion: "v1",
		},
	}
}

func rateBudgetPeriodColumnNames() []string {
	return []string{
		"id", "provider", "billing_anchor", "period_key", "period_start", "period_end", "call_limit", "reserved_calls", "used_calls",
		"plan_name", "license_reference", "config_version", "config_frozen_at", "first_reserved_at",
	}
}

func rateBudgetPeriodRows(id int64, config RateBudgetConfig, reservedCalls, usedCalls int, frozenAt *time.Time) *sqlmock.Rows {
	config = config.canonical()
	var frozen any
	if frozenAt != nil {
		frozen = frozenAt.UTC()
	}
	return sqlmock.NewRows(rateBudgetPeriodColumnNames()).AddRow(
		id, config.Provider, config.BillingAnchor, config.PeriodKey, config.PeriodStart, config.PeriodEnd, config.CallLimit,
		reservedCalls, usedCalls, nullableString(config.PlanName), nullableString(config.LicenseReference), config.ConfigVersion, frozen, frozen,
	)
}

func rateRequestAttemptColumnNames() []string {
	return []string{
		"id", "budget_period_id", "provider", "logical_request_key", "request_kind", "normalized_bucket_start", "attempt_no", "request_state",
		"not_before", "lease_owner", "lease_expires_at", "reserved_at", "dispatched_at", "charged_at", "completed_at", "response_snapshot_group_id", "error_code", "error_detail",
	}
}

func rateRequestAttemptRows(id, budgetPeriodID int64, input RateRequestReservationInput, attemptNo int, state RateRequestState, notBefore, leaseExpiresAt *time.Time) *sqlmock.Rows {
	reservedAt := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	var normalizedBucketStart any
	if input.NormalizedBucketStart != nil {
		normalizedBucketStart = input.NormalizedBucketStart.UTC()
	}
	var notBeforeValue any
	if notBefore != nil {
		notBeforeValue = notBefore.UTC()
	}
	var leaseOwner any
	var leaseExpiry any
	if leaseExpiresAt != nil {
		leaseOwner, leaseExpiry = "previous-worker", leaseExpiresAt.UTC()
	}
	var dispatchedAt any
	if state == RateRequestStateDispatched {
		dispatchedAt = reservedAt.Add(time.Minute)
	}
	return sqlmock.NewRows(rateRequestAttemptColumnNames()).AddRow(
		id, budgetPeriodID, input.Provider, input.LogicalRequestKey, input.RequestKind, normalizedBucketStart, attemptNo, state,
		notBeforeValue, leaseOwner, leaseExpiry, reservedAt, dispatchedAt, nil, nil, nil, nil, nil,
	)
}
