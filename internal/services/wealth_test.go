package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"monera-digital/internal/repository"
)

func TestWealthService_GetProducts(t *testing.T) {
	mockRepo := new(MockWealthRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(mockRepo, nil, mockJournalRepo, nil)

	now := time.Now().Format(time.RFC3339)
	mockRepo.On("GetActiveProducts", mock.Anything).Return([]*repository.WealthProductModel{
		{
			ID:               1,
			Title:            "USDT 7日增值",
			Currency:         "USDT",
			APY:              "5.50",
			Duration:         7,
			MinAmount:        "100",
			MaxAmount:        "50000",
			TotalQuota:       "100000",
			SoldQuota:        "50000",
			Status:           2,
			AutoRenewAllowed: true,
			CreatedAt:        now,
		},
	}, nil)

	products, total, err := service.GetProducts(context.Background(), 1, 10)

	assert.NoError(t, err)
	assert.Len(t, products, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "USDT 7日增值", products[0].Title)
	assert.Equal(t, "USDT", products[0].Currency)
	assert.Equal(t, 5.5, products[0].APY)
	mockRepo.AssertExpectations(t)
}

func TestWealthService_Subscribe_InsufficientBalance(t *testing.T) {
	mockRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(mockRepo, mockAccountRepo, mockJournalRepo, nil)

	now := time.Now().Format(time.RFC3339)
	mockRepo.On("GetProductByID", mock.Anything, int64(1)).Return(&repository.WealthProductModel{
		ID:               1,
		Title:            "USDT 7日增值",
		Currency:         "USDT",
		APY:              "5.5",
		Duration:         7,
		MinAmount:        "100",
		MaxAmount:        "50000",
		TotalQuota:       "100000",
		SoldQuota:        "50000",
		Status:           1,
		AutoRenewAllowed: true,
		CreatedAt:        now,
	}, nil)
	mockAccountRepo.On("GetAccountByUserIDAndCurrency", mock.Anything, int64(1), "USDT").Return(&repository.AccountModel{
		ID:            1,
		UserID:        1,
		Type:          "FUND",
		Currency:      "USDT",
		Balance:       "100",
		FrozenBalance: "0",
	}, nil)

	_, err := service.Subscribe(context.Background(), 1, 1, "5000", false, "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient")
}

func TestWealthService_Subscribe_ProductNotFound(t *testing.T) {
	mockRepo := new(MockWealthRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(mockRepo, nil, mockJournalRepo, nil)

	mockRepo.On("GetProductByID", mock.Anything, int64(999)).Return(nil, repository.ErrNotFound)

	_, err := service.Subscribe(context.Background(), 1, 999, "1000", false, "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "product")
}

func TestWealthService_GetOrders(t *testing.T) {
	mockRepo := new(MockWealthRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(mockRepo, nil, mockJournalRepo, nil)

	now := time.Now().Format(time.RFC3339)
	mockRepo.On("GetOrdersByUserID", mock.Anything, int64(1)).Return([]*repository.WealthOrderModel{
		{
			ID:               1,
			UserID:           1,
			ProductID:        1,
			ProductTitle:     "USDT 7日增值",
			Amount:           "5000",
			InterestExpected: "52.88",
			InterestPaid:     "0",
			InterestAccrued:  "18.21",
			StartDate:        "2026-01-10",
			EndDate:          "2026-01-17",
			AutoRenew:        false,
			Status:           1,
			CreatedAt:        now,
		},
	}, nil)

	orders, total, err := service.GetOrders(context.Background(), 1, 1, 20)

	assert.NoError(t, err)
	assert.Len(t, orders, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "USDT 7日增值", orders[0].ProductTitle)
	assert.Equal(t, "5000", orders[0].Amount)
	mockRepo.AssertExpectations(t)
}

func TestWealthService_Redeem_OrderNotFound(t *testing.T) {
	mockRepo := new(MockWealthRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(mockRepo, nil, mockJournalRepo, nil)

	mockRepo.On("GetOrderByID", mock.Anything, int64(999)).Return(nil, repository.ErrNotFound)

	err := service.Redeem(context.Background(), 1, 999, "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "order")
}

func TestWealthService_Redeem_AlreadyRedeemed(t *testing.T) {
	mockRepo := new(MockWealthRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(mockRepo, nil, mockJournalRepo, nil)

	now := time.Now().Format(time.RFC3339)
	mockRepo.On("GetOrderByID", mock.Anything, int64(1)).Return(&repository.WealthOrderModel{
		ID:               1,
		UserID:           1,
		ProductID:        1,
		ProductTitle:     "USDT 7日增值",
		Amount:           "5000",
		InterestExpected: "52.88",
		InterestPaid:     "52.88",
		InterestAccrued:  "0",
		StartDate:        "2026-01-10",
		EndDate:          "2026-01-17",
		AutoRenew:        false,
		Status:           3,
		RedemptionAmount: "5000",
		CreatedAt:        now,
	}, nil)

	err := service.Redeem(context.Background(), 1, 1, "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redeemed")
}

func TestWealthService_Redeem_Success(t *testing.T) {
	mockRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(mockRepo, mockAccountRepo, mockJournalRepo, nil)

	now := time.Now().Format(time.RFC3339)
	pastDate := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	expiredDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")

	mockRepo.On("GetOrderByID", mock.Anything, int64(1)).Return(&repository.WealthOrderModel{
		ID:               1,
		UserID:           1,
		ProductID:        1,
		ProductTitle:     "USDT 7日增值",
		Currency:         "USDT",
		Amount:           "5000",
		InterestExpected: "52.88",
		InterestPaid:     "0",
		InterestAccrued:  "18.21",
		StartDate:        pastDate,
		EndDate:          expiredDate,
		AutoRenew:        false,
		Status:           1,
		CreatedAt:        now,
	}, nil)

	mockAccountRepo.On("GetAccountByUserIDAndCurrency", mock.Anything, int64(1), "USDT").Return(&repository.AccountModel{
		ID:            1,
		UserID:        1,
		Type:          "FUND",
		Currency:      "USDT",
		Balance:       "50000",
		FrozenBalance: "5000",
	}, nil)

	mockAccountRepo.On("UnfreezeBalance", mock.Anything, int64(1), "5000").Return(nil)
	mockAccountRepo.On("AddBalance", mock.Anything, int64(1), "18.21").Return(nil)

	mockJournalRepo.On("CreateJournalRecord", mock.Anything, mock.AnythingOfType("*repository.JournalModel")).Return(nil)
	mockRepo.On("UpdateOrder", mock.Anything, mock.AnythingOfType("*repository.WealthOrderModel")).Return(nil)

	err := service.Redeem(context.Background(), 1, 1, "full")

	assert.NoError(t, err)
	mockAccountRepo.AssertExpectations(t)
	mockJournalRepo.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
}

func TestWealthService_Redeem_SetsCorrectStatus(t *testing.T) {
	now := time.Now().Format(time.RFC3339)

	t.Run("expired order sets status to 3 (SETTLED)", func(t *testing.T) {
		mockRepo := new(MockWealthRepository)
		mockAccountRepo := new(MockAccountRepository)
		mockJournalRepo := new(MockJournalRepository)
		service := NewWealthService(mockRepo, mockAccountRepo, mockJournalRepo, nil)

		expiredDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")

		var updatedOrder *repository.WealthOrderModel
		mockRepo.On("GetOrderByID", mock.Anything, int64(1)).Return(&repository.WealthOrderModel{
			ID:              1,
			UserID:          1,
			ProductID:       1,
			Currency:        "USDT",
			Amount:          "5000",
			InterestAccrued: "18.21",
			EndDate:         expiredDate,
			Status:          1,
			CreatedAt:       now,
		}, nil)

		mockAccountRepo.On("GetAccountByUserIDAndCurrency", mock.Anything, int64(1), "USDT").Return(&repository.AccountModel{
			ID:            1,
			UserID:        1,
			Currency:      "USDT",
			Balance:       "50000",
			FrozenBalance: "5000",
		}, nil)

		mockAccountRepo.On("UnfreezeBalance", mock.Anything, int64(1), "5000").Return(nil)
		mockAccountRepo.On("AddBalance", mock.Anything, int64(1), "18.21").Return(nil)
		mockJournalRepo.On("CreateJournalRecord", mock.Anything, mock.AnythingOfType("*repository.JournalModel")).Return(nil)
		mockRepo.On("UpdateOrder", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			updatedOrder = args.Get(1).(*repository.WealthOrderModel)
		}).Return(nil)

		err := service.Redeem(context.Background(), 1, 1, "full")

		assert.NoError(t, err)
		assert.NotNil(t, updatedOrder)
		assert.Equal(t, 3, updatedOrder.Status, "Expired order should have status 3 (SETTLED)")
	})

	t.Run("early redemption sets status to 4 (REDEEMED)", func(t *testing.T) {
		mockRepo := new(MockWealthRepository)
		mockAccountRepo := new(MockAccountRepository)
		mockJournalRepo := new(MockJournalRepository)
		service := NewWealthService(mockRepo, mockAccountRepo, mockJournalRepo, nil)

		futureDate := time.Now().AddDate(0, 0, 20).Format("2006-01-02")

		var updatedOrder *repository.WealthOrderModel
		mockRepo.On("GetOrderByID", mock.Anything, int64(2)).Return(&repository.WealthOrderModel{
			ID:              2,
			UserID:          1,
			ProductID:       1,
			Currency:        "USDT",
			Amount:          "10000",
			InterestAccrued: "0",
			EndDate:         futureDate,
			Status:          1,
			CreatedAt:       now,
		}, nil)

		mockAccountRepo.On("GetAccountByUserIDAndCurrency", mock.Anything, int64(1), "USDT").Return(&repository.AccountModel{
			ID:            2,
			UserID:        1,
			Currency:      "USDT",
			Balance:       "40000",
			FrozenBalance: "10000",
		}, nil)

		mockAccountRepo.On("UnfreezeBalance", mock.Anything, int64(2), "10000").Return(nil)
		mockJournalRepo.On("CreateJournalRecord", mock.Anything, mock.AnythingOfType("*repository.JournalModel")).Return(nil)
		mockRepo.On("UpdateOrder", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			updatedOrder = args.Get(1).(*repository.WealthOrderModel)
		}).Return(nil)

		err := service.Redeem(context.Background(), 1, 2, "full")

		assert.NoError(t, err)
		assert.NotNil(t, updatedOrder)
		assert.Equal(t, 4, updatedOrder.Status, "Early redemption should have status 4 (REDEEMED)")
	})
}

func TestWealthService_Redeem_EarlyRedemption(t *testing.T) {
	mockRepo := new(MockWealthRepository)
	mockAccountRepo := new(MockAccountRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(mockRepo, mockAccountRepo, mockJournalRepo, nil)

	now := time.Now().Format(time.RFC3339)
	today := time.Now().Format("2006-01-02")
	futureDate := time.Now().AddDate(0, 0, 20).Format("2006-01-02")

	mockRepo.On("GetOrderByID", mock.Anything, int64(2)).Return(&repository.WealthOrderModel{
		ID:               2,
		UserID:           1,
		ProductID:        1,
		ProductTitle:     "USDT 30日稳健",
		Currency:         "USDT",
		Amount:           "10000",
		InterestExpected: "65.75",
		InterestPaid:     "0",
		InterestAccrued:  "0",
		StartDate:        today,
		EndDate:          futureDate,
		AutoRenew:        false,
		Status:           1,
		CreatedAt:        now,
	}, nil)

	mockAccountRepo.On("GetAccountByUserIDAndCurrency", mock.Anything, int64(1), "USDT").Return(&repository.AccountModel{
		ID:            2,
		UserID:        1,
		Type:          "FUND",
		Currency:      "USDT",
		Balance:       "40000",
		FrozenBalance: "10000",
	}, nil)

	mockAccountRepo.On("UnfreezeBalance", mock.Anything, int64(2), "10000").Return(nil)
	mockJournalRepo.On("CreateJournalRecord", mock.Anything, mock.AnythingOfType("*repository.JournalModel")).Return(nil)
	mockRepo.On("UpdateOrder", mock.Anything, mock.AnythingOfType("*repository.WealthOrderModel")).Return(nil)

	err := service.Redeem(context.Background(), 1, 2, "full")

	assert.NoError(t, err)
	mockAccountRepo.AssertExpectations(t)
	mockJournalRepo.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
}
