package companyfund

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrOwnedProviderPayloadUnavailable covers absent, purged, or inaccessible
	// owned payloads without revealing whether a particular raw body existed.
	ErrOwnedProviderPayloadUnavailable = errors.New("owned provider payload is unavailable")
	// ErrOwnedProviderPayloadIntegrity covers malformed ciphertext or any digest
	// mismatch without including raw data in the error.
	ErrOwnedProviderPayloadIntegrity = errors.New("owned provider payload integrity verification failed")
	// ErrOwnedProviderPayloadEncryptionFailed deliberately hides KMS/cipher
	// implementation details, key versions, and provider body context.
	ErrOwnedProviderPayloadEncryptionFailed = errors.New("owned provider payload encryption failed")
)

const (
	maxOwnedPayloadKeyVersionBytes = 64
	// AES-GCM ciphertext includes a nonce and authentication tag. Reserve a
	// conservative 64 bytes under the database/repository ciphertext cap so a
	// valid maximum plaintext never fails only after encryption has occurred.
	maxOwnedPayloadPlaintextSize = maxOwnedPayloadCiphertextSize - 64
	// MaxOwnedProviderPayloadPlaintextBytes is the largest raw body that can be
	// encrypted by the owned payload service without exceeding its durable
	// ciphertext bound. HTTP ingress must not accept a larger body.
	MaxOwnedProviderPayloadPlaintextBytes = maxOwnedPayloadPlaintextSize
)

type providerEventWriter interface {
	InsertProviderEvent(ctx context.Context, input ProviderEventInput) (ProviderEventInsertResult, error)
}

// OwnedProviderPayloadInput contains the provider metadata and raw binary body
// captured by a company-owned API integration. It cannot reference Safeheron's
// deposit-owned raw webhook record.
type OwnedProviderPayloadInput struct {
	Channel         Channel
	ProviderEventID string
	EventType       string
	// ProviderEventVersion carries the provider-side API or Webhook contract
	// pin when the caller has one. It remains optional for legacy provider
	// deliveries that do not expose a versioned contract.
	ProviderEventVersion string
	ProviderOrgKey       string
	ProviderAccountKey   string
	Body                 []byte
	KeyVersion           string
	Retention            time.Duration
	LegalHold            bool
}

// OwnedProviderPayloadService owns the raw API/Airwallex payload boundary. It
// encrypts before persistence and only decrypts an active, non-purged lease.
type OwnedProviderPayloadService struct {
	events providerEventWriter
	cipher PayloadCipher
	now    func() time.Time
}

func NewOwnedProviderPayloadService(events providerEventWriter, payloadCipher PayloadCipher, now func() time.Time) (*OwnedProviderPayloadService, error) {
	if events == nil {
		return nil, fmt.Errorf("owned provider payload event writer is required")
	}
	if payloadCipher == nil {
		return nil, fmt.Errorf("owned provider payload cipher is required")
	}
	if now == nil {
		return nil, fmt.Errorf("owned provider payload clock is required")
	}
	return &OwnedProviderPayloadService{events: events, cipher: payloadCipher, now: now}, nil
}

// Ingest encrypts an owned provider body and persists only its ciphertext plus
// a SHA-256 audit digest. It never logs or returns the raw body.
func (s *OwnedProviderPayloadService) Ingest(ctx context.Context, input OwnedProviderPayloadInput) (ProviderEventInsertResult, error) {
	if err := input.validate(); err != nil {
		return ProviderEventInsertResult{}, err
	}
	if s == nil || s.events == nil || s.cipher == nil {
		return ProviderEventInsertResult{}, fmt.Errorf("owned provider payload service is not configured")
	}

	ciphertext, err := s.cipher.Encrypt(input.KeyVersion, input.Body)
	if err != nil {
		return ProviderEventInsertResult{}, ErrOwnedProviderPayloadEncryptionFailed
	}
	if len(ciphertext) == 0 || len(ciphertext) > maxOwnedPayloadCiphertextSize {
		return ProviderEventInsertResult{}, fmt.Errorf("owned provider payload ciphertext must be non-empty and within the configured size limit")
	}

	digest := payloadSHA256Hex(input.Body)
	return s.events.InsertProviderEvent(ctx, ProviderEventInput{
		Channel:                       input.Channel,
		ProviderEventID:               input.ProviderEventID,
		EventType:                     input.EventType,
		ProviderEventVersion:          input.ProviderEventVersion,
		ProviderOrgKey:                input.ProviderOrgKey,
		ProviderAccountKey:            input.ProviderAccountKey,
		SourceKind:                    ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:           digest,
		OwnedPayloadCiphertext:        append([]byte(nil), ciphertext...),
		OwnedPayloadDigest:            digest,
		OwnedPayloadKeyVersion:        input.KeyVersion,
		OwnedPayloadRetentionDuration: input.Retention,
		OwnedPayloadLegalHold:         input.LegalHold,
	})
}

