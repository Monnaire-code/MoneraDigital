package services

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"monera-digital/internal/cache"
	"monera-digital/internal/config"
	"monera-digital/internal/models"
	"monera-digital/internal/utils"
)

// AuthService provides authentication functionality
type AuthService struct {
	DB               *sql.DB
	jwtSecret        string
	tokenBlacklist   *cache.TokenBlacklist
	twoFactorService *TwoFactorService
	cfg              *config.Config
}

// NewAuthService creates a new AuthService instance
func NewAuthService(db *sql.DB, jwtSecret string, cfg *config.Config) *AuthService {
	return &AuthService{
		DB:        db,
		jwtSecret: jwtSecret,
		cfg:       cfg,
	}
}

// SetTwoFactorService injects TwoFactorService dependency
func (s *AuthService) SetTwoFactorService(twoFactor *TwoFactorService) {
	s.twoFactorService = twoFactor
}

// SetTokenBlacklist sets the token blacklist for logout functionality
func (s *AuthService) SetTokenBlacklist(tb *cache.TokenBlacklist) {
	s.tokenBlacklist = tb
}

// LoginResponse represents the login API response
type LoginResponse struct {
	User               *models.User `json:"user,omitempty"`
	Token              string       `json:"token,omitempty"`
	AccessToken        string       `json:"accessToken,omitempty"`
	RefreshToken       string       `json:"refreshToken,omitempty"`
	TokenType          string       `json:"tokenType,omitempty"`
	ExpiresIn          int          `json:"expiresIn,omitempty"`
	ExpiresAt          time.Time    `json:"expiresAt,omitempty"`
	Requires2FA        bool         `json:"requires2FA,omitempty"`
	RequiresActivation bool         `json:"requiresActivation,omitempty"`
	NeedsContactInfo   bool         `json:"needsContactInfo,omitempty"`
	PendingApproval    bool         `json:"pendingApproval,omitempty"`
	Message            string       `json:"message,omitempty"`
	UserID             int          `json:"userId,omitempty"`
}

// Register handles user registration
func (s *AuthService) Register(req models.RegisterRequest) (*models.User, error) {
	// Check if email already exists
	var exists bool
	err := s.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)", req.Email).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("email already registered")
	}

	// Hash password
	hashedPassword, err := utils.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	// Generate activation code
	activationCode, err := utils.GenerateActivationCode()
	if err != nil {
		return nil, fmt.Errorf("failed to generate activation code: %w", err)
	}

	hashedCode, err := utils.HashActivationCode(activationCode)
	if err != nil {
		return nil, fmt.Errorf("failed to hash activation code: %w", err)
	}

	expiresAt := time.Now().Add(5 * time.Minute)

	// Insert user into database with PENDING status
	var user models.User
	query := `
		INSERT INTO users (email, password, status, activation_code, activation_expires_at, created_at, updated_at)
		VALUES ($1, $2, 'PENDING', $3, $4, NOW(), NOW())
		RETURNING id, email, status, created_at, two_factor_enabled`

	err = s.DB.QueryRow(query, req.Email, hashedPassword, hashedCode, expiresAt).Scan(
		&user.ID, &user.Email, &user.Status, &user.CreatedAt, &user.TwoFactorEnabled,
	)
	if err != nil {
		return nil, err
	}

	// Create FUND accounts for 4 currencies (BTC, ETH, USDT, USDC)
	if err := s.createDefaultAccounts(user.ID); err != nil {
		return nil, fmt.Errorf("failed to create default accounts: %w", err)
	}

	// Create account in Core Account System (fire and forget)
	_, _ = s.createCoreAccount(user.ID, req.Email)

	return &user, nil
}

// createDefaultAccounts creates default FUND accounts for the user
func (s *AuthService) createDefaultAccounts(userID int) error {
	currencies := []string{"BTC", "ETH", "USDT", "USDC"}
	now := time.Now()

	for _, currency := range currencies {
		_, err := s.DB.Exec(`
			INSERT INTO account (user_id, type, currency, balance, frozen_balance, version, created_at, updated_at)
			VALUES ($1, 'FUND', $2, 0, 0, 1, $3, $3)`,
			userID, currency, now)
		if err != nil {
			return err
		}
	}
	return nil
}

