package handlers

import (
	"net/http"
	"strconv"
	"time"

	"monera-digital/internal/models"

	"github.com/gin-gonic/gin"
)

type depositResponse struct {
	ID          int     `json:"id"`
	Amount      string  `json:"amount"`
	Asset       string  `json:"asset"`
	Chain       string  `json:"chain"`
	Status      string  `json:"status"`
	TxHash      string  `json:"txHash"`
	FromAddress *string `json:"fromAddress"`
	ToAddress   *string `json:"toAddress"`
	CreatedAt   string  `json:"createdAt"`
	CreditedAt  *string `json:"creditedAt"`
}

func toDepositResponse(d *models.Deposit) depositResponse {
	r := depositResponse{
		ID:        d.ID,
		Amount:    d.Amount,
		Asset:     d.Asset,
		Chain:     d.Chain,
		Status:    string(d.Status),
		TxHash:    d.TxHash,
		CreatedAt: d.CreatedAt.Format(time.RFC3339),
	}
	if d.FromAddress.Valid {
		r.FromAddress = &d.FromAddress.String
	}
	if d.ToAddress.Valid {
		r.ToAddress = &d.ToAddress.String
	}
	if d.ConfirmedAt.Valid {
		s := d.ConfirmedAt.Time.Format(time.RFC3339)
		r.CreditedAt = &s
	}
	return r
}

func (h *Handler) GetDeposits(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
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

	resp := make([]depositResponse, len(deposits))
	for i, d := range deposits {
		resp[i] = toDepositResponse(d)
	}

	c.JSON(http.StatusOK, gin.H{
		"total":    total,
		"deposits": resp,
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
