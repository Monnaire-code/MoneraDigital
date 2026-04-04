// internal/middleware/error_handler.go
package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"monera-digital/internal/validator"
)

// ErrorResponse represents a standardized error response
type ErrorResponse struct {
	Code    string `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// errorMapping maps error messages to HTTP responses
var errorMapping = map[string]struct {
	status int
	code   string
	msg    string
}{
	"email not found":                 {http.StatusUnauthorized, "EMAIL_NOT_FOUND", "Email input error or does not exist"},
	"invalid credentials":             {http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid email or password"},
	"user account is disabled":        {http.StatusUnauthorized, "ACCOUNT_DISABLED", "Your account has been disabled. Please contact support."},
	"email already registered":        {http.StatusConflict, "EMAIL_ALREADY_EXISTS", "Email is already registered"},
	"invalid refresh token":           {http.StatusUnauthorized, "INVALID_REFRESH_TOKEN", "Refresh token is invalid or expired"},
	"refresh token has been revoked":  {http.StatusUnauthorized, "TOKEN_REVOKED", "Refresh token has been revoked"},
	"token blacklist not initialized": {http.StatusInternalServerError, "INTERNAL_ERROR", "Token service is not properly initialized"},
	"unauthorized":                    {http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required"},
	"not found":                       {http.StatusNotFound, "NOT_FOUND", "Resource not found"},
}

// ErrorHandler middleware for handling errors consistently
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) > 0 {
			err := c.Errors.Last()
			handleError(c, err.Err)
		}
	}
}

// handleError maps errors to appropriate HTTP status codes and responses
func handleError(c *gin.Context, err error) {
	if err == nil {
		return
	}

	// Check for validation errors
	var validationErr *validator.ValidationError
	if errors.As(err, &validationErr) {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Code:    "VALIDATION_ERROR",
			Error:   validationErr.Error(),
			Message: validationErr.Error(),
			Details: validationErr.Field,
		})
		return
	}

	// Check for mapped errors
	errMsg := err.Error()
	if mapping, ok := errorMapping[errMsg]; ok {
		c.JSON(mapping.status, ErrorResponse{
			Code:    mapping.code,
			Error:   mapping.msg,
			Message: mapping.msg,
		})
		return
	}

	// Generic internal server error
	c.JSON(http.StatusInternalServerError, ErrorResponse{
		Code:    "INTERNAL_ERROR",
		Error:   "An internal server error occurred",
		Message: "An internal server error occurred",
		Details: errMsg,
	})
}

// RecoveryHandler middleware for recovering from panics
func RecoveryHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				c.JSON(http.StatusInternalServerError, ErrorResponse{
					Code:    "PANIC_RECOVERED",
					Error:   "An unexpected error occurred",
					Message: "An unexpected error occurred",
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}
