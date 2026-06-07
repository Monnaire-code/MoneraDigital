// internal/services/fund_service.go
package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"monera-digital/internal/dto"
	"monera-digital/internal/models"
	"monera-digital/internal/repository/postgres"
)

var ErrFundNotFound = errors.New("fund report not found")

const fundTrendMonths = 5

type FundReportRepository interface {
	GetLatest(ctx context.Context) (*models.FundReport, error)
	GetTrend(ctx context.Context, limit int) ([]models.FundReport, error)
	GetAllocationsByReportID(ctx context.Context, reportID int64) ([]models.FundAssetAllocation, error)
}

type FundService struct {
	repo FundReportRepository
}

func NewFundService(repo FundReportRepository) *FundService {
	return &FundService{repo: repo}
}

func (s *FundService) GetStats(ctx context.Context) (*dto.FundStatsData, error) {
	latest, err := s.repo.GetLatest(ctx)
	if err != nil {
		return nil, mapGetLatestError(err)
	}

	trendDesc, err := s.repo.GetTrend(ctx, fundTrendMonths)
	if err != nil {
		return nil, fmt.Errorf("get fund trend: %w", err)
	}

	allocs, err := s.repo.GetAllocationsByReportID(ctx, latest.ID)
	if err != nil {
		return nil, fmt.Errorf("get fund allocations: %w", err)
	}

	return buildFundStatsData(latest, trendDesc, allocs), nil
}

// mapGetLatestError translates the repository layer's not-found sentinel
// into the service layer's public sentinel so the HTTP handler can map
// it to 404. Any other error is wrapped with context for diagnostics.
func mapGetLatestError(err error) error {
	if errors.Is(err, postgres.ErrFundNotFound) {
		return ErrFundNotFound
	}
	return fmt.Errorf("get latest fund report: %w", err)
}

func buildFundStatsData(latest *models.FundReport, trendDesc []models.FundReport, allocs []models.FundAssetAllocation) *dto.FundStatsData {
	trend := make([]dto.FundTrendPoint, 0, len(trendDesc))
	// Repo returns DESC; the homepage chart reads oldest -> newest, so reverse.
	for i := len(trendDesc) - 1; i >= 0; i-- {
		r := trendDesc[i]
		trend = append(trend, dto.FundTrendPoint{
			Month: r.ReportDate.Format("2006-01"),
			Aum:   r.TotalAum,
		})
	}

	// Allocation pct is recomputed from amount / latest.TotalAum rather
	// than trusted from the DB row. This guarantees the public payload
	// is internally consistent (sums to 1.0 within float precision) even
	// if seed data or admin edits introduce drift. Defends against
	// audit finding 2.2.
	total := latest.TotalAum
	allocItems := make([]dto.FundAllocationItem, 0, len(allocs))
	for _, a := range allocs {
		pct := 0.0
		if total > 0 {
			pct = a.Amount / total
		}
		allocItems = append(allocItems, dto.FundAllocationItem{
			Category: a.Category,
			Amount:   a.Amount,
			Pct:      pct,
		})
	}

	return &dto.FundStatsData{
		Current: dto.FundCurrentMetrics{
			ReportDate:  latest.ReportDate.Format("2006-01"),
			TotalAum:    latest.TotalAum,
			ActualApy:   nullableFloat(latest.ActualApy),
			WeightedApy: nullableFloat(latest.WeightedApy),
			MonthGrowth: nullableFloat(latest.MonthGrowth),
			NewCapital:  nullableFloat(latest.NewCapital),
		},
		Trend:       trend,
		Allocations: allocItems,
	}
}

func nullableFloat(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0
}
