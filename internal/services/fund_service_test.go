// internal/services/fund_service_test.go
package services

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"monera-digital/internal/dto"
	"monera-digital/internal/models"
	"monera-digital/internal/repository/postgres"
)

type MockFundReportRepository struct {
	mock.Mock
}

func (m *MockFundReportRepository) GetLatest(ctx context.Context) (*models.FundReport, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.FundReport), args.Error(1)
}

func (m *MockFundReportRepository) GetTrend(ctx context.Context, limit int) ([]models.FundReport, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.FundReport), args.Error(1)
}

func (m *MockFundReportRepository) GetAllocationsByReportID(ctx context.Context, reportID int64) ([]models.FundAssetAllocation, error) {
	args := m.Called(ctx, reportID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.FundAssetAllocation), args.Error(1)
}

func may2026Report() *models.FundReport {
	return &models.FundReport{
		ID:          5,
		ReportDate:  time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		TotalAum:    14820125.94,
		NewCapital:  sql.NullFloat64{Float64: 2130800.00, Valid: true},
		MonthGrowth: sql.NullFloat64{Float64: 0.0460902827200177, Valid: true},
		ActualApy:   sql.NullFloat64{Float64: 0.1623, Valid: true},
		WeightedApy: sql.NullFloat64{Float64: 0.5871, Valid: true},
		Note:        sql.NullString{String: "May 2026 narrative", Valid: true},
		CreatedAt:   time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}
}

func may2026Allocations() []models.FundAssetAllocation {
	return []models.FundAssetAllocation{
		{ID: 1, ReportID: 5, Category: "DeFi Yield Farming", Amount: 3857328.43, Pct: 0.26028, SortOrder: 1, CreatedAt: time.Now()},
		{ID: 2, ReportID: 5, Category: "Proactive Trading", Amount: 9879372.87, Pct: 0.66647, SortOrder: 2, CreatedAt: time.Now()},
		{ID: 3, ReportID: 5, Category: "Venture Investing", Amount: 1000000.00, Pct: 0.06750, SortOrder: 3, CreatedAt: time.Now()},
		{ID: 4, ReportID: 5, Category: "Token, NFT, Points and other Assets", Amount: 83424.64, Pct: 0.00563, SortOrder: 4, CreatedAt: time.Now()},
	}
}

func trendReports() []models.FundReport {
	return []models.FundReport{
		{ID: 5, ReportDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), TotalAum: 14820125.94},
		{ID: 4, ReportDate: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), TotalAum: 12130239.76},
		{ID: 3, ReportDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), TotalAum: 6780508.82},
		{ID: 2, ReportDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), TotalAum: 3335009.43},
		{ID: 1, ReportDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), TotalAum: 1000000.00},
	}
}

func TestFundService_GetStats_Success(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	service := NewFundService(mockRepo)

	latest := may2026Report()
	trend := trendReports()
	allocs := may2026Allocations()

	mockRepo.On("GetLatest", mock.Anything).Return(latest, nil)
	mockRepo.On("GetTrend", mock.Anything, 5).Return(trend, nil)
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return(allocs, nil)

	data, err := service.GetStats(context.Background())

	assert.NoError(t, err)
	assert.NotNil(t, data)

	assert.Equal(t, "2026-05", data.Current.ReportDate)
	assert.Equal(t, 14820125.94, data.Current.TotalAum)
	assert.Equal(t, 0.1623, data.Current.ActualApy)
	assert.Equal(t, 0.5871, data.Current.WeightedApy)
	assert.Equal(t, 0.0460902827200177, data.Current.MonthGrowth)
	assert.Equal(t, 2130800.00, data.Current.NewCapital)

	assert.Len(t, data.Trend, 5)
	assert.Equal(t, "2026-01", data.Trend[0].Month)
	assert.Equal(t, 1000000.00, data.Trend[0].Aum)
	assert.Equal(t, "2026-05", data.Trend[4].Month)
	assert.Equal(t, 14820125.94, data.Trend[4].Aum)

	assert.Len(t, data.Allocations, 4)
	assert.Equal(t, "DeFi Yield Farming", data.Allocations[0].Category)
	assert.InDelta(t, 3857328.43/14820125.94, data.Allocations[0].Pct, 1e-9)
	assert.Equal(t, "Proactive Trading", data.Allocations[1].Category)
	assert.InDelta(t, 9879372.87/14820125.94, data.Allocations[1].Pct, 1e-9)
	assert.Equal(t, "Token, NFT, Points and other Assets", data.Allocations[3].Category)

	mockRepo.AssertExpectations(t)
}

