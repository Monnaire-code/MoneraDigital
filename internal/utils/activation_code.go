package utils

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"

	"golang.org/x/crypto/bcrypt"
)

const (
	ActivationCodeLength = 6
	BcryptCost           = 5
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
	hash, err := bcrypt.GenerateFromPassword([]byte(code), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash activation code: %w", err)
	}
	return string(hash), nil
}

func VerifyActivationCode(code, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(code))
	return err == nil
}

func ConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
