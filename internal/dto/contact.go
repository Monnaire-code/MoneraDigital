package dto

import "time"

// SubmitContactInfoRequest represents request to submit contact information
type SubmitContactInfoRequest struct {
	Phone    string `json:"phone,omitempty"`
	Telegram string `json:"telegram,omitempty"`
	Wechat   string `json:"wechat,omitempty"`
}

// SubmitContactInfoResponse represents response after submitting contact info
type SubmitContactInfoResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
	Status      string `json:"status,omitempty"`
	RedirectURL string `json:"redirectUrl,omitempty"`
	ReviewDays  int    `json:"reviewDays,omitempty"`
	ReviewDate  string `json:"reviewDate,omitempty"`
}

// UserInfoResponse represents user information response
type UserInfoResponse struct {
	ID                 int       `json:"id"`
	Email              string    `json:"email"`
	Status             string    `json:"status"`
	Phone              string    `json:"phone,omitempty"`
	Telegram           string    `json:"telegram,omitempty"`
	Wechat             string    `json:"wechat,omitempty"`
	ContactSubmittedAt time.Time `json:"contactSubmittedAt,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
}
