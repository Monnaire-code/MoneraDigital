package companyfund

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func companyFundSyncRunAdapterInputForSafeheron(input SafeheronTransactionHistorySyncRunInput) (CompanyFundSyncRunInput, error) {
	if input.Channel != ChannelSafeheron || input.CompanyFundAccountID <= 0 {
		return CompanyFundSyncRunInput{}, fmt.Errorf("invalid Safeheron transaction-history sync-run input")
	}
	switch input.SyncKind {
	case SafeheronTransactionHistorySyncKind, SafeheronTransactionHistoryLateStatusSyncKind:
	default:
		return CompanyFundSyncRunInput{}, fmt.Errorf("invalid Safeheron transaction-history sync-run kind")
	}
	if _, err := normalizeSafeheronHistoryRequired("Safeheron history provider account key", input.ProviderAccountKey, maxProviderFactAccountKeyBytes); err != nil {
		return CompanyFundSyncRunInput{}, err
	}
	expectedInput := SafeheronTransactionHistoryReconcileInput{
		Account: CompanyFundAccount{
			ID:                 input.CompanyFundAccountID,
			Channel:            ChannelSafeheron,
			ProviderAccountKey: input.ProviderAccountKey,
			Enabled:            true,
		},
		ProviderAccountKey: input.ProviderAccountKey,
		WindowStart:        input.WindowStart,
		WindowEnd:          input.WindowEnd,
		SyncKind:           input.SyncKind,
	}
	if input.SyncKind == SafeheronTransactionHistoryLateStatusSyncKind {
		// A late-status caller owns an explicit, independent key. Its value is
		// still bounded by the generic canonical input validation below.
		expectedInput.WindowKey = input.WindowKey
	}
	expectedWindowKey := safeheronTransactionHistorySyncRunInput(expectedInput).WindowKey
	if input.WindowKey != expectedWindowKey {
		return CompanyFundSyncRunInput{}, fmt.Errorf("Safeheron transaction-history sync-run window key does not match the explicit account window")
	}
	return (CompanyFundSyncRunInput{
		Channel:     input.Channel,
		SyncKind:    input.SyncKind,
		WindowKey:   input.WindowKey,
		WindowStart: input.WindowStart,
		WindowEnd:   input.WindowEnd,
		Checkpoint:  json.RawMessage(`{}`),
	}).canonical()
}

func companyFundSyncRunAdapterInputForAirwallex(input AirwallexFinancialTransactionsSyncRunInput) (CompanyFundSyncRunInput, error) {
	if input.Channel != ChannelAirwallex || input.CompanyFundAccountID <= 0 ||
		input.PageSize < 1 || input.PageSize > maxAirwallexFinancialTransactionPageSize {
		return CompanyFundSyncRunInput{}, fmt.Errorf("invalid Airwallex financial-transactions sync-run input")
	}
	switch input.SyncKind {
	case AirwallexFinancialTransactionsSyncKind, AirwallexFinancialTransactionsLateStatusSyncKind:
	default:
		return CompanyFundSyncRunInput{}, fmt.Errorf("invalid Airwallex financial-transactions sync-run kind")
	}
	if _, err := normalizeAirwallexReconcilerAccountKey(input.ProviderAccountKey); err != nil {
		return CompanyFundSyncRunInput{}, err
	}
	if _, err := parseAirwallexAPIVersion(input.APIVersion); err != nil {
		return CompanyFundSyncRunInput{}, err
	}
	if _, err := normalizeAirwallexReconcilerVersion("Airwallex reconciliation schema version", input.SchemaVersion); err != nil {
		return CompanyFundSyncRunInput{}, err
	}
	if _, err := normalizeAirwallexReconcilerVersion("Airwallex reconciliation event version", input.EventVersion); err != nil {
		return CompanyFundSyncRunInput{}, err
	}
	expectedInput := AirwallexFinancialTransactionsReconcileInput{
		Account: CompanyFundAccount{
			ID:                 input.CompanyFundAccountID,
			Channel:            ChannelAirwallex,
			ProviderAccountKey: input.ProviderAccountKey,
			Enabled:            true,
		},
		ProviderAccountKey: input.ProviderAccountKey,
		WindowStart:        input.WindowStart,
		WindowEnd:          input.WindowEnd,
		APIVersion:         input.APIVersion,
		SchemaVersion:      input.SchemaVersion,
		EventVersion:       input.EventVersion,
		SyncKind:           input.SyncKind,
	}
	if input.SyncKind == AirwallexFinancialTransactionsLateStatusSyncKind {
		// The late-status pass owns a stable, independent account/date key. It
		// is still bounded by generic canonical validation below.
		expectedInput.WindowKey = input.WindowKey
	}
	expectedWindowKey := airwallexFinancialTransactionsSyncRunInput(expectedInput, input.PageSize).WindowKey
	if input.WindowKey != expectedWindowKey {
		return CompanyFundSyncRunInput{}, fmt.Errorf("Airwallex financial-transactions sync-run window key does not match the explicit account window")
	}
	return (CompanyFundSyncRunInput{
		Channel:     input.Channel,
		SyncKind:    input.SyncKind,
		WindowKey:   input.WindowKey,
		WindowStart: input.WindowStart,
		WindowEnd:   input.WindowEnd,
		Checkpoint:  json.RawMessage(`{}`),
	}).canonical()
}

