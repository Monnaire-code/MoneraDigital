package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"monera-digital/internal/dto"
	"monera-digital/internal/services"
)

type ContactHandler struct {
	contactService *services.ContactService
}

func NewContactHandler(contactService *services.ContactService) *ContactHandler {
	return &ContactHandler{
		contactService: contactService,
	}
}

func (h *ContactHandler) SubmitContactInfo(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    "UNAUTHORIZED",
			"message": "User not authenticated",
		})
		return
	}

	var req dto.SubmitContactInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    "INVALID_REQUEST",
			"message": "Invalid request body",
			"error":   err.Error(),
		})
		return
	}

	result, err := h.contactService.SubmitContactInfo(c.Request.Context(), userID.(int), req)
	if err != nil {
		switch err {
		case services.ErrUserNotEmailVerified:
			c.JSON(http.StatusForbidden, gin.H{
				"code":    "EMAIL_NOT_VERIFIED",
				"message": "Please verify your email first",
			})
		case services.ErrUserAlreadySubmitted:
			c.JSON(http.StatusConflict, gin.H{
				"code":    "ALREADY_SUBMITTED",
				"message": "Contact information already submitted",
			})
		case services.ErrInvalidPhoneFormat:
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    "INVALID_PHONE_FORMAT",
				"message": "Invalid phone number format. Please use international format (e.g., +86-13800138000)",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":    "INTERNAL_ERROR",
				"message": "Failed to submit contact information",
			})
		}
		return
	}

	c.JSON(http.StatusOK, result)
}
