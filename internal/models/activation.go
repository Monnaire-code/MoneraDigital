package models

import "time"

// SendActivationRequest represents request to send activation code
type SendActivationRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// VerifyActivationRequest represents request to verify activation code
type VerifyActivationRequest struct {
	Email string `json:"email" binding:"required,email"`
	Code  string `json:"code" binding:"required,len=6"`
}

// ActivationInfo represents activation code information stored in DB
type ActivationInfo struct {
	Code      string    `json:"-" db:"activation_code"`
	Attempts  int       `json:"-" db:"activation_attempts"`
	ExpiresAt time.Time `json:"-" db:"activation_expires_at"`
}

// ActivationResponse represents the API response for activation
type ActivationResponse struct {
	Success            bool   `json:"success"`
	Message            string `json:"message,omitempty"`
	RequiresActivation bool   `json:"requiresActivation,omitempty"`
	RemainingAttempts  int    `json:"remainingAttempts,omitempty"`
}

// LoginResponseWithActivation extends LoginResponse with activation info
type LoginResponseWithActivation struct {
	User               *User  `json:"user,omitempty"`
	Token              string `json:"token,omitempty"`
	AccessToken        string `json:"accessToken,omitempty"`
	RefreshToken       string `json:"refreshToken,omitempty"`
	TokenType          string `json:"tokenType,omitempty"`
	ExpiresIn          int    `json:"expiresIn,omitempty"`
	ExpiresAt          string `json:"expiresAt,omitempty"`
	Requires2FA        bool   `json:"requires2FA,omitempty"`
	RequiresActivation bool   `json:"requiresActivation,omitempty"`
	UserID             int    `json:"userId,omitempty"`
}
