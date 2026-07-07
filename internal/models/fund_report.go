// internal/models/fund_report.go
package models

import (
	"database/sql"
	"time"
)

type FundReport struct {
	ID            int64           `json:"id" db:"id"`
	ReportDate    time.Time       `json:"reportDate" db:"report_date"`
	TotalAum      float64         `json:"totalAum" db:"total_aum"`
	InitialAum    sql.NullFloat64 `json:"initialAum,omitempty" db:"initial_aum"`
	MonthStartAum sql.NullFloat64 `json:"monthStartAum,omitempty" db:"month_start_aum"`
	NewCapital    sql.NullFloat64 `json:"newCapital,omitempty" db:"new_capital"`
	MonthGrowth   sql.NullFloat64 `json:"monthGrowth,omitempty" db:"month_growth"`
	ActualApy     sql.NullFloat64 `json:"actualApy,omitempty" db:"actual_apy"`
	WeightedApy   sql.NullFloat64 `json:"weightedApy,omitempty" db:"weighted_apy"`
	Note          sql.NullString  `json:"note,omitempty" db:"note"`
	CreatedAt     time.Time       `json:"createdAt" db:"created_at"`
	UpdatedAt     time.Time       `json:"updatedAt" db:"updated_at"`
}

type FundAssetAllocation struct {
	ID        int64     `json:"id" db:"id"`
	ReportID  int64     `json:"reportId" db:"report_id"`
	Category  string    `json:"category" db:"category"`
	Amount    float64   `json:"amount" db:"amount"`
	Pct       float64   `json:"pct" db:"pct"`
	SortOrder int       `json:"sortOrder" db:"sort_order"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}
