package companyfund

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Reconcile pages every Financial Transactions API item in a bounded UTC
// window. It stores only encrypted owned events; a later provider-event worker
// performs normalization and movement upsert after raw-fact persistence.
func (r *AirwallexFinancialTransactionsReconciler) Reconcile(
	ctx context.Context,
	input AirwallexFinancialTransactionsReconcileInput,
) (AirwallexFinancialTransactionsReconcileResult, error) {
	if r == nil || r.client == nil || r.ingester == nil || r.syncRuns == nil {
		return AirwallexFinancialTransactionsReconcileResult{}, fmt.Errorf("Airwallex financial transactions reconciler is not configured")
	}
	normalizedInput, err := r.validateInput(input)
	if err != nil {
		return AirwallexFinancialTransactionsReconcileResult{}, err
	}
	if strings.TrimSpace(r.client.PinnedAPIVersion()) != r.config.APIVersion {
		return AirwallexFinancialTransactionsReconcileResult{}, fmt.Errorf("Airwallex financial transactions client API version no longer matches reconciler pin")
	}
	if loginAsScope := r.client.PinnedLoginAsScope(); loginAsScope != normalizedInput.ProviderAccountKey {
		return AirwallexFinancialTransactionsReconcileResult{}, fmt.Errorf("Airwallex financial transactions client login scope no longer matches configured company account")
	}

	runInput := airwallexFinancialTransactionsSyncRunInput(normalizedInput, r.config.PageSize)
	run, err := r.syncRuns.OpenAirwallexFinancialTransactionsSyncRun(ctx, runInput)
	if err != nil {
		return AirwallexFinancialTransactionsReconcileResult{}, fmt.Errorf("open Airwallex financial transactions sync run: %w", err)
	}
	result := airwallexFinancialTransactionsResult(run, AirwallexFinancialTransactionsSyncCheckpoint{})
	if run.ID <= 0 {
		return result, fmt.Errorf("opened Airwallex financial transactions sync run has an invalid ID")
	}
	checkpoint, pageNum, err := validateAirwallexFinancialTransactionsCheckpoint(run.Checkpoint)
	if err != nil {
		// The exact run is already leased, so return its identity for the
		// runtime's retry/partial finalization path.
		return result, err
	}
	result = airwallexFinancialTransactionsResult(run, checkpoint)
	processedEventIDs := airwallexFinancialTransactionsCheckpointEventIDSet(checkpoint)
	pagesFetched := 0

	for pagesFetched < r.config.MaxPages {
		if pageNum > maxAirwallexFinancialTransactionPageNumber {
			return result, fmt.Errorf("Airwallex financial transactions checkpoint page exceeds provider bound")
		}
		page, err := r.client.ListFinancialTransactions(ctx, AirwallexFinancialTransactionsRequest{
			FromCreatedAt: normalizedInput.WindowStart,
			ToCreatedAt:   normalizedInput.WindowEnd,
			PageNum:       pageNum,
			PageSize:      r.config.PageSize,
		})
		if err != nil {
			return result, fmt.Errorf("list Airwallex financial transactions page %d: %w", pageNum, err)
		}
		if len(page.Items) > r.config.PageSize {
			return result, fmt.Errorf("Airwallex financial transactions page %d exceeds requested page size", pageNum)
		}
		pagesFetched++
		result.PagesFetched = pagesFetched

		for _, transaction := range page.Items {
			ownedEvent, err := r.ownedEventInput(normalizedInput, transaction)
			if err != nil {
				return result, err
			}
			if _, completed := processedEventIDs[ownedEvent.ProviderEventID]; completed {
				continue
			}
			inserted, err := r.ingester.Ingest(ctx, ownedEvent)
			if err != nil {
				return result, fmt.Errorf("ingest Airwallex financial transaction snapshot: %w", err)
			}
			processedEventIDs[ownedEvent.ProviderEventID] = struct{}{}
			checkpoint.InFlightPageNum = airwallexReconcilerIntPointer(pageNum)
			checkpoint.InFlightEventIDs = append(checkpoint.InFlightEventIDs, ownedEvent.ProviderEventID)
			checkpoint.CandidatesSeen++
			if inserted.Inserted {
				checkpoint.EventsCreated++
			} else {
				checkpoint.EventsExisting++
			}
			if err := r.syncRuns.CheckpointAirwallexFinancialTransactionsSyncRun(ctx, run.ID, cloneAirwallexFinancialTransactionsCheckpoint(checkpoint)); err != nil {
				return result, fmt.Errorf("checkpoint Airwallex financial transactions item: %w", err)
			}
			result = airwallexFinancialTransactionsResult(run, checkpoint)
			result.PagesFetched = pagesFetched
		}

		checkpoint.InFlightPageNum = nil
		checkpoint.InFlightEventIDs = nil
		// A provider claim that page 2000 still has a next page is outside the
		// documented contract. Keep this page as the retry point instead of
		// persisting an impossible page 2001 checkpoint.
		checkpoint.NextPageNum = pageNum
		if pageNum < maxAirwallexFinancialTransactionPageNumber {
			checkpoint.NextPageNum = pageNum + 1
		}
		processedEventIDs = make(map[string]struct{})
		result = airwallexFinancialTransactionsResult(run, checkpoint)
		result.PagesFetched = pagesFetched
		if !page.HasMore {
			if err := r.syncRuns.CompleteAirwallexFinancialTransactionsSyncRun(ctx, run.ID, cloneAirwallexFinancialTransactionsCheckpoint(checkpoint)); err != nil {
				return result, fmt.Errorf("complete Airwallex financial transactions sync run: %w", err)
			}
			return result, nil
		}
		if err := r.syncRuns.CheckpointAirwallexFinancialTransactionsSyncRun(ctx, run.ID, cloneAirwallexFinancialTransactionsCheckpoint(checkpoint)); err != nil {
			return result, fmt.Errorf("checkpoint Airwallex financial transactions page: %w", err)
		}
		if pagesFetched >= r.config.MaxPages || pageNum >= maxAirwallexFinancialTransactionPageNumber {
			return result, fmt.Errorf("Airwallex financial transactions pagination reached configured safety limit")
		}
		pageNum = checkpoint.NextPageNum
	}
	return result, fmt.Errorf("Airwallex financial transactions pagination reached configured safety limit")
}

