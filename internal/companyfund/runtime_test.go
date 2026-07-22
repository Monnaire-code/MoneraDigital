package companyfund

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestCompanyFundRuntime_DrainProviderEventsIsBoundedAndStopsAtConfiguredLimit(t *testing.T) {
	worker := &companyFundRuntimeEventWorkerStub{results: []companyFundRuntimeEventWorkerCall{
		{result: ProviderEventWorkerResult{Claimed: true, EventID: 1, Outcome: ProviderEventFinalizeProcessed}},
		{result: ProviderEventWorkerResult{Claimed: true, EventID: 2, Outcome: ProviderEventFinalizeIgnored}},
		{result: ProviderEventWorkerResult{}},
	}}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{ProviderEventWorker: worker}, CompanyFundRuntimeConfig{
		EventDrainLimit: 2,
	})

	result, err := runtime.DrainProviderEvents(context.Background())
	if err != nil {
		t.Fatalf("DrainProviderEvents() error = %v", err)
	}
	if result.Attempts != 2 || result.Claimed != 2 || !result.LimitReached || worker.callCount() != 2 {
		t.Fatalf("DrainProviderEvents() result = %#v workerCalls=%d", result, worker.callCount())
	}
}

func TestProviderEventDrainFailureKindDoesNotExposeUnderlyingErrorText(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", want: "none"},
		{name: "lease lost", err: fmt.Errorf("renew event lease: %w", ErrProviderEventLeaseNotOwned), want: "lease_not_owned"},
		{name: "claim lost", err: ErrProviderEventClaimLost, want: "claim_lost"},
		{name: "canceled", err: context.Canceled, want: "context_canceled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "context_deadline_exceeded"},
		{name: "database", err: fmt.Errorf("finalize: %w", &pgconn.PgError{Code: "23514", Message: "must not leak"}), want: "database_23514"},
		{name: "opaque", err: errors.New("provider payload must not be logged"), want: "processing_error"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := providerEventDrainFailureKind(testCase.err); got != testCase.want {
				t.Fatalf("providerEventDrainFailureKind() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestCompanyFundRuntime_StartIsTheExplicitBoundaryForBackgroundEventPolling(t *testing.T) {
	worker := &companyFundRuntimeEventWorkerStub{notify: make(chan struct{}, 1)}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{ProviderEventWorker: worker}, CompanyFundRuntimeConfig{
		EventPollInterval:    time.Hour,
		EventMaxIdleInterval: time.Hour,
	})
	if worker.callCount() != 0 {
		t.Fatalf("NewCompanyFundRuntime() must not poll before Start, calls=%d", worker.callCount())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.Start(ctx)
	defer runtime.Stop()
	select {
	case <-worker.notify:
	case <-time.After(time.Second):
		t.Fatal("Start() did not begin the event polling loop")
	}
}

func TestCompanyFundRuntime_ProviderEventWakeCoalescesBeforeStart(t *testing.T) {
	worker := &companyFundRuntimeEventWorkerStub{}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{ProviderEventWorker: worker}, CompanyFundRuntimeConfig{
		EventPollInterval:    time.Hour,
		EventMaxIdleInterval: time.Hour,
	})
	if !runtime.NotifyProviderEvent() {
		t.Fatal("first provider-event wake should queue before Start")
	}
	if runtime.NotifyProviderEvent() {
		t.Fatal("second provider-event wake should coalesce while one wake is pending")
	}
	if worker.callCount() != 0 {
		t.Fatalf("NotifyProviderEvent must not process work before Start, calls=%d", worker.callCount())
	}
}

func TestCompanyFundRuntime_ProviderEventWakeDrainsWithoutWaitingMaxIdle(t *testing.T) {
	worker := &companyFundRuntimeEventWorkerStub{
		notify: make(chan struct{}, 8),
		results: []companyFundRuntimeEventWorkerCall{
			{result: ProviderEventWorkerResult{}}, // startup empty
			{result: ProviderEventWorkerResult{Claimed: true, EventID: 9, Outcome: ProviderEventFinalizeProcessed}},
			{result: ProviderEventWorkerResult{}}, // drain complete
		},
	}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{ProviderEventWorker: worker}, CompanyFundRuntimeConfig{
		EventPollInterval:    30 * time.Millisecond,
		EventMaxIdleInterval: time.Hour, // deep idle ceiling must not block wake
		EventDrainLimit:      10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.Start(ctx)
	defer runtime.Stop()

	// Wait for startup scan (empty).
	select {
	case <-worker.notify:
	case <-time.After(time.Second):
		t.Fatal("startup scan missing")
	}

	_ = runtime.NotifyProviderEvent()

	deadline := time.After(500 * time.Millisecond)
	for worker.callCount() < 3 {
		select {
		case <-worker.notify:
		case <-deadline:
			t.Fatalf("wake did not drain promptly; calls=%d", worker.callCount())
		}
	}
}

func TestCompanyFundRuntime_DefaultsEventMaxIdleToTenMinutes(t *testing.T) {
	runtime, err := NewCompanyFundRuntime(CompanyFundRuntimeDependencies{}, CompanyFundRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.config.EventMaxIdleInterval != 10*time.Minute {
		t.Fatalf("EventMaxIdleInterval = %s, want 10m", runtime.config.EventMaxIdleInterval)
	}
	if runtime.config.EventPollInterval != time.Second {
		t.Fatalf("EventPollInterval = %s, want 1s", runtime.config.EventPollInterval)
	}
}

func TestCompanyFundRuntime_ProviderEventCyclePublishesDurableRetryDue(t *testing.T) {
	due := time.Now().Add(45 * time.Second).Round(time.Microsecond)
	worker := &companyFundRuntimeEventWorkerStub{due: due}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{ProviderEventWorker: worker}, CompanyFundRuntimeConfig{})

	outcome, err := runtime.providerEventCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.NextDue.Equal(due) {
		t.Fatalf("NextDue=%s, want %s", outcome.NextDue, due)
	}
}

func TestCompanyFundRuntime_NotifyProviderEventIsNoopWithoutWorker(t *testing.T) {
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{}, CompanyFundRuntimeConfig{})
	if runtime.NotifyProviderEvent() {
		t.Fatal("NotifyProviderEvent must be a safe no-op without a provider-event worker")
	}
	if runtime.ProviderEventWakeFunc() != nil {
		t.Fatal("ProviderEventWakeFunc must be nil without a provider-event worker")
	}
}

func TestCompanyFundRuntime_ReconciliationUsesAdaptiveLoopNotFixedMinuteTicker(t *testing.T) {
	now := time.Date(2026, time.July, 10, 4, 0, 0, 0, time.UTC)
	safeheron := &companyFundRuntimeSafeheronReconcilerStub{calls: []companyFundRuntimeSafeheronCall{
		{err: ErrCompanyFundSyncRunNotReady},
	}}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, []CompanyFundAccount{validCompanyFundRuntimeAccounts()[0]}),
		SafeheronReconciler: safeheron,
		SyncRunFinalizer:    &companyFundRuntimeFinalizerStub{},
	}, CompanyFundRuntimeConfig{
		ReconciliationPollInterval:  time.Minute,
		EventMaxIdleInterval:        10 * time.Minute,
		ReconciliationSchedule:      ReconciliationDailyScheduleConfig{CatchUpDays: 1},
		LateStatusOverlapConfigured: true,
		LateStatusOverlapDays:       0,
		Now:                         nowFunc(now),
	})
	if runtime.reconciliationLoop == nil {
		t.Fatal("expected adaptive reconciliation loop")
	}

	// Empty / no-work cycle must not report Worked (would pin min-idle forever).
	outcome, err := runtime.reconciliationCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Worked {
		t.Fatalf("no-work recon cycle must not set Worked: %#v", outcome)
	}
	if outcome.NextDue.IsZero() {
		t.Fatal("recon cycle must publish NextDue for daily trigger / maintenance")
	}
}

