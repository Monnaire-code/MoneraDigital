package companyfund

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrCompanyFundSyncRunAlreadyTerminal means this exact window has already
	// completed or was deliberately skipped. Callers treat it as a no-work
	// outcome, not as a retryable provider failure.
	ErrCompanyFundSyncRunAlreadyTerminal = errors.New("company-fund sync run is already terminal")
	// ErrCompanyFundSyncRunNotReady means the exact window is leased elsewhere
	// or its database retry time has not arrived. It is also a no-work outcome.
	ErrCompanyFundSyncRunNotReady = errors.New("company-fund sync run is not ready")
	ErrCompanyFundSyncRunNotOpen  = errors.New("company-fund sync run is not open in this adapter")
	ErrCompanyFundSyncRunActive   = errors.New("company-fund sync run is already active in this adapter")
)

const (
	companyFundSyncRunAdapterPostClaimRetryMinimum  = time.Minute
	companyFundSyncRunAdapterPostClaimFinalizeWait  = 5 * time.Second
	companyFundSyncRunAdapterPostClaimFailureDetail = "company-fund reconciliation run initialization deferred"
)

// CompanyFundReconciliationSyncRunRepository is the exact persistence surface
// used by typed provider reconcilers. It intentionally excludes ClaimNext: a
// reconciler may only claim the run it just created/read by immutable window.
type CompanyFundReconciliationSyncRunRepository interface {
	CreateCompanyFundSyncRun(ctx context.Context, input CompanyFundSyncRunInput) (CompanyFundSyncRunCreateResult, error)
	ClaimCompanyFundSyncRunExact(ctx context.Context, input CompanyFundSyncRunExactClaimInput, owner string, leaseDuration time.Duration) (*CompanyFundSyncRun, error)
	RenewCompanyFundSyncRunLease(ctx context.Context, runID int64, owner string, leaseDuration time.Duration) (time.Time, error)
	UpdateCompanyFundSyncRunProgress(ctx context.Context, runID int64, owner string, update CompanyFundSyncRunProgressUpdate) (CompanyFundSyncRunProgress, error)
	FinalizeCompanyFundSyncRun(ctx context.Context, runID int64, owner string, outcome CompanyFundSyncRunFinalizeOutcome, retryAt *time.Time, failureDetail string) error
}

// CompanyFundReconciliationSyncRunAdapterConfig is process-scoped lease
// identity for a scheduler/worker replica. The owner must be unique across
// concurrently running replicas; PostgreSQL remains the source of truth for
// retry timing and live-lease ownership.
type CompanyFundReconciliationSyncRunAdapterConfig struct {
	LeaseOwner    string
	LeaseDuration time.Duration
}

// CompanyFundReconciliationSyncRunAdapter adapts the generic sync-run store
// to the two provider-specific reconciler contracts. It stores no provider
// credentials and never chooses an account from a webhook envelope.
type CompanyFundReconciliationSyncRunAdapter struct {
	repository    CompanyFundReconciliationSyncRunRepository
	leaseOwner    string
	leaseDuration time.Duration
	leaseSequence atomic.Uint64

	activeMu sync.Mutex
	active   map[int64]*companyFundReconciliationActiveRun
}

type companyFundReconciliationActiveRun struct {
	mu             sync.Mutex
	exact          CompanyFundSyncRunExactClaimInput
	leaseOwner     string
	checkpoint     json.RawMessage
	candidatesSeen int
	eventsCreated  int
}

func NewCompanyFundReconciliationSyncRunAdapter(
	repository CompanyFundReconciliationSyncRunRepository,
	config CompanyFundReconciliationSyncRunAdapterConfig,
) (*CompanyFundReconciliationSyncRunAdapter, error) {
	if repository == nil {
		return nil, fmt.Errorf("company-fund reconciliation sync-run repository is required")
	}
	if err := validateCompanyFundSyncLeaseOwner(config.LeaseOwner); err != nil {
		return nil, err
	}
	if _, err := companyFundSyncLeaseDurationMicroseconds(config.LeaseDuration); err != nil {
		return nil, err
	}
	return &CompanyFundReconciliationSyncRunAdapter{
		repository:    repository,
		leaseOwner:    config.LeaseOwner,
		leaseDuration: config.LeaseDuration,
		active:        make(map[int64]*companyFundReconciliationActiveRun),
	}, nil
}

