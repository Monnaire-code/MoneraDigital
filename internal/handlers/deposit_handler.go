package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetDeposits(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}

	deposits, total, err := h.DepositService.GetDeposits(c.Request.Context(), userID.(int), limit, offset)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total":    total,
		"deposits": deposits,
	})
}

// HandleDepositWebhook is the Phase 0 Core-API webhook endpoint that Phase 1
// replaced with `/api/webhooks/safeheron`. Plan §6 S-4 mandates 410 Gone so any
// stale Core-API caller surfaces the routing change instead of silently 200-ing
// against an empty stub. T7-S-6.
func (h *Handler) HandleDepositWebhook(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"error":      "endpoint deprecated",
		"successor":  "/api/webhooks/safeheron",
		"deprecated": "phase1",
	})
}
