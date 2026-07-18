package companyfund

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"monera-digital/internal/safeheron"
)

const (
	safeheronTransactionStatusChangedEventType = "TRANSACTION_STATUS_CHANGED"
	safeheronTransactionCreatedEventType       = "TRANSACTION_CREATED"
	safeheronAMLKYTAlertEventType              = "AML_KYT_ALERT"
)

// SafeheronTransactionMapping is an explicit provider-key mapping selected by
// configured Safeheron coin keys. NetworkFamily and asset identity are never
// inferred from a ticker or an address.
type SafeheronTransactionMapping struct {
	NetworkFamily  string
	PrincipalAsset SafeheronAssetMapping
	FeeAsset       *SafeheronAssetMapping
}

// SafeheronTransactionMappingResolver supplies the pre-approved mapping for
// one transaction snapshot. Its implementation can use an immutable config
// map or a separately managed registry, but must not guess from CoinKey text.
type SafeheronTransactionMappingResolver interface {
	ResolveSafeheronTransactionMapping(ctx context.Context, snapshot safeheron.TransactionSnapshot) (SafeheronTransactionMapping, error)
}

// SafeheronRegistrySnapshotProvider provides the immutable account/policy view
// used for one normalization. AccountRegistry satisfies this interface.
type SafeheronRegistrySnapshotProvider interface {
	Snapshot() *AccountRegistrySnapshot
}

type SafeheronProviderEventNormalizerConfig struct {
	MappingResolver        SafeheronTransactionMappingResolver
	RegistrySnapshots      SafeheronRegistrySnapshotProvider
	HistoryAccountResolver SafeheronHistoryAccountContextResolver
}

// SafeheronProviderEventNormalizer adapts already-verified Safeheron JSONB
// source payloads into the provider-event worker contract. It has no database,
// HTTP, container, or deposit-worker lifecycle dependency.
type SafeheronProviderEventNormalizer struct {
	mappings        SafeheronTransactionMappingResolver
	registries      SafeheronRegistrySnapshotProvider
	historyAccounts SafeheronHistoryAccountContextResolver
}

func NewSafeheronProviderEventNormalizer(config SafeheronProviderEventNormalizerConfig) (*SafeheronProviderEventNormalizer, error) {
	if config.MappingResolver == nil {
		return nil, fmt.Errorf("Safeheron transaction mapping resolver is required")
	}
	if config.RegistrySnapshots == nil {
		return nil, fmt.Errorf("Safeheron account registry snapshot provider is required")
	}
	return &SafeheronProviderEventNormalizer{
		mappings: config.MappingResolver, registries: config.RegistrySnapshots, historyAccounts: config.HistoryAccountResolver,
	}, nil
}

func (normalizer *SafeheronProviderEventNormalizer) NormalizeProviderEvent(ctx context.Context, lease ProviderEventLease, sourceBytes []byte) (ProviderEventNormalizationResult, error) {
	if normalizer == nil || normalizer.mappings == nil || normalizer.registries == nil {
		return ProviderEventNormalizationResult{}, fmt.Errorf("Safeheron provider event normalizer is not configured")
	}
	if err := validateSafeheronProviderEventLease(lease); err != nil {
		return ProviderEventNormalizationResult{}, err
	}
	if lease.EventType == SafeheronTransactionHistorySnapshotEventType {
		return normalizer.normalizeTransactionHistorySnapshotEvent(ctx, lease, sourceBytes)
	}
	envelope, err := parseSafeheronProviderEventEnvelope(sourceBytes)
	if err != nil {
		return ProviderEventNormalizationResult{}, err
	}
	if envelope.EventType != strings.TrimSpace(lease.EventType) {
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("event type does not match provider-event lease")
	}

	switch envelope.EventType {
	case safeheronAMLKYTAlertEventType, safeheronTransactionCreatedEventType:
		if err := validateSafeheronNonReportableEventDetail(envelope.EventDetail); err != nil {
			return ProviderEventNormalizationResult{}, err
		}
		// AML-only callbacks and transaction-created deliveries do not contain a
		// reportable, final transaction fact under this adapter contract. They
		// remain durably auditable in the inbox; a later status callback or
		// history reconciliation can supply the complete transaction snapshot.
		return ProviderEventNormalizationResult{Ignored: true}, nil
	case safeheronTransactionStatusChangedEventType:
		return normalizer.normalizeTransactionStatusEvent(ctx, lease, envelope.EventDetail)
	default:
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("unsupported Safeheron provider event type")
	}
}

