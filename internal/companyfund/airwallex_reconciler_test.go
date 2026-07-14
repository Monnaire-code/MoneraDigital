package companyfund

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAirwallexFinancialTransactionsReconciler_PaginatesAndPersistsOneEncryptedEventPerExactItem(t *testing.T) {
	client := &airwallexFinancialTransactionsClientStub{
		apiVersion:   airwallexTestAPIVersion,
		loginAsScope: "awx-main",
		pages: map[int]AirwallexFinancialTransactionsPage{
			0: {HasMore: true, Items: []AirwallexFinancialTransaction{
				testAirwallexFinancialTransaction("ft_1", "deposit_1", `{"id":"ft_1","amount":1}`),
				testAirwallexFinancialTransaction("ft_2", "deposit_2", `{"id":"ft_2","amount":2}`),
			}},
			1: {HasMore: false, Items: []AirwallexFinancialTransaction{
				testAirwallexFinancialTransaction("ft_3", "transfer_3", `{"id":"ft_3","amount":3}`),
			}},
		},
	}
	ingester := &airwallexOwnedEventIngestorStub{}
	syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{ID: 91}}
	reconciler := newAirwallexFinancialTransactionsReconcilerForTest(t, client, ingester, syncRuns)
	input := validAirwallexFinancialTransactionsReconcileInput()

	result, err := reconciler.Reconcile(context.Background(), input)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.PagesFetched != 2 || result.CandidatesSeen != 3 || result.EventsCreated != 3 || result.EventsExisting != 0 {
		t.Fatalf("Reconcile() result = %#v", result)
	}
	if len(client.requests) != 2 || client.requests[0].PageNum != 0 || client.requests[1].PageNum != 1 {
		t.Fatalf("list requests = %#v, want zero-based complete pagination", client.requests)
	}
	if len(syncRuns.opens) != 1 || syncRuns.opens[0].CompanyFundAccountID != input.Account.ID ||
		syncRuns.opens[0].ProviderAccountKey != input.ProviderAccountKey || syncRuns.opens[0].APIVersion != input.APIVersion ||
		syncRuns.opens[0].SchemaVersion != input.SchemaVersion || syncRuns.opens[0].EventVersion != input.EventVersion ||
		syncRuns.opens[0].PageSize != 2 || syncRuns.opens[0].WindowKey == "" {
		t.Fatalf("sync run input = %#v, want explicit configured account and immutable pins", syncRuns.opens)
	}
	for _, request := range client.requests {
		if !request.FromCreatedAt.Equal(input.WindowStart.UTC()) || !request.ToCreatedAt.Equal(input.WindowEnd.UTC()) || request.PageSize != 2 {
			t.Fatalf("reconciliation request = %#v", request)
		}
	}
	if len(ingester.inputs) != 3 {
		t.Fatalf("owned event inputs = %#v, want one per financial transaction item", ingester.inputs)
	}
	seenIDs := make(map[string]struct{}, len(ingester.inputs))
	for index, event := range ingester.inputs {
		if event.Channel != ChannelAirwallex || event.ProviderAccountKey != input.ProviderAccountKey || event.ProviderOrgKey != "" ||
			event.EventType != AirwallexFinancialTransactionSnapshotEventType || event.ProviderEventVersion != input.APIVersion ||
			event.KeyVersion != "payload-v1" || event.Retention != 24*time.Hour {
			t.Fatalf("owned event %d = %#v", index, event)
		}
		if !strings.HasPrefix(event.ProviderEventID, "airwallex-financial-transaction:v1:") {
			t.Fatalf("event ID %q must be deterministic and versioned", event.ProviderEventID)
		}
		if _, exists := seenIDs[event.ProviderEventID]; exists {
			t.Fatalf("duplicate deterministic event ID %q", event.ProviderEventID)
		}
		seenIDs[event.ProviderEventID] = struct{}{}
	}
	if len(syncRuns.checkpoints) == 0 || syncRuns.completed == nil || syncRuns.completed.NextPageNum != 2 || syncRuns.completed.InFlightPageNum != nil {
		t.Fatalf("sync run checkpoints = %#v completed=%#v", syncRuns.checkpoints, syncRuns.completed)
	}
}

