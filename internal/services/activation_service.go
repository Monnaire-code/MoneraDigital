package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"monera-digital/internal/models"
	"monera-digital/internal/utils"
)

const (
	ActivationCodeExpiryMinutes = 5
	MaxActivationAttempts       = 3 // Max 3 attempts before 1 minute lockout
	ActivationLockoutMinutes    = 1 // Lockout duration after max attempts
)

var (
	ErrUserNotFound         = errors.New("user not found")
	ErrCodeExpired          = errors.New("activation code expired")
	ErrCodeInvalid          = errors.New("invalid activation code")
	ErrMaxAttemptsExceeded  = errors.New("maximum activation attempts exceeded")
	ErrUserAlreadyActivated = errors.New("user already activated")
	ErrUserLockedOut        = errors.New("user is temporarily locked out due to too many failed attempts")
)

type ActivationService struct {
	db           *sql.DB
	rateLimiter  *RateLimiter
	emailService *EmailService
	jwtSecret    string
}

func NewActivationService(db *sql.DB, rateLimiter *RateLimiter, emailService *EmailService, jwtSecret string) *ActivationService {
	return &ActivationService{
		db:           db,
		rateLimiter:  rateLimiter,
		emailService: emailService,
		jwtSecret:    jwtSecret,
	}
}

type SendActivationResult struct {
	Success    bool
	Message    string
	RetryAfter int
}

func (s *ActivationService) SendActivationCode(ctx context.Context, email string, clientIP string) (*SendActivationResult, error) {
	emailRateLimit, err := s.rateLimiter.CheckAndIncrement(ctx, "email", email, "send_activation", RateLimitMaxAttemptsEmail)
	if err != nil {
		return nil, fmt.Errorf("failed to check email rate limit: %w", err)
	}

	if !emailRateLimit.Allowed {
		return &SendActivationResult{
			Success:    false,
			Message:    "too many requests",
			RetryAfter: emailRateLimit.RetryAfter,
		}, nil
	}

	ipRateLimit, err := s.rateLimiter.CheckAndIncrement(ctx, "ip", clientIP, "send_activation", RateLimitMaxAttemptsIP)
	if err != nil {
		return nil, fmt.Errorf("failed to check IP rate limit: %w", err)
	}

	if !ipRateLimit.Allowed {
		return &SendActivationResult{
			Success:    false,
			Message:    "too many requests",
			RetryAfter: ipRateLimit.RetryAfter,
		}, nil
	}

	user, err := s.getUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return &SendActivationResult{
				Success: true,
				Message: "if user exists, activation code will be sent",
			}, nil
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if user.Status == models.UserStatusActive {
		return &SendActivationResult{
			Success: true,
			Message: "user already activated",
		}, nil
	}

	code, err := utils.GenerateActivationCode()
	if err != nil {
		return nil, fmt.Errorf("failed to generate activation code: %w", err)
	}

	hashedCode, err := utils.HashActivationCode(code)
	if err != nil {
		return nil, fmt.Errorf("failed to hash activation code: %w", err)
	}

	expiresAt := time.Now().Add(ActivationCodeExpiryMinutes * time.Minute)

	err = s.updateActivationCode(ctx, user.ID, hashedCode, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("failed to update activation code: %w", err)
	}

	if err := s.emailService.SendActivationEmail(ctx, email, code); err != nil {
		fmt.Printf("[ActivationService] Failed to send activation email: %v\n", err)
	}

	return &SendActivationResult{
		Success: true,
		Message: "activation code sent",
	}, nil
}

