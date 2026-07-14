package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/companyfund"
)

type airwallexWebhookIngestorStub struct {
	ingest func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error)
}

func (s airwallexWebhookIngestorStub) Ingest(ctx context.Context, input companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
	return s.ingest(ctx, input)
}

type airwallexWebhookVerifierStub struct {
	verify func(timestamp, signature string, rawBody []byte) error
}

func (s airwallexWebhookVerifierStub) Verify(timestamp, signature string, rawBody []byte) error {
	return s.verify(timestamp, signature, rawBody)
}

func newTestCompanyFundAirwallexWebhookHandler(t *testing.T, verifier AirwallexWebhookSignatureVerifier, ingestor AirwallexWebhookPayloadIngestor) *CompanyFundAirwallexWebhookHandler {
	return newTestCompanyFundAirwallexWebhookHandlerWithWake(t, verifier, ingestor, nil)
}

func newTestCompanyFundAirwallexWebhookHandlerWithWake(t *testing.T, verifier AirwallexWebhookSignatureVerifier, ingestor AirwallexWebhookPayloadIngestor, wake func()) *CompanyFundAirwallexWebhookHandler {
	t.Helper()
	handler, err := NewCompanyFundAirwallexWebhookHandler(CompanyFundAirwallexWebhookHandlerConfig{
		Verifier:             verifier,
		Ingestor:             ingestor,
		Wake:                 wake,
		ProviderEventVersion: "2026-05-29",
		KeyVersion:           "payload-v1",
		Retention:            48 * time.Hour,
		LegalHold:            true,
	})
	if err != nil {
		t.Fatalf("NewCompanyFundAirwallexWebhookHandler() error = %v", err)
	}
	return handler
}

func newTestAirwallexWebhookVerifier(t *testing.T, now time.Time) *companyfund.AirwallexWebhookVerifier {
	t.Helper()
	verifier, err := companyfund.NewAirwallexWebhookVerifier(companyfund.AirwallexWebhookVerifierConfig{
		Secret: "webhook-secret",
		MaxAge: time.Minute,
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewAirwallexWebhookVerifier() error = %v", err)
	}
	return verifier
}

func runCompanyFundAirwallexWebhook(handler *CompanyFundAirwallexWebhookHandler, body []byte, timestamp, signature string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/webhooks/airwallex", bytes.NewReader(body))
	context.Request.Header.Set("x-timestamp", timestamp)
	context.Request.Header.Set("x-signature", signature)
	handler.Receive(context)
	return response
}

func airwallexWebhookTestSignature(timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte("webhook-secret"))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestCompanyFundAirwallexWebhookHandler_IngestsExactVerifiedBody(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	timestamp := "1783652400000"
	body := []byte(`{"id":"evt_123","name":"deposit.created","account_id":"acct_1","org_id":"org_1","data":{"amount":"12.34"}}`)
	var got companyfund.OwnedProviderPayloadInput
	handler := newTestCompanyFundAirwallexWebhookHandler(t, newTestAirwallexWebhookVerifier(t, now), airwallexWebhookIngestorStub{
		ingest: func(_ context.Context, input companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			got = input
			return companyfund.ProviderEventInsertResult{ID: 7, Inserted: true}, nil
		},
	})

	response := runCompanyFundAirwallexWebhook(handler, body, timestamp, airwallexWebhookTestSignature(timestamp, body))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got.Channel != companyfund.ChannelAirwallex || got.ProviderEventID != "evt_123" || got.EventType != "deposit.created" || got.ProviderEventVersion != "2026-05-29" || got.ProviderAccountKey != "acct_1" || got.ProviderOrgKey != "org_1" || got.KeyVersion != "payload-v1" || got.Retention != 48*time.Hour || !got.LegalHold || string(got.Body) != string(body) {
		t.Fatalf("ingest input = %#v", got)
	}
}

func TestCompanyFundAirwallexWebhookHandler_RejectsReorderedSignedBody(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	timestamp := "1783652400000"
	original := []byte(`{"id":"evt_123","name":"deposit.created","account_id":"acct_1"}`)
	reordered := []byte(`{"account_id":"acct_1","name":"deposit.created","id":"evt_123"}`)
	woke := false
	handler := newTestCompanyFundAirwallexWebhookHandlerWithWake(t, newTestAirwallexWebhookVerifier(t, now), airwallexWebhookIngestorStub{
		ingest: func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			t.Fatal("reordered body must not be ingested")
			return companyfund.ProviderEventInsertResult{}, nil
		},
	}, func() { woke = true })

	response := runCompanyFundAirwallexWebhook(handler, reordered, timestamp, airwallexWebhookTestSignature(timestamp, original))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if woke {
		t.Fatal("signature failure must not wake reconciliation")
	}
}