func TestCompanyFundRuntime_AirwallexWebhookWakeInterruptsAdaptiveReconIdle(t *testing.T) {
	airwallex := &companyFundRuntimeAirwallexReconcilerStub{
		contract: AirwallexFinancialTransactionsReconciliationContract{
			APIVersion: airwallexTestAPIVersion, SchemaVersion: "schema-v1", EventVersion: "event-v1", LoginAsScope: "awx-main",
		},
	}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, validCompanyFundRuntimeAccounts()),
		AirwallexReconciler: airwallex,
		SyncRunFinalizer:    &companyFundRuntimeFinalizerStub{},
	}, CompanyFundRuntimeConfig{
		ReconciliationPollInterval:  time.Hour,
		EventMaxIdleInterval:        time.Hour,
		AirwallexWebhookLookback:    time.Hour,
		LateStatusOverlapConfigured: true,
		LateStatusOverlapDays:       0,
		ReconciliationSchedule:      ReconciliationDailyScheduleConfig{CatchUpDays: 1},
		Now:                         nowFunc(time.Date(2026, time.July, 10, 4, 0, 0, 0, time.UTC)),
	})
	if runtime.reconciliationLoop == nil {
		t.Fatal("expected adaptive reconciliation loop for Airwallex")
	}
	if !runtime.NotifyAirwallexWebhook() {
		t.Fatal("expected first Airwallex wake to queue")
	}
	// Drain wake path only (same as cycle's first step) to assert prompt catch-up.
	if !runtime.drainAirwallexWake() {
		t.Fatal("queued Airwallex wake must be drainable")
	}
	result, err := runtime.ReconcileAirwallexWebhookWindow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Reconciliations != 1 || len(airwallex.inputs) != 1 {
		t.Fatalf("webhook catch-up result=%#v inputs=%d", result, len(airwallex.inputs))
	}
}