func TestAirwallexFinancialTransactionsReconciler_RetriesPartialPageWithoutReingestingCompletedRawItem(t *testing.T) {
	client := &airwallexFinancialTransactionsClientStub{
		apiVersion:   airwallexTestAPIVersion,
		loginAsScope: "awx-main",
		pages: map[int]AirwallexFinancialTransactionsPage{
			0: {Items: []AirwallexFinancialTransaction{
				testAirwallexFinancialTransaction("ft_1", "deposit_1", `{"id":"ft_1","amount":1}`),
				testAirwallexFinancialTransaction("ft_2", "deposit_2", `{"id":"ft_2","amount":2}`),
			}},
		},
	}
	ingester := &airwallexOwnedEventIngestorStub{failOnCall: 2, err: errors.New("temporary encrypted inbox failure")}
	syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{ID: 92}}
	reconciler := newAirwallexFinancialTransactionsReconcilerForTest(t, client, ingester, syncRuns)
	input := validAirwallexFinancialTransactionsReconcileInput()

	if _, err := reconciler.Reconcile(context.Background(), input); err == nil {
		t.Fatal("first Reconcile() must surface the temporary second-item failure")
	}
	if syncRuns.run.Checkpoint.InFlightPageNum == nil || *syncRuns.run.Checkpoint.InFlightPageNum != 0 || len(syncRuns.run.Checkpoint.InFlightEventIDs) != 1 || syncRuns.run.Checkpoint.CandidatesSeen != 1 || syncRuns.run.Checkpoint.EventsCreated != 1 {
		t.Fatalf("partial checkpoint = %#v", syncRuns.run.Checkpoint)
	}

	ingester.failOnCall = 0
	result, err := reconciler.Reconcile(context.Background(), input)
	if err != nil {
		t.Fatalf("retry Reconcile() error = %v", err)
	}
	if result.CandidatesSeen != 2 || result.EventsCreated != 2 || result.EventsExisting != 0 || result.PagesFetched != 1 {
		t.Fatalf("retry result = %#v", result)
	}
	if len(ingester.inputs) != 3 {
		t.Fatalf("ingester calls = %d, want first item once, failed second item twice", len(ingester.inputs))
	}
	if ingester.inputs[0].ProviderEventID == ingester.inputs[1].ProviderEventID {
		t.Fatal("first and second financial transaction items must not share an event identity")
	}
	if ingester.inputs[1].ProviderEventID != ingester.inputs[2].ProviderEventID {
		t.Fatal("retry must use the exact same deterministic event identity for the failed raw item")
	}
}

