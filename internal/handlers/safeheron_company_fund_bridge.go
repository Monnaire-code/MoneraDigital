package handlers

import (
	"context"
	"errors"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/wallet/deposit"
)

// SafeheronEventSourceLookup is the read-only source reference needed by the
// company-fund ledger after the deposit-owned raw event has been persisted.
// It intentionally excludes deposit worker lifecycle methods.
type SafeheronEventSourceLookup interface {
	LookupEventSource(ctx context.Context, eventID string) (deposit.EventSource, error)
}

// SafeheronCompanyFundProviderBridge is satisfied by companyfund.DBRepository.
// It only inserts a provider event reference; no normalizer or external HTTP
// operation is allowed in the synchronous webhook acknowledgement path.
type SafeheronCompanyFundProviderBridge interface {
	InsertProviderEvent(ctx context.Context, input companyfund.ProviderEventInput) (companyfund.ProviderEventInsertResult, error)
}

func (h *SafeheronWebhookHandler) lookupCompanyFundSafeheronSource(
	ctx context.Context,
	eventID string,
	payloadDigest string,
) (deposit.EventSource, error) {
	if h == nil || h.companyFundSourceLookup == nil {
		return deposit.EventSource{}, errors.New("Safeheron company-fund source lookup is not configured")
	}
	source, err := h.companyFundSourceLookup.LookupEventSource(ctx, eventID)
	if err != nil {
		return deposit.EventSource{}, err
	}
	if source.ID <= 0 || source.PayloadDigest != payloadDigest {
		return deposit.EventSource{}, errors.New("Safeheron source payload digest mismatch")
	}
	return source, nil
}

func (h *SafeheronWebhookHandler) bridgeCompanyFundProviderEvent(
	ctx context.Context,
	eventID, eventType, payloadDigest string,
	source deposit.EventSource,
) error {
	if h == nil || h.companyFundProviderBridge == nil {
		return errors.New("Safeheron company-fund provider bridge is not configured")
	}
	if source.ID <= 0 || source.PayloadDigest != payloadDigest {
		return errors.New("Safeheron source payload digest mismatch")
	}
	safeheronEventID := source.ID
	_, err := h.companyFundProviderBridge.InsertProviderEvent(ctx, companyfund.ProviderEventInput{
		Channel:                 companyfund.ChannelSafeheron,
		ProviderEventID:         eventID,
		EventType:               eventType,
		SourceKind:              companyfund.ProviderEventSourceExistingSafeheronWebhookRef,
		SafeheronWebhookEventID: &safeheronEventID,
		SourcePayloadDigest:     payloadDigest,
	})
	if err != nil {
		return err
	}
	// Wake only after durable insert/idempotent lookup succeeded. The signal is
	// coalescible; lost wakes are recovered by startup and max-idle scans.
	if h.companyFundProviderEventWake != nil {
		h.companyFundProviderEventWake()
	}
	return nil
}