func TestFundService_GetStats_NoLatestReport(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	service := NewFundService(mockRepo)

	mockRepo.On("GetLatest", mock.Anything).Return(nil, ErrFundNotFound)

	data, err := service.GetStats(context.Background())

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFundNotFound)
	assert.Nil(t, data)
	mockRepo.AssertExpectations(t)
}

func TestFundService_GetStats_RepoError(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	service := NewFundService(mockRepo)

	mockRepo.On("GetLatest", mock.Anything).Return(nil, assert.AnError)

	data, err := service.GetStats(context.Background())

	assert.Error(t, err)
	assert.Nil(t, data)
	mockRepo.AssertExpectations(t)
}

func TestFundService_GetStats_AllocationsEmpty(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	service := NewFundService(mockRepo)

	latest := may2026Report()
	mockRepo.On("GetLatest", mock.Anything).Return(latest, nil)
	mockRepo.On("GetTrend", mock.Anything, 5).Return(trendReports(), nil)
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return([]models.FundAssetAllocation{}, nil)

	data, err := service.GetStats(context.Background())

	assert.NoError(t, err)
	assert.NotNil(t, data)
	assert.Empty(t, data.Allocations)
	assert.Equal(t, 14820125.94, data.Current.TotalAum)
	mockRepo.AssertExpectations(t)
}

// A1: service must normalise the repository's not-found sentinel to the
// service-level sentinel so the HTTP handler can map it to 404. Without
// this, real empty-table queries would surface as 500.
func TestFundService_GetStats_NormalisesRepoNotFound(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	service := NewFundService(mockRepo)

	mockRepo.On("GetLatest", mock.Anything).Return(nil, postgres.ErrFundNotFound)

	data, err := service.GetStats(context.Background())

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFundNotFound)
	assert.NotEqual(t, errors.Unwrap(err), postgres.ErrFundNotFound)
	assert.Nil(t, data)
	mockRepo.AssertExpectations(t)
}

// L1: subsequent GetStats calls within the cache TTL must not re-query the
// repository. Monthly AUM data changes at most once per month, so a 60s
// in-memory cache collapses N concurrent homepage fetches into 1 DB hit
// and removes the "too many requests" symptom caused by the global
// 5/min/IP rate limiter hammering this public read endpoint.
func TestFundService_GetStats_CacheHitWithinTTL(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	// Frozen clock starting at t=0.
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	service := NewFundServiceWithClock(mockRepo, now)

	latest := may2026Report()
	mockRepo.On("GetLatest", mock.Anything).Return(latest, nil).Once()
	mockRepo.On("GetTrend", mock.Anything, 5).Return(trendReports(), nil).Once()
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return(may2026Allocations(), nil).Once()

	// First call: populates cache.
	first, err := service.GetStats(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 14820125.94, first.Current.TotalAum)

	// Advance the clock 30s — still inside the 60s TTL window.
	clock = clock.Add(30 * time.Second)

	// Second call must hit cache, not the repo.
	second, err := service.GetStats(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 14820125.94, second.Current.TotalAum)

	// The repo should have been touched exactly once.
	mockRepo.AssertNumberOfCalls(t, "GetLatest", 1)
	mockRepo.AssertNumberOfCalls(t, "GetTrend", 1)
	mockRepo.AssertNumberOfCalls(t, "GetAllocationsByReportID", 1)
}

