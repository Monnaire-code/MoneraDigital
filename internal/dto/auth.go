// internal/dto/auth.go
package dto

import "time"

// RegisterRequest DTO for user registration
type RegisterRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginRequest DTO for user login
type LoginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse DTO for login response
type LoginResponse struct {
	AccessToken        string    `json:"accessToken,omitempty"`
	RefreshToken       string    `json:"refreshToken,omitempty"`
	TokenType          string    `json:"tokenType,omitempty"`
	ExpiresIn          int       `json:"expiresIn,omitempty"`
	ExpiresAt          time.Time `json:"expiresAt,omitempty"`
	User               *UserInfo `json:"user,omitempty"`
	Token              string    `json:"token,omitempty"`
	Requires2FA        bool      `json:"requires2FA,omitempty"`
	RequiresActivation bool      `json:"requiresActivation,omitempty"`
	UserID             int       `json:"userId,omitempty"`
}

// UserInfo DTO for user information
type UserInfo struct {
	ID               int    `json:"id"`
	Email            string `json:"email"`
	Status           string `json:"status"`
	TwoFactorEnabled bool   `json:"twoFactorEnabled"`
	Phone            string `json:"phone,omitempty"`
	Telegram         string `json:"telegram,omitempty"`
	Wechat           string `json:"wechat,omitempty"`
}

// RefreshTokenRequest DTO for token refresh
type RefreshTokenRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

// RefreshTokenResponse DTO for token refresh response
type RefreshTokenResponse struct {
	AccessToken string    `json:"accessToken"`
	TokenType   string    `json:"tokenType"`
	ExpiresIn   int       `json:"expiresIn"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// LogoutRequest DTO for logout
type LogoutRequest struct {
	Token string `json:"token" binding:"required"`
}

// Setup2FARequest DTO for 2FA setup
type Setup2FARequest struct {
	Email string `json:"email" binding:"required,email"`
}

// Setup2FAResponse DTO for 2FA setup response
type Setup2FAResponse struct {
	Secret string `json:"secret"`
	QRCode string `json:"qrCode"`
}

// Enable2FARequest DTO for enabling 2FA
type Enable2FARequest struct {
	Token string `json:"token" binding:"required,len=6"`
}

// Verify2FALoginRequest DTO for 2FA verification during login
type Verify2FALoginRequest struct {
	Email string `json:"email" binding:"required,email"`
	Token string `json:"token" binding:"required,len=6"`
}
