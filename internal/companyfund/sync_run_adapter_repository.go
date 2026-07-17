package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const claimCompanyFundSyncRunExactSQL = `
SELECT ` + companyFundSyncRunReturnedColumns + `
FROM company_fund_sync_runs
WHERE id = $1
	AND channel = $2
	AND sync_kind = $3
	AND window_key = $4
	AND window_start = $5
	AND window_end = $6
	AND (
		status = 'PENDING'
		OR (status IN ('FAILED', 'PARTIAL') AND next_attempt_at IS NOT NULL AND next_attempt_at <= NOW())
		OR (status = 'LEASED' AND lease_expires_at <= NOW())
	)
FOR UPDATE SKIP LOCKED`

const updateClaimedCompanyFundSyncRunExactSQL = `
UPDATE company_fund_sync_runs
SET status = 'LEASED',
	lease_owner = $7,
	lease_expires_at = NOW() + ($8::bigint * INTERVAL '1 microsecond'),
	next_attempt_at = NULL,
	attempt_count = attempt_count + 1,
	started_at = COALESCE(started_at, NOW()),
	completed_at = NULL,
	last_error = NULL,
	updated_at = NOW()
WHERE id = $1
	AND channel = $2
	AND sync_kind = $3
	AND window_key = $4
	AND window_start = $5
	AND window_end = $6
	AND (
		status = 'PENDING'
		OR (status IN ('FAILED', 'PARTIAL') AND next_attempt_at IS NOT NULL AND next_attempt_at <= NOW())
		OR (status = 'LEASED' AND lease_expires_at <= NOW())
	)
RETURNING attempt_count, lease_expires_at`

// CompanyFundSyncRunExactClaimInput identifies exactly one previously-created
// reconciliation window. In particular, it never permits a channel-level
// queue claim to substitute another account/window for the requested work.
type CompanyFundSyncRunExactClaimInput struct {
	RunID       int64
	Channel     Channel
	SyncKind    string
	WindowKey   string
	WindowStart time.Time
	WindowEnd   time.Time
}

func (input CompanyFundSyncRunExactClaimInput) canonical() (CompanyFundSyncRunExactClaimInput, error) {
	if input.RunID <= 0 {
		return CompanyFundSyncRunExactClaimInput{}, fmt.Errorf("company-fund sync-run ID must be positive")
	}
	window, err := (CompanyFundSyncRunInput{
		Channel:     input.Channel,
		SyncKind:    input.SyncKind,
		WindowKey:   input.WindowKey,
		WindowStart: input.WindowStart,
		WindowEnd:   input.WindowEnd,
	}).canonical()
	if err != nil {
		return CompanyFundSyncRunExactClaimInput{}, err
	}
	input.Channel = window.Channel
	input.SyncKind = window.SyncKind
	input.WindowKey = window.WindowKey
	input.WindowStart = window.WindowStart
	input.WindowEnd = window.WindowEnd
	return input, nil
}

// ClaimCompanyFundSyncRunExact obtains a short database lease for exactly the
// named run. It returns nil without mutation when the requested row is not
// eligible yet, terminal, locked by another worker, or no longer matches the
// immutable window identity.
func (r *DBRepository) ClaimCompanyFundSyncRunExact(
	ctx context.Context,
	input CompanyFundSyncRunExactClaimInput,
	owner string,
	leaseDuration time.Duration,
) (*CompanyFundSyncRun, error) {
	canonical, err := input.canonical()
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
		return nil, fmt.Errorf("begin exact company-fund sync-run claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	run, err := scanCompanyFundSyncRun(tx.QueryRowContext(ctx, claimCompanyFundSyncRunExactSQL,
		canonical.RunID,
		canonical.Channel,
		canonical.SyncKind,
		canonical.WindowKey,
		canonical.WindowStart,
		canonical.WindowEnd,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select exact claimable company-fund sync run: %w", err)
	}

	var expiresAt time.Time
	if err := tx.QueryRowContext(ctx, updateClaimedCompanyFundSyncRunExactSQL,
		canonical.RunID,
		canonical.Channel,
		canonical.SyncKind,
		canonical.WindowKey,
		canonical.WindowStart,
		canonical.WindowEnd,
		owner,
		microseconds,
	).Scan(&run.AttemptCount, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCompanyFundSyncRunClaimLost
		}
		return nil, fmt.Errorf("claim exact company-fund sync run: %w", err)
	}
	run.Status = CompanyFundSyncRunStatusLeased
	run.LeaseOwner = owner
	run.LeaseExpiresAt = companyFundSyncRunTimePointer(expiresAt)
	run.NextAttemptAt = nil
	run.CompletedAt = nil
	run.LastError = ""
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit exact company-fund sync-run claim: %w", err)
	}
	committed = true
	return &run, nil
}