// OpenSafeheronTransactionHistorySyncRun creates/reuses and then leases the
// exact Safeheron history window. A terminal or not-yet-retryable window
// returns one of the documented no-work sentinels without provider I/O.
func (a *CompanyFundReconciliationSyncRunAdapter) OpenSafeheronTransactionHistorySyncRun(
	ctx context.Context,
	input SafeheronTransactionHistorySyncRunInput,
) (SafeheronTransactionHistorySyncRun, error) {
	generic, err := companyFundSyncRunAdapterInputForSafeheron(input)
	if err != nil {
		return SafeheronTransactionHistorySyncRun{}, err
	}
	run, err := a.open(ctx, generic, validateSafeheronTransactionHistoryAdapterCheckpoint)
	if err != nil {
		return SafeheronTransactionHistorySyncRun{}, err
	}
	checkpoint, err := decodeSafeheronTransactionHistorySyncCheckpoint(run.Checkpoint)
	if err != nil {
		return SafeheronTransactionHistorySyncRun{}, err
	}
	return SafeheronTransactionHistorySyncRun{ID: run.ID, AttemptCount: run.AttemptCount, Checkpoint: checkpoint}, nil
}

func (a *CompanyFundReconciliationSyncRunAdapter) CheckpointSafeheronTransactionHistorySyncRun(
	ctx context.Context,
	runID int64,
	checkpoint SafeheronTransactionHistorySyncCheckpoint,
) error {
	raw, normalized, err := encodeSafeheronTransactionHistorySyncCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	return a.checkpoint(ctx, runID, raw, normalized.CandidatesSeen, normalized.EventsCreated)
}

func (a *CompanyFundReconciliationSyncRunAdapter) CompleteSafeheronTransactionHistorySyncRun(
	ctx context.Context,
	runID int64,
	checkpoint SafeheronTransactionHistorySyncCheckpoint,
) error {
	raw, normalized, err := encodeSafeheronTransactionHistorySyncCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	return a.complete(ctx, runID, raw, normalized.CandidatesSeen, normalized.EventsCreated)
}

// OpenAirwallexFinancialTransactionsSyncRun performs the equivalent exact
// lease for one explicitly configured Airwallex account/window/version pin.
func (a *CompanyFundReconciliationSyncRunAdapter) OpenAirwallexFinancialTransactionsSyncRun(
	ctx context.Context,
	input AirwallexFinancialTransactionsSyncRunInput,
) (AirwallexFinancialTransactionsSyncRun, error) {
	generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
	if err != nil {
		return AirwallexFinancialTransactionsSyncRun{}, err
	}
	run, err := a.open(ctx, generic, validateAirwallexFinancialTransactionsAdapterCheckpoint)
	if err != nil {
		return AirwallexFinancialTransactionsSyncRun{}, err
	}
	checkpoint, err := decodeAirwallexFinancialTransactionsSyncCheckpoint(run.Checkpoint)
	if err != nil {
		return AirwallexFinancialTransactionsSyncRun{}, err
	}
	return AirwallexFinancialTransactionsSyncRun{ID: run.ID, AttemptCount: run.AttemptCount, Checkpoint: checkpoint}, nil
}

func (a *CompanyFundReconciliationSyncRunAdapter) CheckpointAirwallexFinancialTransactionsSyncRun(
	ctx context.Context,
	runID int64,
	checkpoint AirwallexFinancialTransactionsSyncCheckpoint,
) error {
	raw, normalized, err := encodeAirwallexFinancialTransactionsSyncCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	return a.checkpoint(ctx, runID, raw, normalized.CandidatesSeen, normalized.EventsCreated)
}