func TestAirwallexFinancialTransactionsReconciler_DeduplicatesExactPageItemsAndKeepsSourceIDOutOfIdentity(t *testing.T) {
	first := testAirwallexFinancialTransaction("ft_1", "deposit_1", `{"id":"ft_1","amount":1}`)
	duplicate := first
	client := &airwallexFinancialTransactionsClientStub{
		apiVersion:   airwallexTestAPIVersion,
		loginAsScope: "awx-main",
		pages:        map[int]AirwallexFinancialTransactionsPage{0: {Items: []AirwallexFinancialTransaction{first, duplicate}}},
	}
	ingester := &airwallexOwnedEventIngestorStub{hasResult: true, result: ProviderEventInsertResult{Inserted: false}}
	syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{ID: 93}}
	reconciler := newAirwallexFinancialTransactionsReconcilerForTest(t, client, ingester, syncRuns)

	result, err := reconciler.Reconcile(context.Background(), validAirwallexFinancialTransactionsReconcileInput())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(ingester.inputs) != 1 || result.CandidatesSeen != 1 || result.EventsCreated != 0 || result.EventsExisting != 1 {
		t.Fatalf("exact duplicate result = %#v inputs=%#v", result, ingester.inputs)
	}

	digest := payloadSHA256Hex(first.Raw)
	baseID := airwallexFinancialTransactionSnapshotEventID("awx-main", "ft_1", airwallexTestAPIVersion, "schema-v1", "event-v1", digest)
	if baseID != ingester.inputs[0].ProviderEventID {
		t.Fatalf("event ID = %q, want %q", ingester.inputs[0].ProviderEventID, baseID)
	}
	if baseID != airwallexFinancialTransactionSnapshotEventID("awx-main", "ft_1", airwallexTestAPIVersion, "schema-v1", "event-v1", digest) {
		t.Fatal("same account/financial-transaction/version/raw tuple must remain stable")
	}
	if baseID == airwallexFinancialTransactionSnapshotEventID("awx-other", "ft_1", airwallexTestAPIVersion, "schema-v1", "event-v1", digest) ||
		baseID == airwallexFinancialTransactionSnapshotEventID("awx-main", "ft_1", "2026-01-01", "schema-v1", "event-v1", digest) ||
		baseID == airwallexFinancialTransactionSnapshotEventID("awx-main", "ft_1", airwallexTestAPIVersion, "schema-v2", "event-v1", digest) ||
		baseID == airwallexFinancialTransactionSnapshotEventID("awx-main", "ft_1", airwallexTestAPIVersion, "schema-v1", "event-v2", digest) ||
		baseID == airwallexFinancialTransactionSnapshotEventID("awx-main", "ft_1", airwallexTestAPIVersion, "schema-v1", "event-v1", strings.Repeat("b", 64)) {
		t.Fatal("event identity must include account, all pins, and raw digest")
	}
}

func TestAirwallexFinancialTransactionsReconciler_RejectsMismatchedAccountAndPinnedVersionsBeforeHTTP(t *testing.T) {
	client := &airwallexFinancialTransactionsClientStub{apiVersion: airwallexTestAPIVersion, loginAsScope: "awx-main"}
	ingester := &airwallexOwnedEventIngestorStub{}
	syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{ID: 94}}
	reconciler := newAirwallexFinancialTransactionsReconcilerForTest(t, client, ingester, syncRuns)

	for _, testCase := range []struct {
		name   string
		mutate func(*AirwallexFinancialTransactionsReconcileInput)
	}{
		{"account key mismatch", func(input *AirwallexFinancialTransactionsReconcileInput) { input.ProviderAccountKey = "awx-other" }},
		{"disabled account", func(input *AirwallexFinancialTransactionsReconcileInput) { input.Account.Enabled = false }},
		{"schema mismatch", func(input *AirwallexFinancialTransactionsReconcileInput) { input.SchemaVersion = "schema-v2" }},
		{"event mismatch", func(input *AirwallexFinancialTransactionsReconcileInput) { input.EventVersion = "event-v2" }},
		{"API mismatch", func(input *AirwallexFinancialTransactionsReconcileInput) { input.APIVersion = "2026-01-01" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := validAirwallexFinancialTransactionsReconcileInput()
			testCase.mutate(&input)
			if _, err := reconciler.Reconcile(context.Background(), input); err == nil {
				t.Fatal("Reconcile() error = nil, want account/version rejection")
			}
		})
	}
	if len(client.requests) != 0 || len(ingester.inputs) != 0 || len(syncRuns.opens) != 0 {
		t.Fatalf("invalid input must not reach HTTP/event/sync boundaries: requests=%d events=%d opens=%d", len(client.requests), len(ingester.inputs), len(syncRuns.opens))
	}
}

