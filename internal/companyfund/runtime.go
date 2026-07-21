package companyfund

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	defaultCompanyFundEventPollInterval             = time.Second
	defaultCompanyFundEventDrainLimit               = 100
	maxCompanyFundEventDrainLimit                   = 10_000
	defaultCompanyFundReconciliationPollInterval    = time.Minute
	defaultCompanyFundReconciliationFinalizeTimeout = 10 * time.Second
	defaultCompanyFundAirwallexWebhookLookback      = 24 * time.Hour
	maxCompanyFundAirwallexWebhookLookback          = 7 * 24 * time.Hour
)

// CompanyFundProviderEventProcessor is satisfied by ProviderEventWorker. The
// runtime intentionally sees only its one-item, durable lease boundary.
type CompanyFundProviderEventProcessor interface {
	ProcessNext(ctx context.Context) (ProviderEventWorkerResult, error)
}

// CompanyFundAccountSnapshotProvider is satisfied by AccountRegistry. The
// runner takes one immutable snapshot per reconciliation cycle, so a cache
// refresh cannot change the selected account halfway through a provider scan.
type CompanyFundAccountSnapshotProvider interface {
	Snapshot() *AccountRegistrySnapshot
}

// CompanyFundSafeheronReconciliationRunner is satisfied by the history
// reconciler. It contains no webhook or account-resolution behavior.
type CompanyFundSafeheronReconciliationRunner interface {
	Reconcile(ctx context.Context, input SafeheronTransactionHistoryReconcileInput) (SafeheronTransactionHistoryReconcileResult, error)
}

// CompanyFundAirwallexReconciliationRunner is satisfied by the Financial
// Transactions reconciler. The contract comes from the reconciler itself so a
// scheduler cannot accidentally use a different parser/API version tuple.
type CompanyFundAirwallexReconciliationRunner interface {
	Reconcile(ctx context.Context, input AirwallexFinancialTransactionsReconcileInput) (AirwallexFinancialTransactionsReconcileResult, error)
	ReconciliationContract() AirwallexFinancialTransactionsReconciliationContract
}

// CompanyFundReconciliationRunFinalizer is satisfied by the exact sync-run
// adapter. It only finalizes an active, explicitly requested run; it can never
// claim an unrelated account/window from a shared queue.
type CompanyFundReconciliationRunFinalizer interface {
	FinalizeRetry(ctx context.Context, runID int64, retryAt time.Time, failureDetail string) error
	FinalizePartial(ctx context.Context, runID int64, retryAt time.Time, failureDetail string) error
}

// CompanyFundReconciliationRetryPolicy is a bounded backoff policy for
// reconciliation scans. Attempt one uses InitialDelay; later attempts double
// until MaxDelay. It is deliberately separate from provider-event retries so
// operations can tune the two durable queues independently.
type CompanyFundReconciliationRetryPolicy struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

func (policy CompanyFundReconciliationRetryPolicy) validate() error {
	if policy.InitialDelay <= 0 || policy.InitialDelay.Microseconds() <= 0 {
		return fmt.Errorf("company-fund reconciliation retry initial delay must be at least one microsecond")
	}
	if policy.MaxDelay <= 0 || policy.MaxDelay < policy.InitialDelay {
		return fmt.Errorf("company-fund reconciliation retry max delay must be positive and no smaller than the initial delay")
	}
	return nil
}

func (policy CompanyFundReconciliationRetryPolicy) Delay(attempt int) (time.Duration, error) {
	if err := policy.validate(); err != nil {
		return 0, err
	}
	if attempt <= 0 {
		return 0, fmt.Errorf("company-fund reconciliation retry attempt must be positive")
	}
	delay := policy.InitialDelay
	for retry := 1; retry < attempt && delay < policy.MaxDelay; retry++ {
		if delay > policy.MaxDelay/2 {
			return policy.MaxDelay, nil
		}
		delay *= 2
	}
	if delay > policy.MaxDelay {
		return policy.MaxDelay, nil
	}
	return delay, nil
}

