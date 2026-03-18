// internal/dto/lending.go
package dto

import "time"

// ApplyLendingRequest DTO for lending application
type ApplyLendingRequest struct {
	Asset        string `json:"asset" binding:"required,oneof=BTC ETH USDC USDT"`
	Amount       string `json:"amount" binding:"required"`
	DurationDays int    `json:"durationDays" binding:"required,gt=0,lte=365"`
}

// LendingPositionResponse DTO for lending position response
type LendingPositionResponse struct {
	ID           int       `json:"id"`
	UserID       int       `json:"userId"`
	Asset        string    `json:"asset"`
	Amount       float64   `json:"amount"`
	DurationDays int       `json:"durationDays"`
	APY          float64   `json:"apy"`
	Status       string    `json:"status"`
	AccruedYield float64   `json:"accruedYield"`
	StartDate    time.Time `json:"startDate"`
	EndDate      time.Time `json:"endDate"`
	CreatedAt    time.Time `json:"createdAt"`
}

// LendingPositionsListResponse DTO for list of lending positions
type LendingPositionsListResponse struct {
	Positions []LendingPositionResponse `json:"positions"`
	Total     int                       `json:"total"`
	Count     int                       `json:"count"`
}

// CloseLendingRequest DTO for closing a lending position
type CloseLendingRequest struct {
	PositionID int `json:"positionId" binding:"required,gt=0"`
}
