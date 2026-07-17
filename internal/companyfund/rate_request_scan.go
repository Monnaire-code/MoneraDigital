package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func latestRateRequestAttempt(ctx context.Context, tx *sql.Tx, provider, logicalRequestKey string) (int, RateRequestState, error) {
	var (
		attemptNo int
		state     string
	)
	if err := tx.QueryRowContext(ctx, selectLatestRateRequestForUpdateSQL, provider, logicalRequestKey).Scan(&attemptNo, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", nil
		}
		return 0, "", fmt.Errorf("find latest rate request attempt: %w", err)
	}
	return attemptNo, RateRequestState(state), nil
}

func scanRateBudgetPeriod(row *sql.Row) (rateBudgetPeriodRecord, error) {
	var (
		period           rateBudgetPeriodRecord
		planName         sql.NullString
		licenseReference sql.NullString
		configFrozenAt   sql.NullTime
		firstReservedAt  sql.NullTime
	)
	if err := row.Scan(
		&period.ID,
		&period.Provider,
		&period.BillingAnchor,
		&period.PeriodKey,
		&period.PeriodStart,
		&period.PeriodEnd,
		&period.CallLimit,
		&period.ReservedCalls,
		&period.UsedCalls,
		&planName,
		&licenseReference,
		&period.ConfigVersion,
		&configFrozenAt,
		&firstReservedAt,
	); err != nil {
		return rateBudgetPeriodRecord{}, err
	}
	if planName.Valid {
		period.PlanName = planName.String
	}
	if licenseReference.Valid {
		period.LicenseReference = licenseReference.String
	}
	if configFrozenAt.Valid {
		period.ConfigFrozenAt = rateTimePointer(configFrozenAt.Time)
	}
	if firstReservedAt.Valid {
		period.FirstReservedAt = rateTimePointer(firstReservedAt.Time)
	}
	period.BillingAnchor = canonicalRateBillingAnchor(period.BillingAnchor)
	period.PeriodStart = period.PeriodStart.UTC()
	period.PeriodEnd = period.PeriodEnd.UTC()
	return period, nil
}

func scanRateRequestAttempt(row *sql.Row) (RateRequestAttempt, error) {
	var (
		attempt               RateRequestAttempt
		requestKind           string
		requestState          string
		normalizedBucketStart sql.NullTime
		notBefore             sql.NullTime
		leaseOwner            sql.NullString
		leaseExpiresAt        sql.NullTime
		dispatchedAt          sql.NullTime
		chargedAt             sql.NullTime
		completedAt           sql.NullTime
		responseSnapshotGroup sql.NullString
		errorCode             sql.NullString
		errorDetail           sql.NullString
	)
	if err := row.Scan(
		&attempt.ID,
		&attempt.BudgetPeriodID,
		&attempt.Provider,
		&attempt.LogicalRequestKey,
		&requestKind,
		&normalizedBucketStart,
		&attempt.AttemptNo,
		&requestState,
		&notBefore,
		&leaseOwner,
		&leaseExpiresAt,
		&attempt.ReservedAt,
		&dispatchedAt,
		&chargedAt,
		&completedAt,
		&responseSnapshotGroup,
		&errorCode,
		&errorDetail,
	); err != nil {
		return RateRequestAttempt{}, err
	}
	attempt.RequestKind = RateRequestKind(requestKind)
	attempt.State = RateRequestState(requestState)
	if normalizedBucketStart.Valid {
		attempt.NormalizedBucketStart = rateTimePointer(normalizedBucketStart.Time)
	}
	if notBefore.Valid {
		attempt.NotBefore = rateTimePointer(notBefore.Time)
	}
	if leaseOwner.Valid {
		attempt.LeaseOwner = leaseOwner.String
	}
	if leaseExpiresAt.Valid {
		attempt.LeaseExpiresAt = rateTimePointer(leaseExpiresAt.Time)
	}
	if dispatchedAt.Valid {
		attempt.DispatchedAt = rateTimePointer(dispatchedAt.Time)
	}
	if chargedAt.Valid {
		attempt.ChargedAt = rateTimePointer(chargedAt.Time)
	}
	if completedAt.Valid {
		attempt.CompletedAt = rateTimePointer(completedAt.Time)
	}
	if responseSnapshotGroup.Valid {
		attempt.ResponseSnapshotGroup = responseSnapshotGroup.String
	}
	if errorCode.Valid {
		attempt.ErrorCode = errorCode.String
	}
	if errorDetail.Valid {
		attempt.ErrorDetail = errorDetail.String
	}
	return attempt, nil
}

func rateTimePointer(value time.Time) *time.Time {
	return &value
}
