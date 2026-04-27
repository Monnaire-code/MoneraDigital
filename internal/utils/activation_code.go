package utils

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
)

const (
	ActivationCodeLength = 6
	ActivationCodeMax    = 999999
)

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
	return base64.StdEncoding.EncodeToString([]byte(code)), nil
}

func DecryptActivationCode(encryptedCode string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encryptedCode)
	if err != nil {
		return "", fmt.Errorf("failed to decode activation code: %w", err)
	}
	return string(data), nil
}
