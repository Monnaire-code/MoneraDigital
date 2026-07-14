package companyfund

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOwnedProviderPayloadService_IngestsEncryptedPayloadWithDigestAndRetention(t *testing.T) {
	writer := &providerEventWriterStub{result: ProviderEventInsertResult{ID: 71, Inserted: true}}
	payloadCipher := newTestPayloadCipher(t)
	service, err := NewOwnedProviderPayloadService(writer, payloadCipher, func() time.Time { return time.Time{} })
	if err != nil {
		t.Fatalf("NewOwnedProviderPayloadService() error = %v", err)
	}

	body := []byte(`{"payment_id":"pay-7","amount":"12.34"}`)
	result, err := service.Ingest(context.Background(), OwnedProviderPayloadInput{
		Channel:              ChannelAirwallex,
		ProviderEventID:      "payment-event-7",
		EventType:            "PAYMENT",
		ProviderEventVersion: "2026-05-29",
		ProviderOrgKey:       "org-a",
		ProviderAccountKey:   "account-a",
		Body:                 body,
		KeyVersion:           "v1",
		Retention:            2 * time.Hour,
		LegalHold:            true,
	})
	if err != nil || result != (ProviderEventInsertResult{ID: 71, Inserted: true}) {
		t.Fatalf("Ingest() = %#v, %v", result, err)
	}
	if len(writer.inputs) != 1 {
		t.Fatalf("InsertProviderEvent calls = %d, want 1", len(writer.inputs))
	}

	stored := writer.inputs[0]
	wantDigest := sha256Hex(body)
	if stored.SourceKind != ProviderEventSourceOwnedEncryptedPayload ||
		stored.ProviderEventVersion != "2026-05-29" ||
		stored.SourcePayloadDigest != wantDigest ||
		stored.OwnedPayloadDigest != wantDigest ||
		stored.OwnedPayloadKeyVersion != "v1" ||
		stored.OwnedPayloadRetentionDuration != 2*time.Hour ||
		!stored.OwnedPayloadLegalHold {
		t.Fatalf("stored provider event = %#v", stored)
	}
	if len(stored.OwnedPayloadCiphertext) == 0 || bytes.Equal(stored.OwnedPayloadCiphertext, body) {
		t.Fatal("stored event must contain encrypted rather than raw payload bytes")
	}

	decrypted, err := payloadCipher.Decrypt(stored.OwnedPayloadKeyVersion, stored.OwnedPayloadCiphertext)
	if err != nil || !bytes.Equal(decrypted, body) {
		t.Fatalf("stored ciphertext decrypt = %q, %v", decrypted, err)
	}
}

func TestOwnedProviderPayloadService_RejectsInvalidOrOversizedPayloadBeforeStorage(t *testing.T) {
	validInput := func() OwnedProviderPayloadInput {
		return OwnedProviderPayloadInput{
			Channel:         ChannelAirwallex,
			ProviderEventID: "payment-event-8",
			EventType:       "PAYMENT",
			Body:            []byte("body"),
			KeyVersion:      "v1",
			Retention:       time.Hour,
		}
	}

	for _, testCase := range []struct {
		name   string
		mutate func(*OwnedProviderPayloadInput)
	}{
		{"empty body", func(input *OwnedProviderPayloadInput) { input.Body = nil }},
		{"oversized plaintext body", func(input *OwnedProviderPayloadInput) { input.Body = make([]byte, maxOwnedPayloadPlaintextSize+1) }},
		{"blank key version", func(input *OwnedProviderPayloadInput) { input.KeyVersion = "  " }},
		{"zero retention", func(input *OwnedProviderPayloadInput) { input.Retention = 0 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			writer := &providerEventWriterStub{}
			service, err := NewOwnedProviderPayloadService(writer, newTestPayloadCipher(t), time.Now)
			if err != nil {
				t.Fatalf("NewOwnedProviderPayloadService() error = %v", err)
			}
			input := validInput()
			testCase.mutate(&input)
			if _, err := service.Ingest(context.Background(), input); err == nil {
				t.Fatal("Ingest() unexpectedly accepted invalid payload input")
			}
			if len(writer.inputs) != 0 {
				t.Fatal("invalid payload must not be handed to storage")
			}
		})
	}

	writer := &providerEventWriterStub{}
	service, err := NewOwnedProviderPayloadService(writer, oversizedPayloadCipher{}, time.Now)
	if err != nil {
		t.Fatalf("NewOwnedProviderPayloadService() error = %v", err)
	}
	if _, err := service.Ingest(context.Background(), validInput()); err == nil {
		t.Fatal("Ingest() must reject ciphertext larger than the repository bound")
	}
	if len(writer.inputs) != 0 {
		t.Fatal("oversized ciphertext must not be handed to storage")
	}
}

