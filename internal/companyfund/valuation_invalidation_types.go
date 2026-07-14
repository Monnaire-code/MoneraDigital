package companyfund

import "fmt"

const revaluationPendingTransitionTrigger = "DEPENDENCY_INVALIDATED"

// CompanyFundValuationInvalidationInput is the one public boundary for a
// dependency correction. It deliberately combines the null stale transition
// and its leased revaluation job so callers cannot commit one without the
// other.
type CompanyFundValuationInvalidationInput struct {
	Valuation CompanyFundValuationApplyInput
	Job       CompanyFundValuationJobInput
}

type CompanyFundValuationInvalidationResult struct {
	History         CompanyFundValuationHistoryRecord
	HistoryInserted bool
	Job             CompanyFundValuationJobRecord
	JobInserted     bool
}

func (input CompanyFundValuationInvalidationInput) validate() error {
	if input.Valuation.ExpectedCurrentState != nil || input.Valuation.ExpectedCurrentHistoryID != nil ||
		input.Valuation.ExpectedCurrentDependencyFingerprint != "" {
		return fmt.Errorf("valuation invalidation must capture its current-state guard inside the transaction")
	}
	if err := input.Valuation.validate(); err != nil {
		return err
	}
	if input.Valuation.Result.Status != USDValuationStatusStale ||
		input.Valuation.Result.Reason != USDValuationReasonRevaluationPending {
		return fmt.Errorf("valuation invalidation must append STALE/REVALUATION_PENDING")
	}
	if input.Valuation.Result.Value != nil || input.Valuation.Result.ProviderReportedUSD != nil ||
		input.Valuation.CalculatedUSDValue != nil || !input.Valuation.Result.UnitPrice.IsZero() {
		return fmt.Errorf("valuation invalidation must append a null stale valuation")
	}
	if input.Valuation.TransitionTrigger != revaluationPendingTransitionTrigger {
		return fmt.Errorf("valuation invalidation transition trigger must be %q", revaluationPendingTransitionTrigger)
	}
	if input.Job.SourceValuationHistoryID != nil {
		return fmt.Errorf("valuation invalidation job source history is assigned from its pending history")
	}
	if err := input.Job.validate(); err != nil {
		return err
	}
	if input.Job.TransactionID != input.Valuation.TransactionID {
		return fmt.Errorf("valuation invalidation job transaction does not match stale valuation")
	}
	if input.Job.TargetDependencyFingerprint != input.Valuation.DependencyFingerprint {
		return fmt.Errorf("valuation invalidation job target fingerprint does not match stale valuation")
	}
	if input.Job.PolicyVersion != input.Valuation.PolicyVersion {
		return fmt.Errorf("valuation invalidation job policy version does not match stale valuation")
	}
	return nil
}
