package companyfund

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCompanyFundReconciliationSyncRunAdapter_AirwallexPersistsExactCheckpointAndCompletes(t *testing.T) {
	input := testAirwallexSyncRunAdapterInput()
	generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
	if err != nil {
		t.Fatal(err)
	}
	repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusPending)
	adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)

	opened, err := adapter.OpenAirwallexFinancialTransactionsSyncRun(context.Background(), input)
	if err != nil || opened.ID != repository.claimed.ID || opened.Checkpoint.NextPageNum != 0 ||
		opened.Checkpoint.InFlightPageNum != nil || len(opened.Checkpoint.InFlightEventIDs) != 0 ||
		opened.Checkpoint.CandidatesSeen != 0 || opened.Checkpoint.EventsCreated != 0 || opened.Checkpoint.EventsExisting != 0 {
		t.Fatalf("OpenAirwallexFinancialTransactionsSyncRun() = %#v, %v", opened, err)
	}
	if len(repository.createInputs) != 1 || repository.createInputs[0].Channel != ChannelAirwallex ||
		repository.createInputs[0].SyncKind != AirwallexFinancialTransactionsSyncKind ||
		repository.createInputs[0].WindowKey != input.WindowKey {
		t.Fatalf("generic create input = %#v", repository.createInputs)
	}
	if len(repository.exactClaims) != 1 || repository.exactClaims[0].RunID != opened.ID ||
		repository.exactClaims[0].Channel != ChannelAirwallex || repository.exactClaims[0].WindowKey != input.WindowKey {
		t.Fatalf("exact claim input = %#v", repository.exactClaims)
	}

	page := 0
	first := AirwallexFinancialTransactionsSyncCheckpoint{
		InFlightPageNum:  &page,
		InFlightEventIDs: []string{"airwallex-financial-transaction:v1:abc"},
		CandidatesSeen:   1,
		EventsCreated:    1,
	}
	if err := adapter.CheckpointAirwallexFinancialTransactionsSyncRun(context.Background(), opened.ID, first); err != nil {
		t.Fatalf("CheckpointAirwallexFinancialTransactionsSyncRun() = %v", err)
	}
	if len(repository.progresses) != 1 || repository.progresses[0].CandidatesSeenDelta != 1 || repository.progresses[0].EventsCreatedDelta != 1 {
		t.Fatalf("first progress = %#v", repository.progresses)
	}

	complete := AirwallexFinancialTransactionsSyncCheckpoint{NextPageNum: 1, CandidatesSeen: 1, EventsCreated: 1, EventsExisting: 1}
	if err := adapter.CompleteAirwallexFinancialTransactionsSyncRun(context.Background(), opened.ID, complete); err != nil {
		t.Fatalf("CompleteAirwallexFinancialTransactionsSyncRun() = %v", err)
	}
	if len(repository.progresses) != 2 || repository.progresses[1].CandidatesSeenDelta != 0 || repository.progresses[1].EventsCreatedDelta != 0 {
		t.Fatalf("completion must persist a zero-delta page checkpoint: %#v", repository.progresses)
	}
	if len(repository.finalizes) != 1 || repository.finalizes[0].outcome != CompanyFundSyncRunFinalizeSucceeded || repository.finalizes[0].retryAt != nil {
		t.Fatalf("completion finalization = %#v", repository.finalizes)
	}
	if err := adapter.CheckpointAirwallexFinancialTransactionsSyncRun(context.Background(), opened.ID, complete); !errors.Is(err, ErrCompanyFundSyncRunNotOpen) {
		t.Fatalf("completed run checkpoint error = %v, want not-open", err)
	}
}

