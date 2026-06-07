// internal/services/fund_service.go
package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"monera-digital/internal/dto"
	"monera-digital/internal/models"
	"monera-digital/internal/repository/postgres"
)

var ErrFundNotFound = errors.New("fund report not found")

const fundTrendMonths = 5

// fundStatsCacheTTL bounds how long a populated FundStats response is
// reused. Monthly AUM changes at most once per month, so 60s is
// generously fresh while collapsing N concurrent homepage fetches into
// a single repo roundtrip. This is the L1 fix for the
// "GET /api/fund/stats: too many requests" symptom caused by the
// global 5/min/IP rate limiter hitting this public read endpoint.
const fundStatsCacheTTL = 60 * time.Second

type FundReportRepository interface {
	GetLatest(ctx context.Context) (*models.FundReport, error)
	GetTrend(ctx context.Context, limit int) ([]models.FundReport, error)
	GetAllocationsByReportID(ctx context.Context, reportID int64) ([]models.FundAssetAllocation, error)
}

type FundService struct {
	repo FundReportRepository

	// In-memory cache for GetStats. Read-mostly: RLock for hits,
	// write-lock only on the populate path. Errors from the repo are
	// never cached so a transient failure doesn't poison the homepage
	// for the next minute.
	cacheMu       sync.RWMutex
	cachedData    *dto.FundStatsData
	cachedExpires time.Time
	nowFunc       func() time.Time
}

func NewFundService(repo FundReportRepository) *FundService {
	return &FundService{repo: repo, nowFunc: time.Now}
}

// NewFundServiceWithClock is the test-only constructor that lets the
// caller inject a deterministic clock. Production code should use
// NewFundService.
func NewFundServiceWithClock(repo FundReportRepository, nowFunc func() time.Time) *FundService {
	if nowFunc == nil {
		nowFunc = time.Now
	}
	return &FundService{repo: repo, nowFunc: nowFunc}
}

func (s *FundService) GetStats(ctx context.Context) (*dto.FundStatsData, error) {
	if data, ok := s.cachedStats(); ok {
		return data, nil
	}

	data, err := s.fetchStats(ctx)
	if err != nil {
		// Do NOT cache errors. A transient DB blip should not lock
		// the homepage widget into a permanent error state for the
		// rest of the TTL window.
		return nil, err
	}
	s.storeCache(data)
	return data, nil
}

// cachedStats returns the cached payload if one exists and is still
// within its TTL. The RLock is released before any potential repo call
// in the caller path.
func (s *FundService) cachedStats() (*dto.FundStatsData, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.cachedData == nil {
		return nil, false
	}
	if !s.nowFunc().Before(s.cachedExpires) {
		return nil, false
	}
	return s.cachedData, true
}

// storeCache atomically replaces any existing cache entry. Serialised
// via the write lock so concurrent first-callers see a consistent
// snapshot after the populate completes.
func (s *FundService) storeCache(data *dto.FundStatsData) {
	s.cacheMu.Lock()
	s.cachedData = data
	s.cachedExpires = s.nowFunc().Add(fundStatsCacheTTL)
	s.cacheMu.Unlock()
}

// fetchStats is the cold path: read from the repo and assemble the
// public payload. Kept separate from GetStats so the cache bookkeeping
// is testable in isolation.
func (s *FundService) fetchStats(ctx context.Context) (*dto.FundStatsData, error) {
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
