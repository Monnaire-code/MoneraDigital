package companyfund

import (
	"errors"
	"fmt"
	"time"
)

const (
	maxValuationJobTriggerBytes    = 64
	maxValuationJobTriggerIDBytes  = 256
	maxValuationJobLeaseOwnerBytes = 128
	maxValuationJobErrorBytes      = 4096
)

var (
	ErrValuationJobLeaseNotOwned = errors.New("company-fund valuation job lease is not owned or has expired")
	ErrValuationJobClaimLost     = errors.New("company-fund valuation job claim was lost")
)

type ValuationJobState string

const (
	ValuationJobStatePending    ValuationJobState = "PENDING"
	ValuationJobStateLeased     ValuationJobState = "LEASED"
	ValuationJobStateRetryWait  ValuationJobState = "RETRY_WAIT"
	ValuationJobStateSucceeded  ValuationJobState = "SUCCEEDED"
	ValuationJobStateSuperseded ValuationJobState = "SUPERSEDED"
	ValuationJobStateFailed     ValuationJobState = "FAILED"
)

type ValuationJobFinalizeOutcome string

const (
	ValuationJobFinalizeSucceeded  ValuationJobFinalizeOutcome = "SUCCEEDED"
	ValuationJobFinalizeSuperseded ValuationJobFinalizeOutcome = "SUPERSEDED"
	ValuationJobFinalizeFailed     ValuationJobFinalizeOutcome = "FAILED"
	ValuationJobFinalizeRetry      ValuationJobFinalizeOutcome = "RETRY"
)

type CompanyFundValuationJobInput struct {
	TransactionID               int64
	SourceValuationHistoryID    *int64
	TriggerKind                 string
	TriggerID                   string
	TargetDependencyFingerprint string
	PolicyVersion               string
}

type CompanyFundValuationJobRecord struct {
	ID                                   int64
	TransactionID                        int64
	SourceValuationHistoryID             *int64
	TriggerKind                          string
	TriggerID                            string
	TargetDependencyFingerprint          string
	PolicyVersion                        string
	ExpectedCurrentState                 ValuationCurrentStateExpectation
	ExpectedCurrentHistoryID             *int64
	ExpectedCurrentDependencyFingerprint string
	State                                ValuationJobState
	AttemptCount                         int
	NextAttemptAt                        *time.Time
	LeaseOwner                           string
	LeaseExpiresAt                       *time.Time
	LastError                            string
	CompletedAt                          *time.Time
	CreatedAt                            time.Time
}

type CompanyFundValuationJobEnqueueResult struct {
	Job      CompanyFundValuationJobRecord
	Inserted bool
}

type CompanyFundValuationJobLease struct {
	Job CompanyFundValuationJobRecord
}

func (input CompanyFundValuationJobInput) validate() error {
	if input.TransactionID <= 0 {
		return fmt.Errorf("valuation job transaction ID must be positive")
	}
	if input.SourceValuationHistoryID != nil && *input.SourceValuationHistoryID <= 0 {
		return fmt.Errorf("valuation job source history ID must be positive")
	}
	if err := validateRequiredString("valuation job trigger kind", input.TriggerKind, maxValuationJobTriggerBytes); err != nil {
		return err
	}
	if err := validateOptionalValuationString("valuation job trigger ID", input.TriggerID, maxValuationJobTriggerIDBytes); err != nil {
		return err
	}
	if !isLowerSHA256Hex(input.TargetDependencyFingerprint) {
		return fmt.Errorf("valuation job target dependency fingerprint must be lowercase SHA-256 hex")
	}
	return validateRequiredString("valuation job policy version", input.PolicyVersion, maxValuationPolicyVersionBytes)
}

func validateValuationJobLeaseOwner(owner string) error {
	return validateRequiredString("valuation job lease owner", owner, maxValuationJobLeaseOwnerBytes)
}

