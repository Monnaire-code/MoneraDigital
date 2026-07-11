package companyfund

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

func TestSafeheronTransactionHistoryReconciler_PaginatesCanonicalSnapshotsWithoutDustFilter(t *testing.T) {
	first := testSafeheronHistorySnapshot(t, "history-tx-1", "COMPLETED", "0.00000001", "0xsame")
	second := testSafeheronHistorySnapshot(t, "history-tx-2", "COMPLETED", "2", "0xsame")
	third := testSafeheronHistorySnapshot(t, "history-tx-3", "CONFIRMING", "3", "0xother")
	client := &safeheronHistoryClientStub{pages: map[string][]safeheron.TransactionSnapshot{
		"": {first, second}, "history-tx-2": {third},
	}}
	ingester := &safeheronHistoryOwnedEventIngestorStub{}
	syncRuns := &safeheronHistorySyncRunStoreStub{run: SafeheronTransactionHistorySyncRun{ID: 91}}
	reconciler := newSafeheronTransactionHistoryReconcilerForTest(t, client, ingester, syncRuns)
	input := validSafeheronTransactionHistoryReconcileInput()

	result, err := reconciler.Reconcile(context.Background(), input)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.PagesFetched != 2 || result.CandidatesSeen != 3 || result.EventsCreated != 3 || result.EventsExisting != 0 {
		t.Fatalf("Reconcile() result = %#v", result)
	}
	if len(client.requests) != 2 || client.requests[0].Cursor != "" || client.requests[1].Cursor != "history-tx-2" {
		t.Fatalf("history requests = %#v", client.requests)
	}
	if client.lookupCalls != 0 {
		t.Fatalf("history compensator must not call transaction detail lookup, got %d calls", client.lookupCalls)
	}
	for _, request := range client.requests {
		if request.AccountKey != input.ProviderAccountKey || request.Limit != 2 ||
			request.CreateTimeMin != input.WindowStart.UTC().UnixMilli() || request.CreateTimeMax != input.WindowEnd.UTC().UnixMilli() ||
			request.CoinKey != "" || request.FeeCoinKey != "" || request.TransactionStatus != "" || request.TransactionSubStatus != "" ||
			request.CompletedTimeMin != 0 || request.CompletedTimeMax != 0 {
			t.Fatalf("history request must use only account/cursor/create-window/limit: %#v", request)
		}
	}
	if len(ingester.inputs) != 3 {
		t.Fatalf("owned event inputs = %#v", ingester.inputs)
	}
	for index, owned := range ingester.inputs {
		if owned.Channel != ChannelSafeheron || owned.ProviderAccountKey != input.ProviderAccountKey ||
			owned.EventType != SafeheronTransactionHistorySnapshotEventType || owned.KeyVersion != "payload-v1" || owned.Retention != 24*time.Hour ||
			!strings.HasPrefix(owned.ProviderEventID, "safeheron-history:v1:") {
			t.Fatalf("owned history event %d = %#v", index, owned)
		}
	}
	if string(ingester.inputs[0].Body) != string(first.RawPayload) || string(ingester.inputs[2].Body) != string(third.RawPayload) {
		t.Fatalf("owned event must preserve canonical raw snapshot bytes: %#v", ingester.inputs)
	}
	if syncRuns.completed == nil || syncRuns.completed.NextCursor != "history-tx-3" || syncRuns.completed.InFlightCursor != nil || len(syncRuns.completed.InFlightEventIDs) != 0 {
		t.Fatalf("completed checkpoint = %#v", syncRuns.completed)
	}
}

