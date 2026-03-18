package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"monera-digital/internal/repository"
)

func TestWealthService_GetAssets(t *testing.T) {
	mockAccountRepo := new(MockAccountRepository)
	mockJournalRepo := new(MockJournalRepository)
	service := NewWealthService(nil, mockAccountRepo, mockJournalRepo)

	mockAccountRepo.On("GetAccountsByUserID", mock.Anything, int64(1)).Return([]*repository.AccountModel{
		{
			ID:            1,
			UserID:        1,
			Type:          "FUND",
			Currency:      "USDT",
			Balance:       "100000",
			FrozenBalance: "5000",
		},
		{
			ID:            2,
			UserID:        1,
			Type:          "FUND",
			Currency:      "USDC",
			Balance:       "50000",
			FrozenBalance: "0",
		},
	}, nil)

	assets, err := service.GetAssets(context.Background(), 1)

	assert.NoError(t, err)
	assert.Len(t, assets, 2)
	assert.Equal(t, "USDT", assets[0].Currency)
	assert.Equal(t, "100000.0000000", assets[0].Total)
	assert.Equal(t, "95000.0000000", assets[0].Available)
	mockAccountRepo.AssertExpectations(t)
}