func TestCompanyFundReconciliationSyncRunAdapter_SafeheronAndRetryableStatesRemainExact(t *testing.T) {
	t.Run("Safeheron checkpoint", func(t *testing.T) {
		input := testSafeheronSyncRunAdapterInput()
		generic, err := companyFundSyncRunAdapterInputForSafeheron(input)
		if err != nil {
			t.Fatal(err)
		}
		repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusPending)
		adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)
		opened, err := adapter.OpenSafeheronTransactionHistorySyncRun(context.Background(), input)
		if err != nil || opened.ID != repository.claimed.ID {
			t.Fatalf("OpenSafeheronTransactionHistorySyncRun() = %#v, %v", opened, err)
		}
		checkpoint := SafeheronTransactionHistorySyncCheckpoint{NextCursor: "cursor-1", CandidatesSeen: 2, EventsCreated: 1, EventsExisting: 1}
		if err := adapter.CheckpointSafeheronTransactionHistorySyncRun(context.Background(), opened.ID, checkpoint); err != nil {
			t.Fatalf("CheckpointSafeheronTransactionHistorySyncRun() = %v", err)
		}
		if len(repository.exactClaims) != 1 || repository.exactClaims[0].Channel != ChannelSafeheron ||
			repository.exactClaims[0].WindowKey != input.WindowKey || len(repository.progresses) != 1 ||
			repository.progresses[0].CandidatesSeenDelta != 2 || repository.progresses[0].EventsCreatedDelta != 1 {
			t.Fatalf("Safeheron exact adapter state claims=%#v progress=%#v", repository.exactClaims, repository.progresses)
		}
	})

	t.Run("terminal window is no work", func(t *testing.T) {
		input := testAirwallexSyncRunAdapterInput()
		generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
		if err != nil {
			t.Fatal(err)
		}
		repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusSucceeded)
		adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)
		if _, err := adapter.OpenAirwallexFinancialTransactionsSyncRun(context.Background(), input); !errors.Is(err, ErrCompanyFundSyncRunAlreadyTerminal) {
			t.Fatalf("terminal OpenAirwallexFinancialTransactionsSyncRun() error = %v", err)
		}
		if len(repository.exactClaims) != 0 || len(repository.progresses) != 0 || len(repository.finalizes) != 0 {
			t.Fatalf("terminal window must not claim/progress/finalize: %#v", repository)
		}
	})

	t.Run("failed or partial window waits for database eligibility", func(t *testing.T) {
		input := testAirwallexSyncRunAdapterInput()
		generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
		if err != nil {
			t.Fatal(err)
		}
		repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusPartial)
		repository.claimed = nil // exact SQL reports no currently eligible lease.
		adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)
		if _, err := adapter.OpenAirwallexFinancialTransactionsSyncRun(context.Background(), input); !errors.Is(err, ErrCompanyFundSyncRunNotReady) {
			t.Fatalf("deferred OpenAirwallexFinancialTransactionsSyncRun() error = %v", err)
		}
		if len(repository.exactClaims) != 1 || len(repository.progresses) != 0 || len(repository.finalizes) != 0 {
			t.Fatalf("deferred run must make one exact claim attempt only: %#v", repository)
		}
	})

	t.Run("exact claim race is no work", func(t *testing.T) {
		input := testAirwallexSyncRunAdapterInput()
		generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
		if err != nil {
			t.Fatal(err)
		}
		repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusPending)
		repository.claimErr = ErrCompanyFundSyncRunClaimLost
		adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)
		if _, err := adapter.OpenAirwallexFinancialTransactionsSyncRun(context.Background(), input); !errors.Is(err, ErrCompanyFundSyncRunNotReady) {
			t.Fatalf("racing OpenAirwallexFinancialTransactionsSyncRun() error = %v", err)
		}
	})
}

func TestCompanyFundReconciliationSyncRunAdapter_FinalizesRetryableRunWithLease(t *testing.T) {
	input := testAirwallexSyncRunAdapterInput()
	generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
	if err != nil {
		t.Fatal(err)
	}
	repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusPending)
	adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)
	opened, err := adapter.OpenAirwallexFinancialTransactionsSyncRun(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	retryAt := time.Date(2026, time.July, 11, 2, 0, 0, 0, time.UTC)
	if err := adapter.FinalizePartial(context.Background(), opened.ID, retryAt, "safe partial provider page summary"); err != nil {
		t.Fatalf("FinalizePartial() = %v", err)
	}
	if len(repository.finalizes) != 1 || repository.finalizes[0].outcome != CompanyFundSyncRunFinalizePartial ||
		repository.finalizes[0].retryAt == nil || !repository.finalizes[0].retryAt.Equal(retryAt) {
		t.Fatalf("partial finalization = %#v", repository.finalizes)
	}
	if err := adapter.FinalizeRetry(context.Background(), opened.ID, retryAt, "again"); !errors.Is(err, ErrCompanyFundSyncRunNotOpen) {
		t.Fatalf("second finalization error = %v, want not-open", err)
	}
}