func TestAirwallexFinancialTransactionsReconciler_RequiresExactSingleClientLoginScope(t *testing.T) {
	for _, loginAsScope := range []string{"", " awx-main"} {
		t.Run("invalid configured scope", func(t *testing.T) {
			client := &airwallexFinancialTransactionsClientStub{apiVersion: airwallexTestAPIVersion, loginAsScope: loginAsScope}
			_, err := NewAirwallexFinancialTransactionsReconciler(
				client,
				&airwallexOwnedEventIngestorStub{},
				&airwallexFinancialTransactionsSyncRunStoreStub{},
				AirwallexFinancialTransactionsReconcilerConfig{
					APIVersion: airwallexTestAPIVersion, SchemaVersion: "schema-v1", EventVersion: "event-v1",
					PageSize: 2, MaxPages: 1, PayloadKeyVersion: "payload-v1", PayloadRetention: time.Hour,
				},
			)
			if err == nil {
				t.Fatal("missing or whitespace-padded x-login-as scope must prevent reconciler construction")
			}
		})
	}

	client := &airwallexFinancialTransactionsClientStub{apiVersion: airwallexTestAPIVersion, loginAsScope: "awx-other"}
	ingester := &airwallexOwnedEventIngestorStub{}
	syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{ID: 99}}
	reconciler := newAirwallexFinancialTransactionsReconcilerForTest(t, client, ingester, syncRuns)
	if _, err := reconciler.Reconcile(context.Background(), validAirwallexFinancialTransactionsReconcileInput()); err == nil {
		t.Fatal("a client scoped to another Airwallex account must not reach HTTP or sync-run creation")
	}
	if len(client.requests) != 0 || len(syncRuns.opens) != 0 || len(ingester.inputs) != 0 {
		t.Fatalf("scope mismatch crossed a provider boundary: requests=%#v runs=%#v events=%#v", client.requests, syncRuns.opens, ingester.inputs)
	}
}

func TestAirwallexFinancialTransactionsReconciler_UsesIndependentLateStatusSyncKeyOverride(t *testing.T) {
	client := &airwallexFinancialTransactionsClientStub{
		apiVersion:   airwallexTestAPIVersion,
		loginAsScope: "awx-main",
		pages:        map[int]AirwallexFinancialTransactionsPage{0: {}},
	}
	ingester := &airwallexOwnedEventIngestorStub{}
	syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{ID: 98}}
	reconciler := newAirwallexFinancialTransactionsReconcilerForTest(t, client, ingester, syncRuns)
	input := validAirwallexFinancialTransactionsReconcileInput()
	input.SyncKind = AirwallexFinancialTransactionsLateStatusSyncKind
	input.WindowKey = "late-status:v1:2026-07-11:account:7"

	if _, err := reconciler.Reconcile(context.Background(), input); err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if len(syncRuns.opens) != 1 || syncRuns.opens[0].SyncKind != AirwallexFinancialTransactionsLateStatusSyncKind ||
		syncRuns.opens[0].WindowKey != input.WindowKey {
		t.Fatalf("late-status sync input = %#v", syncRuns.opens)
	}
	defaultRun := airwallexFinancialTransactionsSyncRunInput(validAirwallexFinancialTransactionsReconcileInput(), 2)
	if defaultRun.SyncKind != AirwallexFinancialTransactionsSyncKind || defaultRun.WindowKey == input.WindowKey {
		t.Fatalf("default financial-transactions sync key changed unexpectedly: %#v", defaultRun)
	}
}

func TestAirwallexFinancialTransactionsReconciler_StopsAtConfiguredPageLimit(t *testing.T) {
	client := &airwallexFinancialTransactionsClientStub{
		apiVersion:   airwallexTestAPIVersion,
		loginAsScope: "awx-main",
		pages:        map[int]AirwallexFinancialTransactionsPage{0: {HasMore: true}},
	}
	ingester := &airwallexOwnedEventIngestorStub{}
	syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{ID: 95}}
	reconciler, err := NewAirwallexFinancialTransactionsReconciler(client, ingester, syncRuns, AirwallexFinancialTransactionsReconcilerConfig{
		APIVersion:        airwallexTestAPIVersion,
		SchemaVersion:     "schema-v1",
		EventVersion:      "event-v1",
		PageSize:          2,
		MaxPages:          1,
		PayloadKeyVersion: "payload-v1",
		PayloadRetention:  24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), validAirwallexFinancialTransactionsReconcileInput()); err == nil {
		t.Fatal("Reconcile() must reject endless has_more pagination at the configured limit")
	}
	if len(client.requests) != 1 || syncRuns.completed != nil {
		t.Fatalf("page-limit result requests=%#v complete=%#v", client.requests, syncRuns.completed)
	}
}