// CompanyFundRuntimeDependencies composes durable event consumption and the
// two provider reconciliation paths. Event processing is optional so a caller
// can deploy the REST compensator independently, but any reconciliation path
// requires the immutable account cache and exact sync-run finalizer.
type CompanyFundRuntimeDependencies struct {
	ProviderEventWorker CompanyFundProviderEventProcessor
	AccountSnapshots    CompanyFundAccountSnapshotProvider
	SafeheronReconciler CompanyFundSafeheronReconciliationRunner
	AirwallexReconciler CompanyFundAirwallexReconciliationRunner
	SyncRunFinalizer    CompanyFundReconciliationRunFinalizer
}

// CompanyFundRuntimeConfig contains process-local scheduling limits only. It
// has no provider credentials. An empty daily schedule defaults to 03:00 in
// Asia/Singapore (UTC+8), and NewCompanyFundRuntime performs no I/O; callers
// must explicitly call Start with their service lifecycle context.
type CompanyFundRuntimeConfig struct {
	EventPollInterval          time.Duration
	EventDrainLimit            int
	ReconciliationPollInterval time.Duration
	ReconciliationSchedule     ReconciliationDailyScheduleConfig
	// LateStatusOverlapDays defaults to seven unless LateStatusOverlapConfigured
	// is true. An explicit configured zero disables the independent terminal
	// status repair pass without changing the primary daily compensation.
	LateStatusOverlapDays       int
	LateStatusOverlapConfigured bool
	ReconciliationRetryPolicy   CompanyFundReconciliationRetryPolicy
	FinalizeTimeout             time.Duration
	AirwallexWebhookLookback    time.Duration
	Now                         func() time.Time
}

// CompanyFundProviderEventDrainResult is a bounded one-cycle result. A
// caller can inspect it in tests or a metrics layer without accessing raw
// provider payloads.
type CompanyFundProviderEventDrainResult struct {
	Attempts      int
	Claimed       int
	FactCount     int
	MovementCount int
	LimitReached  bool
}

// CompanyFundReconciliationCycleResult records scheduler work without
// exposing account IDs, provider bodies, or provider error messages.
type CompanyFundReconciliationCycleResult struct {
	Windows          int
	Reconciliations  int
	NoWork           int
	SkippedAccounts  int
	Failures         int
	RetryFinalized   int
	PartialFinalized int
}

// CompanyFundRuntime owns the in-process timing only. PostgreSQL sync-run
// leases remain the authoritative cross-replica coordination and retry state.
type CompanyFundRuntime struct {
	dependencies      CompanyFundRuntimeDependencies
	config            CompanyFundRuntimeConfig
	schedule          *ReconciliationDailySchedule
	airwallexContract AirwallexFinancialTransactionsReconciliationContract
	airwallexWake     chan struct{}

	mu        sync.Mutex
	running   bool
	runCancel context.CancelFunc
	runDone   chan struct{}
}

// NewCompanyFundRuntime validates only local dependencies/configuration. It
// never reads the registry, database, queue, or provider; Start(ctx) is the
// explicit lifecycle boundary that begins background polling.
func NewCompanyFundRuntime(dependencies CompanyFundRuntimeDependencies, config CompanyFundRuntimeConfig) (*CompanyFundRuntime, error) {
	normalized, schedule, err := normalizeCompanyFundRuntimeConfig(config)
	if err != nil {
		return nil, err
	}
	hasReconciliation := dependencies.SafeheronReconciler != nil || dependencies.AirwallexReconciler != nil
	if hasReconciliation && dependencies.AccountSnapshots == nil {
		return nil, fmt.Errorf("company-fund runtime account snapshot provider is required for reconciliation")
	}
	if hasReconciliation && dependencies.SyncRunFinalizer == nil {
		return nil, fmt.Errorf("company-fund runtime sync-run finalizer is required for reconciliation")
	}

	runtime := &CompanyFundRuntime{
		dependencies: dependencies,
		config:       normalized,
		schedule:     schedule,
	}
	if dependencies.AirwallexReconciler != nil {
		contract := dependencies.AirwallexReconciler.ReconciliationContract()
		if err := contract.validate(); err != nil {
			return nil, fmt.Errorf("company-fund Airwallex reconciliation contract is invalid: %w", err)
		}
		runtime.airwallexContract = contract
		// A capacity of one intentionally coalesces bursty webhook deliveries:
		// the following REST scan covers every configured account in the same
		// bounded time window, not the webhook envelope's account identifier.
		runtime.airwallexWake = make(chan struct{}, 1)
	}
	return runtime, nil
}