func TestCompanyFundRuntime_ReconciliationDelayDoesNotBlockProviderEventPolling(t *testing.T) {
	worker := &companyFundRuntimeContinuousEventWorkerStub{notify: make(chan struct{}, 4)}
	reconciler := &companyFundRuntimeBlockingSafeheronReconciler{started: make(chan struct{})}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		ProviderEventWorker: worker,
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, []CompanyFundAccount{validCompanyFundRuntimeAccounts()[0]}),
		SafeheronReconciler: reconciler,
		SyncRunFinalizer:    &companyFundRuntimeFinalizerStub{},
	}, CompanyFundRuntimeConfig{
		EventPollInterval:           10 * time.Millisecond,
		ReconciliationPollInterval:  time.Hour,
		ReconciliationSchedule:      ReconciliationDailyScheduleConfig{CatchUpDays: 1},
		LateStatusOverlapConfigured: true,
		LateStatusOverlapDays:       0,
	})

	runtime.Start(context.Background())
	defer runtime.Stop()

	select {
	case <-reconciler.started:
	case <-time.After(time.Second):
		t.Fatal("reconciliation loop did not start")
	}

	deadline := time.After(time.Second)
	for worker.calls.Load() < 2 {
		select {
		case <-worker.notify:
		case <-deadline:
			t.Fatalf("provider event polling stopped while reconciliation was delayed; calls=%d", worker.calls.Load())
		}
	}
}

func TestCompanyFundRuntime_ReconciliationFinalizesEachClaimedFailureWithRetryOrPartial(t *testing.T) {
	now := time.Date(2026, time.July, 10, 3, 1, 0, 0, singaporeLocation(t))
	safeErr := errors.New("safeheron history unavailable")
	airErr := errors.New("airwallex page unavailable")
	finalizer := &companyFundRuntimeFinalizerStub{}
	safeheron := &companyFundRuntimeSafeheronReconcilerStub{calls: []companyFundRuntimeSafeheronCall{{
		result: SafeheronTransactionHistoryReconcileResult{RunID: 101, AttemptCount: 2}, err: safeErr,
	}}}
	airwallex := &companyFundRuntimeAirwallexReconcilerStub{
		contract: AirwallexFinancialTransactionsReconciliationContract{
			APIVersion: airwallexTestAPIVersion, SchemaVersion: "schema-v1", EventVersion: "event-v1", LoginAsScope: "awx-main",
		},
		calls: []companyFundRuntimeAirwallexCall{{
			result: AirwallexFinancialTransactionsReconcileResult{RunID: 202, AttemptCount: 1, PagesFetched: 1}, err: airErr,
		}},
	}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, validCompanyFundRuntimeAccounts()),
		SafeheronReconciler: safeheron,
		AirwallexReconciler: airwallex,
		SyncRunFinalizer:    finalizer,
	}, CompanyFundRuntimeConfig{
		ReconciliationSchedule: ReconciliationDailyScheduleConfig{CatchUpDays: 1},
		ReconciliationRetryPolicy: CompanyFundReconciliationRetryPolicy{
			InitialDelay: 2 * time.Second,
			MaxDelay:     16 * time.Second,
		},
		Now: nowFunc(now),
	})

	result, err := runtime.ReconcileDueWindows(context.Background())
	if !errors.Is(err, safeErr) || !errors.Is(err, airErr) {
		t.Fatalf("ReconcileDueWindows() error = %v, want both provider failures", err)
	}
	if result.Windows != 1 || result.Reconciliations != 2 || result.Failures != 2 || result.RetryFinalized != 1 || result.PartialFinalized != 1 {
		t.Fatalf("ReconcileDueWindows() result = %#v", result)
	}
	if len(finalizer.calls) != 2 {
		t.Fatalf("finalizer calls = %#v, want one per claimed failing run", finalizer.calls)
	}
	if call := finalizer.calls[0]; call.runID != 101 || call.outcome != CompanyFundSyncRunFinalizeRetry || !call.retryAt.Equal(now.UTC().Add(4*time.Second)) {
		t.Fatalf("safeheron finalization = %#v, want second-attempt retry", call)
	}
	if call := finalizer.calls[1]; call.runID != 202 || call.outcome != CompanyFundSyncRunFinalizePartial || !call.retryAt.Equal(now.UTC().Add(2*time.Second)) {
		t.Fatalf("airwallex finalization = %#v, want partial retry", call)
	}
	if len(safeheron.inputs) != 1 || safeheron.inputs[0].Account.ID != 11 || len(airwallex.inputs) != 1 || airwallex.inputs[0].Account.ID != 22 {
		t.Fatalf("reconciliation inputs safe=%#v air=%#v", safeheron.inputs, airwallex.inputs)
	}
}

