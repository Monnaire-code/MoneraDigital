package companyfund

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

func newSafeheronTransactionHistoryReconcilerForTest(
	t *testing.T,
	client *safeheronHistoryClientStub,
	ingester *safeheronHistoryOwnedEventIngestorStub,
	syncRuns *safeheronHistorySyncRunStoreStub,
) *SafeheronTransactionHistoryReconciler {
	t.Helper()
	reconciler, err := NewSafeheronTransactionHistoryReconciler(client, ingester, syncRuns, SafeheronTransactionHistoryReconcilerConfig{
		PageSize: 2, MaxPages: 4, PayloadKeyVersion: "payload-v1", PayloadRetention: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewSafeheronTransactionHistoryReconciler() error = %v", err)
	}
	return reconciler
}

func validSafeheronTransactionHistoryReconcileInput() SafeheronTransactionHistoryReconcileInput {
	return SafeheronTransactionHistoryReconcileInput{
		Account: CompanyFundAccount{
			ID: 11, Channel: ChannelSafeheron, ProviderAccountKey: "safe-vault-main", Enabled: true,
		},
		ProviderAccountKey: "safe-vault-main",
		WindowStart:        time.Date(2026, time.July, 9, 16, 0, 0, 0, time.UTC),
		WindowEnd:          time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC),
	}
}

func testSafeheronHistorySnapshot(t *testing.T, txKey, status, amount, txHash string) safeheron.TransactionSnapshot {
	t.Helper()
	snapshot := safeheron.TransactionSnapshot{
		TxKey: txKey, TxHash: txHash, CoinKey: "USDT_ERC20", TxAmount: amount,
		SourceAccountKey: "safe-vault-main", SourceAddress: "0xfrom", DestinationAddress: "0xto",
		TransactionStatus: status, CreateTime: 1783612800000,
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.RawPayload = raw
	return snapshot
}

type safeheronHistoryClientStub struct {
	pages       map[string][]safeheron.TransactionSnapshot
	errs        map[string]error
	requests    []safeheron.TransactionHistoryRequest
	lookupCalls int
}

func (stub *safeheronHistoryClientStub) ListTransactions(_ context.Context, request safeheron.TransactionHistoryRequest) ([]safeheron.TransactionSnapshot, error) {
	stub.requests = append(stub.requests, request)
	if err := stub.errs[request.Cursor]; err != nil {
		return nil, err
	}
	return append([]safeheron.TransactionSnapshot(nil), stub.pages[request.Cursor]...), nil
}

func (stub *safeheronHistoryClientStub) LookupTransaction(context.Context, safeheron.TransactionLookup) (*safeheron.TransactionSnapshot, error) {
	stub.lookupCalls++
	return nil, fmt.Errorf("history reconciler must not call transaction detail lookup")
}

type safeheronHistoryOwnedEventIngestorStub struct {
	inputs     []OwnedProviderPayloadInput
	hasResult  bool
	result     ProviderEventInsertResult
	failOnCall int
	err        error
}

func (stub *safeheronHistoryOwnedEventIngestorStub) Ingest(_ context.Context, input OwnedProviderPayloadInput) (ProviderEventInsertResult, error) {
	stub.inputs = append(stub.inputs, input)
	if stub.failOnCall > 0 && len(stub.inputs) == stub.failOnCall {
		return ProviderEventInsertResult{}, stub.err
	}
	if stub.hasResult {
		return stub.result, nil
	}
	return ProviderEventInsertResult{ID: int64(len(stub.inputs)), Inserted: true}, nil
}

type safeheronHistorySyncRunStoreStub struct {
	run         SafeheronTransactionHistorySyncRun
	opens       []SafeheronTransactionHistorySyncRunInput
	checkpoints []SafeheronTransactionHistorySyncCheckpoint
	completed   *SafeheronTransactionHistorySyncCheckpoint
}

func (stub *safeheronHistorySyncRunStoreStub) OpenSafeheronTransactionHistorySyncRun(_ context.Context, input SafeheronTransactionHistorySyncRunInput) (SafeheronTransactionHistorySyncRun, error) {
	stub.opens = append(stub.opens, input)
	return stub.run, nil
}

func (stub *safeheronHistorySyncRunStoreStub) CheckpointSafeheronTransactionHistorySyncRun(_ context.Context, runID int64, checkpoint SafeheronTransactionHistorySyncCheckpoint) error {
	if runID != stub.run.ID {
		return fmt.Errorf("unexpected run ID %d", runID)
	}
	stub.run.Checkpoint = checkpoint
	stub.checkpoints = append(stub.checkpoints, checkpoint)
	return nil
}

func (stub *safeheronHistorySyncRunStoreStub) CompleteSafeheronTransactionHistorySyncRun(_ context.Context, runID int64, checkpoint SafeheronTransactionHistorySyncCheckpoint) error {
	if runID != stub.run.ID {
		return fmt.Errorf("unexpected run ID %d", runID)
	}
	stub.run.Checkpoint = checkpoint
	copy := checkpoint
	stub.completed = &copy
	return nil
}
