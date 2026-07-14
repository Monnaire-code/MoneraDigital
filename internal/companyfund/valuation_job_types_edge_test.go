package companyfund

import (
	"strings"
	"testing"
	"time"
)

func TestCompanyFundValuationJobTypes_ValidateInputLeaseAndFinalizeContracts(t *testing.T) {
	valid := newCompanyFundValuationJobInput()
	if err := valid.validate(); err != nil {
		t.Fatalf("valid valuation job input: %v", err)
	}
	zero := int64(0)
	for _, input := range []CompanyFundValuationJobInput{
		{},
		{TransactionID: 1, SourceValuationHistoryID: &zero, TriggerKind: "RATE", TargetDependencyFingerprint: strings.Repeat("a", 64), PolicyVersion: "v1"},
		{TransactionID: 1, TriggerKind: " ", TargetDependencyFingerprint: strings.Repeat("a", 64), PolicyVersion: "v1"},
		{TransactionID: 1, TriggerKind: "RATE", TriggerID: strings.Repeat("x", maxValuationJobTriggerIDBytes+1), TargetDependencyFingerprint: strings.Repeat("a", 64), PolicyVersion: "v1"},
		{TransactionID: 1, TriggerKind: "RATE", TargetDependencyFingerprint: "not-a-digest", PolicyVersion: "v1"},
		{TransactionID: 1, TriggerKind: "RATE", TargetDependencyFingerprint: strings.Repeat("a", 64), PolicyVersion: " "},
	} {
		if err := input.validate(); err == nil {
			t.Fatalf("valuation job input %#v unexpectedly validated", input)
		}
	}
	if err := validateValuationJobLeaseOwner("worker-1"); err != nil {
		t.Fatal(err)
	}
	if err := validateValuationJobLeaseOwner(strings.Repeat("x", maxValuationJobLeaseOwnerBytes+1)); err == nil {
		t.Fatal("oversized lease owner must be rejected")
	}
	for _, duration := range []time.Duration{0, -time.Microsecond, 999 * time.Nanosecond} {
		if _, err := valuationJobLeaseDurationMicroseconds(duration); err == nil {
			t.Fatalf("duration %s must be rejected", duration)
		}
	}
	if microseconds, err := valuationJobLeaseDurationMicroseconds(time.Microsecond); err != nil || microseconds != 1 {
		t.Fatalf("one microsecond = %d, %v", microseconds, err)
	}

	nextAttempt := time.Date(2026, time.July, 12, 0, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		outcome ValuationJobFinalizeOutcome
		state   ValuationJobState
		retry   bool
	}{
		{ValuationJobFinalizeSucceeded, ValuationJobStateSucceeded, false},
		{ValuationJobFinalizeSuperseded, ValuationJobStateSuperseded, false},
		{ValuationJobFinalizeFailed, ValuationJobStateFailed, false},
		{ValuationJobFinalizeRetry, ValuationJobStateRetryWait, true},
	} {
		state, retry := testCase.outcome.state()
		if state != testCase.state || retry != testCase.retry {
			t.Fatalf("outcome %q = %q/%v", testCase.outcome, state, retry)
		}
		retryAt := (*time.Time)(nil)
		if retry {
			retryAt = &nextAttempt
		}
		validated, err := validateValuationJobFinalize(testCase.outcome, retryAt, "provider unavailable")
		if err != nil || validated != testCase.state || validated.terminal() != !retry {
			t.Fatalf("finalize %q = %q, %v", testCase.outcome, validated, err)
		}
	}
	for _, testCase := range []struct {
		outcome ValuationJobFinalizeOutcome
		retryAt *time.Time
		detail  string
	}{
		{ValuationJobFinalizeRetry, nil, "retry"},
		{ValuationJobFinalizeSucceeded, &nextAttempt, ""},
		{ValuationJobFinalizeOutcome("UNKNOWN"), nil, ""},
		{ValuationJobFinalizeFailed, nil, strings.Repeat("x", maxValuationJobErrorBytes+1)},
	} {
		if _, err := validateValuationJobFinalize(testCase.outcome, testCase.retryAt, testCase.detail); err == nil {
			t.Fatalf("unsafe finalization %#v unexpectedly validated", testCase)
		}
	}
}

func TestCompanyFundValuationJobLease_GuardsApplyInput(t *testing.T) {
	apply := newCompanyFundValuationApplyInput()
	jobInput := newCompanyFundValuationJobInput()
	jobInput.TargetDependencyFingerprint = apply.DependencyFingerprint
	job := valuationJobRecordFromInput(jobInput, 501, ValuationJobStateLeased, 1, nil, nil, nil)
	lease := CompanyFundValuationJobLease{Job: job}
	guarded, err := lease.guardedValuationApplyInput(apply)
	if err != nil || guarded.ExpectedCurrentState == nil || *guarded.ExpectedCurrentState != ValuationCurrentStateExpectationNone ||
		guarded.ExpectedCurrentHistoryID != nil || guarded.ExpectedCurrentDependencyFingerprint != "" {
		t.Fatalf("guarded apply = %#v, %v", guarded, err)
	}

	for _, mutate := range []func(*CompanyFundValuationJobLease, *CompanyFundValuationApplyInput){
		func(lease *CompanyFundValuationJobLease, _ *CompanyFundValuationApplyInput) {
			lease.Job.State = ValuationJobStatePending
		},
		func(_ *CompanyFundValuationJobLease, input *CompanyFundValuationApplyInput) { input.TransactionID++ },
		func(_ *CompanyFundValuationJobLease, input *CompanyFundValuationApplyInput) {
			input.DependencyFingerprint = strings.Repeat("b", 64)
		},
		func(_ *CompanyFundValuationJobLease, input *CompanyFundValuationApplyInput) {
			input.PolicyVersion = "other"
		},
		func(_ *CompanyFundValuationJobLease, input *CompanyFundValuationApplyInput) {
			state := ValuationCurrentStateExpectationNone
			input.ExpectedCurrentState = &state
		},
		func(lease *CompanyFundValuationJobLease, _ *CompanyFundValuationApplyInput) {
			lease.Job.ExpectedCurrentState = ValuationCurrentStateExpectationHistory
		},
	} {
		brokenLease := lease
		brokenInput := apply
		mutate(&brokenLease, &brokenInput)
		if _, err := brokenLease.guardedValuationApplyInput(brokenInput); err == nil {
			t.Fatal("unsafe valuation job/apply pairing unexpectedly validated")
		}
	}
}
