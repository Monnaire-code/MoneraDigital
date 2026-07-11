package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ClaimNextRateRequest leases one request using PostgreSQL clock_timestamp() and SKIP
// LOCKED. It deliberately excludes DISPATCHED: a dispatched call can only be
// explicitly finalized as UNKNOWN/FAILED/etc. before a retry creates a new
// charged attempt.
func (r *DBRepository) ClaimNextRateRequest(ctx context.Context, owner string, leaseDuration time.Duration) (*RateRequestLease, error) {
	if err := validateRateRequestLeaseOwner(owner); err != nil {
		return nil, err
	}
	microseconds, err := rateRequestLeaseDurationMicroseconds(leaseDuration)
	if err != nil {
		return nil, err
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin rate request claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	attempt, err := scanRateRequestAttempt(tx.QueryRowContext(ctx, claimNextRateRequestSQL))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select claimable rate request: %w", err)
	}

	var leaseExpiresAt time.Time
	if err := tx.QueryRowContext(ctx, updateClaimedRateRequestSQL, attempt.ID, owner, microseconds).Scan(&leaseExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRateRequestClaimLost
		}
		return nil, fmt.Errorf("claim rate request: %w", err)
	}
	attempt.State = RateRequestStateLeased
	attempt.NotBefore = nil
	attempt.LeaseOwner = owner
	attempt.LeaseExpiresAt = rateTimePointer(leaseExpiresAt)
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit rate request claim: %w", err)
	}
	committed = true
	return &RateRequestLease{Attempt: attempt}, nil
}

// MarkRateRequestDispatched is the boundary immediately before external HTTP.
// It verifies the live lease owner and moves used_calls, without touching the
// already-reserved count. The provider request must be sent only after this
// transaction commits successfully.
func (r *DBRepository) MarkRateRequestDispatched(ctx context.Context, requestID int64, owner string) error {
	if requestID <= 0 {
		return fmt.Errorf("rate request ID must be positive")
	}
	if err := validateRateRequestLeaseOwner(owner); err != nil {
		return err
	}
	if err := r.requireDB(); err != nil {
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mark rate request dispatched: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var provider string
	var logicalRequestKey string
	if err := tx.QueryRowContext(ctx, selectRateRequestLogicalKeySQL, requestID).Scan(&provider, &logicalRequestKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRateRequestLeaseNotOwned
		}
		return fmt.Errorf("read rate request lock key: %w", err)
	}
	if err := validateRequiredString("rate request provider", provider, maxRateProviderBytes); err != nil {
		return fmt.Errorf("read rate request lock key: %w", err)
	}
	if err := validateRequiredString("rate logical request key", logicalRequestKey, maxRateLogicalRequestKeyBytes); err != nil {
		return fmt.Errorf("read rate request lock key: %w", err)
	}
	if _, err := tx.ExecContext(ctx, lockRateRequestLogicalKeySQL, RateRequestAdvisoryLockKey(provider, logicalRequestKey)); err != nil {
		return fmt.Errorf("lock rate request logical key before dispatch: %w", err)
	}

	var budgetID int64
	if err := tx.QueryRowContext(ctx, markRateRequestDispatchedSQL, requestID, owner).Scan(&budgetID, &provider); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRateRequestLeaseNotOwned
		}
		return fmt.Errorf("mark rate request dispatched: %w", err)
	}
	var usedCalls int
	if err := tx.QueryRowContext(ctx, consumeReservedRateBudgetCallSQL, budgetID, provider).Scan(&usedCalls); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("rate budget period %d for provider %q has no reserved call to consume", budgetID, provider)
		}
		return fmt.Errorf("consume reserved rate budget call: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark rate request dispatched: %w", err)
	}
	committed = true
	return nil
}

// FinalizeDispatchedRateRequest makes a dispatched request terminal. It never
// turns a DISPATCHED record back into a claimable state; a retry must reserve a
// new attempt and consumes another budget unit.
func (r *DBRepository) FinalizeDispatchedRateRequest(ctx context.Context, requestID int64, completion RateRequestCompletion) error {
	if requestID <= 0 {
		return fmt.Errorf("rate request ID must be positive")
	}
	if err := completion.validate(); err != nil {
		return err
	}
	if err := r.requireDB(); err != nil {
		return err
	}

	var finalizedID int64
	if err := r.db.QueryRowContext(ctx, finalizeDispatchedRateRequestSQL,
		requestID,
		completion.State,
		nullableString(completion.ResponseSnapshotGroup),
		nullableString(completion.ErrorCode),
		nullableString(completion.ErrorDetail),
	).Scan(&finalizedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("rate request %d is not dispatched and cannot be finalized", requestID)
		}
		return fmt.Errorf("finalize dispatched rate request: %w", err)
	}
	return nil
}

// RecoverStaleDispatchedRateRequests terminalizes requests whose provider call
// outcome was never recorded after dispatch. It preserves already-consumed
// quota, makes no provider call, and never creates a retry; a later worker
// must reserve an explicit new attempt if policy permits one.
func (r *DBRepository) RecoverStaleDispatchedRateRequests(ctx context.Context, recoveryWindow time.Duration, limit int) (int, error) {
	microseconds, err := rateRequestLeaseDurationMicroseconds(recoveryWindow)
	if err != nil {
		return 0, fmt.Errorf("stale dispatched recovery window: %w", err)
	}
	if limit <= 0 {
		return 0, fmt.Errorf("stale dispatched recovery limit must be positive")
	}
	if err := r.requireDB(); err != nil {
		return 0, err
	}

	var recovered int
	if err := r.db.QueryRowContext(ctx, recoverStaleDispatchedRateRequestsSQL, microseconds, limit).Scan(&recovered); err != nil {
		return 0, fmt.Errorf("recover stale dispatched rate requests: %w", err)
	}
	return recovered, nil
}
