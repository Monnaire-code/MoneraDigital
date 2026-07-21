package services

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"monera-digital/internal/cache"
	"monera-digital/internal/config"
	"monera-digital/internal/models"
	"monera-digital/internal/utils"

	"github.com/DATA-DOG/go-sqlmock"
)

// Helper to create mock DB
func newMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock DB: %v", err)
	}
	return mockDB, mock
}

// Helper to create mock config
func newMockConfig() *config.Config {
	return &config.Config{
		CoreAPIURL: "http://127.0.0.1:1",
	}
}

func TestMain(m *testing.M) {
	utils.SetActivationCodeKey([]byte("0123456789abcdef0123456789abcdef"))
	os.Exit(m.Run())
}

// ==================== AuthService Tests ====================

func TestAuthService_Register_Success(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM users WHERE email = \$1\)`).
		WithArgs("test@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	mock.ExpectQuery(`INSERT INTO users`).
		WithArgs("test@example.com", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "status", "created_at", "two_factor_enabled"}).
			AddRow(1, "test@example.com", models.UserStatusPending, time.Now(), false))
	for _, currency := range []string{"BTC", "ETH", "USDT", "USDC"} {
		mock.ExpectExec(`INSERT INTO account`).
			WithArgs(1, currency, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}

	service := NewAuthService(db, "test-secret", newMockConfig())
	req := models.RegisterRequest{
		Email:    "test@example.com",
		Password: "password123",
	}

	user, err := service.Register(req)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if user == nil {
		t.Fatal("Expected user, got nil")
	}
	if user.Email != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got '%s'", user.Email)
	}
	if user.ID != 1 {
		t.Errorf("Expected ID 1, got %d", user.ID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestAuthService_Register_EmailAlreadyExists(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM users WHERE email = \$1\)`).
		WithArgs("existing@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	service := NewAuthService(db, "test-secret", newMockConfig())
	req := models.RegisterRequest{
		Email:    "existing@example.com",
		Password: "password123",
	}

	user, err := service.Register(req)

	if err == nil {
		t.Fatal("Expected error for duplicate email, got nil")
	}
	if user != nil {
		t.Error("Expected nil user on error")
	}
	if err.Error() != "email already registered" {
		t.Errorf("Expected 'email already registered', got '%s'", err.Error())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestAuthService_Register_DBError(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM users WHERE email = \$1\)`).
		WithArgs("test@example.com").
		WillReturnError(errors.New("database connection failed"))

	service := NewAuthService(db, "test-secret", newMockConfig())
	req := models.RegisterRequest{
		Email:    "test@example.com",
		Password: "password123",
	}

	_, err := service.Register(req)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}

func TestAuthService_Login_Success(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	hashedPassword, _ := utils.HashPassword("password123")

	mock.ExpectQuery(`SELECT id, email, password, status, two_factor_enabled FROM users WHERE email = \$1`).
		WithArgs("test@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "password", "status", "two_factor_enabled"}).
			AddRow(1, "test@example.com", hashedPassword, models.UserStatusActive, false))

	service := NewAuthService(db, "test-secret", newMockConfig())
	req := models.LoginRequest{
		Email:    "test@example.com",
		Password: "password123",
	}

	resp, err := service.Login(req)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.AccessToken == "" {
		t.Error("Expected access token, got empty")
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("Expected 'Bearer', got '%s'", resp.TokenType)
	}
	if resp.ExpiresIn != 86400 {
		t.Errorf("Expected 86400, got %d", resp.ExpiresIn)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestAuthService_Login_UserNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, email, password, status, two_factor_enabled FROM users WHERE email = \$1`).
		WithArgs("nonexistent@example.com").
		WillReturnError(sql.ErrNoRows)

	service := NewAuthService(db, "test-secret", newMockConfig())
	req := models.LoginRequest{
		Email:    "nonexistent@example.com",
		Password: "password123",
	}

	_, err := service.Login(req)

	if err == nil {
		t.Fatal("Expected error for invalid credentials, got nil")
	}
	if err.Error() != "invalid credentials" {
		t.Errorf("Expected 'invalid credentials', got '%s'", err.Error())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestAuthService_Login_WrongPassword(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	hashedPassword, _ := utils.HashPassword("correctpassword")

	mock.ExpectQuery(`SELECT id, email, password, status, two_factor_enabled FROM users WHERE email = \$1`).
		WithArgs("test@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "password", "status", "two_factor_enabled"}).
			AddRow(1, "test@example.com", hashedPassword, models.UserStatusActive, false))

	service := NewAuthService(db, "test-secret", newMockConfig())
	req := models.LoginRequest{
		Email:    "test@example.com",
		Password: "wrongpassword",
	}

	_, err := service.Login(req)

	if err == nil {
		t.Fatal("Expected error for wrong password, got nil")
	}
	if err.Error() != "invalid credentials" {
		t.Errorf("Expected 'invalid credentials', got '%s'", err.Error())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestAuthService_Login_DBError(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	mock.ExpectQuery(`SELECT id, email, password, status, two_factor_enabled FROM users WHERE email = \$1`).
		WithArgs("test@example.com").
		WillReturnError(errors.New("database connection failed"))

	service := NewAuthService(db, "test-secret", newMockConfig())
	req := models.LoginRequest{
		Email:    "test@example.com",
		Password: "password123",
	}

	_, err := service.Login(req)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}

func TestAuthService_SetTokenBlacklist(t *testing.T) {
	db, _ := newMockDB(t)
	defer db.Close()

	service := NewAuthService(db, "test-secret", newMockConfig())

	if service.tokenBlacklist != nil {
		t.Error("Expected nil tokenBlacklist initially")
	}

	tb := &cache.TokenBlacklist{}
	service.SetTokenBlacklist(tb)

	if service.tokenBlacklist == nil {
		t.Error("Expected tokenBlacklist to be set")
	}
}

// ==================== Utils Tests ====================

func TestPasswordHashing(t *testing.T) {
	password := "securePass"
	hash, err := utils.HashPassword(password)
	if err != nil {
		t.Fatalf("Hash failed: %v", err)
	}

	if !utils.CheckPasswordHash(password, hash) {
		t.Errorf("Password check failed")
	}

	if utils.CheckPasswordHash("wrong", hash) {
		t.Errorf("Password check matched wrong password")
	}
}

func TestPasswordHashing_UniqueHashes(t *testing.T) {
	password := "samePassword"
	hash1, _ := utils.HashPassword(password)
	hash2, _ := utils.HashPassword(password)

	// Same password should produce different hashes (due to salt)
	if hash1 == hash2 {
		t.Error("Expected different hashes for same password")
	}

	// But both should validate correctly
	if !utils.CheckPasswordHash(password, hash1) {
		t.Error("First hash should validate")
	}
	if !utils.CheckPasswordHash(password, hash2) {
		t.Error("Second hash should validate")
	}
}

func TestGenerateJWT(t *testing.T) {
	token, err := utils.GenerateJWT(123, "test@example.com", "secret")
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	if token == "" {
		t.Error("Expected non-empty token")
	}

	// Verify token can be parsed
	claims, err := utils.ParseJWT(token, "secret")
	if err != nil {
		t.Fatalf("Failed to parse JWT: %v", err)
	}

	if claims["userId"].(float64) != 123 {
		t.Errorf("Expected userId 123, got %v", claims["userId"])
	}
	if claims["email"] != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got %v", claims["email"])
	}
}

func TestParseJWT_InvalidSecret(t *testing.T) {
	token, _ := utils.GenerateJWT(123, "test@example.com", "correct-secret")

	_, err := utils.ParseJWT(token, "wrong-secret")
	if err == nil {
		t.Error("Expected error for wrong secret")
	}
}

func TestParseJWT_InvalidToken(t *testing.T) {
	_, err := utils.ParseJWT("invalid-token", "secret")
	if err == nil {
		t.Error("Expected error for invalid token")
	}
}

func TestParseJWT_ExpiredToken(t *testing.T) {
	// Create an expired token manually
	// Note: This is a simplified test - in real scenarios, you'd need to
	// manipulate time or use a custom claims struct with custom expiry
	t.Skip("Requires time manipulation or custom claims - skipping for now")
}

// ==================== Skip2FAAndLogin Tests ====================

func TestAuthService_Skip2FAAndLogin_Success(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	// Mock GetUserByID query
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "status", "two_factor_enabled", "phone", "telegram", "wechat"}).
			AddRow(1, "test@example.com", models.UserStatusActive, false, nil, nil, nil))

	service := NewAuthService(db, "test-secret", newMockConfig())

	resp, err := service.Skip2FAAndLogin(1)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.AccessToken == "" {
		t.Error("Expected access token, got empty")
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("Expected 'Bearer', got '%s'", resp.TokenType)
	}
	if resp.User == nil {
		t.Fatal("Expected user in response")
	}
	if resp.User.Email != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got '%s'", resp.User.Email)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestAuthService_Skip2FAAndLogin_UserNotFound(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	// Mock GetUserByID query - user not found
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(999).
		WillReturnError(sql.ErrNoRows)

	service := NewAuthService(db, "test-secret", newMockConfig())

	_, err := service.Skip2FAAndLogin(999)

	if err == nil {
		t.Fatal("Expected error for non-existent user, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) && !contains(err.Error(), "user not found") {
		t.Errorf("Expected 'user not found' error, got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestAuthService_Skip2FAAndLogin_2FAEnabled(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	// Mock GetUserByID query - user with 2FA enabled
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "status", "two_factor_enabled", "phone", "telegram", "wechat"}).
			AddRow(1, "test@example.com", models.UserStatusActive, true, nil, nil, nil))

	service := NewAuthService(db, "test-secret", newMockConfig())

	_, err := service.Skip2FAAndLogin(1)

	if err == nil {
		t.Fatal("Expected error when 2FA is enabled, got nil")
	}
	if err.Error() != "cannot skip 2FA as it is enabled for this account" {
		t.Errorf("Expected 'cannot skip 2FA as it is enabled for this account', got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestAuthService_Skip2FAAndLogin_DBError(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	// Mock GetUserByID query - database error
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(1).
		WillReturnError(errors.New("database connection failed"))

	service := NewAuthService(db, "test-secret", newMockConfig())

	_, err := service.Skip2FAAndLogin(1)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