func (a *CompanyFundReconciliationSyncRunAdapter) CompleteAirwallexFinancialTransactionsSyncRun(
	ctx context.Context,
	runID int64,
	checkpoint AirwallexFinancialTransactionsSyncCheckpoint,
) error {
	raw, normalized, err := encodeAirwallexFinancialTransactionsSyncCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	return a.complete(ctx, runID, raw, normalized.CandidatesSeen, normalized.EventsCreated)
}

// FinalizeRetry returns an active exact run to the durable FAILED retry queue.
// failureDetail must already be a safe summary; raw provider data never enters
// this adapter or the sync-run error column.
func (a *CompanyFundReconciliationSyncRunAdapter) FinalizeRetry(ctx context.Context, runID int64, retryAt time.Time, failureDetail string) error {
	return a.finalizeRetryable(ctx, runID, CompanyFundSyncRunFinalizeRetry, retryAt, failureDetail)
}

// FinalizePartial records a retryable partially completed run. It is for a
// caller that has intentionally completed a safe subset and wants the generic
// retry queue to resume according to database next_attempt_at semantics.
func (a *CompanyFundReconciliationSyncRunAdapter) FinalizePartial(ctx context.Context, runID int64, retryAt time.Time, failureDetail string) error {
	return a.finalizeRetryable(ctx, runID, CompanyFundSyncRunFinalizePartial, retryAt, failureDetail)
}

func (a *CompanyFundReconciliationSyncRunAdapter) open(
	ctx context.Context,
	input CompanyFundSyncRunInput,
	validateCheckpoint func(json.RawMessage, int, int) error,
) (CompanyFundSyncRun, error) {
	if a == nil || a.repository == nil {
		return CompanyFundSyncRun{}, fmt.Errorf("company-fund reconciliation sync-run adapter is not configured")
	}
	canonical, err := input.canonical()
	if err != nil {
		return CompanyFundSyncRun{}, err
	}
	created, err := a.repository.CreateCompanyFundSyncRun(ctx, canonical)
	if err != nil {
		return CompanyFundSyncRun{}, fmt.Errorf("create company-fund reconciliation sync run: %w", err)
	}
	if err := companyFundSyncRunAdapterMatchesWindow(created.Run, canonical); err != nil {
		return CompanyFundSyncRun{}, err
	}
	switch created.Run.Status {
	case CompanyFundSyncRunStatusSucceeded, CompanyFundSyncRunStatusSkipped:
		return CompanyFundSyncRun{}, fmt.Errorf("%w: %s", ErrCompanyFundSyncRunAlreadyTerminal, created.Run.Status)
	}
	if err := validateCheckpoint(created.Run.Checkpoint, created.Run.CandidatesSeen, created.Run.EventsCreated); err != nil {
		return CompanyFundSyncRun{}, fmt.Errorf("validate stored company-fund reconciliation checkpoint: %w", err)
	}
	if a.hasActive(created.Run.ID) {
		return CompanyFundSyncRun{}, ErrCompanyFundSyncRunNotReady
	}

	exact := CompanyFundSyncRunExactClaimInput{
		RunID:       created.Run.ID,
		Channel:     canonical.Channel,
		SyncKind:    canonical.SyncKind,
		WindowKey:   canonical.WindowKey,
		WindowStart: canonical.WindowStart,
		WindowEnd:   canonical.WindowEnd,
	}
	claimOwner := a.nextLeaseOwner()
	claimed, err := a.repository.ClaimCompanyFundSyncRunExact(ctx, exact, claimOwner, a.leaseDuration)
	if err != nil {
		if errors.Is(err, ErrCompanyFundSyncRunClaimLost) {
			return CompanyFundSyncRun{}, ErrCompanyFundSyncRunNotReady
		}
		return CompanyFundSyncRun{}, fmt.Errorf("claim exact company-fund reconciliation sync run: %w", err)
	}
	if claimed == nil {
		return CompanyFundSyncRun{}, ErrCompanyFundSyncRunNotReady
	}
	if err := companyFundSyncRunAdapterMatchesWindow(*claimed, canonical); err != nil {
		return CompanyFundSyncRun{}, a.releasePostClaimOpenFailure(ctx, claimed, claimOwner, err)
	}
	if claimed.Status != CompanyFundSyncRunStatusLeased || claimed.LeaseOwner != claimOwner {
		return CompanyFundSyncRun{}, a.releasePostClaimOpenFailure(ctx, claimed, claimOwner, fmt.Errorf("exact company-fund reconciliation sync run was not leased to this adapter"))
	}
	if err := validateCheckpoint(claimed.Checkpoint, claimed.CandidatesSeen, claimed.EventsCreated); err != nil {
		return CompanyFundSyncRun{}, a.releasePostClaimOpenFailure(ctx, claimed, claimOwner, fmt.Errorf("validate claimed company-fund reconciliation checkpoint: %w", err))
	}
	state := &companyFundReconciliationActiveRun{
		exact:          exact,
		leaseOwner:     claimOwner,
		checkpoint:     append(json.RawMessage(nil), claimed.Checkpoint...),
		candidatesSeen: claimed.CandidatesSeen,
		eventsCreated:  claimed.EventsCreated,
	}
	if err := a.registerActive(state); err != nil {
		released := a.releasePostClaimOpenFailure(ctx, claimed, claimOwner, err)
		if errors.Is(err, ErrCompanyFundSyncRunActive) && released == err {
			return CompanyFundSyncRun{}, ErrCompanyFundSyncRunNotReady
		}
		return CompanyFundSyncRun{}, released
	}
	return *claimed, nil
}

