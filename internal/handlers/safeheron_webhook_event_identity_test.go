package handlers

import (
	"context"
	"regexp"
	"testing"

	"monera-digital/internal/safeheron"
	"monera-digital/internal/wallet/deposit"
)

var safeheronContentEventIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestWebhook_ContentEventIDIsFixedDigestAndIgnoresOuterEnvelope(t *testing.T) {
	payload := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","transactionStatus":"COMPLETED"}`)
	eventIDs := make([]string, 0, 2)
	handler := newContentIdentityWebhookHandler("TRANSACTION_STATUS_CHANGED", "tx-content", "COMPLETED", &payload, &eventIDs)

	first := runWebhook(handler, `{"sig":"first-envelope"}`)
	assertSafeheronCompanyFundAck(t, first.Code, first.Body.String())
	second := runWebhook(handler, `{"sig":"second-envelope"}`)
	assertSafeheronCompanyFundAck(t, second.Code, second.Body.String())
	if len(eventIDs) != 2 {
		t.Fatalf("recorded event IDs = %v", eventIDs)
	}
	if !safeheronContentEventIDPattern.MatchString(eventIDs[0]) || eventIDs[0] != eventIDs[1] {
		t.Fatalf("same decrypted payload must produce one fixed content event ID, got %v", eventIDs)
	}
}

func TestWebhook_ContentEventIDSeparatesDifferentDecryptedPayloads(t *testing.T) {
	payload := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","revision":1}`)
	eventIDs := make([]string, 0, 2)
	handler := newContentIdentityWebhookHandler("TRANSACTION_STATUS_CHANGED", "tx-content", "COMPLETED", &payload, &eventIDs)

	first := runWebhook(handler, `{"sig":"first"}`)
	assertSafeheronCompanyFundAck(t, first.Code, first.Body.String())
	payload = []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","revision":2}`)
	second := runWebhook(handler, `{"sig":"second"}`)
	assertSafeheronCompanyFundAck(t, second.Code, second.Body.String())
	if len(eventIDs) != 2 || eventIDs[0] == eventIDs[1] {
		t.Fatalf("different decrypted payloads with the same tx/status must remain distinct, got %v", eventIDs)
	}
}

func TestWebhook_AMLContentEventIDRetainsDistinctAlertSemantics(t *testing.T) {
	payload := []byte(`{"eventType":"AML_KYT_ALERT","finding":"one"}`)
	eventIDs := make([]string, 0, 3)
	handler := newContentIdentityWebhookHandler("AML_KYT_ALERT", "tx-aml", "", &payload, &eventIDs)

	for _, outer := range []string{`{"sig":"first"}`, `{"sig":"retry"}`} {
		response := runWebhook(handler, outer)
		assertSafeheronCompanyFundAck(t, response.Code, response.Body.String())
	}
	payload = []byte(`{"eventType":"AML_KYT_ALERT","finding":"two"}`)
	response := runWebhook(handler, `{"sig":"new-alert"}`)
	assertSafeheronCompanyFundAck(t, response.Code, response.Body.String())

	if len(eventIDs) != 3 || eventIDs[0] != eventIDs[1] || eventIDs[1] == eventIDs[2] {
		t.Fatalf("AML retries must deduplicate while distinct alert content coexists, got %v", eventIDs)
	}
}

func newContentIdentityWebhookHandler(eventType, txKey, status string, payload *[]byte, eventIDs *[]string) *SafeheronWebhookHandler {
	return NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventType: eventType,
				EventDetail: safeheron.EventDetail{
					TxKey:             txKey,
					TransactionStatus: status,
				},
				RawBody: append([]byte(nil), (*payload)...),
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, event *deposit.Event) (bool, error) {
			*eventIDs = append(*eventIDs, event.EventID)
			return true, nil
		}},
		nil,
	)
}