func (r *AirwallexFinancialTransactionsReconciler) validateInput(input AirwallexFinancialTransactionsReconcileInput) (AirwallexFinancialTransactionsReconcileInput, error) {
	if input.Account.ID <= 0 || input.Account.Channel != AccountChannelAirwallex || !input.Account.Enabled {
		return AirwallexFinancialTransactionsReconcileInput{}, fmt.Errorf("Airwallex reconciliation requires an enabled configured company account")
	}
	configuredAccountKey, err := normalizeAirwallexReconcilerAccountKey(input.Account.ProviderAccountKey)
	if err != nil {
		return AirwallexFinancialTransactionsReconcileInput{}, err
	}
	providedAccountKey, err := normalizeAirwallexReconcilerAccountKey(input.ProviderAccountKey)
	if err != nil || configuredAccountKey != providedAccountKey {
		return AirwallexFinancialTransactionsReconcileInput{}, fmt.Errorf("Airwallex reconciliation provider account key does not match configured company account")
	}
	loginAsScope, err := normalizeAirwallexReconcilerAccountKey(r.client.PinnedLoginAsScope())
	if err != nil || loginAsScope != configuredAccountKey {
		return AirwallexFinancialTransactionsReconcileInput{}, fmt.Errorf("Airwallex reconciliation account is outside the configured login scope")
	}
	apiVersion, err := parseAirwallexAPIVersion(input.APIVersion)
	if err != nil || apiVersion != r.config.APIVersion {
		return AirwallexFinancialTransactionsReconcileInput{}, fmt.Errorf("Airwallex reconciliation API version does not match pin")
	}
	schemaVersion, err := normalizeAirwallexReconcilerVersion("Airwallex reconciliation schema version", input.SchemaVersion)
	if err != nil || schemaVersion != r.config.SchemaVersion {
		return AirwallexFinancialTransactionsReconcileInput{}, fmt.Errorf("Airwallex reconciliation schema version does not match pin")
	}
	eventVersion, err := normalizeAirwallexReconcilerVersion("Airwallex reconciliation event version", input.EventVersion)
	if err != nil || eventVersion != r.config.EventVersion {
		return AirwallexFinancialTransactionsReconcileInput{}, fmt.Errorf("Airwallex reconciliation event version does not match pin")
	}
	if input.WindowStart.IsZero() || input.WindowEnd.IsZero() || !input.WindowStart.Before(input.WindowEnd) {
		return AirwallexFinancialTransactionsReconcileInput{}, fmt.Errorf("Airwallex reconciliation requires a non-empty UTC window")
	}
	if input.SyncKind != "" {
		if err := validateRequiredString("Airwallex financial transactions sync kind override", input.SyncKind, maxCompanyFundSyncKindBytes); err != nil {
			return AirwallexFinancialTransactionsReconcileInput{}, err
		}
	}
	if input.WindowKey != "" {
		if err := validateRequiredString("Airwallex financial transactions window key override", input.WindowKey, maxCompanyFundSyncWindowKeyBytes); err != nil {
			return AirwallexFinancialTransactionsReconcileInput{}, err
		}
	}
	input.ProviderAccountKey = providedAccountKey
	input.Account.ProviderAccountKey = configuredAccountKey
	input.APIVersion = apiVersion
	input.SchemaVersion = schemaVersion
	input.EventVersion = eventVersion
	input.WindowStart = input.WindowStart.UTC()
	input.WindowEnd = input.WindowEnd.UTC()
	return input, nil
}