func (a *CompanyFundReconciliationSyncRunAdapter) checkpoint(ctx context.Context, runID int64, raw json.RawMessage, candidatesSeen, eventsCreated int) error {
	state, err := a.activeRun(runID)
	if err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return a.persistCheckpoint(ctx, state, raw, candidatesSeen, eventsCreated)
}

func (a *CompanyFundReconciliationSyncRunAdapter) complete(ctx context.Context, runID int64, raw json.RawMessage, candidatesSeen, eventsCreated int) error {
	state, err := a.activeRun(runID)
	if err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := a.persistCheckpoint(ctx, state, raw, candidatesSeen, eventsCreated); err != nil {
		return err
	}
	if _, err := a.repository.RenewCompanyFundSyncRunLease(ctx, state.exact.RunID, state.leaseOwner, a.leaseDuration); err != nil {
		a.removeActive(state)
		return fmt.Errorf("renew company-fund reconciliation lease before completion: %w", err)
	}
	if err := a.repository.FinalizeCompanyFundSyncRun(ctx, state.exact.RunID, state.leaseOwner, CompanyFundSyncRunFinalizeSucceeded, nil, ""); err != nil {
		if errors.Is(err, ErrCompanyFundSyncRunLeaseNotOwned) {
			a.removeActive(state)
		}
		return fmt.Errorf("complete company-fund reconciliation sync run: %w", err)
	}
	a.removeActive(state)
	return nil
}

func (a *CompanyFundReconciliationSyncRunAdapter) finalizeRetryable(ctx context.Context, runID int64, outcome CompanyFundSyncRunFinalizeOutcome, retryAt time.Time, failureDetail string) error {
	state, err := a.activeRun(runID)
	if err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, err := a.repository.RenewCompanyFundSyncRunLease(ctx, state.exact.RunID, state.leaseOwner, a.leaseDuration); err != nil {
		a.removeActive(state)
		return fmt.Errorf("renew company-fund reconciliation lease before retry finalization: %w", err)
	}
	if err := a.repository.FinalizeCompanyFundSyncRun(ctx, state.exact.RunID, state.leaseOwner, outcome, &retryAt, failureDetail); err != nil {
		if errors.Is(err, ErrCompanyFundSyncRunLeaseNotOwned) {
			a.removeActive(state)
		}
		return fmt.Errorf("finalize retryable company-fund reconciliation sync run: %w", err)
	}
	a.removeActive(state)
	return nil
}

