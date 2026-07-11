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

func TestEnqueueCompanyFundValuationJob_VerifiesSameTransactionSourceAndIdempotency(t *testing.T) {
	t.Run("inserts after same-transaction source check", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()

		input := newCompanyFundValuationJobInput()
		job := valuationJobRecordFromInput(input, 401, ValuationJobStatePending, 0, nil, nil, nil)
		job.ExpectedCurrentState = ValuationCurrentStateExpectationHistory
		job.ExpectedCurrentHistoryID = input.SourceValuationHistoryID
		job.ExpectedCurrentDependencyFingerprint = input.TargetDependencyFingerprint
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
			WithArgs(input.TransactionID).
			WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, input.SourceValuationHistoryID, input.TargetDependencyFingerprint))
		mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryForValuationJobSQL)).
			WithArgs(input.TransactionID, *input.SourceValuationHistoryID).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*input.SourceValuationHistoryID))
		mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationJobSQL)).
			WithArgs(
				input.TransactionID,
				*input.SourceValuationHistoryID,
				input.TriggerKind,
				input.TriggerID,
				input.TargetDependencyFingerprint,
				input.PolicyVersion,
				ValuationCurrentStateExpectationHistory,
				*input.SourceValuationHistoryID,
				input.TargetDependencyFingerprint,
			).WillReturnRows(companyFundValuationJobRows(job))
		mock.ExpectCommit()

		result, err := NewDBRepository(db).EnqueueCompanyFundValuationJob(context.Background(), input)
		if err != nil || !result.Inserted || result.Job.ID != job.ID {
			t.Fatalf("EnqueueCompanyFundValuationJob() = %#v, %v; want inserted job", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("exact target reads existing immutable job", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()

		input := newCompanyFundValuationJobInput()
		job := valuationJobRecordFromInput(input, 402, ValuationJobStateRetryWait, 2, nil, nil, nil)
		job.ExpectedCurrentState = ValuationCurrentStateExpectationHistory
		job.ExpectedCurrentHistoryID = input.SourceValuationHistoryID
		job.ExpectedCurrentDependencyFingerprint = input.TargetDependencyFingerprint
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
			WithArgs(input.TransactionID).
			WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, input.SourceValuationHistoryID, input.TargetDependencyFingerprint))
		mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryForValuationJobSQL)).
			WithArgs(input.TransactionID, *input.SourceValuationHistoryID).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*input.SourceValuationHistoryID))
		mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationJobSQL)).
			WillReturnRows(sqlmock.NewRows(companyFundValuationJobColumnNames()))
		mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundValuationJobByTargetSQL)).
			WithArgs(input.TransactionID, input.TargetDependencyFingerprint, input.PolicyVersion).
			WillReturnRows(companyFundValuationJobRows(job))
		mock.ExpectCommit()

		result, err := NewDBRepository(db).EnqueueCompanyFundValuationJob(context.Background(), input)
		if err != nil || result.Inserted || result.Job.ID != job.ID {
			t.Fatalf("EnqueueCompanyFundValuationJob() = %#v, %v; want existing job", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("same target rejects changed immutable trigger metadata", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()

		input := newCompanyFundValuationJobInput()
		existing := valuationJobRecordFromInput(input, 403, ValuationJobStatePending, 0, nil, nil, nil)
		existing.ExpectedCurrentState = ValuationCurrentStateExpectationHistory
		existing.ExpectedCurrentHistoryID = input.SourceValuationHistoryID
		existing.ExpectedCurrentDependencyFingerprint = input.TargetDependencyFingerprint
		existing.TriggerID = "snapshot:other"
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
			WithArgs(input.TransactionID).
			WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, input.SourceValuationHistoryID, input.TargetDependencyFingerprint))
		mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryForValuationJobSQL)).
			WithArgs(input.TransactionID, *input.SourceValuationHistoryID).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*input.SourceValuationHistoryID))
		mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationJobSQL)).
			WillReturnRows(sqlmock.NewRows(companyFundValuationJobColumnNames()))
		mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundValuationJobByTargetSQL)).
			WithArgs(input.TransactionID, input.TargetDependencyFingerprint, input.PolicyVersion).
			WillReturnRows(companyFundValuationJobRows(existing))
		mock.ExpectRollback()

		if _, err := NewDBRepository(db).EnqueueCompanyFundValuationJob(context.Background(), input); err == nil || !strings.Contains(err.Error(), "immutable field trigger_id") {
			t.Fatalf("changed immutable job metadata must fail, got %v", err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("rejects foreign source history before enqueue", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()

		input := newCompanyFundValuationJobInput()
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
			WithArgs(input.TransactionID).
			WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, nil, ""))
		mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryForValuationJobSQL)).
			WithArgs(input.TransactionID, *input.SourceValuationHistoryID).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectRollback()

		if _, err := NewDBRepository(db).EnqueueCompanyFundValuationJob(context.Background(), input); err == nil || !strings.Contains(err.Error(), "does not belong") {
			t.Fatalf("foreign source history must fail, got %v", err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("initial job persists an explicit no-current guard", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()

		input := newCompanyFundValuationJobInput()
		input.SourceValuationHistoryID = nil
		job := valuationJobRecordFromInput(input, 404, ValuationJobStatePending, 0, nil, nil, nil)
		job.ExpectedCurrentState = ValuationCurrentStateExpectationNone
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
			WithArgs(input.TransactionID).
			WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, nil, ""))
		mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationJobSQL)).
			WithArgs(
				input.TransactionID,
				nil,
				input.TriggerKind,
				input.TriggerID,
				input.TargetDependencyFingerprint,
				input.PolicyVersion,
				ValuationCurrentStateExpectationNone,
				nil,
				nil,
			).WillReturnRows(companyFundValuationJobRows(job))
		mock.ExpectCommit()

		result, err := NewDBRepository(db).EnqueueCompanyFundValuationJob(context.Background(), input)
		if err != nil || !result.Inserted || result.Job.ExpectedCurrentState != ValuationCurrentStateExpectationNone || result.Job.ExpectedCurrentHistoryID != nil {
			t.Fatalf("initial job must persist NONE guard, got %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})
}

func TestClaimNextCompanyFundValuationJob_RecoversExpiredLeaseAndSkipsLocked(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationJobInput()
	pastExpiry := time.Date(2026, time.July, 10, 2, 0, 0, 0, time.UTC)
	newExpiry := time.Date(2026, time.July, 10, 3, 5, 0, 0, time.UTC)
	job := valuationJobRecordFromInput(input, 403, ValuationJobStateLeased, 2, nil, &pastExpiry, nil)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(claimNextCompanyFundValuationJobSQL)).
		WillReturnRows(companyFundValuationJobRows(job))
	mock.ExpectQuery(regexp.QuoteMeta(updateClaimedCompanyFundValuationJobSQL)).
		WithArgs(job.ID, "worker-2", int64((5 * time.Minute).Microseconds())).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count", "lease_expires_at"}).AddRow(3, newExpiry))
	mock.ExpectCommit()

	lease, err := NewDBRepository(db).ClaimNextCompanyFundValuationJob(context.Background(), "worker-2", 5*time.Minute)
	if err != nil || lease == nil || lease.Job.State != ValuationJobStateLeased || lease.Job.AttemptCount != 3 || lease.Job.LeaseOwner != "worker-2" || lease.Job.LeaseExpiresAt == nil || !lease.Job.LeaseExpiresAt.Equal(newExpiry) {
		t.Fatalf("ClaimNextCompanyFundValuationJob() = %#v, %v; want recovered lease", lease, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestClaimNextCompanyFundValuationJob_LeavesTerminalOrUnavailableWorkUntouched(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(claimNextCompanyFundValuationJobSQL)).
		WillReturnRows(sqlmock.NewRows(companyFundValuationJobColumnNames()))
	mock.ExpectRollback()

	lease, err := NewDBRepository(db).ClaimNextCompanyFundValuationJob(context.Background(), "worker-4", time.Minute)
	if err != nil || lease != nil {
		t.Fatalf("ClaimNextCompanyFundValuationJob() = %#v, %v; want no claim", lease, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestCompanyFundValuationJobLeaseOwnershipAndRetry(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	expiresAt := time.Date(2026, time.July, 10, 3, 5, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(renewCompanyFundValuationJobLeaseSQL)).
		WithArgs(int64(404), "worker-3", int64(time.Minute.Microseconds())).
		WillReturnRows(sqlmock.NewRows([]string{"lease_expires_at"}).AddRow(expiresAt))
	if actual, err := NewDBRepository(db).RenewCompanyFundValuationJobLease(context.Background(), 404, "worker-3", time.Minute); err != nil || !actual.Equal(expiresAt) {
		t.Fatalf("RenewCompanyFundValuationJobLease() = %v, %v; want %v, nil", actual, err, expiresAt)
	}

	retryAt := expiresAt.Add(time.Minute)
	mock.ExpectQuery(regexp.QuoteMeta(finalizeCompanyFundValuationJobSQL)).
		WithArgs(int64(404), "worker-3", ValuationJobStateRetryWait, retryAt, "provider timeout").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(404))
	if err := NewDBRepository(db).FinalizeCompanyFundValuationJob(context.Background(), 404, "worker-3", ValuationJobFinalizeRetry, &retryAt, "provider timeout"); err != nil {
		t.Fatalf("FinalizeCompanyFundValuationJob(retry): %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(finalizeCompanyFundValuationJobSQL)).
		WithArgs(int64(404), "late-worker", ValuationJobStateSucceeded, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	err := NewDBRepository(db).FinalizeCompanyFundValuationJob(context.Background(), 404, "late-worker", ValuationJobFinalizeSucceeded, nil, "")
	if !errors.Is(err, ErrValuationJobLeaseNotOwned) {
		t.Fatalf("FinalizeCompanyFundValuationJob(lost lease) = %v, want ErrValuationJobLeaseNotOwned", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestCompanyFundValuationJobFinalize_RejectsUnsafeRetryShapesBeforeDatabaseUse(t *testing.T) {
	retryAt := time.Date(2026, time.July, 10, 3, 5, 0, 0, time.UTC)
	repository := NewDBRepository(nil)
	if err := repository.FinalizeCompanyFundValuationJob(context.Background(), 405, "worker-5", ValuationJobFinalizeRetry, nil, "retry"); err == nil {
		t.Fatal("retry without next attempt time must fail before database use")
	}
	if err := repository.FinalizeCompanyFundValuationJob(context.Background(), 405, "worker-5", ValuationJobFinalizeSucceeded, &retryAt, ""); err == nil {
		t.Fatal("terminal outcome with retry time must fail before database use")
	}
}

func TestCompanyFundValuationJobSQL_UsesSameTransactionFKAndSafeLeaseTransitions(t *testing.T) {
	allSQL := strings.Join([]string{
		selectCompanyFundTransactionForValuationSQL,
		selectValuationHistoryForValuationJobSQL,
		insertCompanyFundValuationJobSQL,
		selectCompanyFundValuationJobByTargetSQL,
		claimNextCompanyFundValuationJobSQL,
		updateClaimedCompanyFundValuationJobSQL,
		renewCompanyFundValuationJobLeaseSQL,
		finalizeCompanyFundValuationJobSQL,
	}, "\n")
	for _, contract := range []string{
		"transaction_id = $1\n  AND id = $2",
		"FOR UPDATE OF transaction",
		"expected_current_state",
		"ON CONFLICT (transaction_id, target_dependency_fingerprint, policy_version) DO NOTHING",
		"FOR UPDATE SKIP LOCKED",
		"job_state = 'RETRY_WAIT' AND next_attempt_at <= clock_timestamp()",
		"job_state = 'LEASED' AND lease_expires_at <= clock_timestamp()",
		"lease_owner = $2",
		"lease_expires_at > clock_timestamp()",
		"completed_at = CASE WHEN $3 IN ('SUCCEEDED', 'SUPERSEDED', 'FAILED') THEN clock_timestamp() ELSE NULL END",
	} {
		if !strings.Contains(allSQL, contract) {
			t.Fatalf("valuation job SQL is missing %q", contract)
		}
	}
	if strings.Contains(allSQL, "NOW()") {
		t.Fatalf("valuation job lease SQL must use PostgreSQL clock_timestamp: %s", allSQL)
	}
	for _, terminal := range []string{"SUCCEEDED", "SUPERSEDED", "FAILED"} {
		if strings.Contains(claimNextCompanyFundValuationJobSQL, terminal) {
			t.Fatalf("terminal job state %q must never be claimable: %s", terminal, claimNextCompanyFundValuationJobSQL)
		}
	}
}
