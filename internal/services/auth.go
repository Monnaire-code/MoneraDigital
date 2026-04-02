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
}

// NewAuthService creates a new AuthService instance
func NewAuthService(db *sql.DB, jwtSecret string) *AuthService {
	return &AuthService{
		DB:        db,
		jwtSecret: jwtSecret,
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
	User         *models.User `json:"user,omitempty"`
	Token        string       `json:"token,omitempty"`
	AccessToken  string       `json:"accessToken,omitempty"`
	RefreshToken string       `json:"refreshToken,omitempty"`
	TokenType    string       `json:"tokenType,omitempty"`
	ExpiresIn    int          `json:"expiresIn,omitempty"`
	ExpiresAt    time.Time    `json:"expiresAt,omitempty"`
	Requires2FA  bool         `json:"requires2FA,omitempty"`
	UserID       int          `json:"userId,omitempty"`
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

	// Insert user into database
	var user models.User
	query := `
		INSERT INTO users (email, password, created_at)
		VALUES ($1, $2, NOW())
		RETURNING id, email, created_at, two_factor_enabled`

	err = s.DB.QueryRow(query, req.Email, hashedPassword).Scan(
		&user.ID, &user.Email, &user.CreatedAt, &user.TwoFactorEnabled,
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

	cfg := config.Load()
	coreAPIURL := fmt.Sprintf("http://localhost:%s/api/core/accounts/create", cfg.Port)

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

	// Check if user account is disabled
	if user.Status == models.UserStatusDisabled {
		return nil, errors.New("user account is disabled")
	}

	// Generate JWT token directly (no 2FA check during login)
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