func normalizeAirwallexReconcilerAccountKey(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxProviderFactAccountKeyBytes {
		return "", fmt.Errorf("Airwallex reconciliation provider account key must be an exact bounded value")
	}
	return value, nil
}

func airwallexFinancialTransactionsSyncRunInput(input AirwallexFinancialTransactionsReconcileInput, pageSize int) AirwallexFinancialTransactionsSyncRunInput {
	syncKind := input.SyncKind
	if syncKind == "" {
		syncKind = AirwallexFinancialTransactionsSyncKind
	}
	windowKey := input.WindowKey
	if windowKey == "" {
		windowKey = "airwallex-financial-transactions:v1:" + payloadSHA256Hex([]byte(lengthDelimitedTuple([]string{
			input.ProviderAccountKey,
			input.WindowStart.UTC().Format(time.RFC3339Nano),
			input.WindowEnd.UTC().Format(time.RFC3339Nano),
			input.APIVersion,
			input.SchemaVersion,
			input.EventVersion,
			fmt.Sprintf("%d", pageSize),
		})))
	}
	return AirwallexFinancialTransactionsSyncRunInput{
		Channel:              ChannelAirwallex,
		SyncKind:             syncKind,
		WindowKey:            windowKey,
		CompanyFundAccountID: input.Account.ID,
		ProviderAccountKey:   input.ProviderAccountKey,
		WindowStart:          input.WindowStart,
		WindowEnd:            input.WindowEnd,
		APIVersion:           input.APIVersion,
		SchemaVersion:        input.SchemaVersion,
		EventVersion:         input.EventVersion,
		PageSize:             pageSize,
	}
}

func (r *AirwallexFinancialTransactionsReconciler) ownedEventInput(input AirwallexFinancialTransactionsReconcileInput, transaction AirwallexFinancialTransaction) (OwnedProviderPayloadInput, error) {
	providerID, err := validateAirwallexFinancialTransactionID(transaction.ProviderID)
	if err != nil {
		return OwnedProviderPayloadInput{}, fmt.Errorf("Airwallex financial transaction snapshot ID is invalid")
	}
	raw := append([]byte(nil), transaction.Raw...)
	trimmedRaw := bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > MaxOwnedProviderPayloadPlaintextBytes || len(trimmedRaw) < 2 ||
		trimmedRaw[0] != '{' || trimmedRaw[len(trimmedRaw)-1] != '}' || !json.Valid(trimmedRaw) {
		return OwnedProviderPayloadInput{}, fmt.Errorf("Airwallex financial transaction snapshot raw payload is invalid")
	}
	rawDigest := payloadSHA256Hex(raw)
	return OwnedProviderPayloadInput{
		Channel:              ChannelAirwallex,
		ProviderEventID:      airwallexFinancialTransactionSnapshotEventID(input.ProviderAccountKey, providerID, input.APIVersion, input.SchemaVersion, input.EventVersion, rawDigest),
		EventType:            AirwallexFinancialTransactionSnapshotEventType,
		ProviderEventVersion: input.APIVersion,
		ProviderAccountKey:   input.ProviderAccountKey,
		Body:                 raw,
		KeyVersion:           r.config.PayloadKeyVersion,
		Retention:            r.config.PayloadRetention,
	}, nil
}

// airwallexFinancialTransactionSnapshotEventID is deliberately based on the
// Financial Transactions line ID and exact raw digest. source_id is excluded:
// no official contract has proven it is a stable cross-resource identity.
func airwallexFinancialTransactionSnapshotEventID(providerAccountKey, financialTransactionID, apiVersion, schemaVersion, eventVersion, rawDigest string) string {
	canonical := lengthDelimitedTuple([]string{
		"airwallex-financial-transaction-snapshot",
		"v1",
		providerAccountKey,
		financialTransactionID,
		apiVersion,
		schemaVersion,
		eventVersion,
		rawDigest,
	})
	return "airwallex-financial-transaction:v1:" + payloadSHA256Hex([]byte(canonical))
}