func TestCompanyFundRuntime_ReconciliationNoWorkDoesNotFinalize(t *testing.T) {
	now := time.Date(2026, time.July, 10, 3, 1, 0, 0, singaporeLocation(t))
	finalizer := &companyFundRuntimeFinalizerStub{}
	safeheron := &companyFundRuntimeSafeheronReconcilerStub{calls: []companyFundRuntimeSafeheronCall{{
		err: ErrCompanyFundSyncRunNotReady,
	}}}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, []CompanyFundAccount{validCompanyFundRuntimeAccounts()[0]}),
		SafeheronReconciler: safeheron,
		SyncRunFinalizer:    finalizer,
	}, CompanyFundRuntimeConfig{
		ReconciliationSchedule: ReconciliationDailyScheduleConfig{CatchUpDays: 1},
		Now:                    nowFunc(now),
	})

	result, err := runtime.ReconcileDueWindows(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDueWindows() error = %v", err)
	}
	if result.NoWork != 1 || result.Failures != 0 || len(finalizer.calls) != 0 {
		t.Fatalf("no-work result = %#v finalizations=%#v", result, finalizer.calls)
	}
}

func TestCompanyFundRuntime_DefaultsLateStatusOverlapToSevenDays(t *testing.T) {
	runtime, err := NewCompanyFundRuntime(CompanyFundRuntimeDependencies{}, CompanyFundRuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.config.LateStatusOverlapDays != DefaultCompanyFundLateStatusOverlapDays || runtime.config.LateStatusOverlapConfigured {
		t.Fatalf("late-status default = days:%d configured:%t", runtime.config.LateStatusOverlapDays, runtime.config.LateStatusOverlapConfigured)
	}
}

func TestCompanyFundRuntime_LateStatusRollingOverlapReobservesDPlus2TerminalForBothProviders(t *testing.T) {
	location := singaporeLocation(t)
	transactionAt := time.Date(2026, time.July, 9, 12, 0, 0, 0, location).UTC()
	current := time.Date(2026, time.July, 10, 3, 1, 0, 0, location)
	safeheron := &companyFundRuntimeLateStatusSafeheronStub{transactionAt: transactionAt}
	airwallex := &companyFundRuntimeLateStatusAirwallexStub{
		transactionAt: transactionAt,
		contract: AirwallexFinancialTransactionsReconciliationContract{
			APIVersion: airwallexTestAPIVersion, SchemaVersion: "schema-v1", EventVersion: "event-v1", LoginAsScope: "awx-main",
		},
	}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, validCompanyFundRuntimeAccounts()),
		SafeheronReconciler: safeheron,
		AirwallexReconciler: airwallex,
		SyncRunFinalizer:    &companyFundRuntimeFinalizerStub{},
	}, CompanyFundRuntimeConfig{
		ReconciliationSchedule:      ReconciliationDailyScheduleConfig{CatchUpDays: 1},
		LateStatusOverlapConfigured: true,
		LateStatusOverlapDays:       3,
		Now:                         func() time.Time { return current },
	})

	first, err := runtime.ReconcileDueWindows(context.Background())
	if err != nil || first.Windows != 2 || first.Reconciliations != 4 {
		t.Fatalf("first daily/late run = %#v, %v", first, err)
	}
	if len(safeheron.inputs) != 2 || len(airwallex.inputs) != 2 ||
		safeheron.inputs[1].SyncKind != SafeheronTransactionHistoryLateStatusSyncKind ||
		airwallex.inputs[1].SyncKind != AirwallexFinancialTransactionsLateStatusSyncKind ||
		safeheron.inputs[1].WindowKey != "late-status:v1:2026-07-09:account:11" ||
		airwallex.inputs[1].WindowKey != "late-status:v1:2026-07-09:account:22" {
		t.Fatalf("first late-status inputs safe=%#v air=%#v", safeheron.inputs, airwallex.inputs)
	}

	// The transaction becomes terminal on D+2 after a missed webhook. The
	// next eligible daily key is independent of the original terminal run and
	// its rolling three-day interval still covers D, so both provider stubs can
	// observe the correction.
	safeheron.terminalAvailable = true
	airwallex.terminalAvailable = true
	current = time.Date(2026, time.July, 12, 3, 1, 0, 0, location)
	second, err := runtime.ReconcileDueWindows(context.Background())
	if err != nil || second.Windows != 2 || second.Reconciliations != 4 || !safeheron.terminalReobserved || !airwallex.terminalReobserved {
		t.Fatalf("D+2 terminal repair = %#v, %v safe=%#v air=%#v", second, err, safeheron, airwallex)
	}
	if safeheron.inputs[3].WindowKey != "late-status:v1:2026-07-11:account:11" ||
		airwallex.inputs[3].WindowKey != "late-status:v1:2026-07-11:account:22" {
		t.Fatalf("second late-status keys safe=%#v air=%#v", safeheron.inputs, airwallex.inputs)
	}

	current = current.Add(time.Minute)
	if _, err := runtime.ReconcileDueWindows(context.Background()); err != nil {
		t.Fatal(err)
	}
	if safeheron.inputs[5].WindowKey != "late-status:v1:2026-07-11:account:11" ||
		airwallex.inputs[5].WindowKey != "late-status:v1:2026-07-11:account:22" {
		t.Fatalf("same-day late-status key must remain stable safe=%#v air=%#v", safeheron.inputs, airwallex.inputs)
	}
}