func TestSafeheronTransactionHistoryReconciler_RetriesPartialCursorPageWithoutReingestingCompletedSnapshot(t *testing.T) {
	first := testSafeheronHistorySnapshot(t, "history-tx-1", "COMPLETED", "1", "0xsame")
	second := testSafeheronHistorySnapshot(t, "history-tx-2", "COMPLETED", "2", "0xsame")
	client := &safeheronHistoryClientStub{pages: map[string][]safeheron.TransactionSnapshot{"": {first, second}}}
	ingester := &safeheronHistoryOwnedEventIngestorStub{failOnCall: 2, err: errors.New("temporary owned-inbox failure")}
	syncRuns := &safeheronHistorySyncRunStoreStub{run: SafeheronTransactionHistorySyncRun{ID: 92}}
	reconciler := newSafeheronTransactionHistoryReconcilerForTest(t, client, ingester, syncRuns)

	if _, err := reconciler.Reconcile(context.Background(), validSafeheronTransactionHistoryReconcileInput()); err == nil {
		t.Fatal("first Reconcile() must return the second-item failure")
	}
	checkpoint := syncRuns.run.Checkpoint
	if checkpoint.InFlightCursor == nil || *checkpoint.InFlightCursor != "" || len(checkpoint.InFlightEventIDs) != 1 || checkpoint.CandidatesSeen != 1 || checkpoint.EventsCreated != 1 {
		t.Fatalf("partial checkpoint = %#v", checkpoint)
	}

	ingester.failOnCall = 0
	result, err := reconciler.Reconcile(context.Background(), validSafeheronTransactionHistoryReconcileInput())
	if err != nil {
		t.Fatalf("retry Reconcile() error = %v", err)
	}
	if result.PagesFetched != 2 || result.CandidatesSeen != 2 || result.EventsCreated != 2 || result.EventsExisting != 0 {
		t.Fatalf("retry result = %#v", result)
	}
	if len(ingester.inputs) != 3 || ingester.inputs[0].ProviderEventID == ingester.inputs[1].ProviderEventID ||
		ingester.inputs[1].ProviderEventID != ingester.inputs[2].ProviderEventID {
		t.Fatalf("retry ingester inputs = %#v", ingester.inputs)
	}
}

func TestSafeheronTransactionHistoryReconciler_UsesTxKeyNotTxHashAndAuditsRevisions(t *testing.T) {
	first := testSafeheronHistorySnapshot(t, "history-tx-1", "PENDING", "1", "0xsame")
	second := testSafeheronHistorySnapshot(t, "history-tx-2", "PENDING", "1", "0xsame")
	client := &safeheronHistoryClientStub{pages: map[string][]safeheron.TransactionSnapshot{"": {first, second}}}
	ingester := &safeheronHistoryOwnedEventIngestorStub{}
	syncRuns := &safeheronHistorySyncRunStoreStub{run: SafeheronTransactionHistorySyncRun{ID: 93}}
	reconciler := newSafeheronTransactionHistoryReconcilerForTest(t, client, ingester, syncRuns)

	if _, err := reconciler.Reconcile(context.Background(), validSafeheronTransactionHistoryReconcileInput()); err != nil {
		t.Fatal(err)
	}
	if len(ingester.inputs) != 2 || ingester.inputs[0].ProviderEventID == ingester.inputs[1].ProviderEventID {
		t.Fatalf("same txHash with distinct txKey must create distinct history events: %#v", ingester.inputs)
	}
	digest := payloadSHA256Hex(first.RawPayload)
	base := safeheronTransactionHistorySnapshotEventID("safe-vault-main", "history-tx-1", "PENDING", digest)
	if base != safeheronTransactionHistorySnapshotEventID("safe-vault-main", "history-tx-1", "PENDING", digest) ||
		base == safeheronTransactionHistorySnapshotEventID("safe-vault-other", "history-tx-1", "PENDING", digest) ||
		base == safeheronTransactionHistorySnapshotEventID("safe-vault-main", "history-tx-1", "COMPLETED", digest) ||
		base == safeheronTransactionHistorySnapshotEventID("safe-vault-main", "history-tx-1", "PENDING", strings.Repeat("b", 64)) {
		t.Fatalf("history event identity must include account/txKey/status/raw digest")
	}
}