func (a *CompanyFundReconciliationSyncRunAdapter) persistCheckpoint(ctx context.Context, state *companyFundReconciliationActiveRun, raw json.RawMessage, candidatesSeen, eventsCreated int) error {
	canonical, err := canonicalCompanyFundSyncCheckpoint(raw)
	if err != nil {
		return err
	}
	if candidatesSeen < state.candidatesSeen || eventsCreated < state.eventsCreated {
		return fmt.Errorf("company-fund reconciliation checkpoint counters cannot decrease")
	}
	candidatesDelta := candidatesSeen - state.candidatesSeen
	eventsCreatedDelta := eventsCreated - state.eventsCreated
	if candidatesDelta == 0 && eventsCreatedDelta == 0 && bytes.Equal(canonical, state.checkpoint) {
		return nil
	}
	if _, err := a.repository.RenewCompanyFundSyncRunLease(ctx, state.exact.RunID, state.leaseOwner, a.leaseDuration); err != nil {
		a.removeActive(state)
		return fmt.Errorf("renew company-fund reconciliation lease before checkpoint: %w", err)
	}
	progress, err := a.repository.UpdateCompanyFundSyncRunProgress(ctx, state.exact.RunID, state.leaseOwner, CompanyFundSyncRunProgressUpdate{
		Checkpoint:          canonical,
		CandidatesSeenDelta: candidatesDelta,
		EventsCreatedDelta:  eventsCreatedDelta,
	})
	if err != nil {
		if errors.Is(err, ErrCompanyFundSyncRunLeaseNotOwned) {
			a.removeActive(state)
		}
		return fmt.Errorf("checkpoint company-fund reconciliation sync run: %w", err)
	}
	if progress.CandidatesSeen != candidatesSeen || progress.EventsCreated != eventsCreated || !bytes.Equal(progress.Checkpoint, canonical) {
		return fmt.Errorf("company-fund reconciliation checkpoint persistence did not converge")
	}
	state.checkpoint = append(state.checkpoint[:0], progress.Checkpoint...)
	state.candidatesSeen = progress.CandidatesSeen
	state.eventsCreated = progress.EventsCreated
	return nil
}

func (a *CompanyFundReconciliationSyncRunAdapter) activeRun(runID int64) (*companyFundReconciliationActiveRun, error) {
	if a == nil || runID <= 0 {
		return nil, ErrCompanyFundSyncRunNotOpen
	}
	a.activeMu.Lock()
	state := a.active[runID]
	a.activeMu.Unlock()
	if state == nil {
		return nil, ErrCompanyFundSyncRunNotOpen
	}
	return state, nil
}

func (a *CompanyFundReconciliationSyncRunAdapter) registerActive(state *companyFundReconciliationActiveRun) error {
	if state == nil || state.exact.RunID <= 0 || state.leaseOwner == "" {
		return fmt.Errorf("invalid company-fund reconciliation active run")
	}
	a.activeMu.Lock()
	defer a.activeMu.Unlock()
	if _, exists := a.active[state.exact.RunID]; exists {
		return ErrCompanyFundSyncRunActive
	}
	a.active[state.exact.RunID] = state
	return nil
}

func (a *CompanyFundReconciliationSyncRunAdapter) hasActive(runID int64) bool {
	if a == nil || runID <= 0 {
		return false
	}
	a.activeMu.Lock()
	_, exists := a.active[runID]
	a.activeMu.Unlock()
	return exists
}

// nextLeaseOwner creates a per-claim owner token. The configured owner remains
// the replica identity, while the suffix prevents a recovered expired lease
// from authorizing an older in-memory active run to mutate a newer claim.
func (a *CompanyFundReconciliationSyncRunAdapter) nextLeaseOwner() string {
	sequence := a.leaseSequence.Add(1)
	suffix := fmt.Sprintf(":%d", sequence)
	base := a.leaseOwner
	if len(base)+len(suffix) > maxCompanyFundSyncLeaseOwnerBytes {
		sum := sha256.Sum256([]byte(base))
		base = fmt.Sprintf("cf-%x", sum[:16])
	}
	return base + suffix
}