func TestOwnedProviderPayloadService_EnforcesPlaintextBoundaryBeforeEncryption(t *testing.T) {
	validInput := func(body []byte) OwnedProviderPayloadInput {
		return OwnedProviderPayloadInput{
			Channel:         ChannelAirwallex,
			ProviderEventID: "payload-boundary",
			EventType:       "PAYMENT",
			Body:            body,
			KeyVersion:      "v1",
			Retention:       time.Hour,
		}
	}

	writer := &providerEventWriterStub{}
	service, err := NewOwnedProviderPayloadService(writer, newTestPayloadCipher(t), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(context.Background(), validInput(bytes.Repeat([]byte{0x01}, maxOwnedPayloadPlaintextSize))); err != nil {
		t.Fatalf("max plaintext body must fit the standard cipher bound: %v", err)
	}
	if len(writer.inputs) != 1 || len(writer.inputs[0].OwnedPayloadCiphertext) > maxOwnedPayloadCiphertextSize {
		t.Fatalf("max plaintext persisted unexpected ciphertext: %#v", writer.inputs)
	}

	countingCipher := &encryptCountingCipher{}
	writer = &providerEventWriterStub{}
	service, err = NewOwnedProviderPayloadService(writer, countingCipher, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(context.Background(), validInput(bytes.Repeat([]byte{0x01}, maxOwnedPayloadPlaintextSize+1))); err == nil {
		t.Fatal("max+1 plaintext body must be rejected before encryption")
	}
	if countingCipher.encryptCalls != 0 || len(writer.inputs) != 0 {
		t.Fatalf("oversized plaintext must not reach cipher/storage: calls=%d inputs=%d", countingCipher.encryptCalls, len(writer.inputs))
	}
}

func TestOwnedProviderPayloadService_HidesCipherEncryptionFailure(t *testing.T) {
	secretFailure := errors.New("kms key-version-v1 for customer-secret-body failed")
	writer := &providerEventWriterStub{}
	service, err := NewOwnedProviderPayloadService(writer, encryptionFailingCipher{err: secretFailure}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Ingest(context.Background(), OwnedProviderPayloadInput{
		Channel:         ChannelAirwallex,
		ProviderEventID: "encrypt-failure",
		EventType:       "PAYMENT",
		Body:            []byte(`{"sensitive":"body"}`),
		KeyVersion:      "v1",
		Retention:       time.Hour,
	})
	if !errors.Is(err, ErrOwnedProviderPayloadEncryptionFailed) {
		t.Fatalf("Ingest() error = %v, want generic encryption sentinel", err)
	}
	if strings.Contains(err.Error(), secretFailure.Error()) || strings.Contains(err.Error(), "v1") || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("Ingest() leaked cipher failure details: %q", err)
	}
	if len(writer.inputs) != 0 {
		t.Fatal("encryption failure must not reach storage")
	}
}

func TestOwnedProviderPayloadService_DecryptLeaseVerifiesAvailabilityAndDigestWithoutLeakingPayload(t *testing.T) {
	body := []byte(`{"secret":"do-not-return-in-error"}`)
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	retentionUntil := now.Add(time.Hour)
	payloadCipher := newTestPayloadCipher(t)
	ciphertext, err := payloadCipher.Encrypt("v1", body)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	service, err := NewOwnedProviderPayloadService(&providerEventWriterStub{}, payloadCipher, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewOwnedProviderPayloadService() error = %v", err)
	}

	lease := ProviderEventLease{
		SourceKind:                 ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:        sha256Hex(body),
		OwnedPayloadDigest:         sha256Hex(body),
		OwnedPayloadCiphertext:     ciphertext,
		OwnedPayloadKeyVersion:     "v1",
		OwnedPayloadRetentionUntil: &retentionUntil,
	}
	decrypted, err := service.DecryptLease(lease)
	if err != nil || !bytes.Equal(decrypted, body) {
		t.Fatalf("DecryptLease() = %q, %v", decrypted, err)
	}

	purgedAt := now
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0x01
	for _, testCase := range []struct {
		name    string
		lease   ProviderEventLease
		wantErr error
	}{
		{"wrong source", ProviderEventLease{SourceKind: ProviderEventSourceExistingSafeheronWebhookRef}, ErrOwnedProviderPayloadUnavailable},
		{"purged", ProviderEventLease{SourceKind: ProviderEventSourceOwnedEncryptedPayload, OwnedPayloadPurgedAt: &purgedAt}, ErrOwnedProviderPayloadUnavailable},
		{"missing ciphertext", ProviderEventLease{SourceKind: ProviderEventSourceOwnedEncryptedPayload, OwnedPayloadKeyVersion: "v1"}, ErrOwnedProviderPayloadUnavailable},
		{"tampered ciphertext", ProviderEventLease{SourceKind: ProviderEventSourceOwnedEncryptedPayload, SourcePayloadDigest: sha256Hex(body), OwnedPayloadDigest: sha256Hex(body), OwnedPayloadKeyVersion: "v1", OwnedPayloadCiphertext: tampered, OwnedPayloadRetentionUntil: &retentionUntil}, ErrOwnedProviderPayloadIntegrity},
		{"digest mismatch", ProviderEventLease{SourceKind: ProviderEventSourceOwnedEncryptedPayload, SourcePayloadDigest: strings.Repeat("a", 64), OwnedPayloadDigest: strings.Repeat("a", 64), OwnedPayloadKeyVersion: "v1", OwnedPayloadCiphertext: ciphertext, OwnedPayloadRetentionUntil: &retentionUntil}, ErrOwnedProviderPayloadIntegrity},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := service.DecryptLease(testCase.lease)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("DecryptLease() error = %v, want %v", err, testCase.wantErr)
			}
			if strings.Contains(err.Error(), string(body)) || bytes.Contains([]byte(err.Error()), ciphertext) {
				t.Fatalf("DecryptLease() leaked raw payload or ciphertext in error %q", err)
			}
		})
	}
}