func TestCompanyFundRuntime_LateStatusPassStillRunsWhenPrimaryDailyPassFails(t *testing.T) {
	now := time.Date(2026, time.July, 10, 3, 1, 0, 0, singaporeLocation(t))
	primaryErr := errors.New("primary history page unavailable")
	safeheron := &companyFundRuntimeSafeheronReconcilerStub{calls: []companyFundRuntimeSafeheronCall{
		{err: primaryErr},
		{},
	}}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, []CompanyFundAccount{validCompanyFundRuntimeAccounts()[0]}),
		SafeheronReconciler: safeheron,
		SyncRunFinalizer:    &companyFundRuntimeFinalizerStub{},
	}, CompanyFundRuntimeConfig{
		ReconciliationSchedule:      ReconciliationDailyScheduleConfig{CatchUpDays: 1},
		LateStatusOverlapConfigured: true,
		LateStatusOverlapDays:       1,
		Now:                         nowFunc(now),
	})

	result, err := runtime.ReconcileDueWindows(context.Background())
	if !errors.Is(err, primaryErr) || result.Windows != 2 || result.Reconciliations != 2 || len(safeheron.inputs) != 2 {
		t.Fatalf("primary failure / late repair = %#v, %v inputs=%#v", result, err, safeheron.inputs)
	}
	if safeheron.inputs[1].SyncKind != SafeheronTransactionHistoryLateStatusSyncKind || safeheron.inputs[1].WindowKey != "late-status:v1:2026-07-09:account:11" {
		t.Fatalf("late repair input = %#v", safeheron.inputs[1])
	}
}

func TestCompanyFundRuntime_FinalizesClaimedReconciliationWithLiveContextAfterParentCancellation(t *testing.T) {
	now := time.Date(2026, time.July, 10, 3, 1, 0, 0, singaporeLocation(t))
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	finalizer := &companyFundRuntimeFinalizerStub{}
	safeheron := &companyFundRuntimeSafeheronReconcilerStub{calls: []companyFundRuntimeSafeheronCall{{
		result: SafeheronTransactionHistoryReconcileResult{RunID: 333, AttemptCount: 1},
		err:    errors.New("provider request canceled"),
		cancel: cancel,
	}}}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, []CompanyFundAccount{validCompanyFundRuntimeAccounts()[0]}),
		SafeheronReconciler: safeheron,
		SyncRunFinalizer:    finalizer,
	}, CompanyFundRuntimeConfig{
		ReconciliationSchedule: ReconciliationDailyScheduleConfig{CatchUpDays: 1},
		FinalizeTimeout:        time.Second,
		Now:                    nowFunc(now),
	})

	_, _ = runtime.ReconcileDueWindows(parent)
	if len(finalizer.calls) != 1 || finalizer.calls[0].contextErr != nil {
		t.Fatalf("finalization must use a cancellation-independent bounded context: %#v", finalizer.calls)
	}
}

