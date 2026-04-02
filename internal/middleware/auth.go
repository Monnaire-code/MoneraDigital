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

// AuthMiddleware validates JWT tokens in Authorization header
// and checks if user account is disabled
func AuthMiddleware(jwtSecret string, userRepo repository.User) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		// Check if user account is disabled
		if userRepo != nil {
			isDisabled, err := userRepo.IsDisabled(context.Background(), claims.UserID)
			if err == nil && isDisabled {
				c.JSON(http.StatusForbidden, ErrorResponse{
					Code:    "USER_DISABLED",
					Message: "User account is disabled",
				})
				c.Abort()
				return
			}
		}

		// Store user info in context
		c.Set("userID", claims.UserID)
		c.Set("email", claims.Email)

		c.Next()
	}
}
