// internal/services/fund_service_test.go
package services

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
