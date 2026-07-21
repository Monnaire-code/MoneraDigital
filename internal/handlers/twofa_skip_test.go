package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"monera-digital/internal/services"
)

func setupTestDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock DB: %v", err)
	}
	return db, mock
}

func TestTwoFAHandler_Skip2FALogin_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/2fa/skip",
		strings.NewReader(`{}`)) // Missing userId
	c.Request.Header.Set("Content-Type", "application/json")

	handler.Skip2FALogin(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["error"] == nil {
		t.Error("Expected error in response")
	}
}

func TestTwoFAHandler_Skip2FALogin_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, mock := setupTestDB(t)
	defer db.Close()

	// Mock GetUserByID query - user without 2FA enabled
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "status", "two_factor_enabled", "phone", "telegram", "wechat"}).
			AddRow(1, "test@example.com", "ACTIVE", false, nil, nil, nil))

	authService := services.NewAuthService(db, "test-secret", nil)
	handler := &Handler{
		AuthService: authService,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/2fa/skip",
		strings.NewReader(`{"userId": 1}`))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.Skip2FALogin(c)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["accessToken"] == nil || response["accessToken"] == "" {
		t.Error("Expected accessToken in response")
	}
	if response["tokenType"] != "Bearer" {
		t.Errorf("Expected tokenType 'Bearer', got '%v'", response["tokenType"])
	}
	if response["user"] == nil {
		t.Error("Expected user in response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestTwoFAHandler_Skip2FALogin_UserNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, mock := setupTestDB(t)
	defer db.Close()

	// Mock GetUserByID query - user not found
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(999).
		WillReturnError(sql.ErrNoRows)

	authService := services.NewAuthService(db, "test-secret", nil)
	handler := &Handler{
		AuthService: authService,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/2fa/skip",
		strings.NewReader(`{"userId": 999}`))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.Skip2FALogin(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["error"] == nil {
		t.Error("Expected error in response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestTwoFAHandler_Skip2FALogin_2FAEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, mock := setupTestDB(t)
	defer db.Close()

	// Mock GetUserByID query - user with 2FA enabled
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "status", "two_factor_enabled", "phone", "telegram", "wechat"}).
			AddRow(1, "test@example.com", "ACTIVE", true, nil, nil, nil))

	authService := services.NewAuthService(db, "test-secret", nil)
	handler := &Handler{
		AuthService: authService,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/2fa/skip",
		strings.NewReader(`{"userId": 1}`))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.Skip2FALogin(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["error"] == nil {
		t.Error("Expected error in response")
	}

	errMsg, ok := response["error"].(string)
	if !ok || !strings.Contains(errMsg, "cannot skip 2FA") {
		t.Errorf("Expected 'cannot skip 2FA' error message, got: %v", response["error"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestTwoFAHandler_Skip2FALogin_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, mock := setupTestDB(t)
	defer db.Close()

	// Mock GetUserByID query - database error
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(1).
		WillReturnError(errors.New("database connection failed"))

	authService := services.NewAuthService(db, "test-secret", nil)
	handler := &Handler{
		AuthService: authService,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/2fa/skip",
		strings.NewReader(`{"userId": 1}`))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.Skip2FALogin(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["error"] == nil {
		t.Error("Expected error in response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestTwoFAHandler_Skip2FALogin_InvalidUserIdType(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/2fa/skip",
		strings.NewReader(`{"userId": "invalid"}`)) // Invalid userId type
	c.Request.Header.Set("Content-Type", "application/json")

	handler.Skip2FALogin(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestTwoFAHandler_Skip2FALogin_ZeroUserId(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := &Handler{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/2fa/skip",
		strings.NewReader(`{"userId": 0}`)) // Zero userId (required field validation)
	c.Request.Header.Set("Content-Type", "application/json")

	handler.Skip2FALogin(c)

	// Gin binding with "required" tag should reject zero value
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestTwoFAHandler_Skip2FALogin_ResponseFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, mock := setupTestDB(t)
	defer db.Close()

	// Mock GetUserByID query
	mock.ExpectQuery(`SELECT id, email, status, two_factor_enabled, phone, telegram, wechat\s+FROM users WHERE id = \$1`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "status", "two_factor_enabled", "phone", "telegram", "wechat"}).
			AddRow(1, "test@example.com", "ACTIVE", false, nil, nil, nil))

	authService := services.NewAuthService(db, "test-secret", nil)
	handler := &Handler{
		AuthService: authService,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/2fa/skip",
		strings.NewReader(`{"userId": 1}`))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.Skip2FALogin(c)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Verify response structure matches LoginResponse
	var response struct {
		AccessToken  string    `json:"accessToken"`
		RefreshToken string    `json:"refreshToken"`
		TokenType    string    `json:"tokenType"`
		ExpiresIn    int       `json:"expiresIn"`
		ExpiresAt    time.Time `json:"expiresAt"`
		Token        string    `json:"token"`
		User         struct {
			ID               int    `json:"id"`
			Email            string `json:"email"`
			TwoFactorEnabled bool   `json:"twoFactorEnabled"`
		} `json:"user"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.AccessToken == "" {
		t.Error("Expected accessToken in response")
	}
	if response.TokenType != "Bearer" {
		t.Errorf("Expected tokenType 'Bearer', got '%s'", response.TokenType)
	}
	if response.ExpiresIn != 86400 {
		t.Errorf("Expected expiresIn 86400, got %d", response.ExpiresIn)
	}
	if response.User.ID != 1 {
		t.Errorf("Expected user ID 1, got %d", response.User.ID)
	}
	if response.User.Email != "test@example.com" {
		t.Errorf("Expected user email 'test@example.com', got '%s'", response.User.Email)
	}
	if response.User.TwoFactorEnabled != false {
		t.Errorf("Expected user twoFactorEnabled false, got %v", response.User.TwoFactorEnabled)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}
