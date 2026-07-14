package companyfund

import (
	"context"
	"errors"
	"fmt"
)

// SafeheronWebhookPayloadReader reads the already verified raw payload by its
// Safeheron-owned INTEGER event reference. It intentionally exposes no
// deposit worker lifecycle state.
type SafeheronWebhookPayloadReader interface {
	ReadSafeheronWebhookPayload(ctx context.Context, safeheronWebhookEventID int) ([]byte, error)
}

// OwnedProviderEventPayloadDecryptor is satisfied by
// OwnedProviderPayloadService. It keeps decryption and retention checks inside
// the company-fund payload ownership boundary.
type OwnedProviderEventPayloadDecryptor interface {
	DecryptLease(lease ProviderEventLease) ([]byte, error)
}

// ProviderEventSourceBytesReader dispatches the two intentionally different
// durable source models. It never attempts to infer a source from channel,
// account, organization, or nullable metadata.
type ProviderEventSourceBytesReader struct {
	safeheron SafeheronWebhookPayloadReader
	owned     OwnedProviderEventPayloadDecryptor
}

func NewProviderEventSourceBytesReader(safeheron SafeheronWebhookPayloadReader, owned OwnedProviderEventPayloadDecryptor) (*ProviderEventSourceBytesReader, error) {
	if safeheron == nil {
		return nil, fmt.Errorf("Safeheron webhook payload reader is required")
	}
	if owned == nil {
		return nil, fmt.Errorf("owned provider payload decryptor is required")
	}
	return &ProviderEventSourceBytesReader{safeheron: safeheron, owned: owned}, nil
}

func (reader *ProviderEventSourceBytesReader) ReadProviderEventPayload(ctx context.Context, lease ProviderEventLease) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("provider event source reader is not configured")
	}

	switch lease.SourceKind {
	case ProviderEventSourceExistingSafeheronWebhookRef:
		if reader.safeheron == nil || lease.SafeheronWebhookEventID == nil || *lease.SafeheronWebhookEventID <= 0 {
			return nil, NewPermanentProviderEventError(fmt.Errorf("invalid Safeheron raw event source reference"))
		}
		payload, err := reader.safeheron.ReadSafeheronWebhookPayload(ctx, *lease.SafeheronWebhookEventID)
		if err != nil {
			return nil, fmt.Errorf("read Safeheron provider event payload: %w", err)
		}
		return append([]byte(nil), payload...), nil
	case ProviderEventSourceOwnedEncryptedPayload:
		if reader.owned == nil {
			return nil, fmt.Errorf("owned provider payload decryptor is not configured")
		}
		payload, err := reader.owned.DecryptLease(lease)
		if err != nil {
			if errors.Is(err, ErrOwnedProviderPayloadUnavailable) || errors.Is(err, ErrOwnedProviderPayloadIntegrity) {
				return nil, NewPermanentProviderEventError(err)
			}
			return nil, fmt.Errorf("read owned provider event payload: %w", err)
		}
		return append([]byte(nil), payload...), nil
	default:
		return nil, NewPermanentProviderEventError(fmt.Errorf("unsupported provider event source kind %q", lease.SourceKind))
	}
}
