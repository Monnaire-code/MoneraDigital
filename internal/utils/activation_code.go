package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
)

const (
	ActivationCodeLength = 6
	ActivationCodeMax    = 999999
)

var encryptionKey []byte

func SetActivationCodeKey(key []byte) {
	encryptionKey = key
}

func GenerateActivationCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(ActivationCodeMax+1))
	if err != nil {
		return "", fmt.Errorf("failed to generate activation code: %w", err)
	}
	code := n.Int64()
	return fmt.Sprintf("%06d", code), nil
}

func HashActivationCode(code string) (string, error) {
	return EncryptActivationCode(code)
}

func VerifyActivationCode(code, hash string) bool {
	decryptedCode, err := DecryptActivationCode(hash)
	if err != nil {
		return false
	}
	return ConstantTimeCompare(code, decryptedCode)
}

func ConstantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func EncryptActivationCode(code string) (string, error) {
	if len(encryptionKey) == 0 {
		return "", fmt.Errorf("encryption key not set")
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(code), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func DecryptActivationCode(encryptedCode string) (string, error) {
	if len(encryptionKey) == 0 {
		return "", fmt.Errorf("encryption key not set")
	}

	data, err := base64.StdEncoding.DecodeString(encryptedCode)
	if err != nil {
		return "", fmt.Errorf("failed to decode activation code: %w", err)
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}

	return string(plaintext), nil
}
