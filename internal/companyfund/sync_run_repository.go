package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const companyFundSyncRunReturnedColumns = `
id,
channel,
sync_kind,
window_key,
window_start,
window_end,
status,
checkpoint::TEXT,
candidates_seen,
events_created,
transactions_upserted,
transactions_skipped,
attempt_count,
lease_owner,
lease_expires_at,
started_at,
completed_at,
next_attempt_at,
last_error,
created_at,
updated_at`

const insertCompanyFundSyncRunSQL = `
INSERT INTO company_fund_sync_runs (
	channel,
	sync_kind,
	window_key,
	window_start,
	window_end,
	checkpoint
) VALUES ($1, $2, $3, $4, $5, $6::jsonb)
ON CONFLICT (channel, sync_kind, window_key) DO NOTHING
RETURNING ` + companyFundSyncRunReturnedColumns

const selectCompanyFundSyncRunByWindowSQL = `
SELECT ` + companyFundSyncRunReturnedColumns + `
FROM company_fund_sync_runs
WHERE channel = $1
	AND sync_kind = $2
	AND window_key = $3`

const claimNextCompanyFundSyncRunSQL = `
SELECT ` + companyFundSyncRunReturnedColumns + `
FROM company_fund_sync_runs
WHERE channel = $1
	AND sync_kind = $2
	AND (
		status = 'PENDING'
	OR (status IN ('FAILED', 'PARTIAL') AND next_attempt_at IS NOT NULL AND next_attempt_at <= NOW())
	OR (status = 'LEASED' AND lease_expires_at <= NOW())
	)
ORDER BY window_start, id
FOR UPDATE SKIP LOCKED
LIMIT 1`

const updateClaimedCompanyFundSyncRunSQL = `
UPDATE company_fund_sync_runs
SET status = 'LEASED',
	lease_owner = $2,
	lease_expires_at = NOW() + ($3::bigint * INTERVAL '1 microsecond'),
	next_attempt_at = NULL,
	attempt_count = attempt_count + 1,
	started_at = COALESCE(started_at, NOW()),
	completed_at = NULL,
	last_error = NULL,
	updated_at = NOW()
WHERE id = $1
	AND (
		status = 'PENDING'
		OR (status IN ('FAILED', 'PARTIAL') AND next_attempt_at IS NOT NULL AND next_attempt_at <= NOW())
		OR (status = 'LEASED' AND lease_expires_at <= NOW())
	)
RETURNING attempt_count, lease_expires_at`

const renewCompanyFundSyncRunLeaseSQL = `
UPDATE company_fund_sync_runs
SET lease_expires_at = NOW() + ($3::bigint * INTERVAL '1 microsecond'),
	updated_at = NOW()
WHERE id = $1
	AND status = 'LEASED'
	AND lease_owner = $2
	AND lease_expires_at > NOW()
RETURNING lease_expires_at`

const updateCompanyFundSyncRunProgressSQL = `
UPDATE company_fund_sync_runs
SET checkpoint = COALESCE($3::jsonb, checkpoint),
	candidates_seen = candidates_seen + $4,
	events_created = events_created + $5,
	transactions_upserted = transactions_upserted + $6,
	transactions_skipped = transactions_skipped + $7,
	updated_at = NOW()
WHERE id = $1
	AND status = 'LEASED'
	AND lease_owner = $2
	AND lease_expires_at > NOW()
RETURNING checkpoint::TEXT,
	candidates_seen,
	events_created,
	transactions_upserted,
	transactions_skipped`

const finalizeCompanyFundSyncRunSQL = `
UPDATE company_fund_sync_runs
SET status = $3,
	completed_at = NOW(),
	next_attempt_at = $4,
	lease_owner = NULL,
	lease_expires_at = NULL,
	last_error = $5,
	updated_at = NOW()
WHERE id = $1
	AND status = 'LEASED'
	AND lease_owner = $2
	AND lease_expires_at > NOW()
	AND ($3 NOT IN ('FAILED', 'PARTIAL') OR ($4 IS NOT NULL AND $4 > NOW()))
RETURNING id`

