package companyfund

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	// ErrPayloadCipherKeyUnavailable intentionally does not identify the
	// requested version or key material.
	ErrPayloadCipherKeyUnavailable = errors.New("payload cipher key is unavailable")
	// ErrPayloadCiphertextInvalid intentionally does not expose ciphertext or
	// authentication details to callers.
	ErrPayloadCiphertextInvalid = errors.New("payload ciphertext is invalid")
)

// PayloadCipher encrypts the binary raw payload owned by the company-fund
// feature. It is intentionally separate from the application's string-based
// encryption service so provider payloads never cross that interface.
type PayloadCipher interface {
	Encrypt(keyVersion string, plaintext []byte) ([]byte, error)
	Decrypt(keyVersion string, ciphertext []byte) ([]byte, error)
}

// AES256GCMPayloadCipher stores versioned AES-256 keys. Ciphertext is encoded
// as nonce || GCM-sealed bytes, without any text/base64 conversion.
type AES256GCMPayloadCipher struct {
	keys   map[string][]byte
	random io.Reader
}

func NewAES256GCMPayloadCipher(keys map[string][]byte) (*AES256GCMPayloadCipher, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one payload cipher key is required")
	}

	clonedKeys := make(map[string][]byte, len(keys))
	for version, key := range keys {
		if strings.TrimSpace(version) == "" {
			return nil, fmt.Errorf("payload cipher key version must be non-empty")
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("payload cipher key must be exactly 32 bytes")
		}
		clonedKeys[version] = append([]byte(nil), key...)
	}

	return &AES256GCMPayloadCipher{keys: clonedKeys, random: rand.Reader}, nil
}

func (c *AES256GCMPayloadCipher) Encrypt(keyVersion string, plaintext []byte) ([]byte, error) {
	gcm, err := c.gcmForKey(keyVersion)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return nil, fmt.Errorf("generate payload cipher nonce: %w", err)
	}
	return append(nonce, gcm.Seal(nil, nonce, plaintext, nil)...), nil
}

func (c *AES256GCMPayloadCipher) Decrypt(keyVersion string, ciphertext []byte) ([]byte, error) {
	gcm, err := c.gcmForKey(keyVersion)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrPayloadCiphertextInvalid
	}

	nonce := ciphertext[:gcm.NonceSize()]
	sealed := ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrPayloadCiphertextInvalid
	}
	return plaintext, nil
}

func (c *AES256GCMPayloadCipher) gcmForKey(keyVersion string) (cipher.AEAD, error) {
	if c == nil {
		return nil, ErrPayloadCipherKeyUnavailable
	}
	key, ok := c.keys[keyVersion]
	if !ok {
		return nil, ErrPayloadCipherKeyUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize payload cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize payload cipher mode: %w", err)
	}
	return gcm, nil
}
