package companyfund

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"monera-digital/internal/safeheron"
)

// Reconcile pages one configured Safeheron account through the official
// history API. It stores only encrypted canonical snapshots; no detail lookup,
// webhook replay, deposit lifecycle access, or ledger upsert occurs here.
func (r *SafeheronTransactionHistoryReconciler) Reconcile(
	ctx context.Context,
	input SafeheronTransactionHistoryReconcileInput,
) (SafeheronTransactionHistoryReconcileResult, error) {
	if r == nil || r.client == nil || r.ingester == nil || r.syncRuns == nil {
		return SafeheronTransactionHistoryReconcileResult{}, fmt.Errorf("Safeheron transaction history reconciler is not configured")
	}
	normalizedInput, err := r.validateInput(input)
	if err != nil {
		return SafeheronTransactionHistoryReconcileResult{}, err
	}

	run, err := r.syncRuns.OpenSafeheronTransactionHistorySyncRun(ctx, safeheronTransactionHistorySyncRunInput(normalizedInput))
	if err != nil {
		return SafeheronTransactionHistoryReconcileResult{}, fmt.Errorf("open Safeheron transaction history sync run: %w", err)
	}
	result := safeheronTransactionHistoryResult(run, SafeheronTransactionHistorySyncCheckpoint{})
	if run.ID <= 0 {
		return result, fmt.Errorf("opened Safeheron transaction history sync run has an invalid ID")
	}
	checkpoint, cursor, err := validateSafeheronTransactionHistoryCheckpoint(run.Checkpoint)
	if err != nil {
		// The run has already been leased. Preserve its identity so the runtime
		// can return that exact lease to the durable retry queue.
		return result, err
	}
	result = safeheronTransactionHistoryResult(run, checkpoint)

	for pagesFetched := 0; pagesFetched < r.config.MaxPages; pagesFetched++ {
		snapshots, err := r.client.ListTransactions(ctx, safeheron.TransactionHistoryRequest{
			Limit:         r.config.PageSize,
			Cursor:        cursor,
			AccountKey:    normalizedInput.ProviderAccountKey,
			CreateTimeMin: normalizedInput.WindowStart.UnixMilli(),
			CreateTimeMax: normalizedInput.WindowEnd.UnixMilli(),
		})
		if err != nil {
			return result, fmt.Errorf("list Safeheron transaction history cursor %q: %w", cursor, err)
		}
		result.PagesFetched = pagesFetched + 1

		processed := make(map[string]struct{}, len(checkpoint.InFlightEventIDs))
		for _, eventID := range checkpoint.InFlightEventIDs {
			processed[eventID] = struct{}{}
		}
		for _, snapshot := range snapshots {
			ownedEvent, err := r.ownedEventInput(normalizedInput, snapshot)
			if err != nil {
				return result, err
			}
			if _, alreadyProcessed := processed[ownedEvent.ProviderEventID]; alreadyProcessed {
				continue
			}
			inserted, err := r.ingester.Ingest(ctx, ownedEvent)
			if err != nil {
				return result, fmt.Errorf("ingest Safeheron transaction history snapshot: %w", err)
			}
			processed[ownedEvent.ProviderEventID] = struct{}{}
			checkpoint.InFlightCursor = safeheronHistoryStringPointer(cursor)
			checkpoint.InFlightEventIDs = append(checkpoint.InFlightEventIDs, ownedEvent.ProviderEventID)
			checkpoint.CandidatesSeen++
			if inserted.Inserted {
				checkpoint.EventsCreated++
			} else {
				checkpoint.EventsExisting++
			}
			if err := r.syncRuns.CheckpointSafeheronTransactionHistorySyncRun(ctx, run.ID, cloneSafeheronTransactionHistoryCheckpoint(checkpoint)); err != nil {
				return result, fmt.Errorf("checkpoint Safeheron transaction history item: %w", err)
			}
			result = safeheronTransactionHistoryResult(run, checkpoint)
			result.PagesFetched = pagesFetched + 1
		}

		if len(snapshots) < int(r.config.PageSize) {
			if len(snapshots) > 0 {
				lastCursor, err := safeheronHistoryCursorForSnapshot(snapshots[len(snapshots)-1])
				if err != nil {
					return result, err
				}
				checkpoint.NextCursor = lastCursor
			}
			checkpoint.InFlightCursor = nil
			checkpoint.InFlightEventIDs = nil
			result = safeheronTransactionHistoryResult(run, checkpoint)
			result.PagesFetched = pagesFetched + 1
			if err := r.syncRuns.CompleteSafeheronTransactionHistorySyncRun(ctx, run.ID, cloneSafeheronTransactionHistoryCheckpoint(checkpoint)); err != nil {
				return result, fmt.Errorf("complete Safeheron transaction history sync run: %w", err)
			}
			return result, nil
		}

		nextCursor, err := safeheronHistoryCursorForSnapshot(snapshots[len(snapshots)-1])
		if err != nil {
			return result, err
		}
		if nextCursor == cursor {
			return result, fmt.Errorf("Safeheron transaction history cursor did not advance")
		}
		checkpoint.NextCursor = nextCursor
		checkpoint.InFlightCursor = nil
		checkpoint.InFlightEventIDs = nil
		if err := r.syncRuns.CheckpointSafeheronTransactionHistorySyncRun(ctx, run.ID, cloneSafeheronTransactionHistoryCheckpoint(checkpoint)); err != nil {
			return result, fmt.Errorf("checkpoint Safeheron transaction history page: %w", err)
		}
		result = safeheronTransactionHistoryResult(run, checkpoint)
		result.PagesFetched = pagesFetched + 1
		cursor = nextCursor
	}
	return result, fmt.Errorf("Safeheron transaction history pagination reached configured safety limit")
}