// createCoreAccount creates an account in the Core Account System
func (s *AuthService) createCoreAccount(userID int, email string) (string, error) {
	accountReq := map[string]interface{}{
		"externalId":  strconv.Itoa(userID),
		"accountType": "INDIVIDUAL",
		"profile": map[string]interface{}{
			"email":     email,
			"firstName": "",
			"lastName":  "",
		},
		"metadata": map[string]interface{}{
			"source": "monera_web",
		},
	}

	jsonData, err := json.Marshal(accountReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	coreAPIURL := s.cfg.CoreAPIURL + "/api/core/accounts/create"

	resp, err := http.Post(coreAPIURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Sprintf("core_simulated_%d", userID), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("core account creation failed with status: %d", resp.StatusCode)
	}

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			AccountID string `json:"accountId"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return response.Data.AccountID, nil
}

// Login handles user authentication
// Note: 2FA is no longer required during login. It's only required for sensitive operations.
func (s *AuthService) Login(req models.LoginRequest) (*LoginResponse, error) {
	var user models.User
	var hashedPassword string

	query := `SELECT id, email, password, status, two_factor_enabled FROM users WHERE email = $1`
	err := s.DB.QueryRow(query, req.Email).Scan(&user.ID, &user.Email, &hashedPassword, &user.Status, &user.TwoFactorEnabled)

	if err == sql.ErrNoRows {
		return nil, errors.New("invalid credentials")
	} else if err != nil {
		return nil, err
	}

	// Verify password
	if !utils.CheckPasswordHash(req.Password, hashedPassword) {
		return nil, errors.New("invalid credentials")
	}

	// Check user account status and return appropriate response
	switch user.Status {
	case models.UserStatusDisabled:
		return nil, errors.New("user account is disabled")

	case models.UserStatusPending:
		return &LoginResponse{
			User:               &user,
			RequiresActivation: true,
			Message:            "Please verify your email first",
		}, nil

	case models.UserStatusEmailVerified:
		return &LoginResponse{
			User:             &user,
			NeedsContactInfo: true,
			Message:          "Please submit your contact information",
		}, nil

	case models.UserStatusInfoSubmitted:
		return &LoginResponse{
			User:            &user,
			PendingApproval: true,
			Message:         "Your account is under review",
		}, nil

	case models.UserStatusActive:
		// Normal login - generate JWT token
		token, err := utils.GenerateJWT(user.ID, user.Email, s.jwtSecret)
		if err != nil {
			return nil, err
		}

		expiresAt := time.Now().Add(24 * time.Hour)

		return &LoginResponse{
			User:        &user,
			Token:       token,
			AccessToken: token,
			TokenType:   "Bearer",
			ExpiresIn:   86400,
			ExpiresAt:   expiresAt,
		}, nil

	default:
		return nil, errors.New("invalid account status")
	}
}

// Verify2FAAndLogin verifies 2FA token and completes login
func (s *AuthService) Verify2FAAndLogin(userID int, token string) (*LoginResponse, error) {
	// 获取用户信息
	user, err := s.GetUserByID(userID)
	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	// 检查用户状态
	if user.Status == models.UserStatusDisabled {
		return nil, errors.New("user account is disabled")
	}

	// 验证2FA令牌
	valid, err := s.twoFactorService.Verify(userID, token)
	if err != nil {
		return nil, fmt.Errorf("2FA verification failed: %w", err)
	}
	if !valid {
		return nil, fmt.Errorf("invalid 2FA token")
	}

	// 生成JWT令牌
	jwtToken, err := utils.GenerateJWT(user.ID, user.Email, s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	expiresAt := time.Now().Add(24 * time.Hour)

	return &LoginResponse{
		User:        user,
		Token:       jwtToken,
		AccessToken: jwtToken,
		TokenType:   "Bearer",
		ExpiresIn:   86400,
		ExpiresAt:   expiresAt,
	}, nil
}

// Skip2FAAndLogin allows skipping 2FA if not enabled and completes login
func (s *AuthService) Skip2FAAndLogin(userID int) (*LoginResponse, error) {
	// 获取用户信息
	user, err := s.GetUserByID(userID)
	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	// 检查用户状态
	if user.Status == models.UserStatusDisabled {
		return nil, errors.New("user account is disabled")
	}

	// 如果用户已经启用了2FA，则不允许跳过
	if user.TwoFactorEnabled {
		return nil, errors.New("cannot skip 2FA as it is enabled for this account")
	}

	// 生成JWT令牌
	jwtToken, err := utils.GenerateJWT(user.ID, user.Email, s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	expiresAt := time.Now().Add(24 * time.Hour)

	return &LoginResponse{
		User:        user,
		Token:       jwtToken,
		AccessToken: jwtToken,
		TokenType:   "Bearer",
		ExpiresIn:   86400,
		ExpiresAt:   expiresAt,
	}, nil
}

// Verify2FA verifies a 2FA token for a user
func (s *AuthService) Verify2FA(userID int, token string) (bool, error) {
	if s.twoFactorService == nil {
		return false, errors.New("two factor service not initialized")
	}
	return s.twoFactorService.Verify(userID, token)
}

// GetUserByID retrieves a user by their ID
func (s *AuthService) GetUserByID(userID int) (*models.User, error) {
	var user models.User
	query := `SELECT id, email, two_factor_enabled FROM users WHERE id = $1`
	err := s.DB.QueryRow(query, userID).Scan(&user.ID, &user.Email, &user.TwoFactorEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	return &user, nil
}