func validateAirwallexFinancialTransactionsCheckpoint(checkpoint AirwallexFinancialTransactionsSyncCheckpoint) (AirwallexFinancialTransactionsSyncCheckpoint, int, error) {
	if checkpoint.NextPageNum < 0 || checkpoint.NextPageNum > maxAirwallexFinancialTransactionPageNumber+1 ||
		checkpoint.CandidatesSeen < 0 || checkpoint.EventsCreated < 0 || checkpoint.EventsExisting < 0 {
		return AirwallexFinancialTransactionsSyncCheckpoint{}, 0, fmt.Errorf("Airwallex financial transactions sync checkpoint is invalid")
	}
	pageNum := checkpoint.NextPageNum
	if checkpoint.InFlightPageNum != nil {
		if *checkpoint.InFlightPageNum < 0 || *checkpoint.InFlightPageNum > maxAirwallexFinancialTransactionPageNumber || len(checkpoint.InFlightEventIDs) > maxAirwallexFinancialTransactionPageSize {
			return AirwallexFinancialTransactionsSyncCheckpoint{}, 0, fmt.Errorf("Airwallex financial transactions in-flight checkpoint is invalid")
		}
		pageNum = *checkpoint.InFlightPageNum
	} else if len(checkpoint.InFlightEventIDs) != 0 {
		return AirwallexFinancialTransactionsSyncCheckpoint{}, 0, fmt.Errorf("Airwallex financial transactions in-flight checkpoint is invalid")
	}
	seenEventIDs := make(map[string]struct{}, len(checkpoint.InFlightEventIDs))
	for _, eventID := range checkpoint.InFlightEventIDs {
		if err := validateAirwallexFinancialTransactionsCheckpointEventID(eventID); err != nil {
			return AirwallexFinancialTransactionsSyncCheckpoint{}, 0, err
		}
		if _, exists := seenEventIDs[eventID]; exists {
			return AirwallexFinancialTransactionsSyncCheckpoint{}, 0, fmt.Errorf("Airwallex financial transactions in-flight checkpoint is invalid")
		}
		seenEventIDs[eventID] = struct{}{}
	}
	return cloneAirwallexFinancialTransactionsCheckpoint(checkpoint), pageNum, nil
}

func airwallexFinancialTransactionsCheckpointEventIDSet(checkpoint AirwallexFinancialTransactionsSyncCheckpoint) map[string]struct{} {
	eventIDs := make(map[string]struct{}, len(checkpoint.InFlightEventIDs))
	for _, eventID := range checkpoint.InFlightEventIDs {
		eventIDs[eventID] = struct{}{}
	}
	return eventIDs
}

func validateAirwallexFinancialTransactionsCheckpointEventID(eventID string) error {
	if err := validateRequiredString("Airwallex financial transactions in-flight event ID", eventID, maxProviderEventIDBytes); err != nil {
		return fmt.Errorf("Airwallex financial transactions in-flight checkpoint is invalid: %w", err)
	}
	if !strings.HasPrefix(eventID, "airwallex-financial-transaction:v1:") {
		return fmt.Errorf("Airwallex financial transactions in-flight checkpoint is invalid")
	}
	return nil
}

func cloneAirwallexFinancialTransactionsCheckpoint(source AirwallexFinancialTransactionsSyncCheckpoint) AirwallexFinancialTransactionsSyncCheckpoint {
	copy := source
	if source.InFlightPageNum != nil {
		copy.InFlightPageNum = airwallexReconcilerIntPointer(*source.InFlightPageNum)
	}
	copy.InFlightEventIDs = append([]string(nil), source.InFlightEventIDs...)
	return copy
}

func airwallexFinancialTransactionsResult(run AirwallexFinancialTransactionsSyncRun, checkpoint AirwallexFinancialTransactionsSyncCheckpoint) AirwallexFinancialTransactionsReconcileResult {
	return AirwallexFinancialTransactionsReconcileResult{
		RunID:          run.ID,
		AttemptCount:   run.AttemptCount,
		CandidatesSeen: checkpoint.CandidatesSeen,
		EventsCreated:  checkpoint.EventsCreated,
		EventsExisting: checkpoint.EventsExisting,
		Checkpoint:     cloneAirwallexFinancialTransactionsCheckpoint(checkpoint),
	}
}

func airwallexReconcilerIntPointer(value int) *int {
	copy := value
	return &copy
}
