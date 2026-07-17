package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ApplyCompanyFundValuation serializes on one transaction row, appends one
// immutable history version, then updates only its valuation projection in the
// same transaction. It has no external I/O or valuation-calculation logic.
func (r *DBRepository) ApplyCompanyFundValuation(ctx context.Context, input CompanyFundValuationApplyInput) (CompanyFundValuationApplyResult, error) {
	if err := input.validate(); err != nil {
		return CompanyFundValuationApplyResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return CompanyFundValuationApplyResult{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CompanyFundValuationApplyResult{}, fmt.Errorf("begin company-fund valuation apply: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, _, err := r.applyCompanyFundValuationTx(ctx, tx, input)
	if err != nil {
		return CompanyFundValuationApplyResult{}, err
	}
	if result.Superseded {
		// Preserve the no-op/rollback behavior for an obsolete leased job.
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return CompanyFundValuationApplyResult{}, fmt.Errorf("commit company-fund valuation apply: %w", err)
	}
	committed = true
	return result, nil
}

type companyFundValuationCurrentState struct {
	TransactionID         int64
	Expectation           ValuationCurrentStateExpectation
	HistoryID             *int64
	DependencyFingerprint string
	Source                USDValuationSource
}

func (r *DBRepository) applyCompanyFundValuationTx(
	ctx context.Context,
	tx *sql.Tx,
	input CompanyFundValuationApplyInput,
) (CompanyFundValuationApplyResult, companyFundValuationCurrentState, error) {
	current, err := lockCompanyFundValuationCurrentState(ctx, tx, input.TransactionID)
	if err != nil {
		return CompanyFundValuationApplyResult{}, companyFundValuationCurrentState{}, err
	}
	result, err := r.applyCompanyFundValuationWithLockedCurrentTx(ctx, tx, input, current)
	return result, current, err
}

func lockCompanyFundValuationCurrentState(
	ctx context.Context,
	tx *sql.Tx,
	transactionID int64,
) (companyFundValuationCurrentState, error) {
	var (
		current                      = companyFundValuationCurrentState{Expectation: ValuationCurrentStateExpectationNone}
		currentValuationHistoryID    sql.NullInt64
		currentDependencyFingerprint string
	)
	if err := tx.QueryRowContext(ctx, selectCompanyFundTransactionForValuationSQL, transactionID).Scan(
		&current.TransactionID,
		&currentValuationHistoryID,
		&currentDependencyFingerprint,
		&current.Source,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return companyFundValuationCurrentState{}, fmt.Errorf("company-fund transaction %d does not exist", transactionID)
		}
		return companyFundValuationCurrentState{}, fmt.Errorf("lock company-fund transaction for valuation: %w", err)
	}
	if currentValuationHistoryID.Valid {
		historyID := currentValuationHistoryID.Int64
		current.Expectation = ValuationCurrentStateExpectationHistory
		current.HistoryID = &historyID
		current.DependencyFingerprint = currentDependencyFingerprint
		if current.DependencyFingerprint == "" {
			return companyFundValuationCurrentState{}, fmt.Errorf("company-fund transaction %d has a current valuation history without a dependency fingerprint", transactionID)
		}
	} else if currentDependencyFingerprint != "" {
		return companyFundValuationCurrentState{}, fmt.Errorf("company-fund transaction %d has a dependency fingerprint without a current valuation history", transactionID)
	}
	return current, nil
}

func (r *DBRepository) applyCompanyFundValuationWithLockedCurrentTx(
	ctx context.Context,
	tx *sql.Tx,
	input CompanyFundValuationApplyInput,
	current companyFundValuationCurrentState,
) (CompanyFundValuationApplyResult, error) {
	if current.Source == USDValuationSourceManual && input.Result.Source != USDValuationSourceManual {
		return CompanyFundValuationApplyResult{Superseded: true}, nil
	}
	if input.expectedCurrentHistoryDoesNotMatch(current) {
		return CompanyFundValuationApplyResult{Superseded: true}, nil
	}

	existing, err := scanCompanyFundValuationHistory(tx.QueryRowContext(ctx, selectValuationHistoryByApplyIdentitySQL,
		input.TransactionID,
		input.DependencyFingerprint,
		input.PolicyVersion,
		input.TransitionTrigger,
	))
	if err == nil {
		if conflictField := immutableValuationHistoryConflict(existing, input); conflictField != "" {
			return CompanyFundValuationApplyResult{}, fmt.Errorf("valuation dependency fingerprint conflicts on immutable field %s", conflictField)
		}
		return CompanyFundValuationApplyResult{History: existing}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CompanyFundValuationApplyResult{}, fmt.Errorf("read valuation history by dependency: %w", err)
	}

	previous, err := scanCompanyFundValuationHistory(tx.QueryRowContext(ctx, selectLatestValuationHistoryForUpdateSQL, input.TransactionID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CompanyFundValuationApplyResult{}, fmt.Errorf("read latest valuation history: %w", err)
	}
	version := int64(1)
	var supersedesHistoryID *int64
	if err == nil {
		version = previous.ValuationVersion + 1
		supersedesHistoryID = &previous.ID
	}

	history, err := scanCompanyFundValuationHistory(tx.QueryRowContext(ctx, insertCompanyFundValuationHistorySQL,
		input.TransactionID,
		version,
		valuationDecimalArg(input.Result.Value),
		valuationDecimalArg(input.Result.ProviderReportedUSD),
		valuationDecimalArg(input.CalculatedUSDValue),
		valuationUnitPriceArg(input.Result),
		input.Result.Status,
		nullableString(string(input.Result.Reason)),
		nullableString(string(input.Result.Basis)),
		nullableTime(input.Result.ValuationTargetAt),
		nullableTime(input.Result.PriceAt),
		nullableString(string(input.Result.Source)),
		nullableString(string(input.Result.Method)),
		nullableString(input.Result.Granularity),
		nullableString(string(input.ProviderValueScope)),
		nullableString(string(input.DerivationMethod)),
		nullableInt64(input.RateSnapshotID),
		nullableInt64(input.ProviderTransactionFactID),
		input.DependencyFingerprint,
		input.PolicyVersion,
		input.TransitionTrigger,
		nullableInt64(supersedesHistoryID),
	))
	if err != nil {
		return CompanyFundValuationApplyResult{}, fmt.Errorf("append company-fund valuation history: %w", err)
	}

	var projectedTransactionID int64
	if err := tx.QueryRowContext(ctx, updateCompanyFundTransactionValuationProjectionSQL,
		input.TransactionID,
		valuationDecimalArg(input.Result.ProviderReportedUSD),
		valuationDecimalArg(input.CalculatedUSDValue),
		valuationDecimalArg(input.Result.Value),
		valuationUnitPriceArg(input.Result),
		input.Result.Status,
		nullableString(string(input.Result.Reason)),
		nullableString(string(input.Result.Basis)),
		nullableTime(input.Result.ValuationTargetAt),
		nullableTime(input.Result.PriceAt),
		nullableString(string(input.Result.Source)),
		nullableString(string(input.Result.Method)),
		nullableString(input.Result.Granularity),
		nullableString(string(input.ProviderValueScope)),
		nullableString(string(input.DerivationMethod)),
		nullableInt64(input.RateSnapshotID),
		history.ID,
		input.PolicyVersion,
		history.ValuationVersion,
	).Scan(&projectedTransactionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompanyFundValuationApplyResult{}, fmt.Errorf("company-fund transaction %d disappeared while applying valuation", input.TransactionID)
		}
		return CompanyFundValuationApplyResult{}, fmt.Errorf("update company-fund valuation projection: %w", err)
	}
	if projectedTransactionID != current.TransactionID {
		return CompanyFundValuationApplyResult{}, fmt.Errorf("valuation projection updated transaction %d, want %d", projectedTransactionID, current.TransactionID)
	}
	return CompanyFundValuationApplyResult{History: history, Inserted: true}, nil
}

func (input CompanyFundValuationApplyInput) expectedCurrentHistoryDoesNotMatch(current companyFundValuationCurrentState) bool {
	if input.ExpectedCurrentState != nil {
		if *input.ExpectedCurrentState == ValuationCurrentStateExpectationNone {
			return current.Expectation != ValuationCurrentStateExpectationNone
		}
		return current.Expectation != ValuationCurrentStateExpectationHistory ||
			current.HistoryID == nil || *current.HistoryID != *input.ExpectedCurrentHistoryID ||
			current.DependencyFingerprint != input.ExpectedCurrentDependencyFingerprint
	}
	if input.ExpectedCurrentHistoryID == nil {
		return false
	}
	return current.Expectation != ValuationCurrentStateExpectationHistory ||
		current.HistoryID == nil || *current.HistoryID != *input.ExpectedCurrentHistoryID ||
		current.DependencyFingerprint != input.ExpectedCurrentDependencyFingerprint
}

func immutableValuationHistoryConflict(existing CompanyFundValuationHistoryRecord, input CompanyFundValuationApplyInput) string {
	checks := []struct {
		field string
		equal bool
	}{
		{"transaction_id", existing.TransactionID == input.TransactionID},
		{"usd_value", equalValuationDecimal(existing.USDValue, input.Result.Value)},
		{"provider_reported_usd_value", equalValuationDecimal(existing.ProviderReportedUSDValue, input.Result.ProviderReportedUSD)},
		{"calculated_usd_value", equalValuationDecimal(existing.CalculatedUSDValue, input.CalculatedUSDValue)},
		{"usd_unit_price", equalValuationDecimal(existing.USDUnitPrice, valuationUnitPricePointer(input.Result))},
		{"usd_valuation_status", existing.Status == input.Result.Status},
		{"usd_valuation_reason_code", existing.Reason == input.Result.Reason},
		{"usd_valuation_basis", existing.Basis == input.Result.Basis},
		{"usd_valuation_time", equalValuationTime(existing.ValuationTime, input.Result.ValuationTargetAt)},
		{"usd_valuation_price_at", equalValuationTime(existing.PriceAt, input.Result.PriceAt)},
		{"usd_valuation_source", existing.Source == input.Result.Source},
		{"usd_valuation_method", existing.Method == input.Result.Method},
		{"usd_valuation_granularity", existing.Granularity == input.Result.Granularity},
		{"usd_provider_value_scope", existing.ProviderValueScope == input.ProviderValueScope},
		{"usd_derivation_method", existing.DerivationMethod == input.DerivationMethod},
		{"usd_rate_snapshot_id", equalValuationID(existing.RateSnapshotID, input.RateSnapshotID)},
		{"provider_transaction_fact_id", equalValuationID(existing.ProviderTransactionFactID, input.ProviderTransactionFactID)},
		{"dependency_fingerprint", existing.DependencyFingerprint == input.DependencyFingerprint},
		{"valuation_policy_version", existing.PolicyVersion == input.PolicyVersion},
		{"transition_trigger", existing.TransitionTrigger == input.TransitionTrigger},
	}
	for _, check := range checks {
		if !check.equal {
			return check.field
		}
	}
	return ""
}

func valuationUnitPricePointer(result USDValuationResult) *decimal.Decimal {
	if result.UnitPrice.IsZero() {
		return nil
	}
	value := result.UnitPrice
	return &value
}

func equalValuationDecimal(left, right *decimal.Decimal) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func equalValuationTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func equalValuationID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