func (normalizer *SafeheronProviderEventNormalizer) normalizeTransactionStatusEvent(ctx context.Context, lease ProviderEventLease, detail json.RawMessage) (ProviderEventNormalizationResult, error) {
	var snapshot safeheron.TransactionSnapshot
	if len(detail) == 0 || json.Unmarshal(detail, &snapshot) != nil {
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("invalid Safeheron transaction status detail")
	}
	mapping, err := normalizer.mappings.ResolveSafeheronTransactionMapping(ctx, snapshot)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ProviderEventNormalizationResult{}, err
		}
		var configurationError *SafeheronAccountContextConfigurationError
		if errors.As(err, &configurationError) {
			return ProviderEventNormalizationResult{}, err
		}
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("Safeheron transaction mapping is unavailable")
	}
	registry := normalizer.registries.Snapshot()
	if registry == nil {
		return ProviderEventNormalizationResult{}, safeheronAccountContextError("account registry snapshot is unavailable")
	}
	eventID := lease.ID
	result, err := NormalizeSafeheronProviderEvent(SafeheronNormalizationInput{
		Snapshot:                snapshot,
		NetworkFamily:           mapping.NetworkFamily,
		PrincipalAsset:          mapping.PrincipalAsset,
		FeeAsset:                mapping.FeeAsset,
		Registry:                registry,
		ProviderEventID:         lease.ProviderEventID,
		LatestProviderEventID:   &eventID,
		SourcePayloadDigest:     lease.SourcePayloadDigest,
		Metadata:                ProviderFactMetadata{Source: ProviderSourceWebhook},
		FirstSeenSource:         TransactionSeenSourceWebhook,
		AuthorizedOccurrenceKey: lease.AuthorizedSafeheronOccurrenceKey,
	})
	if err != nil {
		return ProviderEventNormalizationResult{}, safeheronPermanentNormalizationError("Safeheron transaction normalization rejected payload")
	}
	return result, nil
}

type safeheronProviderEventEnvelope struct {
	EventType   string          `json:"eventType"`
	EventDetail json.RawMessage `json:"eventDetail"`
}

func parseSafeheronProviderEventEnvelope(sourceBytes []byte) (safeheronProviderEventEnvelope, error) {
	var envelope safeheronProviderEventEnvelope
	if len(sourceBytes) == 0 || json.Unmarshal(sourceBytes, &envelope) != nil {
		return safeheronProviderEventEnvelope{}, safeheronPermanentNormalizationError("invalid Safeheron webhook JSONB payload")
	}
	envelope.EventType = strings.TrimSpace(envelope.EventType)
	if envelope.EventType == "" || len(envelope.EventDetail) == 0 || string(envelope.EventDetail) == "null" {
		return safeheronProviderEventEnvelope{}, safeheronPermanentNormalizationError("incomplete Safeheron webhook payload")
	}
	return envelope, nil
}

func validateSafeheronNonReportableEventDetail(detail json.RawMessage) error {
	var payload struct {
		TxKey string `json:"txKey"`
	}
	if json.Unmarshal(detail, &payload) != nil || strings.TrimSpace(payload.TxKey) == "" {
		return safeheronPermanentNormalizationError("invalid non-reportable Safeheron event detail")
	}
	return nil
}

func validateSafeheronProviderEventLease(lease ProviderEventLease) error {
	if lease.Channel != ChannelSafeheron || lease.ID <= 0 || strings.TrimSpace(lease.ProviderEventID) == "" ||
		strings.TrimSpace(lease.EventType) == "" || !isLowerSHA256Hex(lease.SourcePayloadDigest) {
		return safeheronPermanentNormalizationError("invalid Safeheron provider-event lease")
	}
	switch lease.SourceKind {
	case ProviderEventSourceExistingSafeheronWebhookRef:
		if lease.SafeheronWebhookEventID == nil || *lease.SafeheronWebhookEventID <= 0 ||
			lease.EventType == SafeheronTransactionHistorySnapshotEventType {
			return safeheronPermanentNormalizationError("invalid Safeheron webhook provider-event lease")
		}
		if lease.AuthorizedSafeheronOccurrenceKey != "" && !validSafeheronOccurrenceKey(lease.AuthorizedSafeheronOccurrenceKey) {
			return safeheronPermanentNormalizationError("invalid Safeheron routing occurrence authorization")
		}
	case ProviderEventSourceOwnedEncryptedPayload:
		if lease.EventType != SafeheronTransactionHistorySnapshotEventType || lease.SafeheronWebhookEventID != nil ||
			strings.TrimSpace(lease.ProviderAccountKey) == "" || lease.AuthorizedSafeheronOccurrenceKey != "" {
			return safeheronPermanentNormalizationError("invalid Safeheron transaction history provider-event lease")
		}
	default:
		return safeheronPermanentNormalizationError("unsupported Safeheron provider-event source")
	}
	return nil
}

func safeheronPermanentNormalizationError(reason string) error {
	return NewPermanentProviderEventError(fmt.Errorf("%s", reason))
}

var _ ProviderEventNormalizer = (*SafeheronProviderEventNormalizer)(nil)
