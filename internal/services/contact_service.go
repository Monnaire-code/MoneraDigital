package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"monera-digital/internal/dto"
	"monera-digital/internal/models"
)

var (
	ErrInvalidPhoneFormat   = errors.New("invalid phone format")
	ErrUserNotEmailVerified = errors.New("user has not verified email yet")
	ErrUserAlreadySubmitted = errors.New("user has already submitted contact info")
)

type ContactService struct {
	db *sql.DB
}

func NewContactService(db *sql.DB) *ContactService {
	return &ContactService{db: db}
}

// phoneRegex validates international phone numbers
var phoneRegex = regexp.MustCompile(`^\+?[1-9]\d{6,14}$`)

// ValidateContactInfo validates the contact information format
func (s *ContactService) ValidateContactInfo(req dto.SubmitContactInfoRequest) error {
	// Phone is optional but if provided, must be valid format
	if req.Phone != "" {
		if !phoneRegex.MatchString(req.Phone) {
			return ErrInvalidPhoneFormat
		}
	}

	// Telegram and Wechat are optional and not validated
	return nil
}

// SubmitContactInfo submits contact information and updates user status to INFO_SUBMITTED
func (s *ContactService) SubmitContactInfo(ctx context.Context, userID int, req dto.SubmitContactInfoRequest) (*dto.SubmitContactInfoResponse, error) {
	// 1. Get user and verify status
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM users WHERE id = $1`, userID).Scan(&status)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// 2. Verify user status is EMAIL_VERIFIED
	if status != string(models.UserStatusEmailVerified) {
		if status == string(models.UserStatusPending) {
			return nil, ErrUserNotEmailVerified
		}
		if status == string(models.UserStatusInfoSubmitted) || status == string(models.UserStatusActive) {
			return nil, ErrUserAlreadySubmitted
		}
		return nil, ErrUserNotEmailVerified
	}

	// 3. Validate contact info format
	if err := s.ValidateContactInfo(req); err != nil {
		return nil, err
	}

	// 4. Update user with contact info and set status to INFO_SUBMITTED
	now := time.Now()
	_, err = s.db.ExecContext(ctx, `
		UPDATE users 
		SET phone = $1, telegram = $2, wechat = $3, 
		    status = 'INFO_SUBMITTED',
		    contact_submitted_at = $4,
		    updated_at = $4
		WHERE id = $5`,
		nullString(req.Phone),
		nullString(req.Telegram),
		nullString(req.Wechat),
		now,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update contact info: %w", err)
	}

	reviewDate := now.AddDate(0, 0, 3)
	reviewDateStr := reviewDate.Format("2006年1月2日")

	return &dto.SubmitContactInfoResponse{
		Success:     true,
		Message:     "Contact information submitted successfully. Your account is under review.",
		Status:      string(models.UserStatusInfoSubmitted),
		RedirectURL: "/pending-approval",
		ReviewDays:  3,
		ReviewDate:  reviewDateStr,
	}, nil
}

// GetContactInfo retrieves contact information for a user
func (s *ContactService) GetContactInfo(ctx context.Context, userID int) (*dto.UserInfoResponse, error) {
	var user dto.UserInfoResponse
	var phone, telegram, wechat sql.NullString
	var contactSubmittedAt sql.NullTime
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, status, phone, telegram, wechat, contact_submitted_at, created_at
		FROM users WHERE id = $1`, userID).Scan(
		&user.ID, &user.Email, &user.Status,
		&phone, &telegram, &wechat, &contactSubmittedAt, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if phone.Valid {
		user.Phone = phone.String
	}
	if telegram.Valid {
		user.Telegram = telegram.String
	}
	if wechat.Valid {
		user.Wechat = wechat.String
	}
	if contactSubmittedAt.Valid {
		user.ContactSubmittedAt = contactSubmittedAt.Time
	}
	user.CreatedAt = createdAt

	return &user, nil
}

// nullString returns sql.NullString for empty strings
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