func valuationJobLeaseDurationMicroseconds(duration time.Duration) (int64, error) {
	if duration <= 0 || duration.Microseconds() <= 0 {
		return 0, fmt.Errorf("valuation job lease duration must be at least one microsecond")
	}
	return duration.Microseconds(), nil
}

func (outcome ValuationJobFinalizeOutcome) state() (ValuationJobState, bool) {
	switch outcome {
	case ValuationJobFinalizeSucceeded:
		return ValuationJobStateSucceeded, false
	case ValuationJobFinalizeSuperseded:
		return ValuationJobStateSuperseded, false
	case ValuationJobFinalizeFailed:
		return ValuationJobStateFailed, false
	case ValuationJobFinalizeRetry:
		return ValuationJobStateRetryWait, true
	default:
		return "", false
	}
}

func validateValuationJobFinalize(outcome ValuationJobFinalizeOutcome, retryAt *time.Time, failureDetail string) (ValuationJobState, error) {
	state, isRetry := outcome.state()
	if state == "" {
		return "", fmt.Errorf("unsupported valuation job finalize outcome %q", outcome)
	}
	if isRetry {
		if retryAt == nil || retryAt.IsZero() {
			return "", fmt.Errorf("valuation job retry requires a next attempt time")
		}
	} else if retryAt != nil {
		return "", fmt.Errorf("terminal valuation job outcome must not have a retry time")
	}
	if err := validateOptionalValuationString("valuation job failure detail", failureDetail, maxValuationJobErrorBytes); err != nil {
		return "", err
	}
	return state, nil
}

func (state ValuationJobState) terminal() bool {
	return state == ValuationJobStateSucceeded || state == ValuationJobStateSuperseded || state == ValuationJobStateFailed
}

func (lease CompanyFundValuationJobLease) guardedValuationApplyInput(input CompanyFundValuationApplyInput) (CompanyFundValuationApplyInput, error) {
	job := lease.Job
	if job.ID <= 0 || job.State != ValuationJobStateLeased {
		return CompanyFundValuationApplyInput{}, fmt.Errorf("valuation job must be a claimed lease before valuation can apply")
	}
	if input.TransactionID != job.TransactionID {
		return CompanyFundValuationApplyInput{}, fmt.Errorf("valuation job transaction does not match apply input")
	}
	if input.DependencyFingerprint != job.TargetDependencyFingerprint {
		return CompanyFundValuationApplyInput{}, fmt.Errorf("valuation job target fingerprint does not match apply input")
	}
	if input.PolicyVersion != job.PolicyVersion {
		return CompanyFundValuationApplyInput{}, fmt.Errorf("valuation job policy version does not match apply input")
	}
	if input.ExpectedCurrentState != nil || input.ExpectedCurrentHistoryID != nil || input.ExpectedCurrentDependencyFingerprint != "" {
		return CompanyFundValuationApplyInput{}, fmt.Errorf("valuation job current-state guard is repository-owned")
	}
	if err := validateValuationJobCurrentGuard(
		job.ExpectedCurrentState,
		job.ExpectedCurrentHistoryID,
		job.ExpectedCurrentDependencyFingerprint,
	); err != nil {
		return CompanyFundValuationApplyInput{}, err
	}
	expectedState := job.ExpectedCurrentState
	input.ExpectedCurrentState = &expectedState
	input.ExpectedCurrentHistoryID = job.ExpectedCurrentHistoryID
	input.ExpectedCurrentDependencyFingerprint = job.ExpectedCurrentDependencyFingerprint
	if err := input.validate(); err != nil {
		return CompanyFundValuationApplyInput{}, err
	}
	return input, nil
}

func validateValuationJobCurrentGuard(
	state ValuationCurrentStateExpectation,
	historyID *int64,
	fingerprint string,
) error {
	if state != ValuationCurrentStateExpectationNone && state != ValuationCurrentStateExpectationHistory {
		return fmt.Errorf("unsupported valuation job expected current state %q", state)
	}
	return validateValuationApplyCurrentGuard(&state, historyID, fingerprint)
}