func TestSafeheronTransactionHistoryReconciler_RejectsInvalidConfiguredContextBeforeHistoryHTTP(t *testing.T) {
	client := &safeheronHistoryClientStub{}
	ingester := &safeheronHistoryOwnedEventIngestorStub{}
	syncRuns := &safeheronHistorySyncRunStoreStub{run: SafeheronTransactionHistorySyncRun{ID: 94}}
	reconciler := newSafeheronTransactionHistoryReconcilerForTest(t, client, ingester, syncRuns)

	for _, testCase := range []struct {
		name   string
		mutate func(*SafeheronTransactionHistoryReconcileInput)
	}{
		{"account key mismatch", func(input *SafeheronTransactionHistoryReconcileInput) { input.ProviderAccountKey = "safe-vault-other" }},
		{"disabled account", func(input *SafeheronTransactionHistoryReconcileInput) { input.Account.Enabled = false }},
		{"empty window", func(input *SafeheronTransactionHistoryReconcileInput) { input.WindowEnd = input.WindowStart }},
		{"whitespace sync kind override", func(input *SafeheronTransactionHistoryReconcileInput) { input.SyncKind = " " }},
		{"whitespace window key override", func(input *SafeheronTransactionHistoryReconcileInput) { input.WindowKey = " " }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := validSafeheronTransactionHistoryReconcileInput()
			testCase.mutate(&input)
			if _, err := reconciler.Reconcile(context.Background(), input); err == nil {
				t.Fatal("Reconcile() error = nil, want configured-context failure")
			}
		})
	}
	if len(client.requests) != 0 || len(ingester.inputs) != 0 || len(syncRuns.opens) != 0 {
		t.Fatalf("invalid input must not reach history/event/sync boundaries: requests=%d events=%d opens=%d", len(client.requests), len(ingester.inputs), len(syncRuns.opens))
	}
}

func TestSafeheronTransactionHistoryReconciler_UsesIndependentLateStatusSyncKeyOverride(t *testing.T) {
	client := &safeheronHistoryClientStub{pages: map[string][]safeheron.TransactionSnapshot{"": nil}}
	ingester := &safeheronHistoryOwnedEventIngestorStub{}
	syncRuns := &safeheronHistorySyncRunStoreStub{run: SafeheronTransactionHistorySyncRun{ID: 106}}
	reconciler := newSafeheronTransactionHistoryReconcilerForTest(t, client, ingester, syncRuns)
	input := validSafeheronTransactionHistoryReconcileInput()
	input.SyncKind = SafeheronTransactionHistoryLateStatusSyncKind
	input.WindowKey = "late-status:v1:2026-07-11"

	if _, err := reconciler.Reconcile(context.Background(), input); err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if len(syncRuns.opens) != 1 || syncRuns.opens[0].SyncKind != SafeheronTransactionHistoryLateStatusSyncKind ||
		syncRuns.opens[0].WindowKey != input.WindowKey {
		t.Fatalf("late-status sync input = %#v", syncRuns.opens)
	}

	defaultInput := validSafeheronTransactionHistoryReconcileInput()
	defaultRun := safeheronTransactionHistorySyncRunInput(defaultInput)
	if defaultRun.SyncKind != SafeheronTransactionHistorySyncKind || defaultRun.WindowKey == input.WindowKey {
		t.Fatalf("default history sync key changed unexpectedly: %#v", defaultRun)
	}
}

func TestSafeheronTransactionHistoryReconciler_PreservesClaimedRunIdentityWhenCheckpointIsInvalid(t *testing.T) {
	client := &safeheronHistoryClientStub{}
	ingester := &safeheronHistoryOwnedEventIngestorStub{}
	syncRuns := &safeheronHistorySyncRunStoreStub{run: SafeheronTransactionHistorySyncRun{
		ID:           105,
		AttemptCount: 3,
		Checkpoint:   SafeheronTransactionHistorySyncCheckpoint{CandidatesSeen: -1},
	}}
	reconciler := newSafeheronTransactionHistoryReconcilerForTest(t, client, ingester, syncRuns)

	result, err := reconciler.Reconcile(context.Background(), validSafeheronTransactionHistoryReconcileInput())
	if err == nil {
		t.Fatal("Reconcile() error = nil, want invalid checkpoint")
	}
	if result.RunID != 105 || result.AttemptCount != 3 {
		t.Fatalf("claimed checkpoint failure result = %#v, want run identity for retry finalization", result)
	}
	if len(client.requests) != 0 || len(ingester.inputs) != 0 {
		t.Fatalf("invalid checkpoint must not reach provider I/O: requests=%#v events=%#v", client.requests, ingester.inputs)
	}
}
