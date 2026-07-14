package companyfund

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"monera-digital/internal/safeheron"
)

// SafeheronHistoryAccountContextResolver binds an owned history event's
// provider account key to an explicit configured Safeheron account. It is
// separate from address matching: one history API account can expose many
// wallet endpoints, which the immutable registry resolves later.
type SafeheronHistoryAccountContextResolver interface {
	ResolveSafeheronHistoryAccount(ctx context.Context, providerAccountKey string) (SafeheronHistoryAccountContext, error)
}

// SafeheronHistoryAccountContext proves that a history event's provider
// account key is configured. It intentionally is not a wallet account: a
// custody account can own several configured wallet addresses, which remain
// resolved solely through the immutable address registry.
type SafeheronHistoryAccountContext struct {
	ProviderAccountKey string
}

func (normalizer *SafeheronProviderEventNormalizer) normalizeTransactionHistorySnapshotEvent(
	ctx context.Context,
	lease ProviderEventLease,
	sourceBytes []byte,
) (ProviderEventNormalizationResult, error) {
	if normalizer.historyAccounts == nil {
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("Safeheron history account context resolver is unavailable")
	}
	if len(sourceBytes) == 0 || !json.Valid(sourceBytes) {
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("invalid Safeheron transaction history snapshot")
	}
	var snapshot safeheron.TransactionSnapshot
	if err := json.Unmarshal(sourceBytes, &snapshot); err != nil {
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("invalid Safeheron transaction history snapshot")
	}
	// Preserve the owned bytes only as a non-serialized provenance value. The
	// digest remains the lease digest verified by the owned-payload boundary;
	// normalizing this JSON must never invent a different source hash.
	snapshot.RawPayload = append(json.RawMessage(nil), sourceBytes...)

	account, err := normalizer.historyAccounts.ResolveSafeheronHistoryAccount(ctx, lease.ProviderAccountKey)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ProviderEventNormalizationResult{}, err
		}
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("Safeheron history account context is unavailable")
	}
	if err := validateSafeheronHistoryAccountContext(account, lease.ProviderAccountKey); err != nil {
		return ProviderEventNormalizationResult{}, err
	}

	mapping, err := normalizer.mappings.ResolveSafeheronTransactionMapping(ctx, snapshot)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ProviderEventNormalizationResult{}, err
		}
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("Safeheron transaction mapping is unavailable")
	}
	registry := normalizer.registries.Snapshot()
	if registry == nil {
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("Safeheron account registry snapshot is unavailable")
	}
	eventID := lease.ID
	result, err := NormalizeSafeheronProviderEvent(SafeheronNormalizationInput{
		Snapshot:              snapshot,
		NetworkFamily:         mapping.NetworkFamily,
		PrincipalAsset:        mapping.PrincipalAsset,
		FeeAsset:              mapping.FeeAsset,
		Registry:              registry,
		ProviderAccountKey:    lease.ProviderAccountKey,
		ProviderEventID:       lease.ProviderEventID,
		LatestProviderEventID: &eventID,
		SourcePayloadDigest:   lease.SourcePayloadDigest,
		Metadata:              ProviderFactMetadata{Source: ProviderSourceReconciliation},
		FirstSeenSource:       TransactionSeenSourceReconciliation,
	})
	if err != nil {
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("Safeheron transaction history normalization rejected payload")
	}
	return result, nil
}

func validateSafeheronHistoryAccountContext(account SafeheronHistoryAccountContext, providerAccountKey string) error {
	configuredKey, err := normalizeSafeheronHistoryRequired("configured Safeheron history provider account key", account.ProviderAccountKey, maxProviderFactAccountKeyBytes)
	if err != nil || configuredKey != providerAccountKey || providerAccountKey != strings.TrimSpace(providerAccountKey) {
		return safeheronPermanentNormalizationError("Safeheron history provider account context does not match leased event")
	}
	return nil
}