func (s *ActivationService) VerifyActivationCode(ctx context.Context, email string, code string) (*models.EmailVerifiedResponse, error) {
	user, err := s.getUserByEmail(ctx, email)
	if err != nil {
		return nil, ErrUserNotFound
	}

	if user.Status == models.UserStatusActive {
		return nil, ErrUserAlreadyActivated
	}

	// Check if user is locked out (activation_expires_at set to future time means locked)
	if user.ActivationExpiresAt.Valid && time.Now().Before(user.ActivationExpiresAt.Time) {
		// Check if this is a lockout (attempts >= MaxActivationAttempts)
		if user.ActivationAttempts >= MaxActivationAttempts {
			retryAfter := int(time.Until(user.ActivationExpiresAt.Time).Seconds())
			if retryAfter < 0 {
				retryAfter = 0
			}
			return nil, ErrUserLockedOut
		}
	}

	// Check if activation code has expired (but not locked out)
	if !user.ActivationExpiresAt.Valid {
		fmt.Printf("[ActivationService] VerifyActivation: no expiration time for user %d\n", user.ID)
		return nil, ErrCodeExpired
	}

	if time.Now().After(user.ActivationExpiresAt.Time) {
		fmt.Printf("[ActivationService] VerifyActivation: code expired for user %d (expires: %v)\n", user.ID, user.ActivationExpiresAt.Time)
		return nil, ErrCodeExpired
	}

	// Check attempts (only if not locked out)
	if user.ActivationAttempts >= MaxActivationAttempts {
		return nil, ErrMaxAttemptsExceeded
	}

	if !user.ActivationCode.Valid {
		return nil, ErrCodeInvalid
	}

	if !utils.VerifyActivationCode(code, user.ActivationCode.String) {
		err := s.incrementActivationAttemptsAndMaybeLockout(ctx, user.ID, user.ActivationAttempts)
		if err != nil {
			fmt.Printf("[ActivationService] Failed to increment attempts: %v\n", err)
		}
		remaining := MaxActivationAttempts - user.ActivationAttempts - 1
		if remaining <= 0 {
			return nil, ErrMaxAttemptsExceeded
		}
		return nil, ErrCodeInvalid
	}

	err = s.updateUserStatusToEmailVerified(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update user status: %w", err)
	}

	token, err := utils.GenerateJWT(user.ID, user.Email, s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	return &models.EmailVerifiedResponse{
		Success:     true,
		Message:     "Email verified successfully. Please submit your contact information.",
		Status:      string(models.UserStatusEmailVerified),
		RedirectURL: "/contact-info",
		UserID:      user.ID,
		Token:       token,
		AccessToken: token,
		ExpiresIn:   86400,
	}, nil
}

func (s *ActivationService) getUserByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, status, activation_code, activation_attempts, 
		       activation_expires_at, activated_at, created_at, updated_at
		FROM users WHERE email = $1`, email).Scan(
		&user.ID, &user.Email, &user.Status, &user.ActivationCode,
		&user.ActivationAttempts, &user.ActivationExpiresAt, &user.ActivatedAt,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}
	return &user, nil
}

func (s *ActivationService) updateActivationCode(ctx context.Context, userID int, hashedCode string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users 
		SET activation_code = $1, activation_attempts = 0, 
		    activation_expires_at = $2, updated_at = NOW()
		WHERE id = $3`,
		hashedCode, expiresAt, userID,
	)
	return err
}

func (s *ActivationService) incrementActivationAttemptsAndMaybeLockout(ctx context.Context, userID int, currentAttempts int) error {
	newAttempts := currentAttempts + 1

	// Check if we need to lock out the user (after 3 failed attempts)
	if newAttempts >= MaxActivationAttempts {
		// Set lockout expiration to 1 minute from now
		lockoutExpiry := time.Now().Add(ActivationLockoutMinutes * time.Minute)
		_, err := s.db.ExecContext(ctx, `
			UPDATE users 
			SET activation_attempts = $1, activation_expires_at = $2, updated_at = NOW()
			WHERE id = $3`,
			newAttempts, lockoutExpiry, userID,
		)
		return err
	}

	// Just increment attempts without lockout
	_, err := s.db.ExecContext(ctx, `
		UPDATE users 
		SET activation_attempts = $1, updated_at = NOW()
		WHERE id = $2`,
		newAttempts, userID,
	)
	return err
}

// incrementActivationAttempts increments attempt count without lockout (for backward compatibility)
func (s *ActivationService) incrementActivationAttempts(ctx context.Context, userID int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users 
		SET activation_attempts = activation_attempts + 1, updated_at = NOW()
		WHERE id = $1`,
		userID,
	)
	return err
}

func (s *ActivationService) activateUser(ctx context.Context, userID int) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE users 
		SET status = 'EMAIL_VERIFIED', activation_code = NULL, 
		    activation_attempts = 0, activation_expires_at = NULL,
		    activated_at = $1, updated_at = $1
		WHERE id = $2`,
		now, userID,
	)
	return err
}

func (s *ActivationService) updateUserStatusToEmailVerified(ctx context.Context, userID int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users 
		SET status = $1, activation_code = NULL, 
		    activation_attempts = 0, activation_expires_at = NULL,
		    updated_at = NOW()
		WHERE id = $2`,
		models.UserStatusEmailVerified, userID,
	)
	return err
}

func (s *ActivationService) IsUserPending(ctx context.Context, userID int) (bool, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM users WHERE id = $1`, userID).Scan(&status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == string(models.UserStatusPending), nil
}
