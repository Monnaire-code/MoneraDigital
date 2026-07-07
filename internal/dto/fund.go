// internal/dto/fund.go
package dto

// FundStatsResponse is the public homepage payload for the AUM widget.
type FundStatsResponse struct {
	Success bool           `json:"success"`
	Data    *FundStatsData `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type FundStatsData struct {
	Current     FundCurrentMetrics   `json:"current"`
	Trend       []FundTrendPoint     `json:"trend"`
	Allocations []FundAllocationItem `json:"allocations"`
}

type FundCurrentMetrics struct {
	ReportDate  string  `json:"reportDate"`
	TotalAum    float64 `json:"totalAum"`
	ActualApy   float64 `json:"actualApy"`
	WeightedApy float64 `json:"weightedApy"`
	MonthGrowth float64 `json:"monthGrowth"`
	NewCapital  float64 `json:"newCapital"`
}

type FundTrendPoint struct {
	Month string  `json:"month"`
	Aum   float64 `json:"aum"`
}

type FundAllocationItem struct {
	Category string  `json:"category"`
	Amount   float64 `json:"amount"`
	Pct      float64 `json:"pct"`
}