func TestCompanyFundAirwallexWebhookHandler_RejectsMissingIDOrNameAfterVerification(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	timestamp := "1783652400000"
	for _, body := range [][]byte{
		[]byte(`{"name":"deposit.created"}`),
		[]byte(`{"id":"evt_123"}`),
	} {
		handler := newTestCompanyFundAirwallexWebhookHandler(t, newTestAirwallexWebhookVerifier(t, now), airwallexWebhookIngestorStub{
			ingest: func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
				t.Fatal("incomplete envelope must not be ingested")
				return companyfund.ProviderEventInsertResult{}, nil
			},
		})
		response := runCompanyFundAirwallexWebhook(handler, body, timestamp, airwallexWebhookTestSignature(timestamp, body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, want 400", body, response.Code)
		}
	}
}

func TestCompanyFundAirwallexWebhookHandler_AcknowledgesDuplicate(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	timestamp := "1783652400000"
	body := []byte(`{"id":"evt_duplicate","name":"deposit.created"}`)
	handler := newTestCompanyFundAirwallexWebhookHandler(t, newTestAirwallexWebhookVerifier(t, now), airwallexWebhookIngestorStub{
		ingest: func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			return companyfund.ProviderEventInsertResult{ID: 7, Inserted: false}, nil
		},
	})
	response := runCompanyFundAirwallexWebhook(handler, body, timestamp, airwallexWebhookTestSignature(timestamp, body))
	if response.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want 200", response.Code)
	}
}

func TestCompanyFundAirwallexWebhookHandler_WakesOnlyAfterDurableIngest(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	timestamp := "1783652400000"
	body := []byte(`{"id":"evt_wake","name":"deposit.created","account_id":"untrusted-account"}`)
	steps := make([]string, 0, 2)
	handler := newTestCompanyFundAirwallexWebhookHandlerWithWake(t, newTestAirwallexWebhookVerifier(t, now), airwallexWebhookIngestorStub{
		ingest: func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			steps = append(steps, "ingest")
			return companyfund.ProviderEventInsertResult{ID: 9, Inserted: true}, nil
		},
	}, func() {
		steps = append(steps, "wake")
	})

	response := runCompanyFundAirwallexWebhook(handler, body, timestamp, airwallexWebhookTestSignature(timestamp, body))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got, want := strings.Join(steps, ","), "ingest,wake"; got != want {
		t.Fatalf("side effect order = %q, want %q", got, want)
	}
}

func TestCompanyFundAirwallexWebhookHandler_FailsClosedWhenSingleAccountScopeIsNoLongerEligible(t *testing.T) {
	verifyCalls := 0
	ingestCalls := 0
	handler, err := NewCompanyFundAirwallexWebhookHandler(CompanyFundAirwallexWebhookHandlerConfig{
		Verifier: airwallexWebhookVerifierStub{verify: func(string, string, []byte) error {
			verifyCalls++
			return nil
		}},
		Ingestor: airwallexWebhookIngestorStub{ingest: func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			ingestCalls++
			return companyfund.ProviderEventInsertResult{}, nil
		}},
		Eligible:             func() bool { return false },
		ProviderEventVersion: "2026-05-29",
		KeyVersion:           "payload-v1",
		Retention:            time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := runCompanyFundAirwallexWebhook(handler, []byte(`{"id":"evt_scope","name":"deposit.created"}`), "missing", "missing")
	if response.Code != http.StatusServiceUnavailable || verifyCalls != 0 || ingestCalls != 0 {
		t.Fatalf("scope-gated response=%d verify=%d ingest=%d, want 503/0/0", response.Code, verifyCalls, ingestCalls)
	}
}