func normalizeCompanyFundRuntimeConfig(config CompanyFundRuntimeConfig) (CompanyFundRuntimeConfig, *ReconciliationDailySchedule, error) {
	if config.EventPollInterval == 0 {
		config.EventPollInterval = defaultCompanyFundEventPollInterval
	}
	if config.EventPollInterval <= 0 || config.EventPollInterval.Microseconds() <= 0 {
		return CompanyFundRuntimeConfig{}, nil, fmt.Errorf("company-fund event poll interval must be positive")
	}
	if config.EventDrainLimit == 0 {
		config.EventDrainLimit = defaultCompanyFundEventDrainLimit
	}
	if config.EventDrainLimit < 1 || config.EventDrainLimit > maxCompanyFundEventDrainLimit {
		return CompanyFundRuntimeConfig{}, nil, fmt.Errorf("company-fund event drain limit must be between 1 and %d", maxCompanyFundEventDrainLimit)
	}
	if config.ReconciliationPollInterval == 0 {
		config.ReconciliationPollInterval = defaultCompanyFundReconciliationPollInterval
	}
	if config.ReconciliationPollInterval <= 0 || config.ReconciliationPollInterval.Microseconds() <= 0 {
		return CompanyFundRuntimeConfig{}, nil, fmt.Errorf("company-fund reconciliation poll interval must be positive")
	}
	if !config.LateStatusOverlapConfigured {
		config.LateStatusOverlapDays = DefaultCompanyFundLateStatusOverlapDays
	}
	if config.LateStatusOverlapDays < 0 || config.LateStatusOverlapDays > maxCompanyFundLateStatusOverlapDays {
		return CompanyFundRuntimeConfig{}, nil, fmt.Errorf("company-fund late-status overlap days must be between 0 and %d", maxCompanyFundLateStatusOverlapDays)
	}
	if config.ReconciliationRetryPolicy.InitialDelay == 0 && config.ReconciliationRetryPolicy.MaxDelay == 0 {
		config.ReconciliationRetryPolicy = CompanyFundReconciliationRetryPolicy{InitialDelay: time.Minute, MaxDelay: time.Hour}
	}
	if err := config.ReconciliationRetryPolicy.validate(); err != nil {
		return CompanyFundRuntimeConfig{}, nil, err
	}
	if config.FinalizeTimeout == 0 {
		config.FinalizeTimeout = defaultCompanyFundReconciliationFinalizeTimeout
	}
	if config.FinalizeTimeout <= 0 || config.FinalizeTimeout.Microseconds() <= 0 {
		return CompanyFundRuntimeConfig{}, nil, fmt.Errorf("company-fund reconciliation finalization timeout must be positive")
	}
	if config.AirwallexWebhookLookback == 0 {
		config.AirwallexWebhookLookback = defaultCompanyFundAirwallexWebhookLookback
	}
	if config.AirwallexWebhookLookback <= 0 || config.AirwallexWebhookLookback > maxCompanyFundAirwallexWebhookLookback {
		return CompanyFundRuntimeConfig{}, nil, fmt.Errorf("company-fund Airwallex webhook lookback must be between %s and %s", airwallexFinancialTransactionsQueryTimePrecision, maxCompanyFundAirwallexWebhookLookback)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	schedule, err := NewReconciliationDailySchedule(config.ReconciliationSchedule)
	if err != nil {
		return CompanyFundRuntimeConfig{}, nil, err
	}
	return config, schedule, nil
}

// Start explicitly begins the context-cancellable background loops. Calling it
// more than once is safe and never starts a second worker set.
func (runtime *CompanyFundRuntime) Start(parent context.Context) {
	if runtime == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	runtime.mu.Lock()
	if runtime.running {
		runtime.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	runtime.running = true
	runtime.runCancel = cancel
	runtime.runDone = done
	runtime.mu.Unlock()

	go func() {
		defer func() {
			runtime.mu.Lock()
			if runtime.runDone == done {
				runtime.running = false
				runtime.runCancel = nil
				runtime.runDone = nil
			}
			runtime.mu.Unlock()
			close(done)
		}()
		runtime.Run(ctx)
	}()
}

// Stop cancels and waits for a background Start loop. It is safe to call more
// than once and does not cancel a caller-managed direct Run invocation.
func (runtime *CompanyFundRuntime) Stop() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	cancel := runtime.runCancel
	done := runtime.runDone
	runtime.mu.Unlock()
	if cancel == nil || done == nil {
		return
	}
	cancel()
	<-done
}

// Run executes background timing until ctx is canceled. Errors from one
// bounded cycle are deliberately contained: the next tick retries through the
// durable queue rather than permanently disabling company-fund ingestion.
// It performs no logging, which prevents raw provider bytes from leaking from
// parser or transport errors into the process log.
func (runtime *CompanyFundRuntime) Run(ctx context.Context) {
	if runtime == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var loops sync.WaitGroup
	if runtime.dependencies.ProviderEventWorker != nil {
		loops.Add(1)
		go func() {
			defer loops.Done()
			runtime.runProviderEventLoop(ctx)
		}()
	}
	if runtime.hasReconciliation() {
		loops.Add(1)
		go func() {
			defer loops.Done()
			runtime.runReconciliationLoop(ctx)
		}()
	}
	loops.Wait()
}

func (runtime *CompanyFundRuntime) runProviderEventLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			result, err := runtime.DrainProviderEvents(ctx)
			if err != nil && ctx.Err() == nil {
				// Keep provider payloads and database details out of process logs.
				// The durable lease owns retries; this metadata-only signal exposes
				// a repeatedly deferred event without leaking its contents.
				log.Printf("company-fund provider event drain deferred: kind=%s claimed=%d", providerEventDrainFailureKind(err), result.Claimed)
			}
			timer.Reset(runtime.config.EventPollInterval)
		}
	}
}