// B-2: GetStats must return a deep copy of the cached payload, never the
// same pointer. Without this, any caller that mutates the returned
// *dto.FundStatsData or its slices (Trend, Allocations) would corrupt
// the cache for every subsequent caller for the next 60s. Today's HTTP
// handler is well-behaved (it immediately c.JSON-encodes), but a future
// admin endpoint or a debug handler that enriches the struct would be a
// silent 60s AUM-poisoning bug.
func TestFundService_GetStats_ReturnedDataIsIsolatedFromCache(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	service := NewFundServiceWithClock(mockRepo, now)

	latest := may2026Report()
	mockRepo.On("GetLatest", mock.Anything).Return(latest, nil).Once()
	mockRepo.On("GetTrend", mock.Anything, 5).Return(trendReports(), nil).Once()
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return(may2026Allocations(), nil).Once()

	// First call: cache populated.
	first, err := service.GetStats(context.Background())
	assert.NoError(t, err)

	// Caller mutates the returned payload — primitive field, slice
	// element, and slice length.
	first.Current.TotalAum = 999
	first.Trend[0].Aum = 88888
	first.Trend = append(first.Trend, dto.FundTrendPoint{Month: "2099-01", Aum: 0})
	first.Allocations[0].Category = "PWNED"

	// Advance 5s (still inside TTL) and re-read.
	clock = clock.Add(5 * time.Second)
	second, err := service.GetStats(context.Background())
	assert.NoError(t, err)

	// Cache must be pristine — none of the mutations leaked through.
	assert.Equal(t, 14820125.94, second.Current.TotalAum, "primitive field must be isolated")
	assert.Equal(t, 1000000.00, second.Trend[0].Aum, "slice element must be isolated")
	assert.Len(t, second.Trend, 5, "slice length must be isolated (appended element must not leak)")
	assert.Equal(t, "DeFi Yield Farming", second.Allocations[0].Category, "nested struct field must be isolated")
}

// B-2: the same isolation must hold across the singleflight return path
// — the original repo result and the cached clone must be independent
// pointers and independent slices, so even if a caller mutates the
// fresh response (not a cached one), the cache itself stays clean.
func TestFundService_GetStats_OriginalFetchIsIsolatedFromCache(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	service := NewFundServiceWithClock(mockRepo, now)

	latest := may2026Report()
	mockRepo.On("GetLatest", mock.Anything).Return(latest, nil).Once()
	mockRepo.On("GetTrend", mock.Anything, 5).Return(trendReports(), nil).Once()
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return(may2026Allocations(), nil).Once()

	first, err := service.GetStats(context.Background())
	assert.NoError(t, err)
	first.Allocations[0].Amount = 0 // catastrophic caller-side bug

	clock = clock.Add(5 * time.Second)
	second, err := service.GetStats(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 3857328.43, second.Allocations[0].Amount, "mutated first response must not corrupt cached allocation")
}

// L1: once the TTL elapses, the cache must be considered stale and the
// service must re-query the repository. Without this, a stale AUM would
// be served forever after a monthly report release.
func TestFundService_GetStats_CacheExpiresAfterTTL(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	service := NewFundServiceWithClock(mockRepo, now)

	latest := may2026Report()
	// Allow GetLatest to be called twice — once for first request, once
	// after TTL expires.
	mockRepo.On("GetLatest", mock.Anything).Return(latest, nil).Twice()
	mockRepo.On("GetTrend", mock.Anything, 5).Return(trendReports(), nil).Twice()
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return(may2026Allocations(), nil).Twice()

	_, err := service.GetStats(context.Background())
	assert.NoError(t, err)

	// Advance past the 60s TTL.
	clock = clock.Add(61 * time.Second)

	_, err = service.GetStats(context.Background())
	assert.NoError(t, err)

	mockRepo.AssertNumberOfCalls(t, "GetLatest", 2)
}

// L1: a repository error must NOT populate the cache — otherwise a
// transient DB error would be served for the next 60s, leaving the
// homepage widget in a permanent error state.
func TestFundService_GetStats_ErrorNotCached(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	service := NewFundServiceWithClock(mockRepo, now)

	mockRepo.On("GetLatest", mock.Anything).Return(nil, assert.AnError).Once()
	mockRepo.On("GetLatest", mock.Anything).Return(may2026Report(), nil).Once()
	mockRepo.On("GetTrend", mock.Anything, 5).Return(trendReports(), nil).Once()
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return(may2026Allocations(), nil).Once()

	// First call: repo errors, nothing should be cached.
	_, err := service.GetStats(context.Background())
	assert.Error(t, err)

	// Second call (still inside TTL): repo must be called again, not
	// served from cache.
	data, err := service.GetStats(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 14820125.94, data.Current.TotalAum)

	mockRepo.AssertNumberOfCalls(t, "GetLatest", 2)
}