func TestAirwallexFinancialTransactionsReconciler_RejectsOversizedProviderPageAndCorruptCheckpointBeforeIngestion(t *testing.T) {
	t.Run("page exceeds requested size", func(t *testing.T) {
		client := &airwallexFinancialTransactionsClientStub{
			apiVersion:   airwallexTestAPIVersion,
			loginAsScope: "awx-main",
			pages: map[int]AirwallexFinancialTransactionsPage{0: {Items: []AirwallexFinancialTransaction{
				testAirwallexFinancialTransaction("ft_1", "deposit_1", `{"id":"ft_1"}`),
				testAirwallexFinancialTransaction("ft_2", "deposit_2", `{"id":"ft_2"}`),
				testAirwallexFinancialTransaction("ft_3", "deposit_3", `{"id":"ft_3"}`),
			}}},
		}
		ingester := &airwallexOwnedEventIngestorStub{}
		syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{ID: 96}}
		reconciler := newAirwallexFinancialTransactionsReconcilerForTest(t, client, ingester, syncRuns)

		if _, err := reconciler.Reconcile(context.Background(), validAirwallexFinancialTransactionsReconcileInput()); err == nil {
			t.Fatal("Reconcile() must reject a page larger than its requested size")
		}
		if len(ingester.inputs) != 0 || len(syncRuns.checkpoints) != 0 {
			t.Fatalf("oversized page must not reach event/checkpoint mutation: events=%#v checkpoints=%#v", ingester.inputs, syncRuns.checkpoints)
		}
	})

	t.Run("in-flight IDs without page", func(t *testing.T) {
		client := &airwallexFinancialTransactionsClientStub{apiVersion: airwallexTestAPIVersion, loginAsScope: "awx-main"}
		ingester := &airwallexOwnedEventIngestorStub{}
		syncRuns := &airwallexFinancialTransactionsSyncRunStoreStub{run: AirwallexFinancialTransactionsSyncRun{
			ID: 97,
			Checkpoint: AirwallexFinancialTransactionsSyncCheckpoint{
				InFlightEventIDs: []string{"airwallex-financial-transaction:v1:bad"},
			},
		}}
		reconciler := newAirwallexFinancialTransactionsReconcilerForTest(t, client, ingester, syncRuns)

		if _, err := reconciler.Reconcile(context.Background(), validAirwallexFinancialTransactionsReconcileInput()); err == nil {
			t.Fatal("Reconcile() must reject a corrupt in-flight checkpoint")
		}
		if len(client.requests) != 0 || len(ingester.inputs) != 0 {
			t.Fatalf("corrupt checkpoint must not reach HTTP or inbox mutation: requests=%#v events=%#v", client.requests, ingester.inputs)
		}
	})
}

func newAirwallexFinancialTransactionsReconcilerForTest(
	t *testing.T,
	client *airwallexFinancialTransactionsClientStub,
	ingester *airwallexOwnedEventIngestorStub,
	syncRuns *airwallexFinancialTransactionsSyncRunStoreStub,
) *AirwallexFinancialTransactionsReconciler {
	t.Helper()
	if client.loginAsScope == "" {
		client.loginAsScope = "awx-main"
	}
	reconciler, err := NewAirwallexFinancialTransactionsReconciler(client, ingester, syncRuns, AirwallexFinancialTransactionsReconcilerConfig{
		APIVersion:        airwallexTestAPIVersion,
		SchemaVersion:     "schema-v1",
		EventVersion:      "event-v1",
		PageSize:          2,
		MaxPages:          4,
		PayloadKeyVersion: "payload-v1",
		PayloadRetention:  24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewAirwallexFinancialTransactionsReconciler() error = %v", err)
	}
	return reconciler
}

func validAirwallexFinancialTransactionsReconcileInput() AirwallexFinancialTransactionsReconcileInput {
	return AirwallexFinancialTransactionsReconcileInput{
		Account: CompanyFundAccount{
			ID:                 7,
			Channel:            ChannelAirwallex,
			ProviderAccountKey: "awx-main",
			Enabled:            true,
		},
		ProviderAccountKey: "awx-main",
		WindowStart:        time.Date(2026, time.July, 9, 16, 0, 0, 0, time.UTC),
		WindowEnd:          time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC),
		APIVersion:         airwallexTestAPIVersion,
		SchemaVersion:      "schema-v1",
		EventVersion:       "event-v1",
	}
}

