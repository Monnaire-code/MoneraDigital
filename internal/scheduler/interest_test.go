package scheduler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"monera-digital/internal/logger"
	"monera-digital/internal/repository"
	"monera-digital/internal/utils"
)

func init() {
	_ = logger.Init("test")
}

func TestInterestScheduler_CalculateDailyInterest_Success(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	mockWealthRepo.On("GetActiveOrders", mock.Anything).Return([]*repository.WealthOrderModel{
		{
			ID:               1,
			UserID:           1,
			ProductID:        1,
			ProductTitle:     "USDT 7日增值",
			Amount:           "10000",
			InterestAccrued:  "0",
			StartDate:        yesterday,
			EndDate:          tomorrow,
			LastInterestDate: "",
			Duration:         7,
			Currency:         "USDT",
		},
	}, nil)

	mockWealthRepo.On("GetProductByID", mock.Anything, int64(1)).Return(&repository.WealthProductModel{
		ID:       1,
		Title:    "USDT 7日增值",
		APY:      "5.50",
		Currency: "USDT",
	}, nil)

	mockWealthRepo.On("UpdateInterestAccrued", mock.Anything, int64(1), mock.AnythingOfType("string")).Return(nil)

	ordersProcessed, totalInterest, err := scheduler.CalculateDailyInterest(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 1, ordersProcessed)

	// 使用 decimal utils 比较字符串类型的 totalInterest
	hasInterest, _ := utils.GT(totalInterest, "0")
	assert.True(t, hasInterest)
	mockWealthRepo.AssertExpectations(t)
}

func TestInterestScheduler_CalculateDailyInterest_NoActiveOrders(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	mockWealthRepo.On("GetActiveOrders", mock.Anything).Return([]*repository.WealthOrderModel{}, nil)

	ordersProcessed, totalInterest, err := scheduler.CalculateDailyInterest(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0, ordersProcessed)
	assert.Equal(t, "0", totalInterest)
	mockWealthRepo.AssertExpectations(t)
}

func TestInterestScheduler_CalculateDailyInterest_SkipStartDate(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	today := time.Now().Format("2006-01-02")

	mockWealthRepo.On("GetActiveOrders", mock.Anything).Return([]*repository.WealthOrderModel{
		{
			ID:               1,
			UserID:           1,
			ProductID:        1,
			Amount:           "10000",
			InterestAccrued:  "0",
			StartDate:        today,
			EndDate:          "2026-02-15",
			LastInterestDate: "",
			Currency:         "USDT",
		},
	}, nil)

	ordersProcessed, totalInterest, err := scheduler.CalculateDailyInterest(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0, ordersProcessed)
	assert.Equal(t, "0", totalInterest)
	mockWealthRepo.AssertNotCalled(t, "AccrueInterest")
}

func TestInterestScheduler_CalculateDailyInterest_SkipAlreadyAccrued(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	mockWealthRepo.On("GetActiveOrders", mock.Anything).Return([]*repository.WealthOrderModel{
		{
			ID:               1,
			UserID:           1,
			ProductID:        1,
			Amount:           "10000",
			InterestAccrued:  "0",
			StartDate:        yesterday,
			EndDate:          "2026-02-15",
			LastInterestDate: today,
			Currency:         "USDT",
		},
	}, nil)

	ordersProcessed, totalInterest, err := scheduler.CalculateDailyInterest(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0, ordersProcessed)
	assert.Equal(t, "0", totalInterest)
	mockWealthRepo.AssertNotCalled(t, "AccrueInterest")
}

func TestInterestScheduler_CalculateDailyInterest_DatabaseError(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	mockWealthRepo.On("GetActiveOrders", mock.Anything).Return(nil, assert.AnError)

	ordersProcessed, totalInterest, err := scheduler.CalculateDailyInterest(context.Background())

	assert.Error(t, err)
	assert.Equal(t, 0, ordersProcessed)
	assert.Equal(t, "0", totalInterest)
	assert.Contains(t, err.Error(), "failed to get active orders")
}