func providerEventDrainFailureKind(err error) string {
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ErrProviderEventLeaseNotOwned):
		return "lease_not_owned"
	case errors.Is(err, ErrProviderEventClaimLost):
		return "claim_lost"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline_exceeded"
	case errors.As(err, &pgErr) && pgErr.Code != "":
		// SQLSTATE is stable database metadata, unlike an error message which
		// can contain provider-originated values through a wrapped failure.
		return "database_" + pgErr.Code
	default:
		return "processing_error"
	}
}

func (runtime *CompanyFundRuntime) runReconciliationLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, _ = runtime.ReconcileDueWindows(ctx)
			timer.Reset(runtime.config.ReconciliationPollInterval)
		case <-runtime.airwallexWake:
			_, _ = runtime.ReconcileAirwallexWebhookWindow(ctx)
		}
	}
}

// NotifyAirwallexWebhook is the nonblocking callback for a handler to call
// only after it has durably inserted a verified Airwallex webhook inbox event.
// The callback deliberately accepts no webhook account ID: the subsequent
// scan uses only the one configured account that exactly matches the client\'s
// x-login-as scope. The Financial Transactions response cannot prove account
// ownership, so every snapshot is rechecked and multi-account settings fail
// closed.
// It returns true only when it queued a new wake; this also works before
// Start so service composition can finish before background I/O begins. False
// means the Airwallex reconciler is absent or work was safely coalesced.
func (runtime *CompanyFundRuntime) NotifyAirwallexWebhook() bool {
	if runtime == nil || runtime.dependencies.AirwallexReconciler == nil || runtime.airwallexWake == nil || !runtime.AirwallexReconciliationEnabled() {
		return false
	}
	select {
	case runtime.airwallexWake <- struct{}{}:
		return true
	default:
		return false
	}
}

// AirwallexWebhookWakeFunc returns a callback-shaped view of
// NotifyAirwallexWebhook for HTTP handler composition. It never performs
// provider I/O in the request lifecycle.
func (runtime *CompanyFundRuntime) AirwallexWebhookWakeFunc() func() {
	if runtime == nil || runtime.dependencies.AirwallexReconciler == nil || !runtime.AirwallexReconciliationEnabled() {
		return nil
	}
	return func() { _ = runtime.NotifyAirwallexWebhook() }
}

