package companyfund

import (
	"database/sql/driver"
	"strings"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newCompanyFundValuationJobInput() CompanyFundValuationJobInput {
	sourceHistoryID := int64(301)
	return CompanyFundValuationJobInput{
		TransactionID:               71,
		SourceValuationHistoryID:    &sourceHistoryID,
		TriggerKind:                 "RATE_SNAPSHOT_CORRECTED",
		TriggerID:                   "snapshot:81",
		TargetDependencyFingerprint: strings.Repeat("e", 64),
		PolicyVersion:               "valuation-v1",
	}
}

func valuationJobRecordFromInput(
	input CompanyFundValuationJobInput,
	id int64,
	state ValuationJobState,
	attemptCount int,
	nextAttemptAt, leaseExpiresAt, completedAt *time.Time,
) CompanyFundValuationJobRecord {
	leaseOwner := ""
	if leaseExpiresAt != nil {
		leaseOwner = "previous-worker"
	}
	return CompanyFundValuationJobRecord{
		ID:                          id,
		TransactionID:               input.TransactionID,
		SourceValuationHistoryID:    input.SourceValuationHistoryID,
		TriggerKind:                 input.TriggerKind,
		TriggerID:                   input.TriggerID,
		TargetDependencyFingerprint: input.TargetDependencyFingerprint,
		PolicyVersion:               input.PolicyVersion,
		ExpectedCurrentState:        ValuationCurrentStateExpectationNone,
		ExpectedCurrentHistoryID:    nil,
		State:                       state,
		AttemptCount:                attemptCount,
		NextAttemptAt:               nextAttemptAt,
		LeaseOwner:                  leaseOwner,
		LeaseExpiresAt:              leaseExpiresAt,
		CompletedAt:                 completedAt,
		CreatedAt:                   time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC),
	}
}

func companyFundValuationJobColumnNames() []string {
	return []string{
		"id", "transaction_id", "source_valuation_history_id", "trigger_kind", "trigger_id",
		"target_dependency_fingerprint", "policy_version", "expected_current_state", "expected_current_history_id",
		"expected_current_dependency_fingerprint", "job_state", "attempt_count",
		"next_attempt_at", "lease_owner", "lease_expires_at", "last_error", "completed_at", "created_at",
	}
}

func companyFundValuationJobRows(job CompanyFundValuationJobRecord) *sqlmock.Rows {
	return sqlmock.NewRows(companyFundValuationJobColumnNames()).AddRow(
		job.ID,
		job.TransactionID,
		valuationJobTestID(job.SourceValuationHistoryID),
		job.TriggerKind,
		job.TriggerID,
		job.TargetDependencyFingerprint,
		job.PolicyVersion,
		string(job.ExpectedCurrentState),
		valuationJobTestID(job.ExpectedCurrentHistoryID),
		job.ExpectedCurrentDependencyFingerprint,
		string(job.State),
		job.AttemptCount,
		valuationJobTestTime(job.NextAttemptAt),
		job.LeaseOwner,
		valuationJobTestTime(job.LeaseExpiresAt),
		job.LastError,
		valuationJobTestTime(job.CompletedAt),
		job.CreatedAt,
	)
}

func valuationJobTestID(value *int64) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func valuationJobTestTime(value *time.Time) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}