func TestOwnedProviderPayloadService_DecryptLeaseEnforcesRetentionBeforeCipher(t *testing.T) {
	body := []byte(`{"secret":"retention-boundary"}`)
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	expiredAt := now
	trackingCipher := &decryptCountingCipher{}
	service, err := NewOwnedProviderPayloadService(&providerEventWriterStub{}, trackingCipher, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	lease := ProviderEventLease{
		SourceKind:                 ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:        sha256Hex(body),
		OwnedPayloadDigest:         sha256Hex(body),
		OwnedPayloadCiphertext:     []byte("ciphertext-must-not-be-decrypted"),
		OwnedPayloadKeyVersion:     "v1",
		OwnedPayloadRetentionUntil: &expiredAt,
	}
	if _, err := service.DecryptLease(lease); !errors.Is(err, ErrOwnedProviderPayloadUnavailable) {
		t.Fatalf("expired non-hold DecryptLease() error = %v, want unavailable", err)
	} else if strings.Contains(err.Error(), string(body)) {
		t.Fatalf("retention error leaked body: %q", err)
	}
	if trackingCipher.decryptCalls != 0 {
		t.Fatalf("expired non-hold lease must not call decrypt, calls=%d", trackingCipher.decryptCalls)
	}

	lease.OwnedPayloadRetentionUntil = nil
	if _, err := service.DecryptLease(lease); !errors.Is(err, ErrOwnedProviderPayloadUnavailable) {
		t.Fatalf("missing retention DecryptLease() error = %v, want unavailable", err)
	}
	if trackingCipher.decryptCalls != 0 {
		t.Fatalf("missing retention lease must not call decrypt, calls=%d", trackingCipher.decryptCalls)
	}
}

func TestOwnedProviderPayloadService_DecryptLeaseAllowsExpiredLegalHold(t *testing.T) {
	body := []byte(`{"secret":"legal-hold"}`)
	now := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Hour)
	payloadCipher := newTestPayloadCipher(t)
	ciphertext, err := payloadCipher.Encrypt("v1", body)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewOwnedProviderPayloadService(&providerEventWriterStub{}, payloadCipher, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := service.DecryptLease(ProviderEventLease{
		SourceKind:                 ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:        sha256Hex(body),
		OwnedPayloadDigest:         sha256Hex(body),
		OwnedPayloadCiphertext:     ciphertext,
		OwnedPayloadKeyVersion:     "v1",
		OwnedPayloadRetentionUntil: &expiredAt,
		OwnedPayloadLegalHold:      true,
	})
	if err != nil || !bytes.Equal(decrypted, body) {
		t.Fatalf("expired legal-hold DecryptLease() = %q, %v", decrypted, err)
	}
}

type providerEventWriterStub struct {
	inputs []ProviderEventInput
	result ProviderEventInsertResult
	err    error
}

func (s *providerEventWriterStub) InsertProviderEvent(_ context.Context, input ProviderEventInput) (ProviderEventInsertResult, error) {
	s.inputs = append(s.inputs, input)
	return s.result, s.err
}

type oversizedPayloadCipher struct{}

func (oversizedPayloadCipher) Encrypt(string, []byte) ([]byte, error) {
	return make([]byte, maxOwnedPayloadCiphertextSize+1), nil
}

func (oversizedPayloadCipher) Decrypt(string, []byte) ([]byte, error) { return nil, nil }

type encryptCountingCipher struct {
	encryptCalls int
}

func (c *encryptCountingCipher) Encrypt(string, []byte) ([]byte, error) {
	c.encryptCalls++
	return []byte("ciphertext"), nil
}

func (c *encryptCountingCipher) Decrypt(string, []byte) ([]byte, error) { return nil, nil }

type encryptionFailingCipher struct {
	err error
}

func (c encryptionFailingCipher) Encrypt(string, []byte) ([]byte, error) { return nil, c.err }

func (c encryptionFailingCipher) Decrypt(string, []byte) ([]byte, error) { return nil, c.err }

type decryptCountingCipher struct {
	decryptCalls int
}

func (c *decryptCountingCipher) Encrypt(string, []byte) ([]byte, error) {
	return []byte("ciphertext"), nil
}

func (c *decryptCountingCipher) Decrypt(string, []byte) ([]byte, error) {
	c.decryptCalls++
	return []byte("unexpected"), nil
}

func newTestPayloadCipher(t *testing.T) *AES256GCMPayloadCipher {
	t.Helper()
	payloadCipher, err := NewAES256GCMPayloadCipher(map[string][]byte{
		"v1": bytes.Repeat([]byte{0x11}, 32),
	})
	if err != nil {
		t.Fatalf("NewAES256GCMPayloadCipher() error = %v", err)
	}
	return payloadCipher
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