func TestCompanyFundReconciliationSyncRunAdapter_ReleasesClaimWhenPostClaimValidationFails(t *testing.T) {
	input := testAirwallexSyncRunAdapterInput()
	generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("immutable window mismatch", func(t *testing.T) {
		repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusPending)
		repository.claimed.WindowEnd = repository.claimed.WindowEnd.Add(time.Minute)
		adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)

		if _, err := adapter.OpenAirwallexFinancialTransactionsSyncRun(context.Background(), input); err == nil {
			t.Fatal("mismatched claimed window must fail")
		}
		assertPostClaimInitializationRetry(t, repository)
	})

	t.Run("invalid claimed checkpoint", func(t *testing.T) {
		repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusPending)
		repository.claimed.Checkpoint = json.RawMessage(`{"nextPageNum":"invalid"}`)
		adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)

		if _, err := adapter.OpenAirwallexFinancialTransactionsSyncRun(context.Background(), input); err == nil {
			t.Fatal("invalid claimed checkpoint must fail")
		}
		assertPostClaimInitializationRetry(t, repository)
		if _, err := adapter.activeRun(repository.claimed.ID); !errors.Is(err, ErrCompanyFundSyncRunNotOpen) {
			t.Fatalf("failed initialization must not register active run: %v", err)
		}
	})
}

func TestCompanyFundReconciliationSyncRunAdapter_RegisterActiveCollisionFinalizesOnlyNewClaim(t *testing.T) {
	input := testAirwallexSyncRunAdapterInput()
	generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
	if err != nil {
		t.Fatal(err)
	}
	repository := newCompanyFundSyncRunAdapterRepositoryStub(generic, CompanyFundSyncRunStatusPending)
	adapter := newCompanyFundReconciliationSyncRunAdapterForTest(t, repository)
	existing := &companyFundReconciliationActiveRun{
		exact:      CompanyFundSyncRunExactClaimInput{RunID: repository.claimed.ID},
		leaseOwner: "older-live-lease",
		checkpoint: json.RawMessage(`{}`),
	}
	repository.afterExactClaim = func() {
		adapter.activeMu.Lock()
		adapter.active[repository.claimed.ID] = existing
		adapter.activeMu.Unlock()
	}

	if _, err := adapter.OpenAirwallexFinancialTransactionsSyncRun(context.Background(), input); !errors.Is(err, ErrCompanyFundSyncRunNotReady) {
		t.Fatalf("register collision error = %v, want not-ready", err)
	}
	assertPostClaimInitializationRetry(t, repository)
	if repository.finalizes[0].owner == existing.leaseOwner {
		t.Fatalf("collision cleanup must use the new claim owner, got existing owner %q", existing.leaseOwner)
	}
	if state, err := adapter.activeRun(repository.claimed.ID); err != nil || state != existing {
		t.Fatalf("collision cleanup must not remove the real active run: state=%#v err=%v", state, err)
	}
}

func assertPostClaimInitializationRetry(t *testing.T, repository *companyFundSyncRunAdapterRepositoryStub) {
	t.Helper()
	if len(repository.claimOwners) != 1 || len(repository.finalizes) != 1 {
		t.Fatalf("post-claim cleanup calls = claims=%#v finalizes=%#v", repository.claimOwners, repository.finalizes)
	}
	finalize := repository.finalizes[0]
	if finalize.runID != repository.claimed.ID || finalize.owner != repository.claimOwners[0] ||
		finalize.outcome != CompanyFundSyncRunFinalizeRetry || finalize.retryAt == nil ||
		finalize.detail != companyFundSyncRunAdapterPostClaimFailureDetail {
		t.Fatalf("post-claim cleanup finalization = %#v", finalize)
	}
	if !finalize.retryAt.After(time.Now().UTC().Add(-time.Second)) {
		t.Fatalf("post-claim retry timestamp must be safely in the future: %s", finalize.retryAt)
	}
}

func TestCompanyFundReconciliationSyncRunAdapter_RejectsWindowKeyDetachedFromExplicitAccount(t *testing.T) {
	input := testAirwallexSyncRunAdapterInput()
	input.WindowKey = "airwallex-other-account-window"
	if _, err := companyFundSyncRunAdapterInputForAirwallex(input); err == nil {
		t.Fatal("adapter input must reject a window key detached from its explicit account/version/window tuple")
	}

	safeheronInput := testSafeheronSyncRunAdapterInput()
	safeheronInput.WindowKey = "safeheron-other-account-window"
	if _, err := companyFundSyncRunAdapterInputForSafeheron(safeheronInput); err == nil {
		t.Fatal("adapter input must reject a Safeheron window key detached from its explicit account/window tuple")
	}
}

