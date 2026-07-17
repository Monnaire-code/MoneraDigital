package companyfund

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestManualValuationDomainContractIsAccepted(t *testing.T) {
	input := newCompanyFundValuationApplyInput()
	value := decimal.NewFromInt(125)
	input.Result = USDValuationResult{
		Value:     &value,
		UnitPrice: decimal.NewFromInt(25),
		Status:    USDValuationStatusFinal,
		Reason:    USDValuationReasonManualOverride,
		Source:    USDValuationSourceManual,
		Method:    USDValuationMethodManualTotal,
	}
	input.CalculatedUSDValue = nil
	input.RateSnapshotID = nil
	input.PolicyVersion = ManualValuationPolicyVersion
	input.TransitionTrigger = ManualValuationTransitionTrigger
	if err := input.validate(); err != nil {
		t.Fatalf("manual valuation contract rejected: %v", err)
	}
	history := CompanyFundValuationHistoryRecord{
		ID: 901, TransactionID: input.TransactionID, ValuationVersion: 1,
		USDValue: &value, USDUnitPrice: valuationUnitPricePointer(input.Result),
		Status: input.Result.Status, Reason: input.Result.Reason, Source: input.Result.Source, Method: input.Result.Method,
		DependencyFingerprint: input.DependencyFingerprint, PolicyVersion: input.PolicyVersion,
		TransitionTrigger: input.TransitionTrigger, AppliedAt: time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC),
	}
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectQuery("SELECT manual history").WillReturnRows(companyFundValuationHistoryRows(history))
	scanned, err := scanCompanyFundValuationHistory(db.QueryRow("SELECT manual history"))
	if err != nil || scanned.Source != USDValuationSourceManual || scanned.Method != USDValuationMethodManualTotal || scanned.Reason != USDValuationReasonManualOverride || scanned.PolicyVersion != ManualValuationPolicyVersion || scanned.TransitionTrigger != ManualValuationTransitionTrigger {
		t.Fatalf("manual history scan = %#v, %v", scanned, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestCompanyFundCurrentValuatorSkipsManualWithoutAutomaticDependencies(t *testing.T) {
	candidate := newValuationRuntimeCandidate(902, "ETH", decimal.NewFromInt(1))
	historyID := int64(903)
	candidate.CurrentValuationHistoryID = &historyID
	candidate.CurrentValuationDependencyFingerprint = strings.Repeat("a", 64)
	candidate.CurrentValuationStatus = USDValuationStatusFinal
	candidate.CurrentValuationSource = USDValuationSourceManual
	store := &fakeCompanyFundValuationCandidateStore{
		candidates: map[int64]CompanyFundTransactionValuationCandidate{candidate.ID: candidate},
		sweep:      []CompanyFundTransactionValuationCandidate{candidate},
	}
	valuator := &CompanyFundCurrentValuator{store: store}
	result := valuator.ValueTransaction(context.Background(), candidate.ID)
	if result.Err != nil || !result.Skipped || !result.SkippedManual || len(store.applies) != 0 {
		t.Fatalf("manual direct valuation = %#v, applies=%d", result, len(store.applies))
	}
	sweep := valuator.Sweep(context.Background(), 10)
	if sweep.Err != nil || sweep.Failed != 0 || sweep.SkippedManual != 1 || len(store.applies) != 0 {
		t.Fatalf("manual sweep = %#v, applies=%d", sweep, len(store.applies))
	}
}

func TestAutomaticValuationApplyCannotReplaceCurrentManual(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	input := newCompanyFundValuationApplyInput()
	historyID := int64(904)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRowsWithSource(input.TransactionID, &historyID, strings.Repeat("b", 64), USDValuationSourceManual))
	mock.ExpectRollback()
	result, err := NewDBRepository(db).ApplyCompanyFundValuation(context.Background(), input)
	if err != nil || !result.Superseded || result.Inserted || result.History.ID != 0 {
		t.Fatalf("automatic apply over MANUAL = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestQueuedAutomaticValuationJobNoOpsAfterManualCommits(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	input := newCompanyFundValuationApplyInput()
	jobInput := newCompanyFundValuationJobInput()
	jobInput.TransactionID = input.TransactionID
	jobInput.TargetDependencyFingerprint = input.DependencyFingerprint
	jobInput.PolicyVersion = input.PolicyVersion
	job := valuationJobRecordFromInput(jobInput, 906, ValuationJobStateLeased, 1, nil, nil, nil)
	job.ExpectedCurrentState = ValuationCurrentStateExpectationNone
	manualHistoryID := int64(907)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
		WithArgs(input.TransactionID).
		WillReturnRows(companyFundTransactionForValuationRowsWithSource(input.TransactionID, &manualHistoryID, strings.Repeat("d", 64), USDValuationSourceManual))
	mock.ExpectRollback()
	result, err := NewDBRepository(db).ApplyClaimedCompanyFundValuationJob(context.Background(), CompanyFundValuationJobLease{Job: job}, input)
	if err != nil || !result.Superseded || result.Inserted {
		t.Fatalf("queued auto after MANUAL = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestManualValuationQueriesAndAutomaticEntryPointsAreGuarded(t *testing.T) {
	queries := map[string]string{
		"direct candidate": selectCompanyFundTransactionValuationCandidateSQL,
		"repair":           selectCompanyFundValuationRepairCandidatesSQL,
		"cursor repair":    selectCompanyFundValuationRepairCandidatesAfterSQL,
		"job claim":        claimNextCompanyFundValuationJobSQL,
	}
	for name, query := range queries {
		if !strings.Contains(query, "MANUAL") {
			t.Fatalf("%s query lacks explicit MANUAL guard", name)
		}
	}
	if !strings.Contains(selectCompanyFundTransactionForValuationSQL, "usd_valuation_source") {
		t.Fatal("row lock must return the current source for the runtime MANUAL guard")
	}
	entryPoints := map[string]bool{
		"ApplyCompanyFundValuation":                true,
		"InvalidateAndEnqueueCompanyFundValuation": true,
		"EnqueueCompanyFundValuationJob":           true,
		"ApplyClaimedCompanyFundValuationJob":      true,
		"ClaimNextCompanyFundValuationJob":         true,
		"RenewCompanyFundValuationJobLease":        true,
		"FinalizeCompanyFundValuationJob":          true,
	}
	files, err := filepath.Glob("valuation_*repository.go")
	if err != nil {
		t.Fatal(err)
	}
	discovered := make(map[string]bool)
	methodPattern := regexp.MustCompile(`func \(r \*DBRepository\) ([A-Z][A-Za-z0-9]*CompanyFundValuation[A-Za-z0-9]*)\(`)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range methodPattern.FindAllStringSubmatch(string(data), -1) {
			name := match[1]
			if strings.HasPrefix(name, "Apply") || strings.HasPrefix(name, "Invalidate") || strings.HasPrefix(name, "Enqueue") || strings.HasPrefix(name, "Claim") || strings.HasPrefix(name, "Renew") || strings.HasPrefix(name, "Finalize") {
				discovered[name] = true
			}
		}
	}
	for name := range discovered {
		if !entryPoints[name] {
			t.Fatalf("new automatic valuation repository entry point %s lacks an explicit MANUAL guard review", name)
		}
	}
	for name := range entryPoints {
		if !discovered[name] {
			t.Fatalf("enumerated automatic valuation entry point %s was removed or renamed", name)
		}
	}
}

func TestEnqueueAndInvalidationNoOpWhenManualIsCurrent(t *testing.T) {
	for _, testCase := range []struct {
		name string
		run  func(*DBRepository) (bool, error)
	}{
		{name: "enqueue", run: func(repository *DBRepository) (bool, error) {
			result, err := repository.EnqueueCompanyFundValuationJob(context.Background(), newCompanyFundValuationJobInput())
			return result.Superseded, err
		}},
		{name: "invalidation", run: func(repository *DBRepository) (bool, error) {
			result, err := repository.InvalidateAndEnqueueCompanyFundValuation(context.Background(), newCompanyFundValuationInvalidationInput())
			return result.Superseded, err
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			input := newCompanyFundValuationApplyInput()
			historyID := int64(905)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionForValuationSQL)).
				WillReturnRows(companyFundTransactionForValuationRowsWithSource(input.TransactionID, &historyID, strings.Repeat("c", 64), USDValuationSourceManual))
			mock.ExpectRollback()
			superseded, err := testCase.run(NewDBRepository(db))
			if err != nil || !superseded {
				t.Fatalf("manual %s no-op = %v, %v", testCase.name, superseded, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}