// AirwallexReconciliationEnabled reports whether the current immutable
// account snapshot still proves that this one client is scoped to exactly one
// enabled Airwallex company account. It is intentionally evaluated on every
// call so a registry refresh that introduces another account stops both REST
// compensation and webhook wake-ups without a process restart.
func (runtime *CompanyFundRuntime) AirwallexReconciliationEnabled() bool {
	if runtime == nil || runtime.dependencies.AirwallexReconciler == nil || runtime.dependencies.AccountSnapshots == nil {
		return false
	}
	_, ok := ResolveAirwallexSingleAccountScope(runtime.dependencies.AccountSnapshots.Snapshot(), runtime.airwallexContract.LoginAsScope)
	return ok
}

// AirwallexWebhookEligibilityFunc exposes the dynamic ownership gate to the
// HTTP boundary. A route can remain registered while safely returning no work
// after an account-cache refresh invalidates its one-account scope.
func (runtime *CompanyFundRuntime) AirwallexWebhookEligibilityFunc() func() bool {
	if runtime == nil || runtime.dependencies.AirwallexReconciler == nil {
		return nil
	}
	return runtime.AirwallexReconciliationEnabled
}

// DrainProviderEvents processes at most EventDrainLimit durable deliveries.
// It stops at the first worker infrastructure error to prevent a tight retry
// loop; normalizer/provider failures are already terminalized or scheduled by
// ProviderEventWorker itself.
func (runtime *CompanyFundRuntime) DrainProviderEvents(ctx context.Context) (CompanyFundProviderEventDrainResult, error) {
	if runtime == nil || runtime.dependencies.ProviderEventWorker == nil {
		return CompanyFundProviderEventDrainResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := CompanyFundProviderEventDrainResult{}
	for result.Attempts < runtime.config.EventDrainLimit {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Attempts++
		workerResult, err := runtime.dependencies.ProviderEventWorker.ProcessNext(ctx)
		if workerResult.Claimed {
			result.Claimed++
			result.FactCount += workerResult.FactCount
			result.MovementCount += workerResult.MovementCount
		}
		if err != nil {
			return result, err
		}
		if !workerResult.Claimed {
			return result, nil
		}
	}
	result.LimitReached = true
	return result, nil
}

// ReconcileDueWindows scans all configured provider accounts for the
// independently idempotent local-date windows due under the UTC+8 schedule.
// It continues after a per-account failure and returns a joined error only
// after attempting finalization of every claimed failed run.
func (runtime *CompanyFundRuntime) ReconcileDueWindows(ctx context.Context) (CompanyFundReconciliationCycleResult, error) {
	if runtime == nil || !runtime.hasReconciliation() {
		return CompanyFundReconciliationCycleResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now, err := runtime.currentTime()
	if err != nil {
		return CompanyFundReconciliationCycleResult{}, err
	}
	windows, err := runtime.schedule.DueWindows(now)
	if err != nil {
		return CompanyFundReconciliationCycleResult{}, err
	}
	result := CompanyFundReconciliationCycleResult{Windows: len(windows)}
	result, normalErr := runtime.reconcileWindows(ctx, windows, result)
	if runtime.config.LateStatusOverlapDays == 0 {
		return result, normalErr
	}
	lateWindows, err := runtime.schedule.LateStatusWindows(now, runtime.config.LateStatusOverlapDays)
	if err != nil {
		return result, errors.Join(normalErr, err)
	}
	result.Windows += len(lateWindows)
	result, lateErr := runtime.reconcileWindows(ctx, lateWindows, result)
	return result, errors.Join(normalErr, lateErr)
}

// ReconcileAirwallexWebhookWindow performs a bounded REST catch-up for every
// configured Airwallex account. It intentionally never consumes a webhook's
// account_id or organisation identifier; only the cached management settings
// decide the accounts that can enter the ledger.
func (runtime *CompanyFundRuntime) ReconcileAirwallexWebhookWindow(ctx context.Context) (CompanyFundReconciliationCycleResult, error) {
	if runtime == nil || runtime.dependencies.AirwallexReconciler == nil {
		return CompanyFundReconciliationCycleResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now, err := runtime.currentTime()
	if err != nil {
		return CompanyFundReconciliationCycleResult{}, err
	}
	window := CompanyFundReconciliationWindow{
		Key:   "airwallex-webhook-recent",
		Start: now.Add(-runtime.config.AirwallexWebhookLookback),
		End:   now,
	}
	result := CompanyFundReconciliationCycleResult{Windows: 1}
	return runtime.reconcileAirwallexWindow(ctx, window, result)
}

func (runtime *CompanyFundRuntime) reconcileWindows(ctx context.Context, windows []CompanyFundReconciliationWindow, result CompanyFundReconciliationCycleResult) (CompanyFundReconciliationCycleResult, error) {
	snapshot, err := runtime.accountSnapshot()
	if err != nil {
		return result, err
	}
	accounts := snapshot.Accounts()
	airwallexAccount, airwallexEligible := ResolveAirwallexSingleAccountScope(snapshot, runtime.airwallexContract.LoginAsScope)
	var failures []error
	for _, window := range windows {
		for _, account := range accounts {
			if err := ctx.Err(); err != nil {
				return result, errors.Join(append(failures, err)...)
			}
			switch account.Channel {
			case AccountChannelSafeheron:
				if runtime.dependencies.SafeheronReconciler == nil {
					continue
				}
				var reconcileErr error
				result, reconcileErr = runtime.reconcileSafeheronWindow(ctx, account, window, result)
				if reconcileErr != nil {
					failures = append(failures, reconcileErr)
				}
			case AccountChannelAirwallex:
				if runtime.dependencies.AirwallexReconciler == nil || !airwallexEligible || account.ID != airwallexAccount.ID {
					continue
				}
				var reconcileErr error
				result, reconcileErr = runtime.reconcileAirwallexAccountWindow(ctx, account, window, result)
				if reconcileErr != nil {
					failures = append(failures, reconcileErr)
				}
			}
		}
	}
	return result, errors.Join(failures...)
}

func (runtime *CompanyFundRuntime) reconcileAirwallexWindow(ctx context.Context, window CompanyFundReconciliationWindow, result CompanyFundReconciliationCycleResult) (CompanyFundReconciliationCycleResult, error) {
	account, eligible := runtime.airwallexReconciliationAccount()
	if !eligible {
		return result, nil
	}
	var failures []error
	if err := ctx.Err(); err != nil {
		return result, errors.Join(append(failures, err)...)
	}
	var reconcileErr error
	result, reconcileErr = runtime.reconcileAirwallexAccountWindow(ctx, account, window, result)
	if reconcileErr != nil {
		failures = append(failures, reconcileErr)
	}
	return result, errors.Join(failures...)
}

func (runtime *CompanyFundRuntime) airwallexReconciliationAccount() (CompanyFundAccount, bool) {
	if runtime == nil || runtime.dependencies.AirwallexReconciler == nil || runtime.dependencies.AccountSnapshots == nil {
		return CompanyFundAccount{}, false
	}
	return ResolveAirwallexSingleAccountScope(runtime.dependencies.AccountSnapshots.Snapshot(), runtime.airwallexContract.LoginAsScope)
}

func (runtime *CompanyFundRuntime) reconcileSafeheronWindow(ctx context.Context, account CompanyFundAccount, window CompanyFundReconciliationWindow, result CompanyFundReconciliationCycleResult) (CompanyFundReconciliationCycleResult, error) {
	window, eligible := companyFundSafeheronWindow(account, window)
	if !eligible {
		result.SkippedAccounts++
		return result, nil
	}
	providerAccountKey, ok := companyFundRuntimeProviderAccountKey(account)
	if !ok {
		result.SkippedAccounts++
		return result, nil
	}
	result.Reconciliations++
	input := SafeheronTransactionHistoryReconcileInput{
		Account: account, ProviderAccountKey: providerAccountKey, WindowStart: window.Start, WindowEnd: window.End,
	}
	if companyFundLateStatusWindow(window) {
		input.SyncKind = SafeheronTransactionHistoryLateStatusSyncKind
		input.WindowKey = companyFundLateStatusAccountWindowKey(window, account)
	}
	reconcileResult, err := runtime.dependencies.SafeheronReconciler.Reconcile(ctx, input)
	if err == nil {
		return result, nil
	}
	if isCompanyFundReconciliationNoWork(err) {
		result.NoWork++
		return result, nil
	}
	return runtime.finalizeSafeheronFailure(ctx, reconcileResult, result, err)
}

func companyFundSafeheronWindow(account CompanyFundAccount, window CompanyFundReconciliationWindow) (CompanyFundReconciliationWindow, bool) {
	monitoringStartedAt := account.MonitoringStartedAt.UTC()
	if monitoringStartedAt.IsZero() || window.Start.IsZero() || window.End.IsZero() || !window.Start.Before(window.End) {
		return CompanyFundReconciliationWindow{}, false
	}
	if !window.End.After(monitoringStartedAt) {
		return CompanyFundReconciliationWindow{}, false
	}
	if window.Start.Before(monitoringStartedAt) {
		window.Start = monitoringStartedAt
	}
	return window, window.Start.Before(window.End)
}

func (runtime *CompanyFundRuntime) reconcileAirwallexAccountWindow(ctx context.Context, account CompanyFundAccount, window CompanyFundReconciliationWindow, result CompanyFundReconciliationCycleResult) (CompanyFundReconciliationCycleResult, error) {
	providerAccountKey, ok := companyFundRuntimeProviderAccountKey(account)
	if !ok {
		result.SkippedAccounts++
		return result, nil
	}
	result.Reconciliations++
	input := AirwallexFinancialTransactionsReconcileInput{
		Account:            account,
		ProviderAccountKey: providerAccountKey,
		WindowStart:        window.Start,
		WindowEnd:          window.End,
		APIVersion:         runtime.airwallexContract.APIVersion,
		SchemaVersion:      runtime.airwallexContract.SchemaVersion,
		EventVersion:       runtime.airwallexContract.EventVersion,
	}
	if companyFundLateStatusWindow(window) {
		input.SyncKind = AirwallexFinancialTransactionsLateStatusSyncKind
		input.WindowKey = companyFundLateStatusAccountWindowKey(window, account)
	}
	reconcileResult, err := runtime.dependencies.AirwallexReconciler.Reconcile(ctx, input)
	if err == nil {
		return result, nil
	}
	if isCompanyFundReconciliationNoWork(err) {
		result.NoWork++
		return result, nil
	}
	return runtime.finalizeAirwallexFailure(ctx, reconcileResult, result, err)
}

func (runtime *CompanyFundRuntime) finalizeSafeheronFailure(ctx context.Context, reconcileResult SafeheronTransactionHistoryReconcileResult, result CompanyFundReconciliationCycleResult, providerErr error) (CompanyFundReconciliationCycleResult, error) {
	result.Failures++
	if reconcileResult.RunID <= 0 {
		return result, providerErr
	}
	partial := companyFundReconciliationHasPartialProgress(
		reconcileResult.PagesFetched,
		reconcileResult.CandidatesSeen,
		reconcileResult.EventsCreated,
		reconcileResult.EventsExisting,
	)
	finalizeErr := runtime.finalizeReconciliationFailure(ctx, ChannelSafeheron, reconcileResult.RunID, reconcileResult.AttemptCount, partial)
	if finalizeErr == nil {
		if partial {
			result.PartialFinalized++
		} else {
			result.RetryFinalized++
		}
	}
	return result, errors.Join(providerErr, finalizeErr)
}

func (runtime *CompanyFundRuntime) finalizeAirwallexFailure(ctx context.Context, reconcileResult AirwallexFinancialTransactionsReconcileResult, result CompanyFundReconciliationCycleResult, providerErr error) (CompanyFundReconciliationCycleResult, error) {
	result.Failures++
	if reconcileResult.RunID <= 0 {
		return result, providerErr
	}
	partial := companyFundReconciliationHasPartialProgress(
		reconcileResult.PagesFetched,
		reconcileResult.CandidatesSeen,
		reconcileResult.EventsCreated,
		reconcileResult.EventsExisting,
	)
	finalizeErr := runtime.finalizeReconciliationFailure(ctx, ChannelAirwallex, reconcileResult.RunID, reconcileResult.AttemptCount, partial)
	if finalizeErr == nil {
		if partial {
			result.PartialFinalized++
		} else {
			result.RetryFinalized++
		}
	}
	return result, errors.Join(providerErr, finalizeErr)
}

func (runtime *CompanyFundRuntime) finalizeReconciliationFailure(parent context.Context, channel Channel, runID int64, attempt int, partial bool) error {
	if attempt <= 0 {
		attempt = 1
	}
	delay, err := runtime.config.ReconciliationRetryPolicy.Delay(attempt)
	if err != nil {
		return err
	}
	now, err := runtime.currentTime()
	if err != nil {
		return err
	}
	// A provider request may return because the service is shutting down. Once
	// we know an exact run was leased, use a separate bounded DB context to
	// attempt durable finalization rather than leaving the lease active until
	// expiry solely because the request context was canceled.
	if parent == nil {
		parent = context.Background()
	}
	finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), runtime.config.FinalizeTimeout)
	defer cancel()
	retryAt := now.Add(delay)
	detail := "company-fund " + strings.ToLower(string(channel)) + " reconciliation failed; retry scheduled"
	if partial {
		return runtime.dependencies.SyncRunFinalizer.FinalizePartial(finalizeCtx, runID, retryAt, detail)
	}
	return runtime.dependencies.SyncRunFinalizer.FinalizeRetry(finalizeCtx, runID, retryAt, detail)
}

func (runtime *CompanyFundRuntime) accountSnapshot() (*AccountRegistrySnapshot, error) {
	if runtime == nil || runtime.dependencies.AccountSnapshots == nil {
		return nil, fmt.Errorf("company-fund runtime account snapshot provider is not configured")
	}
	snapshot := runtime.dependencies.AccountSnapshots.Snapshot()
	if snapshot == nil {
		return nil, fmt.Errorf("company-fund runtime account snapshot is unavailable")
	}
	return snapshot, nil
}

func (runtime *CompanyFundRuntime) currentTime() (time.Time, error) {
	if runtime == nil || runtime.config.Now == nil {
		return time.Time{}, fmt.Errorf("company-fund runtime clock is not configured")
	}
	now := runtime.config.Now().UTC()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("company-fund runtime clock returned zero time")
	}
	return now, nil
}