func TestCompanyFundRuntime_AirwallexWebhookWakeReconcilesOnlyTheOneScopedAccountAndStopsAfterRegistryBecomesMultiAccount(t *testing.T) {
	now := time.Date(2026, time.July, 10, 4, 15, 30, 0, time.UTC)
	airwallex := &companyFundRuntimeAirwallexReconcilerStub{
		contract: AirwallexFinancialTransactionsReconciliationContract{
			APIVersion: airwallexTestAPIVersion, SchemaVersion: "schema-v1", EventVersion: "event-v1", LoginAsScope: "awx-main",
		},
		calls: []companyFundRuntimeAirwallexCall{{result: AirwallexFinancialTransactionsReconcileResult{RunID: 401}}},
	}
	accounts := validCompanyFundRuntimeAccounts()
	snapshots := companyFundRuntimeSnapshotSource(t, accounts)
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    &snapshots,
		AirwallexReconciler: airwallex,
		SyncRunFinalizer:    &companyFundRuntimeFinalizerStub{},
	}, CompanyFundRuntimeConfig{
		AirwallexWebhookLookback: 2 * time.Hour,
		Now:                      nowFunc(now),
	})

	result, err := runtime.ReconcileAirwallexWebhookWindow(context.Background())
	if err != nil {
		t.Fatalf("ReconcileAirwallexWebhookWindow() error = %v", err)
	}
	if result.Reconciliations != 1 || len(airwallex.inputs) != 1 || airwallex.inputs[0].Account.ProviderAccountKey != "awx-main" {
		t.Fatalf("webhook reconciliation result=%#v inputs=%#v", result, airwallex.inputs)
	}
	for _, input := range airwallex.inputs {
		if !input.WindowStart.Equal(now.Add(-2*time.Hour)) || !input.WindowEnd.Equal(now) {
			t.Fatalf("webhook reconciliation window = [%s,%s), want [%s,%s)", input.WindowStart, input.WindowEnd, now.Add(-2*time.Hour), now)
		}
	}
	if runtime.NotifyAirwallexWebhook() != true || runtime.NotifyAirwallexWebhook() != false {
		t.Fatal("nonblocking Airwallex webhook wake must coalesce while one wake is pending")
	}

	multiAccountSnapshot, err := buildAccountRegistrySnapshot(append(accounts, CompanyFundAccount{
		ID: 23, Channel: AccountChannelAirwallex, ProviderAccountKey: "awx-secondary", Enabled: true,
	}), nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	snapshots.snapshot = multiAccountSnapshot
	result, err = runtime.ReconcileAirwallexWebhookWindow(context.Background())
	if err != nil || result.Reconciliations != 0 || len(airwallex.inputs) != 1 {
		t.Fatalf("multi-account snapshot must fail closed: result=%#v err=%v inputs=%#v", result, err, airwallex.inputs)
	}
	if runtime.NotifyAirwallexWebhook() {
		t.Fatal("webhook wake must fail closed after registry becomes multi-account")
	}
}

func TestCompanyFundRuntime_AirwallexWebhookWakeIsNoopWithoutAirwallexReconciler(t *testing.T) {
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{}, CompanyFundRuntimeConfig{})
	if runtime.NotifyAirwallexWebhook() {
		t.Fatal("Airwallex webhook wake must be a safe no-op when the reconciler is absent")
	}
	result, err := runtime.ReconcileAirwallexWebhookWindow(context.Background())
	if err != nil || result.Reconciliations != 0 {
		t.Fatalf("no-op webhook reconciliation = %#v, %v", result, err)
	}
}

func TestCompanyFundRuntime_AirwallexWebhookWakeQueuesBeforeStartWithoutProviderIO(t *testing.T) {
	airwallex := &companyFundRuntimeAirwallexReconcilerStub{
		contract: AirwallexFinancialTransactionsReconciliationContract{
			APIVersion: airwallexTestAPIVersion, SchemaVersion: "schema-v1", EventVersion: "event-v1", LoginAsScope: "awx-main",
		},
	}
	runtime := newCompanyFundRuntimeForTest(t, CompanyFundRuntimeDependencies{
		AccountSnapshots:    companyFundRuntimeSnapshotSource(t, validCompanyFundRuntimeAccounts()),
		AirwallexReconciler: airwallex,
		SyncRunFinalizer:    &companyFundRuntimeFinalizerStub{},
	}, CompanyFundRuntimeConfig{})

	if !runtime.NotifyAirwallexWebhook() || runtime.NotifyAirwallexWebhook() {
		t.Fatal("a pre-Start webhook wake must queue once and coalesce duplicates")
	}
	if len(airwallex.inputs) != 0 {
		t.Fatalf("NotifyAirwallexWebhook() must not make provider I/O before Start: %#v", airwallex.inputs)
	}
}

type companyFundRuntimeEventWorkerCall struct {
	result ProviderEventWorkerResult
	err    error
}

type companyFundRuntimeEventWorkerStub struct {
	mu      sync.Mutex
	results []companyFundRuntimeEventWorkerCall
	calls   int
	notify  chan struct{}
	due     time.Time
}

type companyFundRuntimeContinuousEventWorkerStub struct {
	notify chan struct{}
	calls  atomic.Int32
}

func (stub *companyFundRuntimeContinuousEventWorkerStub) ProcessNext(context.Context) (ProviderEventWorkerResult, error) {
	stub.calls.Add(1)
	select {
	case stub.notify <- struct{}{}:
	default:
	}
	return ProviderEventWorkerResult{}, nil
}

