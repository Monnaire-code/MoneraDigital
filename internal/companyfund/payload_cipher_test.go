package companyfund

import (
	"bytes"
	"errors"
	"testing"
)

func TestAES256GCMPayloadCipher_RoundTripsExactBytes(t *testing.T) {
	payloadCipher, err := NewAES256GCMPayloadCipher(map[string][]byte{
		"v1": bytes.Repeat([]byte{0x42}, 32),
	})
	if err != nil {
		t.Fatalf("NewAES256GCMPayloadCipher() error = %v", err)
	}

	plaintext := []byte{0x00, '{', '"', 'i', 'd', '"', ':', '"', 'e', 'v', 't', '-', '1', '"', '}'}
	ciphertext, err := payloadCipher.Encrypt("v1", plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if len(ciphertext) <= len(plaintext) || bytes.Equal(ciphertext, plaintext) {
		t.Fatalf("Encrypt() returned invalid ciphertext length/content")
	}

	decrypted, err := payloadCipher.Decrypt("v1", ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("Decrypt() = %x, want exact %x", decrypted, plaintext)
	}
}

func TestAES256GCMPayloadCipher_RejectsUnknownKeyAndTampering(t *testing.T) {
	payloadCipher, err := NewAES256GCMPayloadCipher(map[string][]byte{
		"v1": bytes.Repeat([]byte{0x24}, 32),
	})
	if err != nil {
		t.Fatalf("NewAES256GCMPayloadCipher() error = %v", err)
	}
	if _, err := payloadCipher.Encrypt("missing", []byte("payload")); !errors.Is(err, ErrPayloadCipherKeyUnavailable) {
		t.Fatalf("Encrypt(unknown key) error = %v, want ErrPayloadCipherKeyUnavailable", err)
	}

	ciphertext, err := payloadCipher.Encrypt("v1", []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	ciphertext[len(ciphertext)-1] ^= 0x01
	if _, err := payloadCipher.Decrypt("v1", ciphertext); !errors.Is(err, ErrPayloadCiphertextInvalid) {
		t.Fatalf("Decrypt(tampered) error = %v, want ErrPayloadCiphertextInvalid", err)
	}
}
