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

func TestClaimNextRateRequest_RecoversStaleLease(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	input := newRateRequestReservationInput("coingecko:stale-lease")
	pastExpiry := time.Date(2026, 7, 10, 2, 0, 0, 0, time.UTC)
	newExpiry := time.Date(2026, 7, 10, 3, 5, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(claimNextRateRequestSQL)).
		WillReturnRows(rateRequestAttemptRows(81, 41, input, 1, RateRequestStateLeased, nil, &pastExpiry))
	mock.ExpectQuery(regexp.QuoteMeta(updateClaimedRateRequestSQL)).
		WithArgs(int64(81), "worker-2", int64((5 * time.Minute).Microseconds())).
		WillReturnRows(sqlmock.NewRows([]string{"lease_expires_at"}).AddRow(newExpiry))
	mock.ExpectCommit()

	lease, err := NewDBRepository(db).ClaimNextRateRequest(context.Background(), "worker-2", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextRateRequest() error = %v", err)
	}
	if lease == nil || lease.Attempt.State != RateRequestStateLeased || lease.Attempt.LeaseOwner != "worker-2" || lease.Attempt.LeaseExpiresAt == nil || !lease.Attempt.LeaseExpiresAt.Equal(newExpiry) {
		t.Fatalf("ClaimNextRateRequest() = %#v, want stale lease reclaimed by worker-2", lease)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestClaimNextRateRequest_DispatchedAttemptIsNeverClaimable(t *testing.T) {
	if strings.Contains(claimNextRateRequestSQL, "DISPATCHED") {
		t.Fatalf("claim SQL must not make dispatched requests claimable: %s", claimNextRateRequestSQL)
	}
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(claimNextRateRequestSQL)).
		WillReturnRows(sqlmock.NewRows(rateRequestAttemptColumnNames()))
	mock.ExpectRollback()

	lease, err := NewDBRepository(db).ClaimNextRateRequest(context.Background(), "worker-3", time.Minute)
	if err != nil || lease != nil {
		t.Fatalf("ClaimNextRateRequest() = %#v, %v; want no claim for dispatched-only work", lease, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestMarkRateRequestDispatched_UsesLogicalLockBeforeRequestAndBudgetRows(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectRateRequestLogicalKeySQL)).
		WithArgs(int64(91)).
		WillReturnRows(sqlmock.NewRows([]string{"provider", "logical_request_key"}).AddRow("coingecko", "coingecko:dispatch-91"))
	mock.ExpectExec(regexp.QuoteMeta(lockRateRequestLogicalKeySQL)).
		WithArgs(RateRequestAdvisoryLockKey("coingecko", "coingecko:dispatch-91")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(markRateRequestDispatchedSQL)).
		WithArgs(int64(91), "worker-4").
		WillReturnRows(sqlmock.NewRows([]string{"budget_period_id", "provider"}).AddRow(41, "coingecko"))
	mock.ExpectQuery(regexp.QuoteMeta(consumeReservedRateBudgetCallSQL)).
		WithArgs(int64(41), "coingecko").
		WillReturnRows(sqlmock.NewRows([]string{"used_calls"}).AddRow(2))
	mock.ExpectCommit()

	if err := NewDBRepository(db).MarkRateRequestDispatched(context.Background(), 91, "worker-4"); err != nil {
		t.Fatalf("MarkRateRequestDispatched() error = %v", err)
	}
	if strings.Contains(consumeReservedRateBudgetCallSQL, "reserved_calls =") {
		t.Fatalf("dispatch consumption must not mutate reserved_calls: %s", consumeReservedRateBudgetCallSQL)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestMarkRateRequestDispatched_RejectsExpiredOrForeignLease(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectRateRequestLogicalKeySQL)).
		WithArgs(int64(91)).
		WillReturnRows(sqlmock.NewRows([]string{"provider", "logical_request_key"}).AddRow("coingecko", "coingecko:dispatch-91"))
	mock.ExpectExec(regexp.QuoteMeta(lockRateRequestLogicalKeySQL)).
		WithArgs(RateRequestAdvisoryLockKey("coingecko", "coingecko:dispatch-91")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(markRateRequestDispatchedSQL)).
		WithArgs(int64(91), "worker-late").
		WillReturnRows(sqlmock.NewRows([]string{"budget_period_id", "provider"}))
	mock.ExpectRollback()

	err := NewDBRepository(db).MarkRateRequestDispatched(context.Background(), 91, "worker-late")
	if !errors.Is(err, ErrRateRequestLeaseNotOwned) {
		t.Fatalf("MarkRateRequestDispatched() error = %v, want ErrRateRequestLeaseNotOwned", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestFinalizeDispatchedRateRequest_OnlyAllowsTerminalCompletion(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	completion := RateRequestCompletion{State: RateRequestStateUnknown, ResponseSnapshotGroup: "snapshot-group-1", ErrorCode: "TIMEOUT", ErrorDetail: "provider outcome could not be determined"}
	mock.ExpectQuery(regexp.QuoteMeta(finalizeDispatchedRateRequestSQL)).
		WithArgs(int64(93), completion.State, completion.ResponseSnapshotGroup, completion.ErrorCode, completion.ErrorDetail).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(93))

	if err := NewDBRepository(db).FinalizeDispatchedRateRequest(context.Background(), 93, completion); err != nil {
		t.Fatalf("FinalizeDispatchedRateRequest() error = %v", err)
	}
	if !strings.Contains(finalizeDispatchedRateRequestSQL, "completed_at = clock_timestamp()") || !strings.Contains(finalizeDispatchedRateRequestSQL, "request_state = 'DISPATCHED'") {
		t.Fatalf("terminal completion SQL lost dispatched-state or completed-at invariant: %s", finalizeDispatchedRateRequestSQL)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestRecoverStaleDispatchedRateRequests_TerminalizesWithoutRefundOrRetry(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(recoverStaleDispatchedRateRequestsSQL)).
		WithArgs(int64((15 * time.Minute).Microseconds()), 25).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	recovered, err := NewDBRepository(db).RecoverStaleDispatchedRateRequests(context.Background(), 15*time.Minute, 25)
	if err != nil || recovered != 2 {
		t.Fatalf("RecoverStaleDispatchedRateRequests() = %d, %v; want 2, nil", recovered, err)
	}
	for _, contract := range []string{"FOR UPDATE SKIP LOCKED", "request_state = 'UNKNOWN'", "completed_at = clock_timestamp()", "DISPATCH_RECOVERY_TIMEOUT"} {
		if !strings.Contains(recoverStaleDispatchedRateRequestsSQL, contract) {
			t.Fatalf("stale-dispatch recovery SQL is missing %q", contract)
		}
	}
	if strings.Contains(recoverStaleDispatchedRateRequestsSQL, "reserved_calls") || strings.Contains(recoverStaleDispatchedRateRequestsSQL, "used_calls") {
		t.Fatalf("stale-dispatch recovery must not refund or consume quota: %s", recoverStaleDispatchedRateRequestsSQL)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestRecoverStaleDispatchedRateRequests_RejectsInvalidWindowOrLimit(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		window time.Duration
		limit  int
	}{{name: "zero window", window: 0, limit: 1}, {name: "zero limit", window: time.Minute, limit: 0}} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewDBRepository(nil).RecoverStaleDispatchedRateRequests(context.Background(), testCase.window, testCase.limit); err == nil {
				t.Fatal("invalid stale-dispatch recovery input must fail before database use")
			}
		})
	}
}

func TestRateRequestLeaseAndTerminalSQLUseClockTimestamp(t *testing.T) {
	for name, statement := range map[string]string{"claim": claimNextRateRequestSQL, "lease": updateClaimedRateRequestSQL, "dispatch": markRateRequestDispatchedSQL, "complete": finalizeDispatchedRateRequestSQL} {
		if !strings.Contains(statement, "clock_timestamp()") || strings.Contains(statement, "NOW()") {
			t.Fatalf("%s SQL must use clock_timestamp without transaction-start NOW: %s", name, statement)
		}
	}
}