type companyFundRuntimeBlockingSafeheronReconciler struct {
	started chan struct{}
	once    sync.Once
}

func (stub *companyFundRuntimeBlockingSafeheronReconciler) Reconcile(ctx context.Context, _ SafeheronTransactionHistoryReconcileInput) (SafeheronTransactionHistoryReconcileResult, error) {
	stub.once.Do(func() { close(stub.started) })
	<-ctx.Done()
	return SafeheronTransactionHistoryReconcileResult{}, ctx.Err()
}

func (stub *companyFundRuntimeEventWorkerStub) ProcessNext(context.Context) (ProviderEventWorkerResult, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.notify != nil {
		select {
		case stub.notify <- struct{}{}:
		default:
		}
	}
	if stub.calls >= len(stub.results) {
		stub.calls++
		return ProviderEventWorkerResult{}, nil
	}
	call := stub.results[stub.calls]
	stub.calls++
	return call.result, call.err
}

func (stub *companyFundRuntimeEventWorkerStub) callCount() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.calls
}

func (stub *companyFundRuntimeEventWorkerStub) NextProviderEventDue(context.Context) (time.Time, error) {
	return stub.due, nil
}

type companyFundRuntimeSafeheronCall struct {
	result SafeheronTransactionHistoryReconcileResult
	err    error
	cancel context.CancelFunc
}

type companyFundRuntimeSafeheronReconcilerStub struct {
	calls  []companyFundRuntimeSafeheronCall
	inputs []SafeheronTransactionHistoryReconcileInput
}

func (stub *companyFundRuntimeSafeheronReconcilerStub) Reconcile(_ context.Context, input SafeheronTransactionHistoryReconcileInput) (SafeheronTransactionHistoryReconcileResult, error) {
	stub.inputs = append(stub.inputs, input)
	index := len(stub.inputs) - 1
	if index >= len(stub.calls) {
		return SafeheronTransactionHistoryReconcileResult{}, nil
	}
	call := stub.calls[index]
	if call.cancel != nil {
		call.cancel()
	}
	return call.result, call.err
}

type companyFundRuntimeAirwallexCall struct {
	result AirwallexFinancialTransactionsReconcileResult
	err    error
}

type companyFundRuntimeLateStatusSafeheronStub struct {
	inputs             []SafeheronTransactionHistoryReconcileInput
	transactionAt      time.Time
	terminalAvailable  bool
	terminalReobserved bool
}

func (stub *companyFundRuntimeLateStatusSafeheronStub) Reconcile(_ context.Context, input SafeheronTransactionHistoryReconcileInput) (SafeheronTransactionHistoryReconcileResult, error) {
	stub.inputs = append(stub.inputs, input)
	if stub.terminalAvailable && input.SyncKind == SafeheronTransactionHistoryLateStatusSyncKind &&
		!input.WindowStart.After(stub.transactionAt) && input.WindowEnd.After(stub.transactionAt) {
		stub.terminalReobserved = true
	}
	return SafeheronTransactionHistoryReconcileResult{}, nil
}

type companyFundRuntimeLateStatusAirwallexStub struct {
	contract           AirwallexFinancialTransactionsReconciliationContract
	inputs             []AirwallexFinancialTransactionsReconcileInput
	transactionAt      time.Time
	terminalAvailable  bool
	terminalReobserved bool
}

func (stub *companyFundRuntimeLateStatusAirwallexStub) Reconcile(_ context.Context, input AirwallexFinancialTransactionsReconcileInput) (AirwallexFinancialTransactionsReconcileResult, error) {
	stub.inputs = append(stub.inputs, input)
	if stub.terminalAvailable && input.SyncKind == AirwallexFinancialTransactionsLateStatusSyncKind &&
		!input.WindowStart.After(stub.transactionAt) && input.WindowEnd.After(stub.transactionAt) {
		stub.terminalReobserved = true
	}
	return AirwallexFinancialTransactionsReconcileResult{}, nil
}

func (stub *companyFundRuntimeLateStatusAirwallexStub) ReconciliationContract() AirwallexFinancialTransactionsReconciliationContract {
	return stub.contract
}

type companyFundRuntimeAirwallexReconcilerStub struct {
	contract AirwallexFinancialTransactionsReconciliationContract
	calls    []companyFundRuntimeAirwallexCall
	inputs   []AirwallexFinancialTransactionsReconcileInput
}

func (stub *companyFundRuntimeAirwallexReconcilerStub) Reconcile(_ context.Context, input AirwallexFinancialTransactionsReconcileInput) (AirwallexFinancialTransactionsReconcileResult, error) {
	stub.inputs = append(stub.inputs, input)
	index := len(stub.inputs) - 1
	if index >= len(stub.calls) {
		return AirwallexFinancialTransactionsReconcileResult{}, nil
	}
	call := stub.calls[index]
	return call.result, call.err
}

