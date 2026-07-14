package companyfund

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AirwallexWebhookVerifier verifies raw delivery bytes before any payload
// parsing. Its result is intentionally limited to safe sentinel errors.
type AirwallexWebhookVerifier struct {
	secret []byte
	maxAge time.Duration
	now    func() time.Time
}

func NewAirwallexWebhookVerifier(config AirwallexWebhookVerifierConfig) (*AirwallexWebhookVerifier, error) {
	if strings.TrimSpace(config.Secret) == "" {
		return nil, fmt.Errorf("airwallex webhook secret is required")
	}
	maxAge := config.MaxAge
	if maxAge == 0 {
		maxAge = defaultAirwallexWebhookTimestampTolerance
	}
	if maxAge <= 0 {
		return nil, fmt.Errorf("airwallex webhook timestamp tolerance must be positive")
	}
	now := config.Clock
	if now == nil {
		now = time.Now
	}
	return &AirwallexWebhookVerifier{secret: []byte(config.Secret), maxAge: maxAge, now: now}, nil
}

// Verify accepts the unmodified x-timestamp, x-signature, and raw request body.
// It compares the HMAC in constant time before evaluating timestamp tolerance.
func (v *AirwallexWebhookVerifier) Verify(timestamp, signature string, rawBody []byte) error {
	if v == nil || len(v.secret) == 0 || v.now == nil {
		return fmt.Errorf("airwallex webhook verifier is not configured")
	}
	expected := airwallexWebhookDigest(v.secret, timestamp, rawBody)
	received, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || !hmac.Equal(expected, received) {
		return ErrAirwallexWebhookInvalidSignature
	}
	milliseconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || milliseconds <= 0 {
		return ErrAirwallexWebhookInvalidTimestamp
	}
	receivedAt := time.UnixMilli(milliseconds).UTC()
	now := v.now().UTC()
	if receivedAt.Before(now) {
		if now.Sub(receivedAt) > v.maxAge {
			return ErrAirwallexWebhookTimestampOutsideWindow
		}
	} else if receivedAt.Sub(now) > v.maxAge {
		return ErrAirwallexWebhookTimestampOutsideWindow
	}
	return nil
}

func airwallexWebhookDigest(secret []byte, timestamp string, rawBody []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write(rawBody)
	return mac.Sum(nil)
}
