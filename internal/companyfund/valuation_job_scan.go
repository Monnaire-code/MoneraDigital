package companyfund

import (
	"database/sql"
	"time"
)

type valuationJobScanner interface {
	Scan(dest ...any) error
}

func scanCompanyFundValuationJob(row valuationJobScanner) (CompanyFundValuationJobRecord, error) {
	var (
		job                      CompanyFundValuationJobRecord
		sourceHistoryID          sql.NullInt64
		expectedCurrentHistoryID sql.NullInt64
		nextAttemptAt            sql.NullTime
		leaseExpiresAt           sql.NullTime
		completedAt              sql.NullTime
	)
	if err := row.Scan(
		&job.ID,
		&job.TransactionID,
		&sourceHistoryID,
		&job.TriggerKind,
		&job.TriggerID,
		&job.TargetDependencyFingerprint,
		&job.PolicyVersion,
		&job.ExpectedCurrentState,
		&expectedCurrentHistoryID,
		&job.ExpectedCurrentDependencyFingerprint,
		&job.State,
		&job.AttemptCount,
		&nextAttemptAt,
		&job.LeaseOwner,
		&leaseExpiresAt,
		&job.LastError,
		&completedAt,
		&job.CreatedAt,
	); err != nil {
		return CompanyFundValuationJobRecord{}, err
	}
	job.SourceValuationHistoryID = nullableValuationID(sourceHistoryID)
	job.ExpectedCurrentHistoryID = nullableValuationID(expectedCurrentHistoryID)
	job.NextAttemptAt = nullableValuationTime(nextAttemptAt)
	job.LeaseExpiresAt = nullableValuationTime(leaseExpiresAt)
	job.CompletedAt = nullableValuationTime(completedAt)
	return job, nil
}

func valuationJobTimePointer(value time.Time) *time.Time {
	return &value
}
