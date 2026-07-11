package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EnqueueCompanyFundValuationJob deduplicates a target dependency under the
// database unique key. Its expected-current guard is captured only after the
// transaction row is locked, so an initial job can safely expect no history.
func (r *DBRepository) EnqueueCompanyFundValuationJob(ctx context.Context, input CompanyFundValuationJobInput) (CompanyFundValuationJobEnqueueResult, error) {
	if err := input.validate(); err != nil {
		return CompanyFundValuationJobEnqueueResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return CompanyFundValuationJobEnqueueResult{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CompanyFundValuationJobEnqueueResult{}, fmt.Errorf("begin valuation job enqueue: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	current, err := lockCompanyFundValuationCurrentState(ctx, tx, input.TransactionID)
	if err != nil {
		return CompanyFundValuationJobEnqueueResult{}, err
	}
	result, err := r.enqueueCompanyFundValuationJobWithLockedCurrentTx(ctx, tx, input, current)
	if err != nil {
		return CompanyFundValuationJobEnqueueResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompanyFundValuationJobEnqueueResult{}, fmt.Errorf("commit valuation job enqueue: %w", err)
	}
	committed = true
	return result, nil
}

func (r *DBRepository) enqueueCompanyFundValuationJobWithLockedCurrentTx(
	ctx context.Context,
	tx *sql.Tx,
	input CompanyFundValuationJobInput,
	current companyFundValuationCurrentState,
) (CompanyFundValuationJobEnqueueResult, error) {
	if err := validateValuationJobCurrentGuard(current.Expectation, current.HistoryID, current.DependencyFingerprint); err != nil {
		return CompanyFundValuationJobEnqueueResult{}, err
	}
	if input.SourceValuationHistoryID != nil {
		var historyID int64
		if err := tx.QueryRowContext(ctx, selectValuationHistoryForValuationJobSQL,
			input.TransactionID,
			*input.SourceValuationHistoryID,
		).Scan(&historyID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return CompanyFundValuationJobEnqueueResult{}, fmt.Errorf("valuation job source history %d does not belong to transaction %d", *input.SourceValuationHistoryID, input.TransactionID)
			}
			return CompanyFundValuationJobEnqueueResult{}, fmt.Errorf("verify valuation job source history: %w", err)
		}
	}

	job, err := scanCompanyFundValuationJob(tx.QueryRowContext(ctx, insertCompanyFundValuationJobSQL,
		input.TransactionID,
		nullableInt64(input.SourceValuationHistoryID),
		input.TriggerKind,
		nullableString(input.TriggerID),
		input.TargetDependencyFingerprint,
		input.PolicyVersion,
		current.Expectation,
		nullableInt64(current.HistoryID),
		nullableString(current.DependencyFingerprint),
	))
	if err == nil {
		return CompanyFundValuationJobEnqueueResult{Job: job, Inserted: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CompanyFundValuationJobEnqueueResult{}, fmt.Errorf("insert valuation job: %w", err)
	}

	existing, err := scanCompanyFundValuationJob(tx.QueryRowContext(ctx, selectCompanyFundValuationJobByTargetSQL,
		input.TransactionID,
		input.TargetDependencyFingerprint,
		input.PolicyVersion,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompanyFundValuationJobEnqueueResult{}, fmt.Errorf("valuation job target conflict did not return an existing job")
		}
		return CompanyFundValuationJobEnqueueResult{}, fmt.Errorf("read existing valuation job: %w", err)
	}
	if conflictField := immutableValuationJobConflict(existing, input); conflictField != "" {
		return CompanyFundValuationJobEnqueueResult{}, fmt.Errorf("valuation job target conflicts on immutable field %s", conflictField)
	}
	return CompanyFundValuationJobEnqueueResult{Job: existing}, nil
}

// ApplyClaimedCompanyFundValuationJob is the worker-only application path. It
// overwrites any caller guard with the one captured durably at enqueue time so
// an expired or obsolete lease can only return Superseded without a write.
func (r *DBRepository) ApplyClaimedCompanyFundValuationJob(
	ctx context.Context,
	lease CompanyFundValuationJobLease,
	input CompanyFundValuationApplyInput,
) (CompanyFundValuationApplyResult, error) {
	guarded, err := lease.guardedValuationApplyInput(input)
	if err != nil {
		return CompanyFundValuationApplyResult{}, err
	}
	return r.ApplyCompanyFundValuation(ctx, guarded)
}

// ClaimNextCompanyFundValuationJob uses PostgreSQL time and SKIP LOCKED. A
// stale lease is reclaimable; completed states are never candidates.
func (r *DBRepository) ClaimNextCompanyFundValuationJob(ctx context.Context, owner string, leaseDuration time.Duration) (*CompanyFundValuationJobLease, error) {
	if err := validateValuationJobLeaseOwner(owner); err != nil {
		return nil, err
	}
	microseconds, err := valuationJobLeaseDurationMicroseconds(leaseDuration)
	if err != nil {
		return nil, err
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin valuation job claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	job, err := scanCompanyFundValuationJob(tx.QueryRowContext(ctx, claimNextCompanyFundValuationJobSQL))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select claimable valuation job: %w", err)
	}
	var leaseExpiresAt time.Time
	if err := tx.QueryRowContext(ctx, updateClaimedCompanyFundValuationJobSQL, job.ID, owner, microseconds).Scan(&job.AttemptCount, &leaseExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrValuationJobClaimLost
		}
		return nil, fmt.Errorf("claim valuation job: %w", err)
	}
	job.State = ValuationJobStateLeased
	job.NextAttemptAt = nil
	job.LeaseOwner = owner
	job.LeaseExpiresAt = valuationJobTimePointer(leaseExpiresAt)
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit valuation job claim: %w", err)
	}
	committed = true
	return &CompanyFundValuationJobLease{Job: job}, nil
}

func (r *DBRepository) RenewCompanyFundValuationJobLease(ctx context.Context, jobID int64, owner string, leaseDuration time.Duration) (time.Time, error) {
	if jobID <= 0 {
		return time.Time{}, fmt.Errorf("valuation job ID must be positive")
	}
	if err := validateValuationJobLeaseOwner(owner); err != nil {
		return time.Time{}, err
	}
	microseconds, err := valuationJobLeaseDurationMicroseconds(leaseDuration)
	if err != nil {
		return time.Time{}, err
	}
	if err := r.requireDB(); err != nil {
		return time.Time{}, err
	}

	var leaseExpiresAt time.Time
	if err := r.db.QueryRowContext(ctx, renewCompanyFundValuationJobLeaseSQL, jobID, owner, microseconds).Scan(&leaseExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrValuationJobLeaseNotOwned
		}
		return time.Time{}, fmt.Errorf("renew valuation job lease: %w", err)
	}
	return leaseExpiresAt, nil
}