func (r *SafeheronTransactionHistoryReconciler) validateInput(input SafeheronTransactionHistoryReconcileInput) (SafeheronTransactionHistoryReconcileInput, error) {
	if input.Account.ID <= 0 || input.Account.Channel != AccountChannelSafeheron || !input.Account.Enabled {
		return SafeheronTransactionHistoryReconcileInput{}, fmt.Errorf("Safeheron history reconciliation requires an enabled configured company account")
	}
	configuredKey, err := normalizeSafeheronHistoryRequired("configured Safeheron provider account key", input.Account.ProviderAccountKey, maxProviderFactAccountKeyBytes)
	if err != nil {
		return SafeheronTransactionHistoryReconcileInput{}, err
	}
	providedKey, err := normalizeSafeheronHistoryRequired("Safeheron provider account key", input.ProviderAccountKey, maxProviderFactAccountKeyBytes)
	if err != nil || configuredKey != providedKey {
		return SafeheronTransactionHistoryReconcileInput{}, fmt.Errorf("Safeheron history reconciliation provider account key does not match configured company account")
	}
	if input.WindowStart.IsZero() || input.WindowEnd.IsZero() || !input.WindowStart.Before(input.WindowEnd) {
		return SafeheronTransactionHistoryReconcileInput{}, fmt.Errorf("Safeheron history reconciliation requires a non-empty UTC window")
	}
	input.ProviderAccountKey = providedKey
	input.Account.ProviderAccountKey = configuredKey
	input.WindowStart = input.WindowStart.UTC()
	input.WindowEnd = input.WindowEnd.UTC()
	if input.SyncKind != "" {
		if err := validateRequiredString("Safeheron history sync kind override", input.SyncKind, maxCompanyFundSyncKindBytes); err != nil {
			return SafeheronTransactionHistoryReconcileInput{}, err
		}
	}
	if input.WindowKey != "" {
		if err := validateRequiredString("Safeheron history window key override", input.WindowKey, maxCompanyFundSyncWindowKeyBytes); err != nil {
			return SafeheronTransactionHistoryReconcileInput{}, err
		}
	}
	return input, nil
}

func safeheronTransactionHistorySyncRunInput(input SafeheronTransactionHistoryReconcileInput) SafeheronTransactionHistorySyncRunInput {
	syncKind := input.SyncKind
	if syncKind == "" {
		syncKind = SafeheronTransactionHistorySyncKind
	}
	windowKey := input.WindowKey
	if windowKey == "" {
		windowKey = "safeheron-transaction-history:v1:" + payloadSHA256Hex([]byte(lengthDelimitedTuple([]string{
			input.ProviderAccountKey,
			input.WindowStart.Format(time.RFC3339Nano),
			input.WindowEnd.Format(time.RFC3339Nano),
		})))
	}
	return SafeheronTransactionHistorySyncRunInput{
		Channel: ChannelSafeheron, SyncKind: syncKind, WindowKey: windowKey,
		CompanyFundAccountID: input.Account.ID, ProviderAccountKey: input.ProviderAccountKey,
		WindowStart: input.WindowStart, WindowEnd: input.WindowEnd,
	}
}