func TestCompanyFundSyncRunAdapterInputForSafeheron_AcceptsIndependentLateStatusRunKey(t *testing.T) {
	input := testSafeheronSyncRunAdapterInput()
	input.SyncKind = SafeheronTransactionHistoryLateStatusSyncKind
	input.WindowKey = "late-status:v1:2026-07-11"

	generic, err := companyFundSyncRunAdapterInputForSafeheron(input)
	if err != nil || generic.SyncKind != SafeheronTransactionHistoryLateStatusSyncKind || generic.WindowKey != input.WindowKey {
		t.Fatalf("late-status generic sync input = %#v, %v", generic, err)
	}
}

func TestCompanyFundSyncRunAdapterInputForAirwallex_AcceptsIndependentLateStatusRunKey(t *testing.T) {
	input := testAirwallexSyncRunAdapterInput()
	input.SyncKind = AirwallexFinancialTransactionsLateStatusSyncKind
	input.WindowKey = "late-status:v1:2026-07-11:account:7"

	generic, err := companyFundSyncRunAdapterInputForAirwallex(input)
	if err != nil || generic.SyncKind != AirwallexFinancialTransactionsLateStatusSyncKind || generic.WindowKey != input.WindowKey {
		t.Fatalf("late-status generic sync input = %#v, %v", generic, err)
	}
}

func TestDBRepositoryClaimCompanyFundSyncRunExact_UsesImmutableWindowRatherThanQueueClaim(t *testing.T) {
	db, mock := newSyncRunMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := testCompanyFundSyncRunExactClaimInput()
	run := testCompanyFundSyncRunForExactClaim(input, CompanyFundSyncRunStatusPending)
	expiresAt := time.Date(2026, time.July, 11, 1, 5, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(claimCompanyFundSyncRunExactSQL)).
		WithArgs(input.RunID, input.Channel, input.SyncKind, input.WindowKey, input.WindowStart, input.WindowEnd).
		WillReturnRows(companyFundSyncRunRows(run))
	mock.ExpectQuery(regexp.QuoteMeta(updateClaimedCompanyFundSyncRunExactSQL)).
		WithArgs(input.RunID, input.Channel, input.SyncKind, input.WindowKey, input.WindowStart, input.WindowEnd, "reconciler-a", time.Minute.Microseconds()).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count", "lease_expires_at"}).AddRow(3, expiresAt))
	mock.ExpectCommit()

	claimed, err := repository.ClaimCompanyFundSyncRunExact(context.Background(), input, "reconciler-a", time.Minute)
	if err != nil || claimed == nil || claimed.ID != input.RunID || claimed.Status != CompanyFundSyncRunStatusLeased ||
		claimed.LeaseOwner != "reconciler-a" || claimed.LeaseExpiresAt == nil || !claimed.LeaseExpiresAt.Equal(expiresAt) {
		t.Fatalf("ClaimCompanyFundSyncRunExact() = %#v, %v", claimed, err)
	}
	for _, required := range []string{"id = $1", "window_key = $4", "window_start = $5", "window_end = $6", "FOR UPDATE SKIP LOCKED", "next_attempt_at <= NOW()"} {
		if !containsAll(claimCompanyFundSyncRunExactSQL+updateClaimedCompanyFundSyncRunExactSQL, required) {
			t.Fatalf("exact claim SQL missing %q", required)
		}
	}
	assertSyncRunMockExpectations(t, mock)
}

func TestDBRepositoryClaimCompanyFundSyncRunExact_ReturnsNoWorkWhenExactWindowIsIneligible(t *testing.T) {
	db, mock := newSyncRunMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := testCompanyFundSyncRunExactClaimInput()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(claimCompanyFundSyncRunExactSQL)).
		WithArgs(input.RunID, input.Channel, input.SyncKind, input.WindowKey, input.WindowStart, input.WindowEnd).
		WillReturnRows(sqlmock.NewRows(companyFundSyncRunColumns()))
	mock.ExpectRollback()
	claimed, err := repository.ClaimCompanyFundSyncRunExact(context.Background(), input, "reconciler-a", time.Minute)
	if err != nil || claimed != nil {
		t.Fatalf("ineligible exact claim = %#v, %v", claimed, err)
	}
	assertSyncRunMockExpectations(t, mock)
}

