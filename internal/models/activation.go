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
	Status             string `json:"status,omitempty"` // New status after verification (EMAIL_VERIFIED)
	UserID             int    `json:"userId,omitempty"`
}

// EmailVerifiedResponse represents the response after email verification
type EmailVerifiedResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
	Status      string `json:"status"`      // "EMAIL_VERIFIED"
	RedirectURL string `json:"redirectUrl"` // "/contact-info"
	UserID      int    `json:"userId"`
	Token       string `json:"token,omitempty"`
	AccessToken string `json:"accessToken,omitempty"`
	ExpiresIn   int    `json:"expiresIn,omitempty"`
}

// ContactInfoRequest represents the request to submit contact information
type ContactInfoRequest struct {
	Phone    string `json:"phone" binding:"required"`
	Email    string `json:"email,omitempty"` // Read-only, already verified
	Telegram string `json:"telegram,omitempty"`
	Wechat   string `json:"wechat,omitempty"`
}

// ContactInfoResponse represents the response after submitting contact info
type ContactInfoResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
	Status      string `json:"status"`      // "INFO_SUBMITTED"
	RedirectURL string `json:"redirectUrl"` // "/pending-approval"
	ReviewDays  int    `json:"reviewDays"`  // Expected review time in business days
	ReviewDate  string `json:"reviewDate"`  // Expected review completion date
}