// CreateCompanyFundSyncRun creates one independently idempotent reconciliation
// window. A duplicate reads the durable existing run and rejects any attempt
// to reuse the unique key with a different immutable time window.
func (r *DBRepository) CreateCompanyFundSyncRun(ctx context.Context, input CompanyFundSyncRunInput) (CompanyFundSyncRunCreateResult, error) {
	canonical, err := input.canonical()
	if err != nil {
		return CompanyFundSyncRunCreateResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return CompanyFundSyncRunCreateResult{}, err
	}

	run, err := scanCompanyFundSyncRun(r.db.QueryRowContext(ctx, insertCompanyFundSyncRunSQL,
		canonical.Channel,
		canonical.SyncKind,
		canonical.WindowKey,
		canonical.WindowStart,
		canonical.WindowEnd,
		string(canonical.Checkpoint),
	))
	if err == nil {
		return CompanyFundSyncRunCreateResult{Run: run, Inserted: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CompanyFundSyncRunCreateResult{}, fmt.Errorf("insert company-fund sync run: %w", err)
	}

	existing, err := scanCompanyFundSyncRun(r.db.QueryRowContext(ctx, selectCompanyFundSyncRunByWindowSQL,
		canonical.Channel,
		canonical.SyncKind,
		canonical.WindowKey,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompanyFundSyncRunCreateResult{}, fmt.Errorf("company-fund sync-run unique conflict did not return an existing run")
		}
		return CompanyFundSyncRunCreateResult{}, fmt.Errorf("read existing company-fund sync run: %w", err)
	}
	if field := immutableCompanyFundSyncRunConflict(existing, canonical); field != "" {
		return CompanyFundSyncRunCreateResult{}, fmt.Errorf("company-fund sync-run window identity conflicts on immutable field %s", field)
	}
	return CompanyFundSyncRunCreateResult{Run: existing}, nil
}

// ClaimNextCompanyFundSyncRun commits its row claim before returning, so all
// provider HTTP work can occur outside the short SKIP LOCKED transaction.
func (r *DBRepository) ClaimNextCompanyFundSyncRun(ctx context.Context, scope CompanyFundSyncRunClaimScope, owner string, leaseDuration time.Duration) (*CompanyFundSyncRun, error) {
	canonicalScope, err := scope.canonical()
	if err != nil {
		return nil, err
	}
	if err := validateCompanyFundSyncLeaseOwner(owner); err != nil {
		return nil, err
	}
	microseconds, err := companyFundSyncLeaseDurationMicroseconds(leaseDuration)
	if err != nil {
		return nil, err
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin company-fund sync-run claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	run, err := scanCompanyFundSyncRun(tx.QueryRowContext(ctx, claimNextCompanyFundSyncRunSQL, canonicalScope.Channel, canonicalScope.SyncKind))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select claimable company-fund sync run: %w", err)
	}
	var expiresAt time.Time
	if err := tx.QueryRowContext(ctx, updateClaimedCompanyFundSyncRunSQL, run.ID, owner, microseconds).Scan(&run.AttemptCount, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCompanyFundSyncRunClaimLost
		}
		return nil, fmt.Errorf("claim company-fund sync run: %w", err)
	}
	run.Status = CompanyFundSyncRunStatusLeased
	run.LeaseOwner = owner
	run.LeaseExpiresAt = companyFundSyncRunTimePointer(expiresAt)
	run.NextAttemptAt = nil
	run.CompletedAt = nil
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit company-fund sync-run claim: %w", err)
	}
	committed = true
	return &run, nil
}

func (r *DBRepository) RenewCompanyFundSyncRunLease(ctx context.Context, runID int64, owner string, leaseDuration time.Duration) (time.Time, error) {
	if runID <= 0 {
		return time.Time{}, fmt.Errorf("company-fund sync-run ID must be positive")
	}
	if err := validateCompanyFundSyncLeaseOwner(owner); err != nil {
		return time.Time{}, err
	}
	microseconds, err := companyFundSyncLeaseDurationMicroseconds(leaseDuration)
	if err != nil {
		return time.Time{}, err
	}
	if err := r.requireDB(); err != nil {
		return time.Time{}, err
	}

	var expiresAt time.Time
	if err := r.db.QueryRowContext(ctx, renewCompanyFundSyncRunLeaseSQL, runID, owner, microseconds).Scan(&expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrCompanyFundSyncRunLeaseNotOwned
		}
		return time.Time{}, fmt.Errorf("renew company-fund sync-run lease: %w", err)
	}
	return expiresAt, nil
}

// UpdateCompanyFundSyncRunProgress atomically replaces an optional canonical
// checkpoint and adds monotonic counters while the caller owns a live lease.
func (r *DBRepository) UpdateCompanyFundSyncRunProgress(ctx context.Context, runID int64, owner string, update CompanyFundSyncRunProgressUpdate) (CompanyFundSyncRunProgress, error) {
	if runID <= 0 {
		return CompanyFundSyncRunProgress{}, fmt.Errorf("company-fund sync-run ID must be positive")
	}
	if err := validateCompanyFundSyncLeaseOwner(owner); err != nil {
		return CompanyFundSyncRunProgress{}, err
	}
	if err := update.validate(); err != nil {
		return CompanyFundSyncRunProgress{}, err
	}
	if err := r.requireDB(); err != nil {
		return CompanyFundSyncRunProgress{}, err
	}
	checkpoint, err := update.canonicalCheckpoint()
	if err != nil {
		return CompanyFundSyncRunProgress{}, err
	}

	var (
		progress       CompanyFundSyncRunProgress
		checkpointText string
	)
	if err := r.db.QueryRowContext(ctx, updateCompanyFundSyncRunProgressSQL,
		runID,
		owner,
		nullableJSON(checkpoint),
		update.CandidatesSeenDelta,
		update.EventsCreatedDelta,
		update.TransactionsUpsertedDelta,
		update.TransactionsSkippedDelta,
	).Scan(
		&checkpointText,
		&progress.CandidatesSeen,
		&progress.EventsCreated,
		&progress.TransactionsUpserted,
		&progress.TransactionsSkipped,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompanyFundSyncRunProgress{}, ErrCompanyFundSyncRunLeaseNotOwned
		}
		return CompanyFundSyncRunProgress{}, fmt.Errorf("update company-fund sync-run progress: %w", err)
	}
	canonicalCheckpoint, err := canonicalCompanyFundSyncCheckpoint([]byte(checkpointText))
	if err != nil {
		return CompanyFundSyncRunProgress{}, fmt.Errorf("read canonical company-fund sync-run checkpoint: %w", err)
	}
	progress.Checkpoint = canonicalCheckpoint
	return progress, nil
}

