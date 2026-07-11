package companyfund

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func TestAirwallexWebhookVerifier_UsesTimestampAndExactRawBody(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	verifier, err := NewAirwallexWebhookVerifier(AirwallexWebhookVerifierConfig{
		Secret: "webhook-secret",
		MaxAge: time.Minute,
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewAirwallexWebhookVerifier() error = %v", err)
	}
	timestamp := "1783652400000"
	body := []byte(`{ "id":"evt_1", "name":"deposit.created" }`)
	signature := airwallexTestWebhookSignature("webhook-secret", timestamp, body)
	if err := verifier.Verify(timestamp, signature, body); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := verifier.Verify(timestamp, signature, []byte(`{"id":"evt_1","name":"deposit.created"}`)); !errors.Is(err, ErrAirwallexWebhookInvalidSignature) {
		t.Fatalf("reformatted body error = %v, want invalid signature", err)
	}
}

func TestAirwallexWebhookVerifier_ClassifiesTimestampAndSignatureFailures(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	verifier, err := NewAirwallexWebhookVerifier(AirwallexWebhookVerifierConfig{Secret: "webhook-secret", MaxAge: time.Minute, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewAirwallexWebhookVerifier() error = %v", err)
	}
	body := []byte(`{"id":"evt_1"}`)
	expiredTimestamp := "1783652280000"
	expiredSignature := airwallexTestWebhookSignature("webhook-secret", expiredTimestamp, body)
	if err := verifier.Verify(expiredTimestamp, expiredSignature, body); !errors.Is(err, ErrAirwallexWebhookTimestampOutsideWindow) {
		t.Fatalf("expired timestamp error = %v", err)
	}
	futureTimestamp := "1783652520000"
	futureSignature := airwallexTestWebhookSignature("webhook-secret", futureTimestamp, body)
	if err := verifier.Verify(futureTimestamp, futureSignature, body); !errors.Is(err, ErrAirwallexWebhookTimestampOutsideWindow) {
		t.Fatalf("future timestamp error = %v", err)
	}
	malformedTimestamp := "not-milliseconds"
	malformedSignature := airwallexTestWebhookSignature("webhook-secret", malformedTimestamp, body)
	if err := verifier.Verify(malformedTimestamp, malformedSignature, body); !errors.Is(err, ErrAirwallexWebhookInvalidTimestamp) {
		t.Fatalf("malformed timestamp error = %v", err)
	}
	if err := verifier.Verify("1783652400000", "not-hex", body); !errors.Is(err, ErrAirwallexWebhookInvalidSignature) {
		t.Fatalf("malformed signature error = %v", err)
	}
}

func airwallexTestWebhookSignature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