func TestInterestScheduler_CalculateDailyInterest_AccrueError(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")

	mockWealthRepo.On("GetActiveOrders", mock.Anything).Return([]*repository.WealthOrderModel{
		{
			ID:               1,
			UserID:           1,
			ProductID:        1,
			Amount:           "10000",
			InterestAccrued:  "0",
			StartDate:        yesterday,
			EndDate:          "2026-02-15",
			LastInterestDate: "",
			Currency:         "USDT",
		},
	}, nil)

	mockWealthRepo.On("GetProductByID", mock.Anything, int64(1)).Return(&repository.WealthProductModel{
		ID:       1,
		APY:      "5.50",
		Currency: "USDT",
	}, nil)

	mockWealthRepo.On("AccrueInterest", mock.Anything, int64(1), mock.AnythingOfType("string"), today).Return(assert.AnError)

	ordersProcessed, totalInterest, err := scheduler.CalculateDailyInterest(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0, ordersProcessed)
	assert.Equal(t, "0", totalInterest)
}

func TestSchedulerMetrics_RecordInterestRun_Success(t *testing.T) {
	metrics := NewSchedulerMetrics()

	metrics.RecordInterestRun(true, 5, 15.5, "")

	snapshot := metrics.GetSnapshot()
	assert.Equal(t, int64(1), snapshot["interest_run_count"])
	assert.Equal(t, int64(1), snapshot["interest_success_count"])
	assert.Equal(t, int64(0), snapshot["interest_error_count"])
	assert.Equal(t, float64(100.0), snapshot["success_rate"])
	assert.Equal(t, int64(5), snapshot["total_orders_processed"])
	assert.Equal(t, 15.5, snapshot["total_interest_accrued"])
}

func TestSchedulerMetrics_RecordInterestRun_Error(t *testing.T) {
	metrics := NewSchedulerMetrics()

	metrics.RecordInterestRun(false, 0, 0, "database connection failed")
	metrics.RecordInterestRun(true, 3, 10.0, "")

	snapshot := metrics.GetSnapshot()
	assert.Equal(t, int64(2), snapshot["interest_run_count"])
	assert.Equal(t, int64(1), snapshot["interest_success_count"])
	assert.Equal(t, int64(1), snapshot["interest_error_count"])
	assert.Equal(t, float64(50.0), snapshot["success_rate"])
	assert.Equal(t, "database connection failed", snapshot["last_error_message"])
}

func TestSchedulerMetrics_Reset(t *testing.T) {
	metrics := NewSchedulerMetrics()

	metrics.RecordInterestRun(true, 5, 15.5, "")
	metrics.Reset()

	snapshot := metrics.GetSnapshot()
	assert.Equal(t, int64(0), snapshot["interest_run_count"])
	assert.Equal(t, int64(0), snapshot["interest_success_count"])
	assert.Equal(t, int64(0), snapshot["interest_error_count"])
	assert.Equal(t, int64(0), snapshot["total_orders_processed"])
	assert.Equal(t, 0.0, snapshot["total_interest_accrued"])
}

func TestInterestScheduler_SettleOrder_Success(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	mockWealthRepo.On("GetOrderByID", mock.Anything, int64(1)).Return(&repository.WealthOrderModel{
		ID:              1,
		UserID:          1,
		Currency:        "USDT",
		Amount:          "10000",
		InterestAccrued: "15.50",
		Status:          1,
	}, nil)

	mockAccountRepo.On("GetAccountByUserIDAndCurrency", mock.Anything, int64(1), "USDT").Return(&repository.AccountModel{
		ID:       1,
		UserID:   1,
		Currency: "USDT",
		Balance:  "100000",
	}, nil)

	mockAccountRepo.On("UnfreezeBalance", mock.Anything, int64(1), "10000").Return(nil)
	mockAccountRepo.On("AddBalance", mock.Anything, int64(1), mock.AnythingOfType("string")).Return(nil)
	mockWealthRepo.On("SettleOrder", mock.Anything, int64(1), mock.AnythingOfType("string")).Return(nil)
	mockJournalRepo.On("CreateJournalRecord", mock.Anything, mock.AnythingOfType("*repository.JournalModel")).Return(nil)

	err := scheduler.SettleOrder(context.Background(), 1)

	assert.NoError(t, err)
	mockWealthRepo.AssertExpectations(t)
	mockAccountRepo.AssertExpectations(t)
	mockJournalRepo.AssertExpectations(t)
}

func TestInterestScheduler_SettleOrder_AlreadySettled(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	mockWealthRepo.On("GetOrderByID", mock.Anything, int64(1)).Return(&repository.WealthOrderModel{
		ID:     1,
		Status: 2,
	}, nil)

	err := scheduler.SettleOrder(context.Background(), 1)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "order status is not active")
}

