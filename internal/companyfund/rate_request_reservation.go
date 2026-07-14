package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ReserveRateRequest returns an active attempt for the same provider/logical
// key without consuming quota. Otherwise it first locks that logical key,
// then locks the provider billing period, freezes/validates its config,
// reserves one call, and inserts exactly one new attempt in one PostgreSQL
// transaction. No outbound provider work occurs here, so a committed
// reservation remains durable if a worker crashes later.
func (r *DBRepository) ReserveRateRequest(ctx context.Context, input RateRequestReservationInput) (RateRequestReservationResult, error) {
	if err := input.validate(); err != nil {
		return RateRequestReservationResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return RateRequestReservationResult{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RateRequestReservationResult{}, fmt.Errorf("begin rate request reservation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	input = input.canonical()
	if _, err := tx.ExecContext(ctx, lockRateRequestLogicalKeySQL,
		RateRequestAdvisoryLockKey(input.Provider, input.LogicalRequestKey)); err != nil {
		return RateRequestReservationResult{}, fmt.Errorf("lock rate request logical key: %w", err)
	}

	budget := input.Budget
	if _, err := tx.ExecContext(ctx, lockRateBudgetPeriodSQL,
		RateBudgetAdvisoryLockKey(budget.Provider, budget.BillingAnchor, budget.PeriodKey)); err != nil {
		return RateRequestReservationResult{}, fmt.Errorf("lock rate budget period: %w", err)
	}
	if _, err := tx.ExecContext(ctx, insertRateBudgetPeriodSQL,
		budget.Provider,
		budget.BillingAnchor,
		budget.PeriodKey,
		budget.PeriodStart,
		budget.PeriodEnd,
		budget.CallLimit,
		nullableString(budget.PlanName),
		nullableString(budget.LicenseReference),
		budget.ConfigVersion,
	); err != nil {
		return RateRequestReservationResult{}, fmt.Errorf("create rate budget period: %w", err)
	}

	period, err := scanRateBudgetPeriod(tx.QueryRowContext(ctx, selectRateBudgetPeriodForUpdateSQL,
		budget.Provider,
		budget.BillingAnchor,
		budget.PeriodKey,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RateRequestReservationResult{}, fmt.Errorf("rate budget period was not created or found")
		}
		return RateRequestReservationResult{}, fmt.Errorf("lock rate budget period row: %w", err)
	}
	if period.Provider != input.Provider {
		return RateRequestReservationResult{}, fmt.Errorf("%w: period provider %q, request provider %q", ErrRateRequestProviderBudgetMismatch, period.Provider, input.Provider)
	}
	if err := period.matchesConfig(budget); err != nil {
		return RateRequestReservationResult{}, err
	}

	active, err := scanRateRequestAttempt(tx.QueryRowContext(ctx, selectActiveRateRequestForUpdateSQL, input.Provider, input.LogicalRequestKey))
	if err == nil {
		if active.Provider != input.Provider {
			return RateRequestReservationResult{}, fmt.Errorf("%w: active request provider %q, requested provider %q", ErrRateRequestProviderBudgetMismatch, active.Provider, input.Provider)
		}
		if err := tx.Commit(); err != nil {
			return RateRequestReservationResult{}, fmt.Errorf("commit existing active rate request: %w", err)
		}
		committed = true
		return RateRequestReservationResult{Attempt: active, ReusedActive: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RateRequestReservationResult{}, fmt.Errorf("find active rate request: %w", err)
	}
	previousAttemptNo, previousState, err := latestRateRequestAttempt(ctx, tx, input.Provider, input.LogicalRequestKey)
	if err != nil {
		return RateRequestReservationResult{}, err
	}
	if previousAttemptNo == 0 {
		if input.RequestKind == RateRequestKindRetry {
			return RateRequestReservationResult{}, fmt.Errorf("retry rate request requires a previous FAILED or UNKNOWN attempt")
		}
	} else {
		if input.RequestKind != RateRequestKindRetry {
			return RateRequestReservationResult{}, fmt.Errorf("existing rate request requires a RETRY attempt instead of %s", input.RequestKind)
		}
		if !previousState.retryable() {
			return RateRequestReservationResult{}, fmt.Errorf("retry rate request requires a previous FAILED or UNKNOWN attempt")
		}
	}

	var reservedCalls int
	if err := tx.QueryRowContext(ctx, reserveRateBudgetPeriodSQL, period.ID, period.Provider).Scan(&reservedCalls); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RateRequestReservationResult{}, ErrRateBudgetExhausted
		}
		return RateRequestReservationResult{}, fmt.Errorf("reserve rate budget call: %w", err)
	}

	state := RateRequestStatePending
	if input.RequestKind == RateRequestKindRetry && input.NotBefore != nil {
		state = RateRequestStateRetryWait
	}
	attempt, err := scanRateRequestAttempt(tx.QueryRowContext(ctx, insertRateRequestAttemptSQL,
		period.ID,
		period.Provider,
		input.LogicalRequestKey,
		input.RequestKind,
		nullableTime(input.NormalizedBucketStart),
		previousAttemptNo+1,
		state,
		nullableTime(input.NotBefore),
	))
	if err != nil {
		return RateRequestReservationResult{}, fmt.Errorf("insert rate request attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RateRequestReservationResult{}, fmt.Errorf("commit rate request reservation: %w", err)
	}
	committed = true
	return RateRequestReservationResult{Attempt: attempt, Reserved: true}, nil
}
