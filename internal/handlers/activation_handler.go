package handlers

import (
	"net/http"

	"monera-digital/internal/models"
	"monera-digital/internal/services"

	"github.com/gin-gonic/gin"
)

type ActivationHandler struct {
	activationService *services.ActivationService
}

func NewActivationHandler(activationService *services.ActivationService) *ActivationHandler {
	return &ActivationHandler{
		activationService: activationService,
	}
}

func (h *ActivationHandler) SendActivation(c *gin.Context) {
	var req models.SendActivationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "code": "INVALID_REQUEST"})
		return
	}

	clientIP := c.ClientIP()

	result, err := h.activationService.SendActivationCode(c.Request.Context(), req.Email, clientIP)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send activation code", "code": "SEND_FAILED"})
		return
	}

	if !result.Success {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":      "too many requests",
			"code":       "RATE_LIMITED",
			"retryAfter": result.RetryAfter,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "activation code sent",
	})
}

func (h *ActivationHandler) VerifyActivation(c *gin.Context) {
	var req models.VerifyActivationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "code": "INVALID_REQUEST"})
		return
	}

	resp, err := h.activationService.VerifyActivationCode(c.Request.Context(), req.Email, req.Code)
	if err != nil {
		switch err {
		case services.ErrUserNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found", "code": "USER_NOT_FOUND"})
		case services.ErrCodeExpired:
			c.JSON(http.StatusBadRequest, gin.H{"error": "activation code expired", "code": "CODE_EXPIRED"})
		case services.ErrCodeInvalid:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid activation code", "code": "INVALID_CODE"})
		case services.ErrMaxAttemptsExceeded:
			c.JSON(http.StatusBadRequest, gin.H{"error": "maximum attempts exceeded", "code": "MAX_ATTEMPTS"})
		case services.ErrUserAlreadyActivated:
			c.JSON(http.StatusBadRequest, gin.H{"error": "user already activated", "code": "ALREADY_ACTIVATED"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "verification failed", "code": "VERIFICATION_FAILED"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":            true,
		"message":            "account activated successfully",
		"token":              resp.Token,
		"accessToken":        resp.AccessToken,
		"tokenType":          resp.TokenType,
		"expiresIn":          resp.ExpiresIn,
		"expiresAt":          resp.ExpiresAt,
		"requiresActivation": resp.RequiresActivation,
		"userId":             resp.UserID,
	})
}
