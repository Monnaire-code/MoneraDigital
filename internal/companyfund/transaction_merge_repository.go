package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const selectCompanyFundProviderEventProvenanceSQL = `
SELECT channel, source_payload_digest
FROM company_fund_provider_events
WHERE id = $1`

const selectProviderTransactionFactOwnershipSQL = `
SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version
FROM company_fund_provider_transaction_facts
WHERE id = $1`

type transactionStableIdentity struct {
	ProviderAccountKey    string
	ProviderTransactionID string
	ProviderMovementID    string
}

type transactionProviderProvenance struct {
	ProviderEventID           string
	ProviderTransactionFactID *int64
	LatestProviderEventID     *int64
	RawSnapshotDigest         string
}

func (input TransactionUpsertInput) stableIdentity() transactionStableIdentity {
	return transactionStableIdentity{
		ProviderAccountKey:    input.ProviderAccountKey,
		ProviderTransactionID: input.ProviderTransactionID,
		ProviderMovementID:    input.ProviderMovementID,
	}
}

func (input TransactionUpsertInput) providerProvenance() transactionProviderProvenance {
	return transactionProviderProvenance{
		ProviderEventID:           input.ProviderEventID,
		ProviderTransactionFactID: input.ProviderTransactionFactID,
		LatestProviderEventID:     input.LatestProviderEventID,
		RawSnapshotDigest:         input.RawSnapshotDigest,
	}
}

func (r *DBRepository) resolveIncomingTransactionProvenance(ctx context.Context, tx *sql.Tx, input TransactionUpsertInput) (transactionProviderProvenance, error) {
	provenance := input.providerProvenance()
	if err := provenance.validatePair(); err != nil {
		return transactionProviderProvenance{}, err
	}
	if !provenance.hasPair() {
		return provenance, nil
	}

	var providerEventChannel string
	var providerEventDigest string
	if err := tx.QueryRowContext(ctx, selectCompanyFundProviderEventProvenanceSQL, *provenance.LatestProviderEventID).Scan(&providerEventChannel, &providerEventDigest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return transactionProviderProvenance{}, fmt.Errorf("latest provider event %d does not exist", *provenance.LatestProviderEventID)
		}
		return transactionProviderProvenance{}, fmt.Errorf("read latest provider event provenance: %w", err)
	}
	if Channel(providerEventChannel) != input.Channel {
		return transactionProviderProvenance{}, fmt.Errorf("latest provider event channel %q does not match transaction channel %q", providerEventChannel, input.Channel)
	}
	if providerEventDigest != provenance.RawSnapshotDigest {
		return transactionProviderProvenance{}, fmt.Errorf("latest provider event payload digest does not match transaction raw snapshot digest")
	}
	return provenance, nil
}

func (r *DBRepository) validateProviderTransactionFactOwnership(ctx context.Context, tx *sql.Tx, transactionChannel Channel, stableProviderAccountKey, stableProviderTransactionID string, providerTransactionFactID *int64) error {
	if providerTransactionFactID == nil {
		return nil
	}

	var factChannel string
	var factProviderAccountKey sql.NullString
	var factProviderTransactionID sql.NullString
	var allocationState string
	var derivationContractVersion sql.NullString
	if err := tx.QueryRowContext(ctx, selectProviderTransactionFactOwnershipSQL, *providerTransactionFactID).Scan(&factChannel, &factProviderAccountKey, &factProviderTransactionID, &allocationState, &derivationContractVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("provider transaction fact %d does not exist", *providerTransactionFactID)
		}
		return fmt.Errorf("read provider transaction fact ownership: %w", err)
	}
	if Channel(factChannel) != transactionChannel {
		return fmt.Errorf("provider transaction fact channel %q does not match transaction channel %q", factChannel, transactionChannel)
	}
	if stableProviderAccountKey == "" || !factProviderAccountKey.Valid || factProviderAccountKey.String == "" || factProviderAccountKey.String != stableProviderAccountKey {
		return fmt.Errorf("provider transaction fact %d does not have a complete matching provider account identity", *providerTransactionFactID)
	}
	directTransactionMatch := stableProviderTransactionID != "" &&
		factProviderTransactionID.Valid && factProviderTransactionID.String != "" &&
		factProviderTransactionID.String == stableProviderTransactionID
	if directTransactionMatch {
		return nil
	}
	if allocationState != "PROVEN_DERIVABLE" || !derivationContractVersion.Valid || strings.TrimSpace(derivationContractVersion.String) == "" {
		return fmt.Errorf("provider transaction fact %d does not have complete matching identity and is not proven derivable for target transaction", *providerTransactionFactID)
	}
	return nil
}

func resolveStableTransactionIdentity(existing, incoming transactionStableIdentity) (transactionStableIdentity, error) {
	accountKey, err := resolveImmutableTransactionIdentity("provider account key", existing.ProviderAccountKey, incoming.ProviderAccountKey)
	if err != nil {
		return transactionStableIdentity{}, err
	}
	transactionID, err := resolveImmutableTransactionIdentity("provider transaction ID", existing.ProviderTransactionID, incoming.ProviderTransactionID)
	if err != nil {
		return transactionStableIdentity{}, err
	}
	movementID, err := resolveImmutableTransactionIdentity("provider movement ID", existing.ProviderMovementID, incoming.ProviderMovementID)
	if err != nil {
		return transactionStableIdentity{}, err
	}
	return transactionStableIdentity{
		ProviderAccountKey:    accountKey,
		ProviderTransactionID: transactionID,
		ProviderMovementID:    movementID,
	}, nil
}

func resolveImmutableTransactionIdentity(label, existing, incoming string) (string, error) {
	switch {
	case existing == "":
		return incoming, nil
	case incoming == "" || incoming == existing:
		return existing, nil
	default:
		return "", fmt.Errorf("immutable %s conflicts with stored transaction identity", label)
	}
}

func resolveLatestTransactionProvenance(existing, incoming transactionProviderProvenance, incomingMetadataWins bool) transactionProviderProvenance {
	resolved := existing
	if (incomingMetadataWins || resolved.ProviderEventID == "") && incoming.ProviderEventID != "" {
		resolved.ProviderEventID = incoming.ProviderEventID
	}
	if (incomingMetadataWins || resolved.ProviderTransactionFactID == nil) && incoming.ProviderTransactionFactID != nil {
		value := *incoming.ProviderTransactionFactID
		resolved.ProviderTransactionFactID = &value
	}
	if incoming.hasPair() && (incomingMetadataWins || !resolved.hasPair()) {
		providerEventID := *incoming.LatestProviderEventID
		resolved.LatestProviderEventID = &providerEventID
		resolved.RawSnapshotDigest = incoming.RawSnapshotDigest
	}
	return resolved
}

func shouldReplaceProviderAssetIdentity(existing, incoming ProviderOwnedFields) bool {
	if incoming.Asset == nil {
		return false
	}
	if existing.Asset == nil {
		return true
	}
	return compareProviderMetadata(existing.Metadata, incoming.Metadata) > 0
}

func (provenance transactionProviderProvenance) hasPair() bool {
	return provenance.LatestProviderEventID != nil && provenance.RawSnapshotDigest != ""
}

func (provenance transactionProviderProvenance) validatePair() error {
	if (provenance.LatestProviderEventID == nil) != (provenance.RawSnapshotDigest == "") {
		return fmt.Errorf("latest provider event ID and raw snapshot digest must be provided together")
	}
	return nil
}

func (source ProviderFactSource) valid() bool {
	return source == ProviderSourceWebhook || source == ProviderSourceProductDetail || source == ProviderSourceReconciliation
}
