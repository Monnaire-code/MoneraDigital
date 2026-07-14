package companyfund

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestInvalidateAndEnqueueCompanyFundValuation_IsAtomicAndCapturesPendingGuard(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationInvalidationInput()
	previousInput := newCompanyFundValuationApplyInput()
	previous := valuationHistoryFromInput(previousInput, 601, 7, nil)
	previousID := previous.ID
	pending := valuationHistoryFromInput(input.Valuation, 602, 8, &previousID)
	jobInput := input.Job
	jobInput.SourceValuationHistoryID = &pending.ID
	job := valuationJobRecordFromInput(jobInput, 603, ValuationJobStatePending, 0, nil, nil, nil)
	job.ExpectedCurrentState = ValuationCurrentStateExpectationHistory
	job.ExpectedCurrentHistoryID = &pending.ID
	job.ExpectedCurrentDependencyFingerprint = input.Valuation.DependencyFingerprint

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.Valuation.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRows(input.Valuation.TransactionID, &previousID, previous.DependencyFingerprint))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryByApplyIdentitySQL)).
		WithArgs(input.Valuation.TransactionID, input.Valuation.DependencyFingerprint, input.Valuation.PolicyVersion, input.Valuation.TransitionTrigger).
		WillReturnRows(sqlmock.NewRows(companyFundValuationHistoryColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestValuationHistoryForUpdateSQL)).
		WithArgs(input.Valuation.TransactionID).
		WillReturnRows(companyFundValuationHistoryRows(previous))
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationHistorySQL)).
		WithArgs(valuationHistoryInsertArgs(input.Valuation, 8, &previousID)...).
		WillReturnRows(companyFundValuationHistoryRows(pending))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionValuationProjectionSQL)).
		WithArgs(valuationProjectionArgs(input.Valuation, pending)...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(input.Valuation.TransactionID))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryForValuationJobSQL)).
		WithArgs(input.Valuation.TransactionID, pending.ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(pending.ID))
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationJobSQL)).
		WithArgs(
			input.Valuation.TransactionID,
			pending.ID,
			input.Job.TriggerKind,
			input.Job.TriggerID,
			input.Job.TargetDependencyFingerprint,
			input.Job.PolicyVersion,
			ValuationCurrentStateExpectationHistory,
			pending.ID,
			input.Valuation.DependencyFingerprint,
		).
		WillReturnRows(companyFundValuationJobRows(job))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).InvalidateAndEnqueueCompanyFundValuation(context.Background(), input)
	if err != nil {
		t.Fatalf("InvalidateAndEnqueueCompanyFundValuation() error = %v", err)
	}
	if !result.HistoryInserted || !result.JobInserted || result.History.ID != pending.ID || result.Job.ID != job.ID {
		t.Fatalf("InvalidateAndEnqueueCompanyFundValuation() = %#v, want pending history and job", result)
	}
	if result.Job.ExpectedCurrentHistoryID == nil || *result.Job.ExpectedCurrentHistoryID != pending.ID || result.Job.ExpectedCurrentDependencyFingerprint != input.Valuation.DependencyFingerprint {
		t.Fatalf("job must persist the newly current pending guard, got %#v", result.Job)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestInvalidateAndEnqueueCompanyFundValuation_RollsBackPendingHistoryWhenJobInsertFails(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationInvalidationInput()
	previousInput := newCompanyFundValuationApplyInput()
	previous := valuationHistoryFromInput(previousInput, 611, 4, nil)
	previousID := previous.ID
	pending := valuationHistoryFromInput(input.Valuation, 612, 5, &previousID)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.Valuation.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRows(input.Valuation.TransactionID, &previousID, previous.DependencyFingerprint))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryByApplyIdentitySQL)).
		WillReturnRows(sqlmock.NewRows(companyFundValuationHistoryColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectLatestValuationHistoryForUpdateSQL)).
		WillReturnRows(companyFundValuationHistoryRows(previous))
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationHistorySQL)).
		WillReturnRows(companyFundValuationHistoryRows(pending))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionValuationProjectionSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(input.Valuation.TransactionID))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryForValuationJobSQL)).
		WithArgs(input.Valuation.TransactionID, pending.ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(pending.ID))
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationJobSQL)).
		WillReturnError(errors.New("job insert unavailable"))
	mock.ExpectRollback()

	_, err := NewDBRepository(db).InvalidateAndEnqueueCompanyFundValuation(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "insert valuation job") {
		t.Fatalf("job insertion failure must roll back the pending projection, got %v", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestInvalidateAndEnqueueCompanyFundValuation_ReusesExactPendingAndJob(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationInvalidationInput()
	pending := valuationHistoryFromInput(input.Valuation, 621, 8, nil)
	pendingID := pending.ID
	jobInput := input.Job
	jobInput.SourceValuationHistoryID = &pendingID
	job := valuationJobRecordFromInput(jobInput, 622, ValuationJobStatePending, 0, nil, nil, nil)
	job.ExpectedCurrentState = ValuationCurrentStateExpectationHistory
	job.ExpectedCurrentHistoryID = &pendingID
	job.ExpectedCurrentDependencyFingerprint = input.Valuation.DependencyFingerprint

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.Valuation.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRows(input.Valuation.TransactionID, &pendingID, input.Valuation.DependencyFingerprint))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryByApplyIdentitySQL)).
		WithArgs(input.Valuation.TransactionID, input.Valuation.DependencyFingerprint, input.Valuation.PolicyVersion, input.Valuation.TransitionTrigger).
		WillReturnRows(companyFundValuationHistoryRows(pending))
	mock.ExpectQuery(regexp.QuoteMeta(selectValuationHistoryForValuationJobSQL)).
		WithArgs(input.Valuation.TransactionID, pending.ID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(pending.ID))
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundValuationJobSQL)).
		WillReturnRows(sqlmock.NewRows(companyFundValuationJobColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundValuationJobByTargetSQL)).
		WithArgs(input.Valuation.TransactionID, input.Job.TargetDependencyFingerprint, input.Job.PolicyVersion).
		WillReturnRows(companyFundValuationJobRows(job))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).InvalidateAndEnqueueCompanyFundValuation(context.Background(), input)
	if err != nil || result.HistoryInserted || result.JobInserted || result.History.ID != pending.ID || result.Job.ID != job.ID {
		t.Fatalf("exact invalidation retry must only read back durable state, got %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestApplyClaimedCompanyFundValuationJob_ExpiredGuardIsSupersededNoOp(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationApplyInput()
	expectedHistoryID := int64(631)
	jobInput := newCompanyFundValuationJobInput()
	jobInput.TransactionID = input.TransactionID
	jobInput.TargetDependencyFingerprint = input.DependencyFingerprint
	jobInput.PolicyVersion = input.PolicyVersion
	job := valuationJobRecordFromInput(jobInput, 632, ValuationJobStateLeased, 1, nil, nil, nil)
	job.ExpectedCurrentState = ValuationCurrentStateExpectationHistory
	job.ExpectedCurrentHistoryID = &expectedHistoryID
	job.ExpectedCurrentDependencyFingerprint = input.DependencyFingerprint
	lease := CompanyFundValuationJobLease{Job: job}
	newerHistoryID := int64(633)
	newerFingerprint := strings.Repeat("c", 64)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, &newerHistoryID, newerFingerprint))
	mock.ExpectRollback()

	result, err := NewDBRepository(db).ApplyClaimedCompanyFundValuationJob(context.Background(), lease, input)
	if err != nil || !result.Superseded || result.Inserted || result.History.ID != 0 {
		t.Fatalf("expired stale job must be superseded without a history/projection write, got %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestApplyClaimedCompanyFundValuationJob_InitialNoneGuardIsSupersededWhenCurrentAppears(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newCompanyFundValuationApplyInput()
	jobInput := newCompanyFundValuationJobInput()
	jobInput.TransactionID = input.TransactionID
	jobInput.TargetDependencyFingerprint = input.DependencyFingerprint
	jobInput.PolicyVersion = input.PolicyVersion
	job := valuationJobRecordFromInput(jobInput, 641, ValuationJobStateLeased, 1, nil, nil, nil)
	job.ExpectedCurrentState = ValuationCurrentStateExpectationNone
	currentHistoryID := int64(642)
	currentFingerprint := strings.Repeat("d", 64)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRows(input.TransactionID, &currentHistoryID, currentFingerprint))
	mock.ExpectRollback()

	result, err := NewDBRepository(db).ApplyClaimedCompanyFundValuationJob(
		context.Background(),
		CompanyFundValuationJobLease{Job: job},
		input,
	)
	if err != nil || !result.Superseded || result.Inserted || result.History.ID != 0 {
		t.Fatalf("initial NONE guard must supersede when another valuation is current, got %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestApplyClaimedCompanyFundValuationJob_RejectsCallerSuppliedGuard(t *testing.T) {
	input := newCompanyFundValuationApplyInput()
	state := ValuationCurrentStateExpectationNone
	input.ExpectedCurrentState = &state
	jobInput := newCompanyFundValuationJobInput()
	jobInput.TransactionID = input.TransactionID
	jobInput.TargetDependencyFingerprint = input.DependencyFingerprint
	jobInput.PolicyVersion = input.PolicyVersion
	job := valuationJobRecordFromInput(jobInput, 651, ValuationJobStateLeased, 1, nil, nil, nil)
	job.ExpectedCurrentState = ValuationCurrentStateExpectationNone

	_, err := NewDBRepository(nil).ApplyClaimedCompanyFundValuationJob(
		context.Background(),
		CompanyFundValuationJobLease{Job: job},
		input,
	)
	if err == nil || !strings.Contains(err.Error(), "repository-owned") {
		t.Fatalf("worker apply must reject a caller-supplied guard, got %v", err)
	}
}

func TestCompanyFundValuationInvalidationInput_RequiresCanonicalPendingShape(t *testing.T) {
	input := newCompanyFundValuationInvalidationInput()
	input.Valuation.Result.Status = USDValuationStatusFinal
	if _, err := NewDBRepository(nil).InvalidateAndEnqueueCompanyFundValuation(context.Background(), input); err == nil {
		t.Fatal("invalidation must require a stale REVALUATION_PENDING history")
	}
}

func newCompanyFundValuationInvalidationInput() CompanyFundValuationInvalidationInput {
	valuation := newCompanyFundValuationApplyInput()
	valuation.Result.Status = USDValuationStatusStale
	valuation.Result.Reason = USDValuationReasonRevaluationPending
	valuation.Result.Value = nil
	valuation.Result.ProviderReportedUSD = nil
	valuation.Result.UnitPrice = decimal.Zero
	valuation.CalculatedUSDValue = nil
	valuation.TransitionTrigger = "DEPENDENCY_INVALIDATED"
	return CompanyFundValuationInvalidationInput{
		Valuation: valuation,
		Job: CompanyFundValuationJobInput{
			TransactionID:               valuation.TransactionID,
			TriggerKind:                 "RATE_SNAPSHOT_CORRECTED",
			TriggerID:                   "snapshot:81",
			TargetDependencyFingerprint: valuation.DependencyFingerprint,
			PolicyVersion:               valuation.PolicyVersion,
		},
	}
}
