package tests

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"monera-digital/internal/config"
	"monera-digital/internal/models"
	"monera-digital/internal/services"
	"monera-digital/internal/utils"

	_ "github.com/lib/pq"
)

func getTestDB(t *testing.T) *sql.DB {
	// Load DB URL manually since we might not have viper init
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		// Try to read .env
		content, _ := os.ReadFile("../.env")
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "DATABASE_URL=") {
				dbURL = strings.Trim(strings.TrimPrefix(strings.TrimSpace(line), "DATABASE_URL="), "'\"")
				break
			}
		}
	}
	if dbURL == "" {
		t.Skip("DATABASE_URL not found, skipping integration test")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	return db
}

func TestRegisterAndLogin(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()
	utils.SetActivationCodeKey([]byte("0123456789abcdef0123456789abcdef"))
	t.Cleanup(func() { utils.SetActivationCodeKey(nil) })

	// Clean up test user
	testEmail := fmt.Sprintf("test_%d@example.com", time.Now().UnixNano())
	defer db.Exec("DELETE FROM users WHERE email = $1", testEmail)

	jwtSecret := "test-jwt-secret-for-integration-tests"
	authService := services.NewAuthService(db, jwtSecret, &config.Config{CoreAPIURL: "http://127.0.0.1:1"})

	// 1. Test Register
	req := models.RegisterRequest{
		Email:    testEmail,
		Password: "password123",
	}

	user, err := authService.Register(req)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if user.Email != testEmail {
		t.Errorf("Expected email %s, got %s", testEmail, user.Email)
	}
	if user.ID == 0 {
		t.Errorf("Expected non-zero ID")
	}
	if user.Status != models.UserStatusPending {
		t.Errorf("Expected pending user status, got %q", user.Status)
	}

	// 2. Test Login
	loginReq := models.LoginRequest{
		Email:    testEmail,
		Password: "password123",
	}

	resp, err := authService.Login(loginReq)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	if !resp.RequiresActivation {
		t.Error("Expected activation requirement for a newly registered user")
	}
	if resp.Token != "" {
		t.Error("Expected no token before account activation")
	}
	if resp.User.Email != testEmail {
		t.Errorf("Expected user email %s, got %s", testEmail, resp.User.Email)
	}

	// 3. Test Invalid Login
	badReq := models.LoginRequest{
		Email:    testEmail,
		Password: "wrongpassword",
	}
	_, err = authService.Login(badReq)
	if err == nil {
		t.Errorf("Expected error for wrong password, got nil")
	}
}

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
