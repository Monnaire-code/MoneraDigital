// internal/handlers/fund_handler.go
package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"monera-digital/internal/dto"
	"monera-digital/internal/services"
)

type FundHandler struct {
	fundService *services.FundService
}

func NewFundHandler(fundService *services.FundService) *FundHandler {
	return &FundHandler{fundService: fundService}
}

func (h *FundHandler) GetStats(c *gin.Context) {
	data, err := h.fundService.GetStats(c.Request.Context())
	if err != nil {
		if errors.Is(err, services.ErrFundNotFound) {
			c.JSON(http.StatusNotFound, dto.FundStatsResponse{
				Success: false,
				Error:   "No fund report available yet",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, dto.FundStatsResponse{
			Success: false,
			Error:   "Failed to load fund statistics",
		})
		return
	}

	c.JSON(http.StatusOK, dto.FundStatsResponse{
		Success: true,
		Data:    data,
	})
}