func decodeSafeheronTransactionHistorySyncCheckpoint(raw json.RawMessage) (SafeheronTransactionHistorySyncCheckpoint, error) {
	var checkpoint SafeheronTransactionHistorySyncCheckpoint
	if err := decodeCompanyFundSyncRunAdapterCheckpoint(raw, &checkpoint); err != nil {
		return SafeheronTransactionHistorySyncCheckpoint{}, err
	}
	normalized, _, err := validateSafeheronTransactionHistoryCheckpoint(checkpoint)
	if err != nil {
		return SafeheronTransactionHistorySyncCheckpoint{}, err
	}
	return normalized, nil
}

func decodeAirwallexFinancialTransactionsSyncCheckpoint(raw json.RawMessage) (AirwallexFinancialTransactionsSyncCheckpoint, error) {
	var checkpoint AirwallexFinancialTransactionsSyncCheckpoint
	if err := decodeCompanyFundSyncRunAdapterCheckpoint(raw, &checkpoint); err != nil {
		return AirwallexFinancialTransactionsSyncCheckpoint{}, err
	}
	normalized, _, err := validateAirwallexFinancialTransactionsCheckpoint(checkpoint)
	if err != nil {
		return AirwallexFinancialTransactionsSyncCheckpoint{}, err
	}
	return normalized, nil
}

func encodeSafeheronTransactionHistorySyncCheckpoint(checkpoint SafeheronTransactionHistorySyncCheckpoint) (json.RawMessage, SafeheronTransactionHistorySyncCheckpoint, error) {
	normalized, _, err := validateSafeheronTransactionHistoryCheckpoint(checkpoint)
	if err != nil {
		return nil, SafeheronTransactionHistorySyncCheckpoint{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, SafeheronTransactionHistorySyncCheckpoint{}, fmt.Errorf("marshal Safeheron transaction-history checkpoint: %w", err)
	}
	canonical, err := canonicalCompanyFundSyncCheckpoint(raw)
	if err != nil {
		return nil, SafeheronTransactionHistorySyncCheckpoint{}, err
	}
	return canonical, normalized, nil
}

func encodeAirwallexFinancialTransactionsSyncCheckpoint(checkpoint AirwallexFinancialTransactionsSyncCheckpoint) (json.RawMessage, AirwallexFinancialTransactionsSyncCheckpoint, error) {
	normalized, _, err := validateAirwallexFinancialTransactionsCheckpoint(checkpoint)
	if err != nil {
		return nil, AirwallexFinancialTransactionsSyncCheckpoint{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, AirwallexFinancialTransactionsSyncCheckpoint{}, fmt.Errorf("marshal Airwallex financial-transactions checkpoint: %w", err)
	}
	canonical, err := canonicalCompanyFundSyncCheckpoint(raw)
	if err != nil {
		return nil, AirwallexFinancialTransactionsSyncCheckpoint{}, err
	}
	return canonical, normalized, nil
}

func decodeCompanyFundSyncRunAdapterCheckpoint(raw json.RawMessage, target any) error {
	canonical, err := canonicalCompanyFundSyncCheckpoint(raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode company-fund typed sync checkpoint: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errorsIsEOF(err) {
		return fmt.Errorf("decode company-fund typed sync checkpoint: unexpected trailing value")
	}
	return nil
}

func errorsIsEOF(err error) bool {
	return err == io.EOF
}