func (stub *companyFundRuntimeAirwallexReconcilerStub) ReconciliationContract() AirwallexFinancialTransactionsReconciliationContract {
	return stub.contract
}

type companyFundRuntimeFinalizeCall struct {
	runID      int64
	outcome    CompanyFundSyncRunFinalizeOutcome
	retryAt    time.Time
	detail     string
	contextErr error
}

type companyFundRuntimeFinalizerStub struct {
	calls []companyFundRuntimeFinalizeCall
	err   error
}

func (stub *companyFundRuntimeFinalizerStub) FinalizeRetry(ctx context.Context, runID int64, retryAt time.Time, detail string) error {
	stub.calls = append(stub.calls, companyFundRuntimeFinalizeCall{runID: runID, outcome: CompanyFundSyncRunFinalizeRetry, retryAt: retryAt, detail: detail, contextErr: ctx.Err()})
	return stub.err
}

func (stub *companyFundRuntimeFinalizerStub) FinalizePartial(ctx context.Context, runID int64, retryAt time.Time, detail string) error {
	stub.calls = append(stub.calls, companyFundRuntimeFinalizeCall{runID: runID, outcome: CompanyFundSyncRunFinalizePartial, retryAt: retryAt, detail: detail, contextErr: ctx.Err()})
	return stub.err
}

type companyFundRuntimeSnapshotProviderStub struct {
	snapshot *AccountRegistrySnapshot
}

func (stub companyFundRuntimeSnapshotProviderStub) Snapshot() *AccountRegistrySnapshot {
	return stub.snapshot
}

func companyFundRuntimeSnapshotSource(t *testing.T, accounts []CompanyFundAccount) companyFundRuntimeSnapshotProviderStub {
	t.Helper()
	snapshot, err := buildAccountRegistrySnapshot(accounts, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildAccountRegistrySnapshot() error = %v", err)
	}
	return companyFundRuntimeSnapshotProviderStub{snapshot: snapshot}
}

func validCompanyFundRuntimeAccounts() []CompanyFundAccount {
	return []CompanyFundAccount{
		{
			ID: 11, Channel: AccountChannelSafeheron, ProviderAccountKey: "safe-vault-main", NetworkFamily: "EVM",
			NormalizedAddress: "0x0000000000000000000000000000000000000011", Enabled: true,
			MonitoringStartedAt: time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{ID: 22, Channel: AccountChannelAirwallex, ProviderAccountKey: "awx-main", Enabled: true},
	}
}

func TestCompanyFundSafeheronWindowHonorsMonitoringStart(t *testing.T) {
	monitoringStartedAt := time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)
	account := CompanyFundAccount{MonitoringStartedAt: monitoringStartedAt}

	clamped, ok := companyFundSafeheronWindow(account, CompanyFundReconciliationWindow{
		Key:   "crosses-monitoring-start",
		Start: monitoringStartedAt.Add(-time.Hour),
		End:   monitoringStartedAt.Add(time.Hour),
	})
	if !ok || !clamped.Start.Equal(monitoringStartedAt) || !clamped.End.Equal(monitoringStartedAt.Add(time.Hour)) {
		t.Fatalf("clamped window = %#v ok=%t", clamped, ok)
	}

	if _, ok := companyFundSafeheronWindow(account, CompanyFundReconciliationWindow{
		Key:   "before-monitoring-start",
		Start: monitoringStartedAt.Add(-2 * time.Hour),
		End:   monitoringStartedAt,
	}); ok {
		t.Fatal("window ending at monitoring start must be skipped")
	}
	if _, ok := companyFundSafeheronWindow(CompanyFundAccount{}, CompanyFundReconciliationWindow{
		Key:   "missing-monitoring-start",
		Start: monitoringStartedAt,
		End:   monitoringStartedAt.Add(time.Hour),
	}); ok {
		t.Fatal("missing monitoring start must fail closed")
	}
}

func newCompanyFundRuntimeForTest(t *testing.T, dependencies CompanyFundRuntimeDependencies, config CompanyFundRuntimeConfig) *CompanyFundRuntime {
	t.Helper()
	// Existing focused runtime tests exercise their declared normal-window
	// behavior. Late-status overlap gets explicit tests below, while production
	// construction still defaults it to seven days.
	if !config.LateStatusOverlapConfigured {
		config.LateStatusOverlapConfigured = true
		config.LateStatusOverlapDays = 0
	}
	runtime, err := NewCompanyFundRuntime(dependencies, config)
	if err != nil {
		t.Fatalf("NewCompanyFundRuntime() error = %v", err)
	}
	return runtime
}

func nowFunc(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func singaporeLocation(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(DefaultCompanyFundReconciliationTimeZone)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