// FinalizeCompanyFundSyncRun terminalizes a successful/skipped window or
// returns a failed/partial window to the durable retry queue at retryAt.
func (r *DBRepository) FinalizeCompanyFundSyncRun(ctx context.Context, runID int64, owner string, outcome CompanyFundSyncRunFinalizeOutcome, retryAt *time.Time, failureDetail string) error {
	if runID <= 0 {
		return fmt.Errorf("company-fund sync-run ID must be positive")
	}
	if err := validateCompanyFundSyncLeaseOwner(owner); err != nil {
		return err
	}
	status, err := validateCompanyFundSyncFinalize(outcome, retryAt, failureDetail)
	if err != nil {
		return err
	}
	if err := r.requireDB(); err != nil {
		return err
	}

	var finalizedID int64
	if err := r.db.QueryRowContext(ctx, finalizeCompanyFundSyncRunSQL,
		runID,
		owner,
		status,
		nullableTime(retryAt),
		nullableString(failureDetail),
	).Scan(&finalizedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCompanyFundSyncRunLeaseNotOwned
		}
		return fmt.Errorf("finalize company-fund sync run: %w", err)
	}
	return nil
}

func immutableCompanyFundSyncRunConflict(existing CompanyFundSyncRun, input CompanyFundSyncRunInput) string {
	checks := []struct {
		field string
		equal bool
	}{
		{"channel", existing.Channel == input.Channel},
		{"sync_kind", existing.SyncKind == input.SyncKind},
		{"window_key", existing.WindowKey == input.WindowKey},
		{"window_start", existing.WindowStart.Equal(input.WindowStart)},
		{"window_end", existing.WindowEnd.Equal(input.WindowEnd)},
	}
	for _, check := range checks {
		if !check.equal {
			return check.field
		}
	}
	return ""
}

func scanCompanyFundSyncRun(row *sql.Row) (CompanyFundSyncRun, error) {
	var (
		run            CompanyFundSyncRun
		channel        string
		status         string
		checkpointText string
		leaseOwner     sql.NullString
		leaseExpiresAt sql.NullTime
		startedAt      sql.NullTime
		completedAt    sql.NullTime
		nextAttemptAt  sql.NullTime
		lastError      sql.NullString
	)
	if err := row.Scan(
		&run.ID,
		&channel,
		&run.SyncKind,
		&run.WindowKey,
		&run.WindowStart,
		&run.WindowEnd,
		&status,
		&checkpointText,
		&run.CandidatesSeen,
		&run.EventsCreated,
		&run.TransactionsUpserted,
		&run.TransactionsSkipped,
		&run.AttemptCount,
		&leaseOwner,
		&leaseExpiresAt,
		&startedAt,
		&completedAt,
		&nextAttemptAt,
		&lastError,
		&run.CreatedAt,
		&run.UpdatedAt,
	); err != nil {
		return CompanyFundSyncRun{}, err
	}
	checkpoint, err := canonicalCompanyFundSyncCheckpoint([]byte(checkpointText))
	if err != nil {
		return CompanyFundSyncRun{}, fmt.Errorf("scan company-fund sync-run checkpoint: %w", err)
	}
	run.Channel = Channel(channel)
	run.Status = CompanyFundSyncRunStatus(status)
	run.Checkpoint = checkpoint
	if leaseOwner.Valid {
		run.LeaseOwner = leaseOwner.String
	}
	run.LeaseExpiresAt = companyFundSyncRunNullTimePointer(leaseExpiresAt)
	run.StartedAt = companyFundSyncRunNullTimePointer(startedAt)
	run.CompletedAt = companyFundSyncRunNullTimePointer(completedAt)
	run.NextAttemptAt = companyFundSyncRunNullTimePointer(nextAttemptAt)
	if lastError.Valid {
		run.LastError = lastError.String
	}
	return run, nil
}

func companyFundSyncRunNullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return companyFundSyncRunTimePointer(value.Time)
}

func companyFundSyncRunTimePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

func nullableJSON(value []byte) any {
	if value == nil {
		return nil
	}
	return string(value)
}

var _ CompanyFundSyncRunStore = (*DBRepository)(nil)