func TestInterestScheduler_SettleExpiredOrders_AutoRenew(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	expiredOrder := &repository.WealthOrderModel{
		ID:              1,
		UserID:          1,
		ProductID:       1,
		ProductTitle:    "USDT 7日增值",
		Amount:          "10000",
		InterestAccrued: "15.50",
		StartDate:       yesterday,
		EndDate:         yesterday,
		Status:          1,
		AutoRenew:       true,
		Currency:        "USDT",
	}

	mockWealthRepo.On("GetExpiredOrders", mock.Anything).Return([]*repository.WealthOrderModel{expiredOrder}, nil)
	mockWealthRepo.On("GetProductByID", mock.Anything, int64(1)).Return(&repository.WealthProductModel{
		ID:               1,
		Title:            "USDT 7日增值",
		APY:              "5.50",
		Duration:         7,
		Currency:         "USDT",
		Status:           1,
		AutoRenewAllowed: true,
	}, nil)

	mockAccountRepo.On("GetAccountByUserIDAndCurrency", mock.Anything, int64(1), "USDT").Return(&repository.AccountModel{
		ID:            1,
		UserID:        1,
		Currency:      "USDT",
		Balance:       "100000",
		FrozenBalance: "0",
	}, nil)

	newOrder := &repository.WealthOrderModel{
		ID:        2,
		UserID:    1,
		ProductID: 1,
		Amount:    "10000",
		StartDate: today,
		AutoRenew: true,
	}
	mockWealthRepo.On("RenewOrder", mock.Anything, expiredOrder, mock.Anything, mock.Anything, mock.Anything).Return(newOrder, nil)
	mockAccountRepo.On("AddBalance", mock.Anything, int64(1), mock.AnythingOfType("string")).Return(nil)
	mockWealthRepo.On("SettleOrder", mock.Anything, int64(1), mock.AnythingOfType("string")).Return(nil)
	mockJournalRepo.On("CreateJournalRecord", mock.Anything, mock.AnythingOfType("*repository.JournalModel")).Return(nil)

	settledCount, err := scheduler.SettleExpiredOrders(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 1, settledCount)
	mockWealthRepo.AssertExpectations(t)
	mockAccountRepo.AssertExpectations(t)
	mockJournalRepo.AssertExpectations(t)
}

func TestInterestScheduler_SettleExpiredOrders_NoAutoRenew(t *testing.T) {
	mockWealthRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepositoryV2)
	mockJournalRepo := new(MockJournalRepository)

	scheduler := &InterestScheduler{
		repo:        mockWealthRepo,
		accountRepo: mockAccountRepo,
		journalRepo: mockJournalRepo,
		metrics:     NewSchedulerMetrics(),
	}

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	expiredOrder := &repository.WealthOrderModel{
		ID:              1,
		UserID:          1,
		ProductID:       1,
		Amount:          "10000",
		InterestAccrued: "15.50",
		EndDate:         yesterday,
		Status:          1,
		AutoRenew:       false,
		Currency:        "USDT",
	}

	mockWealthRepo.On("GetExpiredOrders", mock.Anything).Return([]*repository.WealthOrderModel{expiredOrder}, nil)
	mockWealthRepo.On("GetOrderByID", mock.Anything, int64(1)).Return(expiredOrder, nil)
	mockAccountRepo.On("GetAccountByUserIDAndCurrency", mock.Anything, int64(1), "USDT").Return(&repository.AccountModel{
		ID:       1,
		UserID:   1,
		Currency: "USDT",
		Balance:  "100000",
	}, nil)
	mockAccountRepo.On("UnfreezeBalance", mock.Anything, int64(1), "10000").Return(nil)
	mockAccountRepo.On("AddBalance", mock.Anything, int64(1), mock.AnythingOfType("string")).Return(nil)
	mockWealthRepo.On("SettleOrder", mock.Anything, int64(1), mock.AnythingOfType("string")).Return(nil)
	mockJournalRepo.On("CreateJournalRecord", mock.Anything, mock.AnythingOfType("*repository.JournalModel")).Return(nil)

	settledCount, err := scheduler.SettleExpiredOrders(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 1, settledCount)
	mockWealthRepo.AssertExpectations(t)
	mockAccountRepo.AssertExpectations(t)
	mockJournalRepo.AssertExpectations(t)
}

func TestMain(m *testing.M) {
	_ = logger.Init("test")
	os.Exit(m.Run())
}
