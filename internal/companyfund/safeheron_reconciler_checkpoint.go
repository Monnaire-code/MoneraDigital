package companyfund

import "fmt"

func validateSafeheronTransactionHistoryCheckpoint(checkpoint SafeheronTransactionHistorySyncCheckpoint) (SafeheronTransactionHistorySyncCheckpoint, string, error) {
	if checkpoint.CandidatesSeen < 0 || checkpoint.EventsCreated < 0 || checkpoint.EventsExisting < 0 {
		return SafeheronTransactionHistorySyncCheckpoint{}, "", fmt.Errorf("Safeheron transaction history sync checkpoint is invalid")
	}
	if err := validateSafeheronHistoryCursor(checkpoint.NextCursor, true); err != nil {
		return SafeheronTransactionHistorySyncCheckpoint{}, "", err
	}
	cursor := checkpoint.NextCursor
	if checkpoint.InFlightCursor != nil {
		if err := validateSafeheronHistoryCursor(*checkpoint.InFlightCursor, true); err != nil {
			return SafeheronTransactionHistorySyncCheckpoint{}, "", err
		}
		cursor = *checkpoint.InFlightCursor
	}
	if len(checkpoint.InFlightEventIDs) > int(maxSafeheronHistoryPageSize) {
		return SafeheronTransactionHistorySyncCheckpoint{}, "", fmt.Errorf("Safeheron transaction history in-flight checkpoint has too many event IDs")
	}
	seen := make(map[string]struct{}, len(checkpoint.InFlightEventIDs))
	for _, eventID := range checkpoint.InFlightEventIDs {
		if _, err := normalizeSafeheronHistoryRequired("Safeheron transaction history in-flight event ID", eventID, maxProviderEventIDBytes); err != nil {
			return SafeheronTransactionHistorySyncCheckpoint{}, "", err
		}
		if _, exists := seen[eventID]; exists {
			return SafeheronTransactionHistorySyncCheckpoint{}, "", fmt.Errorf("Safeheron transaction history in-flight checkpoint has duplicate event IDs")
		}
		seen[eventID] = struct{}{}
	}
	return cloneSafeheronTransactionHistoryCheckpoint(checkpoint), cursor, nil
}

func validateSafeheronHistoryCursor(cursor string, allowEmpty bool) error {
	if cursor == "" && allowEmpty {
		return nil
	}
	if _, err := normalizeSafeheronHistoryRequired("Safeheron transaction history cursor", cursor, maxSafeheronHistoryCursor); err != nil {
		return err
	}
	return nil
}

func cloneSafeheronTransactionHistoryCheckpoint(source SafeheronTransactionHistorySyncCheckpoint) SafeheronTransactionHistorySyncCheckpoint {
	copy := source
	if source.InFlightCursor != nil {
		copy.InFlightCursor = safeheronHistoryStringPointer(*source.InFlightCursor)
	}
	copy.InFlightEventIDs = append([]string(nil), source.InFlightEventIDs...)
	return copy
}

func safeheronTransactionHistoryResult(run SafeheronTransactionHistorySyncRun, checkpoint SafeheronTransactionHistorySyncCheckpoint) SafeheronTransactionHistoryReconcileResult {
	return SafeheronTransactionHistoryReconcileResult{
		RunID: run.ID, AttemptCount: run.AttemptCount, CandidatesSeen: checkpoint.CandidatesSeen, EventsCreated: checkpoint.EventsCreated,
		EventsExisting: checkpoint.EventsExisting, Checkpoint: cloneSafeheronTransactionHistoryCheckpoint(checkpoint),
	}
}

func safeheronHistoryStringPointer(value string) *string {
	copy := value
	return &copy
}