// releasePostClaimOpenFailure clears a just-claimed lease with a generic safe
// retry detail. It intentionally uses a bounded independent context so caller
// cancellation cannot strand a lease until expiry. No checkpoint or provider
// payload details are written to the durable error column.
func (a *CompanyFundReconciliationSyncRunAdapter) releasePostClaimOpenFailure(
	_ context.Context,
	claimed *CompanyFundSyncRun,
	claimOwner string,
	cause error,
) error {
	if claimed == nil || claimed.ID <= 0 || claimOwner == "" {
		return cause
	}
	retryDelay := a.leaseDuration
	if retryDelay < companyFundSyncRunAdapterPostClaimRetryMinimum {
		retryDelay = companyFundSyncRunAdapterPostClaimRetryMinimum
	}
	retryAt := time.Now().UTC().Add(retryDelay)
	finalizeCtx, cancel := context.WithTimeout(context.Background(), companyFundSyncRunAdapterPostClaimFinalizeWait)
	defer cancel()
	if err := a.repository.FinalizeCompanyFundSyncRun(
		finalizeCtx,
		claimed.ID,
		claimOwner,
		CompanyFundSyncRunFinalizeRetry,
		&retryAt,
		companyFundSyncRunAdapterPostClaimFailureDetail,
	); err != nil {
		return fmt.Errorf("release claimed company-fund reconciliation sync run: %w", err)
	}
	return cause
}

func (a *CompanyFundReconciliationSyncRunAdapter) removeActive(state *companyFundReconciliationActiveRun) {
	if a == nil || state == nil {
		return
	}
	a.activeMu.Lock()
	if a.active[state.exact.RunID] == state {
		delete(a.active, state.exact.RunID)
	}
	a.activeMu.Unlock()
}

func companyFundSyncRunAdapterMatchesWindow(run CompanyFundSyncRun, input CompanyFundSyncRunInput) error {
	if run.ID <= 0 || run.Channel != input.Channel || run.SyncKind != input.SyncKind || run.WindowKey != input.WindowKey ||
		!run.WindowStart.Equal(input.WindowStart) || !run.WindowEnd.Equal(input.WindowEnd) {
		return fmt.Errorf("company-fund reconciliation sync-run immutable window does not match requested account window")
	}
	return nil
}

func validateSafeheronTransactionHistoryAdapterCheckpoint(raw json.RawMessage, candidatesSeen, eventsCreated int) error {
	checkpoint, err := decodeSafeheronTransactionHistorySyncCheckpoint(raw)
	if err != nil {
		return err
	}
	if checkpoint.CandidatesSeen != candidatesSeen || checkpoint.EventsCreated != eventsCreated {
		return fmt.Errorf("Safeheron transaction-history checkpoint counters do not match durable sync-run counters")
	}
	return nil
}

func validateAirwallexFinancialTransactionsAdapterCheckpoint(raw json.RawMessage, candidatesSeen, eventsCreated int) error {
	checkpoint, err := decodeAirwallexFinancialTransactionsSyncCheckpoint(raw)
	if err != nil {
		return err
	}
	if checkpoint.CandidatesSeen != candidatesSeen || checkpoint.EventsCreated != eventsCreated {
		return fmt.Errorf("Airwallex financial-transactions checkpoint counters do not match durable sync-run counters")
	}
	return nil
}

var _ SafeheronTransactionHistorySyncRunStore = (*CompanyFundReconciliationSyncRunAdapter)(nil)
var _ AirwallexFinancialTransactionsSyncRunStore = (*CompanyFundReconciliationSyncRunAdapter)(nil)
var _ CompanyFundReconciliationSyncRunRepository = (*DBRepository)(nil)
