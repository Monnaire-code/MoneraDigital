// internal/services/fund_service.go
package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
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

	// sf coalesces concurrent first-callers (cold cache) onto a single
	// repo roundtrip. Without it, N parallel homepage fetches after
	// deploy / TTL expiry / restart would each hit the DB, multiplying
	// load by N exactly when the L1 cache was supposed to absorb it.
	// The singleflight key is a constant because the cache is single-key
	// today; if multi-key caching is ever added, this becomes a
	// parameter and the per-key dedup window stays correct.
	sf singleflight.Group
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
	// Fast path: fresh cache hit. Returns a deep copy so caller mutation
	// cannot poison the cached payload for the next 60s.
	if data, ok := s.cachedCopy(); ok {
		return data, nil
	}

	// Cold path: coalesce concurrent first-callers onto a single repo
	// roundtrip via singleflight. N parallel goroutines all call
	// sf.Do(...) but only one actually runs the function; the rest
	// wait for its result. This is the B-1 fix that turns the
	// commit's "1000 concurrent → 1 DB" promise from aspirational
	// to accurate.
	v, err, _ := s.sf.Do(fundStatsCacheKey, func() (interface{}, error) {
		// Re-check inside singleflight: a previous caller may have
		// populated the cache between our outer miss and entering
		// the singleflight window. Without this, every cold caller
		// would unconditionally re-fetch.
		if data, ok := s.cachedCopy(); ok {
			return data, nil
		}
		data, fetchErr := s.fetchStats(ctx)
		if fetchErr != nil {
			// Do NOT cache errors. A transient DB blip should not
			// lock the homepage widget into a permanent error state
			// for the rest of the TTL window.
			return nil, fetchErr
		}
		s.storeCache(data)
		// Return a clone, not the pointer we just stored. The
		// singleflight result may be shared with N concurrent
		// callers; if any of them mutates their copy, the cache
		// must not be affected.
		return cloneFundStatsData(data), nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*dto.FundStatsData), nil
}

// fundStatsCacheKey is the singleflight grouping key. Single-key today
// (the cache holds at most one entry); see FundService.sf doc.
const fundStatsCacheKey = "fund:stats"

// cachedCopy returns a fresh deep copy of the cached payload if one
// exists and is still within its TTL, otherwise (nil, false). Returning
// a copy — never the cached pointer — is the B-2 fix: any caller
// mutation is local to their own response.
func (s *FundService) cachedCopy() (*dto.FundStatsData, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.cachedData == nil {
		return nil, false
	}
	if !s.nowFunc().Before(s.cachedExpires) {
		return nil, false
	}
	return cloneFundStatsData(s.cachedData), true
}

// storeCache atomically replaces any existing cache entry with a deep
// copy of the supplied payload. Storing a clone (not the caller's
// pointer) means the input can be safely returned to concurrent
// singleflight peers without aliasing.
func (s *FundService) storeCache(data *dto.FundStatsData) {
	s.cacheMu.Lock()
	s.cachedData = cloneFundStatsData(data)
	s.cachedExpires = s.nowFunc().Add(fundStatsCacheTTL)
	s.cacheMu.Unlock()
}

// cloneFundStatsData returns an independent copy of a FundStatsData
// payload, including fresh backing arrays for the Trend and
// Allocations slices. The Current struct is value-copied (it holds
// only primitive fields). Used on every read AND every store so the
// cache and its callers can never observe each other's mutations.
func cloneFundStatsData(d *dto.FundStatsData) *dto.FundStatsData {
	if d == nil {
		return nil
	}
	cloned := *d
	if d.Trend != nil {
		cloned.Trend = make([]dto.FundTrendPoint, len(d.Trend))
		copy(cloned.Trend, d.Trend)
	}
	if d.Allocations != nil {
		cloned.Allocations = make([]dto.FundAllocationItem, len(d.Allocations))
		copy(cloned.Allocations, d.Allocations)
	}
	return &cloned
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