// DecryptLease returns the raw bytes only for an owned, retained lease whose
// ciphertext authenticates and whose plaintext matches both stored digests.
// Retention is enforced here, before cipher use, so delayed purge work cannot
// extend the window in which an expired non-hold payload is decryptable.
func (s *OwnedProviderPayloadService) DecryptLease(lease ProviderEventLease) ([]byte, error) {
	if s == nil || s.cipher == nil || s.now == nil ||
		lease.SourceKind != ProviderEventSourceOwnedEncryptedPayload ||
		lease.OwnedPayloadPurgedAt != nil ||
		len(lease.OwnedPayloadCiphertext) == 0 ||
		strings.TrimSpace(lease.OwnedPayloadKeyVersion) == "" ||
		lease.OwnedPayloadRetentionUntil == nil ||
		lease.OwnedPayloadRetentionUntil.IsZero() {
		return nil, ErrOwnedProviderPayloadUnavailable
	}
	now := s.now().UTC()
	if now.IsZero() || (!lease.OwnedPayloadLegalHold && !now.Before(lease.OwnedPayloadRetentionUntil.UTC())) {
		return nil, ErrOwnedProviderPayloadUnavailable
	}
	if !isLowerSHA256Hex(lease.SourcePayloadDigest) ||
		!isLowerSHA256Hex(lease.OwnedPayloadDigest) ||
		subtle.ConstantTimeCompare([]byte(lease.SourcePayloadDigest), []byte(lease.OwnedPayloadDigest)) != 1 {
		return nil, ErrOwnedProviderPayloadIntegrity
	}

	plaintext, err := s.cipher.Decrypt(lease.OwnedPayloadKeyVersion, lease.OwnedPayloadCiphertext)
	if err != nil {
		if errors.Is(err, ErrPayloadCipherKeyUnavailable) {
			return nil, ErrOwnedProviderPayloadUnavailable
		}
		return nil, ErrOwnedProviderPayloadIntegrity
	}
	actualDigest := payloadSHA256Hex(plaintext)
	if subtle.ConstantTimeCompare([]byte(actualDigest), []byte(lease.OwnedPayloadDigest)) != 1 {
		return nil, ErrOwnedProviderPayloadIntegrity
	}
	return plaintext, nil
}

func (input OwnedProviderPayloadInput) validate() error {
	if !input.Channel.Valid() {
		return fmt.Errorf("unsupported provider event channel %q", input.Channel)
	}
	if err := validateRequiredString("provider event ID", input.ProviderEventID, maxProviderEventIDBytes); err != nil {
		return err
	}
	if err := validateRequiredString("provider event type", input.EventType, maxProviderEventTypeBytes); err != nil {
		return err
	}
	if input.ProviderEventVersion != "" {
		if err := validateRequiredString("provider event version", input.ProviderEventVersion, maxProviderEventVersionBytes); err != nil {
			return err
		}
	}
	if len(input.Body) == 0 || len(input.Body) > maxOwnedPayloadPlaintextSize {
		return fmt.Errorf("owned provider payload plaintext must be non-empty and within the configured size limit")
	}
	if err := validateRequiredString("owned payload key version", input.KeyVersion, maxOwnedPayloadKeyVersionBytes); err != nil {
		return err
	}
	if input.Retention <= 0 {
		return fmt.Errorf("owned provider payload retention must be positive")
	}
	return nil
}

func payloadSHA256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
