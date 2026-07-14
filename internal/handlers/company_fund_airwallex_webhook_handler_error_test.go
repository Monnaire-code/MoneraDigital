package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"monera-digital/internal/companyfund"
)

func TestCompanyFundAirwallexWebhookHandler_RejectsOversizedBodyBeforeVerification(t *testing.T) {
	verifyCalled := false
	handler := newTestCompanyFundAirwallexWebhookHandler(t, airwallexWebhookVerifierStub{
		verify: func(string, string, []byte) error {
			verifyCalled = true
			return nil
		},
	}, airwallexWebhookIngestorStub{
		ingest: func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			t.Fatal("oversized body must not be ingested")
			return companyfund.ProviderEventInsertResult{}, nil
		},
	})
	body := bytes.Repeat([]byte("a"), companyfund.MaxOwnedProviderPayloadPlaintextBytes+1)
	response := runCompanyFundAirwallexWebhook(handler, body, "1783652400000", "ignored")
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.Code)
	}
	if verifyCalled {
		t.Fatal("oversized body must not be verified")
	}
}

func TestCompanyFundAirwallexWebhookHandler_AcceptsExactPlaintextCap(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	timestamp := "1783652400000"
	prefix := []byte(`{"id":"evt_cap","name":"deposit.created","data":"`)
	suffix := []byte(`"}`)
	body := append(append([]byte(nil), prefix...), bytes.Repeat([]byte("a"), companyfund.MaxOwnedProviderPayloadPlaintextBytes-len(prefix)-len(suffix))...)
	body = append(body, suffix...)
	if len(body) != companyfund.MaxOwnedProviderPayloadPlaintextBytes {
		t.Fatalf("test body length = %d, want cap %d", len(body), companyfund.MaxOwnedProviderPayloadPlaintextBytes)
	}
	ingested := false
	handler := newTestCompanyFundAirwallexWebhookHandler(t, newTestAirwallexWebhookVerifier(t, now), airwallexWebhookIngestorStub{
		ingest: func(_ context.Context, input companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			ingested = len(input.Body) == companyfund.MaxOwnedProviderPayloadPlaintextBytes
			return companyfund.ProviderEventInsertResult{ID: 1, Inserted: true}, nil
		},
	})
	response := runCompanyFundAirwallexWebhook(handler, body, timestamp, airwallexWebhookTestSignature(timestamp, body))
	if response.Code != http.StatusOK || !ingested {
		t.Fatalf("exact-cap status=%d ingested=%v, want 200 and true", response.Code, ingested)
	}
}

func TestCompanyFundAirwallexWebhookHandler_HidesIngestFailure(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	timestamp := "1783652400000"
	body := []byte(`{"id":"evt_failure","name":"deposit.created","data":{"secret":"raw-body"}}`)
	secretError := errors.New("database failure for webhook-secret raw-body")
	wakeCalls := 0
	handler := newTestCompanyFundAirwallexWebhookHandlerWithWake(t, newTestAirwallexWebhookVerifier(t, now), airwallexWebhookIngestorStub{
		ingest: func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			return companyfund.ProviderEventInsertResult{}, secretError
		},
	}, func() { wakeCalls++ })
	response := runCompanyFundAirwallexWebhook(handler, body, timestamp, airwallexWebhookTestSignature(timestamp, body))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "webhook-secret") || strings.Contains(response.Body.String(), "raw-body") || strings.Contains(response.Body.String(), secretError.Error()) {
		t.Fatalf("ingest failure response leaked sensitive detail: %q", response.Body.String())
	}
	if wakeCalls != 0 {
		t.Fatalf("wake calls = %d, want no wake after failed ingest", wakeCalls)
	}
}

func TestCompanyFundAirwallexWebhookHandler_RejectsMissingSignatureWithoutLeak(t *testing.T) {
	handler := newTestCompanyFundAirwallexWebhookHandler(t, airwallexWebhookVerifierStub{
		verify: func(string, string, []byte) error {
			t.Fatal("missing signature must not reach verifier")
			return nil
		},
	}, airwallexWebhookIngestorStub{
		ingest: func(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
			t.Fatal("missing signature must not be ingested")
			return companyfund.ProviderEventInsertResult{}, nil
		},
	})
	body := []byte(`{"id":"evt_1","name":"deposit.created","secret":"raw-body"}`)
	response := runCompanyFundAirwallexWebhook(handler, body, "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if strings.Contains(response.Body.String(), "raw-body") {
		t.Fatalf("missing-signature response leaked body: %q", response.Body.String())
	}
}
