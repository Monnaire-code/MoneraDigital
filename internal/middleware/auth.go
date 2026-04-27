// internal/middleware/auth.go
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"monera-digital/internal/models"
	"monera-digital/internal/repository"
)

var activationEndpoints = map[string]bool{
	"/api/auth/send-activation":   true,
	"/api/auth/verify-activation": true,
}

// State-specific endpoint permissions
var stateAllowedEndpoints = map[models.UserStatus][]string{
	models.UserStatusEmailVerified: {
		"/api/contact-info",
		"/api/auth/me",
	},
	models.UserStatusInfoSubmitted: {
		"/api/contact-info",
		"/api/auth/me",
	},
}

func isEndpointAllowedForStatus(path string, status models.UserStatus) bool {
	allowedPaths, exists := stateAllowedEndpoints[status]
	if !exists {
		return false
	}
	for _, allowedPath := range allowedPaths {
		if path == allowedPath || strings.HasPrefix(path, allowedPath+"/") {
			return true
		}
	}
	return false
}

func getStatusMessage(status models.UserStatus) string {
	switch status {
	case models.UserStatusPending:
		return "Please verify your email first"
	case models.UserStatusEmailVerified:
		return "Please submit your contact information"
	case models.UserStatusInfoSubmitted:
		return "Your account is under review"
	case models.UserStatusDisabled:
		return "User account is disabled"
	default:
		return "Account access restricted"
	}
}

// AuthMiddleware validates JWT tokens in Authorization header
// and checks if user account is in a valid state for the requested endpoint
func AuthMiddleware(jwtSecret string, userRepo repository.User) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if this is an activation endpoint - allow PENDING users
		if activationEndpoints[c.Request.URL.Path] {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				Code:    "MISSING_TOKEN",
				Message: "Authorization header is required",
			})
			c.Abort()
			return
		}

		// Extract token from "Bearer <token>" format
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				Code:    "INVALID_TOKEN_FORMAT",
				Message: "Authorization header must be in 'Bearer <token>' format",
			})
			c.Abort()
			return
		}

		token := parts[1]

		// Parse and validate token
		claims := &models.TokenClaims{}
		parsedToken, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(jwtSecret), nil
		})

		if err != nil || !parsedToken.Valid {
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				Code:    "INVALID_TOKEN",
				Message: "Token is invalid or expired",
			})
			c.Abort()
			return
		}

		// Check token type (if present in claims)
		if claims.TokenType != "" && claims.TokenType != "access" {
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				Code:    "INVALID_TOKEN_TYPE",
				Message: "Token type must be 'access'",
			})
			c.Abort()
			return
		}

		// Check user account status
		if userRepo != nil {
			user, err := userRepo.GetByID(context.Background(), claims.UserID)
			if err == nil {
				// Check if account is disabled
				if user.Status == models.UserStatusDisabled {
					c.JSON(http.StatusForbidden, ErrorResponse{
						Code:    "USER_DISABLED",
						Message: getStatusMessage(user.Status),
					})
					c.Abort()
					return
				}

				// Check if user is PENDING
				if user.Status == models.UserStatusPending {
					c.JSON(http.StatusForbidden, ErrorResponse{
						Code:    "ACCOUNT_NOT_ACTIVATED",
						Message: getStatusMessage(user.Status),
					})
					c.Abort()
					return
				}

				// Check if user is EMAIL_VERIFIED or INFO_SUBMITTED
				// Only allow access to specific endpoints based on status
				if user.Status == models.UserStatusEmailVerified || user.Status == models.UserStatusInfoSubmitted {
					if !isEndpointAllowedForStatus(c.Request.URL.Path, user.Status) {
						c.JSON(http.StatusForbidden, ErrorResponse{
							Code:    "ACCOUNT_STATUS_RESTRICTED",
							Message: getStatusMessage(user.Status),
						})
						c.Abort()
						return
					}
				}
			}
		}

		// Store user info in context
		c.Set("userID", claims.UserID)
		c.Set("email", claims.Email)

		c.Next()
	}
}