// L1: many concurrent first-time callers must coalesce onto a single
// repo fetch. This is the actual production amplifier: 3 components
// mount + React 18 StrictMode = 6 simultaneous fetchers on every
// page load. Without singleflight, the cache populates from 6 parallel
// repo roundtrips instead of 1.
func TestFundService_GetStats_ConcurrentFirstCallersShareCache(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	clock := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	service := NewFundServiceWithClock(mockRepo, now)

	latest := may2026Report()
	// Gate the repo on a channel so we can release N waiters at once.
	release := make(chan struct{})
	mockRepo.On("GetLatest", mock.Anything).Return(func(ctx context.Context) *models.FundReport {
		<-release
		return latest
	}, func(ctx context.Context) error {
		<-release
		return nil
	}).Once()
	mockRepo.On("GetTrend", mock.Anything, 5).Return(func(ctx context.Context, limit int) []models.FundReport {
		<-release
		return trendReports()
	}, func(ctx context.Context, limit int) error {
		<-release
		return nil
	}).Once()
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return(func(ctx context.Context, id int64) []models.FundAssetAllocation {
		<-release
		return may2026Allocations()
	}, func(ctx context.Context, id int64) error {
		<-release
		return nil
	}).Once()

	const callers = 6
	results := make([]float64, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(idx int) {
			defer wg.Done()
			data, err := service.GetStats(context.Background())
			if err == nil && data != nil {
				results[idx] = data.Current.TotalAum
			}
			errs[idx] = err
		}(i)
	}

	// Give all goroutines time to pile up on the in-flight gate.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "caller %d", i)
		assert.Equal(t, 14820125.94, results[i], "caller %d", i)
	}
	mockRepo.AssertNumberOfCalls(t, "GetLatest", 1)
	mockRepo.AssertNumberOfCalls(t, "GetTrend", 1)
	mockRepo.AssertNumberOfCalls(t, "GetAllocationsByReportID", 1)
}

// D1: service must compute allocation pct as amount / latest.TotalAum,
// overriding whatever the database stored. This guarantees the public
// payload is always internally consistent (sums to 1.0 within float
// precision) even if seed/admin data is wrong.
func TestFundService_GetStats_PctComputedFromAmountAndTotal(t *testing.T) {
	mockRepo := new(MockFundReportRepository)
	service := NewFundService(mockRepo)

	latest := may2026Report() // total_aum = 14820125.94
	// Deliberately wrong pct values in the database rows.
	wrongAlloc := []models.FundAssetAllocation{
		{ID: 1, ReportID: 5, Category: "DeFi Yield Farming", Amount: 3857328.43, Pct: 0.5, SortOrder: 1, CreatedAt: time.Now()},
		{ID: 2, ReportID: 5, Category: "Proactive Trading", Amount: 9879372.87, Pct: 0.99, SortOrder: 2, CreatedAt: time.Now()},
		{ID: 3, ReportID: 5, Category: "Venture Investing", Amount: 1000000.00, Pct: 0.1, SortOrder: 3, CreatedAt: time.Now()},
		{ID: 4, ReportID: 5, Category: "Token, NFT, Points and other Assets", Amount: 83424.64, Pct: 0.0, SortOrder: 4, CreatedAt: time.Now()},
	}

	mockRepo.On("GetLatest", mock.Anything).Return(latest, nil)
	mockRepo.On("GetTrend", mock.Anything, 5).Return(trendReports(), nil)
	mockRepo.On("GetAllocationsByReportID", mock.Anything, int64(5)).Return(wrongAlloc, nil)

	data, err := service.GetStats(context.Background())

	assert.NoError(t, err)
	assert.InDelta(t, 3857328.43/14820125.94, data.Allocations[0].Pct, 1e-9)
	assert.InDelta(t, 9879372.87/14820125.94, data.Allocations[1].Pct, 1e-9)
	// Composed sum should be ~1.0 regardless of DB pct values.
	sum := 0.0
	for _, a := range data.Allocations {
		sum += a.Pct
	}
	assert.InDelta(t, 1.0, sum, 1e-6)
	mockRepo.AssertExpectations(t)
}