func testAirwallexFinancialTransaction(providerID, sourceID, raw string) AirwallexFinancialTransaction {
	return AirwallexFinancialTransaction{
		ProviderID: providerID,
		SourceID:   sourceID,
		Raw:        []byte(raw),
	}
}

type airwallexFinancialTransactionsClientStub struct {
	apiVersion   string
	loginAsScope string
	pages        map[int]AirwallexFinancialTransactionsPage
	errs         map[int]error
	requests     []AirwallexFinancialTransactionsRequest
}

func (stub *airwallexFinancialTransactionsClientStub) PinnedAPIVersion() string {
	return stub.apiVersion
}

func (stub *airwallexFinancialTransactionsClientStub) PinnedLoginAsScope() string {
	return stub.loginAsScope
}

func (stub *airwallexFinancialTransactionsClientStub) ListFinancialTransactions(_ context.Context, request AirwallexFinancialTransactionsRequest) (AirwallexFinancialTransactionsPage, error) {
	stub.requests = append(stub.requests, request)
	if err := stub.errs[request.PageNum]; err != nil {
		return AirwallexFinancialTransactionsPage{}, err
	}
	return stub.pages[request.PageNum], nil
}

type airwallexOwnedEventIngestorStub struct {
	inputs     []OwnedProviderPayloadInput
	hasResult  bool
	result     ProviderEventInsertResult
	failOnCall int
	err        error
}

func (stub *airwallexOwnedEventIngestorStub) Ingest(_ context.Context, input OwnedProviderPayloadInput) (ProviderEventInsertResult, error) {
	stub.inputs = append(stub.inputs, input)
	if stub.failOnCall > 0 && len(stub.inputs) == stub.failOnCall {
		return ProviderEventInsertResult{}, stub.err
	}
	if stub.hasResult {
		return stub.result, nil
	}
	return ProviderEventInsertResult{ID: int64(len(stub.inputs)), Inserted: true}, nil
}

type airwallexFinancialTransactionsSyncRunStoreStub struct {
	run         AirwallexFinancialTransactionsSyncRun
	opens       []AirwallexFinancialTransactionsSyncRunInput
	checkpoints []AirwallexFinancialTransactionsSyncCheckpoint
	completed   *AirwallexFinancialTransactionsSyncCheckpoint
}

func (stub *airwallexFinancialTransactionsSyncRunStoreStub) OpenAirwallexFinancialTransactionsSyncRun(_ context.Context, input AirwallexFinancialTransactionsSyncRunInput) (AirwallexFinancialTransactionsSyncRun, error) {
	stub.opens = append(stub.opens, input)
	return stub.run, nil
}

func (stub *airwallexFinancialTransactionsSyncRunStoreStub) CheckpointAirwallexFinancialTransactionsSyncRun(_ context.Context, runID int64, checkpoint AirwallexFinancialTransactionsSyncCheckpoint) error {
	if runID != stub.run.ID {
		return fmt.Errorf("unexpected run ID %d", runID)
	}
	stub.run.Checkpoint = checkpoint
	stub.checkpoints = append(stub.checkpoints, checkpoint)
	return nil
}

func (stub *airwallexFinancialTransactionsSyncRunStoreStub) CompleteAirwallexFinancialTransactionsSyncRun(_ context.Context, runID int64, checkpoint AirwallexFinancialTransactionsSyncCheckpoint) error {
	if runID != stub.run.ID {
		return fmt.Errorf("unexpected run ID %d", runID)
	}
	stub.run.Checkpoint = checkpoint
	copy := checkpoint
	stub.completed = &copy
	return nil
}