func (runtime *CompanyFundRuntime) hasReconciliation() bool {
	return runtime != nil && (runtime.dependencies.SafeheronReconciler != nil || runtime.dependencies.AirwallexReconciler != nil)
}

func companyFundRuntimeProviderAccountKey(account CompanyFundAccount) (string, bool) {
	if !account.Enabled {
		return "", false
	}
	key := strings.TrimSpace(account.ProviderAccountKey)
	if key == "" || key != account.ProviderAccountKey {
		return "", false
	}
	return key, true
}

func companyFundLateStatusWindow(window CompanyFundReconciliationWindow) bool {
	return strings.HasPrefix(window.Key, companyFundLateStatusWindowKeyPrefix)
}

func companyFundLateStatusAccountWindowKey(window CompanyFundReconciliationWindow, account CompanyFundAccount) string {
	return window.Key + ":account:" + strconv.FormatInt(account.ID, 10)
}

func companyFundReconciliationHasPartialProgress(values ...int) bool {
	for _, value := range values {
		if value > 0 {
			return true
		}
	}
	return false
}

func isCompanyFundReconciliationNoWork(err error) bool {
	return errors.Is(err, ErrCompanyFundSyncRunAlreadyTerminal) || errors.Is(err, ErrCompanyFundSyncRunNotReady)
}

var _ CompanyFundProviderEventProcessor = (*ProviderEventWorker)(nil)
var _ CompanyFundAccountSnapshotProvider = (*AccountRegistry)(nil)
var _ CompanyFundSafeheronReconciliationRunner = (*SafeheronTransactionHistoryReconciler)(nil)
var _ CompanyFundAirwallexReconciliationRunner = (*AirwallexFinancialTransactionsReconciler)(nil)
var _ CompanyFundReconciliationRunFinalizer = (*CompanyFundReconciliationSyncRunAdapter)(nil)
