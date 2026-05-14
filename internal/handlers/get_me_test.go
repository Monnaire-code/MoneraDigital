package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"monera-digital/internal/dto"
	"monera-digital/internal/services"
)

func TestGetMe_ReturnsTwoFactorEnabled(t *testing.T) {
	// 1. Setup Mock DB
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	// 2. Setup Service and Handler
	authService := services.NewAuthService(db, "secret", nil)
	h := &Handler{
		AuthService: authService,
	}

	// 3. Setup Gin Router with Mock Middleware
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", 1)
		c.Set("email", "test@example.com")
		c.Next()
	})
	r.GET("/api/auth/me", h.GetMe)

	// 4. Expect Query

rows := sqlmock.NewRows([]string{"id", "email", "two_factor_enabled"}).
		AddRow(1, "test@example.com", true)

	mock.ExpectQuery("SELECT id, email, two_factor_enabled FROM users WHERE id = \\$1").
		WithArgs(1).
		WillReturnRows(rows)

	// 5. Perform Request
	req, _ := http.NewRequest("GET", "/api/auth/me", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	// 6. Assertions
	assert.Equal(t, http.StatusOK, resp.Code)

	var userInfo dto.UserInfo
	err = json.Unmarshal(resp.Body.Bytes(), &userInfo)
	assert.NoError(t, err)
	assert.Equal(t, 1, userInfo.ID)
	assert.Equal(t, "test@example.com", userInfo.Email)
	assert.True(t, userInfo.TwoFactorEnabled, "TwoFactorEnabled should be true")

	// Ensure query was executed
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}
