package companyfund

import (
	"context"
	"fmt"
)

// InvalidateAndEnqueueCompanyFundValuation atomically replaces a current USD
// projection with a null STALE/REVALUATION_PENDING history row and creates its
// revaluation job. A crash or database error therefore cannot leave a stale
// projection without work, or work without the corresponding stale state.
func (r *DBRepository) InvalidateAndEnqueueCompanyFundValuation(
	ctx context.Context,
	input CompanyFundValuationInvalidationInput,
) (CompanyFundValuationInvalidationResult, error) {
	if err := input.validate(); err != nil {
		return CompanyFundValuationInvalidationResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return CompanyFundValuationInvalidationResult{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CompanyFundValuationInvalidationResult{}, fmt.Errorf("begin valuation invalidation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	current, err := lockCompanyFundValuationCurrentState(ctx, tx, input.Valuation.TransactionID)
	if err != nil {
		return CompanyFundValuationInvalidationResult{}, err
	}
	if current.Source == USDValuationSourceManual {
		return CompanyFundValuationInvalidationResult{Superseded: true}, nil
	}
	valuation, err := r.applyCompanyFundValuationWithLockedCurrentTx(ctx, tx, input.Valuation, current)
	if err != nil {
		return CompanyFundValuationInvalidationResult{}, err
	}
	if valuation.Superseded || valuation.History.ID <= 0 {
		return CompanyFundValuationInvalidationResult{}, fmt.Errorf("valuation invalidation did not produce a durable pending history")
	}

	jobInput := input.Job
	pendingHistoryID := valuation.History.ID
	jobInput.SourceValuationHistoryID = &pendingHistoryID
	jobCurrent := current
	if valuation.Inserted {
		// The projection update above is in this same locked transaction. Capture
		// that new current pair rather than the superseded pre-invalidation row.
		jobCurrent = companyFundValuationCurrentState{
			TransactionID:         current.TransactionID,
			Expectation:           ValuationCurrentStateExpectationHistory,
			HistoryID:             &pendingHistoryID,
			DependencyFingerprint: input.Valuation.DependencyFingerprint,
		}
	}
	job, err := r.enqueueCompanyFundValuationJobWithLockedCurrentTx(ctx, tx, jobInput, jobCurrent)
	if err != nil {
		return CompanyFundValuationInvalidationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompanyFundValuationInvalidationResult{}, fmt.Errorf("commit valuation invalidation: %w", err)
	}
	committed = true
	return CompanyFundValuationInvalidationResult{
		History:         valuation.History,
		HistoryInserted: valuation.Inserted,
		Job:             job.Job,
		JobInserted:     job.Inserted,
	}, nil
}