// FinalizeCompanyFundValuationJob can only transition a live lease to a
// terminal state or RETRY_WAIT. There is intentionally no terminal-to-pending
// path, so a completed job cannot regress.
func (r *DBRepository) FinalizeCompanyFundValuationJob(ctx context.Context, jobID int64, owner string, outcome ValuationJobFinalizeOutcome, retryAt *time.Time, failureDetail string) error {
	if jobID <= 0 {
		return fmt.Errorf("valuation job ID must be positive")
	}
	if err := validateValuationJobLeaseOwner(owner); err != nil {
		return err
	}
	state, err := validateValuationJobFinalize(outcome, retryAt, failureDetail)
	if err != nil {
		return err
	}
	if err := r.requireDB(); err != nil {
		return err
	}

	var finalizedID int64
	if err := r.db.QueryRowContext(ctx, finalizeCompanyFundValuationJobSQL,
		jobID,
		owner,
		state,
		nullableTime(retryAt),
		nullableString(failureDetail),
	).Scan(&finalizedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrValuationJobLeaseNotOwned
		}
		return fmt.Errorf("finalize valuation job: %w", err)
	}
	return nil
}

func immutableValuationJobConflict(existing CompanyFundValuationJobRecord, input CompanyFundValuationJobInput) string {
	checks := []struct {
		field string
		equal bool
	}{
		{"transaction_id", existing.TransactionID == input.TransactionID},
		{"source_valuation_history_id", equalValuationID(existing.SourceValuationHistoryID, input.SourceValuationHistoryID)},
		{"trigger_kind", existing.TriggerKind == input.TriggerKind},
		{"trigger_id", existing.TriggerID == input.TriggerID},
		{"target_dependency_fingerprint", existing.TargetDependencyFingerprint == input.TargetDependencyFingerprint},
		{"policy_version", existing.PolicyVersion == input.PolicyVersion},
	}
	for _, check := range checks {
		if !check.equal {
			return check.field
		}
	}
	return ""
}