func (r *SafeheronTransactionHistoryReconciler) ownedEventInput(input SafeheronTransactionHistoryReconcileInput, snapshot safeheron.TransactionSnapshot) (OwnedProviderPayloadInput, error) {
	raw, identity, err := canonicalSafeheronHistorySnapshot(snapshot)
	if err != nil {
		return OwnedProviderPayloadInput{}, err
	}
	return OwnedProviderPayloadInput{
		Channel:            ChannelSafeheron,
		ProviderEventID:    safeheronTransactionHistorySnapshotEventID(input.ProviderAccountKey, identity.txKey, identity.status, payloadSHA256Hex(raw)),
		EventType:          SafeheronTransactionHistorySnapshotEventType,
		ProviderAccountKey: input.ProviderAccountKey,
		Body:               raw,
		KeyVersion:         r.config.PayloadKeyVersion,
		Retention:          r.config.PayloadRetention,
	}, nil
}

type safeheronHistorySnapshotIdentity struct {
	txKey  string
	status string
}

func canonicalSafeheronHistorySnapshot(snapshot safeheron.TransactionSnapshot) ([]byte, safeheronHistorySnapshotIdentity, error) {
	raw := append([]byte(nil), snapshot.RawPayload...)
	if len(raw) == 0 || len(raw) > MaxOwnedProviderPayloadPlaintextBytes || !json.Valid(raw) {
		return nil, safeheronHistorySnapshotIdentity{}, fmt.Errorf("Safeheron transaction history snapshot canonical raw payload is invalid")
	}
	var canonical safeheron.TransactionSnapshot
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return nil, safeheronHistorySnapshotIdentity{}, fmt.Errorf("Safeheron transaction history snapshot canonical raw payload is invalid")
	}
	identity, err := safeheronHistorySnapshotIdentityFor(canonical)
	if err != nil {
		return nil, safeheronHistorySnapshotIdentity{}, err
	}
	if strings.TrimSpace(snapshot.TxKey) != identity.txKey || normalizedLifecycleStatus(LifecycleStatus(snapshot.TransactionStatus)) != LifecycleStatus(identity.status) {
		return nil, safeheronHistorySnapshotIdentity{}, fmt.Errorf("Safeheron transaction history snapshot fields do not match canonical raw payload")
	}
	return raw, identity, nil
}

func safeheronHistorySnapshotIdentityFor(snapshot safeheron.TransactionSnapshot) (safeheronHistorySnapshotIdentity, error) {
	txKey, err := normalizeSafeheronHistoryRequired("Safeheron transaction history tx key", snapshot.TxKey, maxProviderEventIDBytes)
	if err != nil {
		return safeheronHistorySnapshotIdentity{}, err
	}
	status := string(normalizedLifecycleStatus(LifecycleStatus(snapshot.TransactionStatus)))
	if _, err := normalizeSafeheronHistoryRequired("Safeheron transaction history status", status, maxProviderEventTypeBytes); err != nil {
		return safeheronHistorySnapshotIdentity{}, err
	}
	return safeheronHistorySnapshotIdentity{txKey: txKey, status: status}, nil
}

func safeheronHistoryCursorForSnapshot(snapshot safeheron.TransactionSnapshot) (string, error) {
	identity, err := safeheronHistorySnapshotIdentityFor(snapshot)
	if err != nil {
		return "", err
	}
	return identity.txKey, nil
}

// safeheronTransactionHistorySnapshotEventID deliberately excludes txHash.
// Safeheron's txKey plus account, status, and canonical raw digest distinguish
// both different transactions and auditable revisions of the same one.
func safeheronTransactionHistorySnapshotEventID(providerAccountKey, txKey, status, rawDigest string) string {
	canonical := lengthDelimitedTuple([]string{
		"safeheron-transaction-history-snapshot", "v1", providerAccountKey, txKey, status, rawDigest,
	})
	return "safeheron-history:v1:" + payloadSHA256Hex([]byte(canonical))
}