func newCompanyFundReconciliationSyncRunAdapterForTest(t *testing.T, repository *companyFundSyncRunAdapterRepositoryStub) *CompanyFundReconciliationSyncRunAdapter {
	t.Helper()
	adapter, err := NewCompanyFundReconciliationSyncRunAdapter(repository, CompanyFundReconciliationSyncRunAdapterConfig{
		LeaseOwner:    "reconciler-a",
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func testAirwallexSyncRunAdapterInput() AirwallexFinancialTransactionsSyncRunInput {
	input := AirwallexFinancialTransactionsSyncRunInput{
		Channel:              ChannelAirwallex,
		SyncKind:             AirwallexFinancialTransactionsSyncKind,
		CompanyFundAccountID: 7,
		ProviderAccountKey:   "awx-main",
		WindowStart:          time.Date(2026, time.July, 9, 16, 0, 0, 0, time.UTC),
		WindowEnd:            time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC),
		APIVersion:           airwallexTestAPIVersion,
		SchemaVersion:        "schema-v1",
		EventVersion:         "event-v1",
		PageSize:             2,
	}
	input.WindowKey = airwallexFinancialTransactionsSyncRunInput(AirwallexFinancialTransactionsReconcileInput{
		Account:            CompanyFundAccount{ID: input.CompanyFundAccountID, Channel: AccountChannelAirwallex, ProviderAccountKey: input.ProviderAccountKey, Enabled: true},
		ProviderAccountKey: input.ProviderAccountKey,
		WindowStart:        input.WindowStart,
		WindowEnd:          input.WindowEnd,
		APIVersion:         input.APIVersion,
		SchemaVersion:      input.SchemaVersion,
		EventVersion:       input.EventVersion,
	}, input.PageSize).WindowKey
	return input
}

func testSafeheronSyncRunAdapterInput() SafeheronTransactionHistorySyncRunInput {
	input := SafeheronTransactionHistorySyncRunInput{
		Channel:              ChannelSafeheron,
		SyncKind:             SafeheronTransactionHistorySyncKind,
		CompanyFundAccountID: 8,
		ProviderAccountKey:   "safeheron-main",
		WindowStart:          time.Date(2026, time.July, 9, 16, 0, 0, 0, time.UTC),
		WindowEnd:            time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC),
	}
	input.WindowKey = safeheronTransactionHistorySyncRunInput(SafeheronTransactionHistoryReconcileInput{
		Account:            CompanyFundAccount{ID: input.CompanyFundAccountID, Channel: AccountChannelSafeheron, ProviderAccountKey: input.ProviderAccountKey, Enabled: true},
		ProviderAccountKey: input.ProviderAccountKey,
		WindowStart:        input.WindowStart,
		WindowEnd:          input.WindowEnd,
	}).WindowKey
	return input
}

func testCompanyFundSyncRunExactClaimInput() CompanyFundSyncRunExactClaimInput {
	return CompanyFundSyncRunExactClaimInput{
		RunID:       88,
		Channel:     ChannelAirwallex,
		SyncKind:    AirwallexFinancialTransactionsSyncKind,
		WindowKey:   "airwallex-exact-claim-a",
		WindowStart: time.Date(2026, time.July, 9, 16, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC),
	}
}

func testCompanyFundSyncRunForExactClaim(input CompanyFundSyncRunExactClaimInput, status CompanyFundSyncRunStatus) CompanyFundSyncRun {
	return CompanyFundSyncRun{
		ID:          input.RunID,
		Channel:     input.Channel,
		SyncKind:    input.SyncKind,
		WindowKey:   input.WindowKey,
		WindowStart: input.WindowStart,
		WindowEnd:   input.WindowEnd,
		Status:      status,
		Checkpoint:  json.RawMessage(`{}`),
		CreatedAt:   input.WindowEnd,
		UpdatedAt:   input.WindowEnd,
	}
}

type companyFundSyncRunAdapterFinalizeCall struct {
	runID   int64
	owner   string
	outcome CompanyFundSyncRunFinalizeOutcome
	retryAt *time.Time
	detail  string
}

type companyFundSyncRunAdapterRepositoryStub struct {
	createInputs    []CompanyFundSyncRunInput
	exactClaims     []CompanyFundSyncRunExactClaimInput
	claimOwners     []string
	renewals        []int64
	progresses      []CompanyFundSyncRunProgressUpdate
	finalizes       []companyFundSyncRunAdapterFinalizeCall
	created         CompanyFundSyncRunCreateResult
	claimed         *CompanyFundSyncRun
	claimErr        error
	afterExactClaim func()
	durable         CompanyFundSyncRunProgress
}

func newCompanyFundSyncRunAdapterRepositoryStub(input CompanyFundSyncRunInput, status CompanyFundSyncRunStatus) *companyFundSyncRunAdapterRepositoryStub {
	run := CompanyFundSyncRun{
		ID:          441,
		Channel:     input.Channel,
		SyncKind:    input.SyncKind,
		WindowKey:   input.WindowKey,
		WindowStart: input.WindowStart,
		WindowEnd:   input.WindowEnd,
		Status:      status,
		Checkpoint:  json.RawMessage(`{}`),
		CreatedAt:   input.WindowEnd,
		UpdatedAt:   input.WindowEnd,
	}
	claimed := run
	claimed.Status = CompanyFundSyncRunStatusLeased
	claimed.LeaseOwner = "reconciler-a"
	return &companyFundSyncRunAdapterRepositoryStub{
		created: CompanyFundSyncRunCreateResult{Run: run, Inserted: status == CompanyFundSyncRunStatusPending},
		claimed: &claimed,
		durable: CompanyFundSyncRunProgress{Checkpoint: json.RawMessage(`{}`)},
	}
}

func (stub *companyFundSyncRunAdapterRepositoryStub) CreateCompanyFundSyncRun(_ context.Context, input CompanyFundSyncRunInput) (CompanyFundSyncRunCreateResult, error) {
	stub.createInputs = append(stub.createInputs, input)
	return stub.created, nil
}

func (stub *companyFundSyncRunAdapterRepositoryStub) ClaimCompanyFundSyncRunExact(_ context.Context, input CompanyFundSyncRunExactClaimInput, owner string, _ time.Duration) (*CompanyFundSyncRun, error) {
	stub.exactClaims = append(stub.exactClaims, input)
	stub.claimOwners = append(stub.claimOwners, owner)
	if stub.claimErr != nil {
		return nil, stub.claimErr
	}
	if stub.claimed == nil {
		return nil, nil
	}
	copy := *stub.claimed
	copy.Checkpoint = append(json.RawMessage(nil), stub.claimed.Checkpoint...)
	copy.LeaseOwner = owner
	if stub.afterExactClaim != nil {
		stub.afterExactClaim()
	}
	return &copy, nil
}

func (stub *companyFundSyncRunAdapterRepositoryStub) RenewCompanyFundSyncRunLease(_ context.Context, runID int64, _ string, _ time.Duration) (time.Time, error) {
	stub.renewals = append(stub.renewals, runID)
	return time.Date(2026, time.July, 11, 1, 0, 0, 0, time.UTC), nil
}

func (stub *companyFundSyncRunAdapterRepositoryStub) UpdateCompanyFundSyncRunProgress(_ context.Context, _ int64, _ string, update CompanyFundSyncRunProgressUpdate) (CompanyFundSyncRunProgress, error) {
	stub.progresses = append(stub.progresses, update)
	if update.Checkpoint != nil {
		stub.durable.Checkpoint = append(json.RawMessage(nil), update.Checkpoint...)
	}
	stub.durable.CandidatesSeen += update.CandidatesSeenDelta
	stub.durable.EventsCreated += update.EventsCreatedDelta
	return stub.durable, nil
}

func (stub *companyFundSyncRunAdapterRepositoryStub) FinalizeCompanyFundSyncRun(_ context.Context, runID int64, owner string, outcome CompanyFundSyncRunFinalizeOutcome, retryAt *time.Time, detail string) error {
	call := companyFundSyncRunAdapterFinalizeCall{runID: runID, owner: owner, outcome: outcome, detail: detail}
	if retryAt != nil {
		copy := *retryAt
		call.retryAt = &copy
	}
	stub.finalizes = append(stub.finalizes, call)
	return nil
}

func containsAll(value, expected string) bool {
	return len(expected) > 0 && regexp.MustCompile(regexp.QuoteMeta(expected)).MatchString(value)
}
